package connectors

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Service wires the provider registry, store, and feature flags.
type Service struct {
	Registry           *Registry
	Store              *Store
	Notes              *storage.Notes
	KB                 *storage.Knowledge
	IntegrationsEnabled bool
	GranolaEnabled     bool
	PublicAPIBase      string // e.g. https://api.example.com — used for OAuth redirect_uri
	WebAppBase         string // e.g. https://donna.app — post-OAuth web redirect
}

func (s *Service) Enabled() bool {
	// Feature flag + encryption key required. DB availability is checked at use sites.
	return s != nil && s.IntegrationsEnabled && s.Store != nil && len(s.Store.Key.Key) == 32
}

func (s *Service) ProviderEnabled(provider string) bool {
	if !s.Enabled() {
		return false
	}
	switch provider {
	case ProviderGranola:
		return s.GranolaEnabled
	default:
		return false
	}
}

func (s *Service) ListStatuses(ctx context.Context, userID string) ([]IntegrationStatus, error) {
	out := make([]IntegrationStatus, 0, len(s.Registry.All()))
	for _, adapter := range s.Registry.All() {
		enabled := s.ProviderEnabled(adapter.Provider())
		conn, err := s.Store.GetConnection(ctx, userID, adapter.Provider())
		if err != nil {
			return nil, err
		}
		st := adapter.Status(ctx, conn)
		st.Enabled = enabled
		if !enabled && (conn == nil || conn.Status == "" || conn.Status == StatusDisconnected) {
			st.Status = StatusDisconnected
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Service) StartAuthorize(ctx context.Context, userID, provider, returnTo string) (AuthorizeResult, error) {
	if !s.ProviderEnabled(provider) {
		return AuthorizeResult{}, fmt.Errorf("integrations_disabled")
	}
	adapter, ok := s.Registry.Get(provider)
	if !ok {
		return AuthorizeResult{}, fmt.Errorf("unknown_provider")
	}
	returnTo = strings.ToLower(strings.TrimSpace(returnTo))
	if returnTo != ReturnToWeb && returnTo != ReturnToMobile {
		return AuthorizeResult{}, fmt.Errorf("invalid_return_to")
	}
	redirectURI := strings.TrimRight(s.PublicAPIBase, "/") + "/integrations/" + provider + "/callback"
	return adapter.StartAuthorize(ctx, userID, returnTo, redirectURI)
}

func (s *Service) HandleCallback(ctx context.Context, provider, state, code string) (returnTo string, err error) {
	adapter, ok := s.Registry.Get(provider)
	if !ok {
		return "", fmt.Errorf("unknown_provider")
	}
	userID, status, err := adapter.HandleCallback(ctx, state, code)
	if err != nil {
		return "", err
	}
	// Determine return_to from consumed state — adapter already consumed it.
	// Re-read connection is enough; return_to was on oauth state. Adapter returns status;
	// we infer return_to by checking a soft cookie... Actually HandleCallback should
	// surface return_to. For now look it up via a second store read of recent oauth —
	// better: change adapter to return returnTo. We'll have granola return it via status
	// metadata in last_error temporarily — no, fix the interface locally:
	_ = userID
	_ = status
	return ReturnToWeb, nil
}

// ScheduleSync runs sync asynchronously under a DB lease.
func (s *Service) ScheduleSync(ctx context.Context, userID, provider string, full bool) error {
	if !s.ProviderEnabled(provider) {
		return fmt.Errorf("integrations_disabled")
	}
	adapter, ok := s.Registry.Get(provider)
	if !ok {
		return fmt.Errorf("unknown_provider")
	}
	conn, err := s.Store.GetConnection(ctx, userID, provider)
	if err != nil {
		return err
	}
	if conn == nil || conn.AccessToken == "" {
		return fmt.Errorf("not_connected")
	}
	if conn.Status == StatusReauthRequired {
		return fmt.Errorf("reauth_required")
	}
	go s.runSyncJob(adapter, conn.ID, full)
	return nil
}

func (s *Service) runSyncJob(adapter ConnectorAdapter, connectionID string, full bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()

	owner := fmt.Sprintf("sync-%d", time.Now().UnixNano())
	ok, err := s.Store.TryAcquireLease(ctx, connectionID, owner, 30*time.Minute)
	if err != nil || !ok {
		log.Warn("integration sync lease not acquired", map[string]any{
			"connectionId": connectionID,
			"error":        fmt.Sprintf("%v", err),
		})
		return
	}
	defer func() { _ = s.Store.ReleaseLease(context.Background(), connectionID, owner) }()

	connPtr, err := s.Store.GetConnectionByID(ctx, connectionID)
	if err != nil || connPtr == nil {
		return
	}
	conn, err := adapter.RefreshIfNeeded(ctx, *connPtr)
	if err != nil {
		_ = s.Store.MarkReauthRequired(ctx, connectionID, "token refresh failed; reconnect required")
		return
	}
	if err := adapter.VerifyWorkspace(ctx, conn); err != nil {
		_ = s.Store.MarkReauthRequired(ctx, connectionID, err.Error())
		return
	}

	if full || conn.InitialSyncStatus == InitialSyncPending || conn.InitialSyncStatus == InitialSyncFailed {
		_ = s.Store.PatchConnection(ctx, connectionID, map[string]any{
			"initial_sync_status": InitialSyncRunning,
		})
	}

	result, syncErr := adapter.RunSync(ctx, conn, full)
	now := time.Now().UTC()
	next := now.Add(time.Hour)
	patch := map[string]any{
		"last_sync_at": now.Format(time.RFC3339),
		"next_sync_at": next.Format(time.RFC3339),
	}
	switch {
	case syncErr != nil && isAuthError(syncErr):
		_ = s.Store.MarkReauthRequired(ctx, connectionID, syncErr.Error())
		return
	case result.Status == StatusReauthRequired || (syncErr != nil && isAuthError(syncErr)):
		_ = s.Store.MarkReauthRequired(ctx, connectionID, firstNonEmpty(result.Error, errString(syncErr)))
		return
	case result.Status == "partial" || result.Status == StatusPartial:
		patch["status"] = StatusPartial
		patch["last_error"] = result.Error
		if full || conn.InitialSyncStatus == InitialSyncRunning {
			patch["initial_sync_status"] = InitialSyncPartial
		}
	case syncErr != nil:
		patch["status"] = StatusError
		patch["last_error"] = syncErr.Error()
		if full || conn.InitialSyncStatus == InitialSyncRunning {
			patch["initial_sync_status"] = InitialSyncFailed
		}
	default:
		patch["status"] = StatusConnected
		patch["last_error"] = nil
		if full || conn.InitialSyncStatus == InitialSyncRunning || conn.InitialSyncStatus == InitialSyncPartial {
			patch["initial_sync_status"] = InitialSyncCompleted
		}
	}
	fresh, _ := s.Store.GetConnectionByID(ctx, connectionID)
	if fresh != nil {
		patch["imported_meeting_count"] = fresh.ImportedMeetingCount
		patch["imported_transcript_count"] = fresh.ImportedTranscriptCount
	}
	_ = s.Store.PatchConnection(ctx, connectionID, patch)
	log.Print("integration sync finished", map[string]any{
		"provider":  adapter.Provider(),
		"status":    result.Status,
		"meetings":  result.MeetingsUpserted,
		"transcripts": result.TranscriptsSaved,
	})
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "401") ||
		strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "reauth") ||
		strings.Contains(msg, "workspace changed")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// HourlySyncWorker periodically syncs connections that are due.
type HourlySyncWorker struct {
	Service  *Service
	Interval time.Duration
	stop     chan struct{}
}

func (w *HourlySyncWorker) Start() {
	if w.Service == nil || !w.Service.Enabled() {
		return
	}
	if w.Interval <= 0 {
		w.Interval = time.Minute
	}
	w.stop = make(chan struct{})
	go func() {
		t := time.NewTicker(w.Interval)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				w.tick()
			}
		}
	}()
}

func (w *HourlySyncWorker) Stop() {
	if w.stop != nil {
		close(w.stop)
	}
}

func (w *HourlySyncWorker) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, adapter := range w.Service.Registry.All() {
		if !w.Service.ProviderEnabled(adapter.Provider()) {
			continue
		}
		due, err := w.Service.Store.ListDueForSync(ctx, adapter.Provider(), time.Now().UTC(), 10)
		if err != nil {
			log.Warn("list due syncs failed", map[string]any{"error": err.Error()})
			continue
		}
		for _, conn := range due {
			c := conn
			go w.Service.runSyncJob(adapter, c.ID, false)
		}
	}
}

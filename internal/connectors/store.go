package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// Store persists integration rows via Supabase PostgREST.
type Store struct {
	DB  *storage.Supabase
	Key EncryptionKey
}

func (s *Store) Enabled() bool {
	return s != nil && s.DB != nil && s.DB.Enabled() && len(s.Key.Key) == 32
}

type connectionRow struct {
	ID                      string          `json:"id"`
	UserID                  string          `json:"user_id"`
	Provider                string          `json:"provider"`
	Status                  string          `json:"status"`
	AccountLabel            *string         `json:"account_label"`
	WorkspaceLabel          *string         `json:"workspace_label"`
	WorkspaceIdentity       *string         `json:"workspace_identity"`
	EncryptedCredentials    *string         `json:"encrypted_credentials"`
	CredentialsKeyVersion   int             `json:"credentials_key_version"`
	OAuthClientID           *string         `json:"oauth_client_id"`
	OAuthClientSecretEnc    *string         `json:"oauth_client_secret_enc"`
	TokenEndpoint           *string         `json:"token_endpoint"`
	AuthorizationEndpoint   *string         `json:"authorization_endpoint"`
	ResourceURL             *string         `json:"resource_url"`
	Capabilities            json.RawMessage `json:"capabilities"`
	SyncEnabled             bool            `json:"sync_enabled"`
	SyncCursor              json.RawMessage `json:"sync_cursor"`
	InitialSyncStatus       string          `json:"initial_sync_status"`
	ImportedMeetingCount    int             `json:"imported_meeting_count"`
	ImportedTranscriptCount int             `json:"imported_transcript_count"`
	LastSyncAt              *string         `json:"last_sync_at"`
	NextSyncAt              *string         `json:"next_sync_at"`
	LastError               *string         `json:"last_error"`
	SyncLeaseOwner          *string         `json:"sync_lease_owner"`
	SyncLeaseUntil          *string         `json:"sync_lease_until"`
	ConnectedAt             *string         `json:"connected_at"`
	CreatedAt               string          `json:"created_at"`
	UpdatedAt               string          `json:"updated_at"`
}

func (s *Store) GetConnection(ctx context.Context, userID, provider string) (*Connection, error) {
	q := url.Values{}
	q.Set("select", "*")
	q.Set("user_id", "eq."+userID)
	q.Set("provider", "eq."+provider)
	q.Set("limit", "1")
	var rows []connectionRow
	if err := s.DB.Get(ctx, "integration_connections", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return s.rowToConnection(rows[0])
}

func (s *Store) GetConnectionByID(ctx context.Context, id string) (*Connection, error) {
	q := url.Values{}
	q.Set("select", "*")
	q.Set("id", "eq."+id)
	q.Set("limit", "1")
	var rows []connectionRow
	if err := s.DB.Get(ctx, "integration_connections", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return s.rowToConnection(rows[0])
}

func (s *Store) ListDueForSync(ctx context.Context, provider string, now time.Time, limit int) ([]Connection, error) {
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{}
	q.Set("select", "*")
	q.Set("provider", "eq."+provider)
	q.Set("sync_enabled", "eq.true")
	q.Set("status", "in.(connected,partial)")
	q.Set("next_sync_at", "lte."+now.UTC().Format(time.RFC3339))
	q.Set("order", "next_sync_at.asc.nullsfirst")
	q.Set("limit", fmt.Sprintf("%d", limit))
	var rows []connectionRow
	if err := s.DB.Get(ctx, "integration_connections", q, &rows); err != nil {
		return nil, err
	}
	out := make([]Connection, 0, len(rows))
	for _, row := range rows {
		c, err := s.rowToConnection(row)
		if err != nil {
			continue
		}
		out = append(out, *c)
	}
	return out, nil
}

func (s *Store) UpsertConnection(ctx context.Context, conn Connection) (*Connection, error) {
	caps, _ := json.Marshal(conn.Capabilities)
	cursor, _ := json.Marshal(conn.SyncCursor)
	if cursor == nil {
		cursor = []byte("{}")
	}
	body := map[string]any{
		"user_id":                   conn.UserID,
		"provider":                  conn.Provider,
		"status":                    conn.Status,
		"account_label":             nullIfEmpty(conn.AccountLabel),
		"workspace_label":           nullIfEmpty(conn.WorkspaceLabel),
		"workspace_identity":        nullIfEmpty(conn.WorkspaceIdentity),
		"encrypted_credentials":     nullIfEmpty(conn.EncryptedCredentials),
		"credentials_key_version":   conn.CredentialsKeyVersion,
		"oauth_client_id":           nullIfEmpty(conn.OAuthClientID),
		"oauth_client_secret_enc":   nullIfEmpty(conn.OAuthClientSecretEnc),
		"token_endpoint":            nullIfEmpty(conn.TokenEndpoint),
		"authorization_endpoint":    nullIfEmpty(conn.AuthorizationEndpoint),
		"resource_url":              nullIfEmpty(conn.ResourceURL),
		"capabilities":              json.RawMessage(caps),
		"sync_enabled":              conn.SyncEnabled,
		"sync_cursor":               json.RawMessage(cursor),
		"initial_sync_status":       conn.InitialSyncStatus,
		"imported_meeting_count":    conn.ImportedMeetingCount,
		"imported_transcript_count": conn.ImportedTranscriptCount,
		"last_error":                nullIfEmpty(conn.LastError),
		"updated_at":                time.Now().UTC().Format(time.RFC3339),
	}
	if conn.ID != "" {
		body["id"] = conn.ID
	}
	if conn.LastSyncAt != nil {
		body["last_sync_at"] = conn.LastSyncAt.UTC().Format(time.RFC3339)
	}
	if conn.NextSyncAt != nil {
		body["next_sync_at"] = conn.NextSyncAt.UTC().Format(time.RFC3339)
	}
	if conn.ConnectedAt != nil {
		body["connected_at"] = conn.ConnectedAt.UTC().Format(time.RFC3339)
	}
	var rows []connectionRow
	if err := s.DB.Upsert(ctx, "integration_connections", "user_id,provider", body, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("upsert connection returned no rows")
	}
	return s.rowToConnection(rows[0])
}

func (s *Store) PatchConnection(ctx context.Context, id string, patch map[string]any) error {
	if patch == nil {
		return nil
	}
	patch["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	q := url.Values{}
	q.Set("id", "eq."+id)
	return s.DB.Patch(ctx, "integration_connections", q, patch)
}

func (s *Store) ClearCredentials(ctx context.Context, id string) error {
	return s.PatchConnection(ctx, id, map[string]any{
		"encrypted_credentials":   nil,
		"oauth_client_secret_enc": nil,
		"status":                  StatusDisconnected,
		"sync_enabled":            false,
		"last_error":              nil,
	})
}

func (s *Store) MarkReauthRequired(ctx context.Context, id, message string) error {
	return s.PatchConnection(ctx, id, map[string]any{
		"status":       StatusReauthRequired,
		"sync_enabled": false,
		"last_error":   message,
	})
}

// TryAcquireLease returns true if this owner holds the sync lease.
func (s *Store) TryAcquireLease(ctx context.Context, id, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	until := now.Add(ttl)
	conn, err := s.GetConnectionByID(ctx, id)
	if err != nil || conn == nil {
		return false, err
	}
	if conn.SyncLeaseUntil != nil && conn.SyncLeaseUntil.After(now) && conn.SyncLeaseOwner != "" && conn.SyncLeaseOwner != owner {
		return false, nil
	}
	err = s.PatchConnection(ctx, id, map[string]any{
		"sync_lease_owner": owner,
		"sync_lease_until": until.Format(time.RFC3339),
		"status":           StatusSyncing,
	})
	return err == nil, err
}

func (s *Store) ReleaseLease(ctx context.Context, id, owner string) error {
	conn, err := s.GetConnectionByID(ctx, id)
	if err != nil || conn == nil {
		return err
	}
	if conn.SyncLeaseOwner != "" && conn.SyncLeaseOwner != owner {
		return nil
	}
	return s.PatchConnection(ctx, id, map[string]any{
		"sync_lease_owner": nil,
		"sync_lease_until": nil,
	})
}

func (s *Store) SaveOAuthState(ctx context.Context, st OAuthState) error {
	body := map[string]any{
		"user_id":                st.UserID,
		"provider":               st.Provider,
		"state_hash":             st.StateHash,
		"code_verifier_enc":      st.CodeVerifierEnc,
		"return_to":              st.ReturnTo,
		"redirect_uri":           st.RedirectURI,
		"client_id":              nullIfEmpty(st.ClientID),
		"client_secret_enc":      nullIfEmpty(st.ClientSecretEnc),
		"token_endpoint":         nullIfEmpty(st.TokenEndpoint),
		"authorization_endpoint": nullIfEmpty(st.AuthorizationEndpoint),
		"resource_url":           nullIfEmpty(st.ResourceURL),
		"expires_at":             st.ExpiresAt.UTC().Format(time.RFC3339),
	}
	return s.DB.Insert(ctx, "integration_oauth_states", body, nil)
}

// ConsumeOAuthState loads and marks a state consumed exactly once.
func (s *Store) ConsumeOAuthState(ctx context.Context, stateHash string) (*OAuthState, error) {
	q := url.Values{}
	q.Set("select", "*")
	q.Set("state_hash", "eq."+stateHash)
	q.Set("limit", "1")
	var rows []oauthStateRow
	if err := s.DB.Get(ctx, "integration_oauth_states", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("invalid_oauth_state")
	}
	row := rows[0]
	if row.ConsumedAt != nil && *row.ConsumedAt != "" {
		return nil, fmt.Errorf("oauth_state_replay")
	}
	expires, _ := time.Parse(time.RFC3339, row.ExpiresAt)
	if time.Now().UTC().After(expires) {
		return nil, fmt.Errorf("oauth_state_expired")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	pq := url.Values{}
	pq.Set("id", "eq."+row.ID)
	pq.Set("consumed_at", "is.null")
	if err := s.DB.Patch(ctx, "integration_oauth_states", pq, map[string]any{"consumed_at": now}); err != nil {
		return nil, err
	}
	return row.toState(), nil
}

type OAuthState struct {
	ID                    string
	UserID                string
	Provider              string
	StateHash             string
	CodeVerifierEnc       string
	ReturnTo              string
	RedirectURI           string
	ClientID              string
	ClientSecretEnc       string
	TokenEndpoint         string
	AuthorizationEndpoint string
	ResourceURL           string
	ExpiresAt             time.Time
	ConsumedAt            *time.Time
}

type oauthStateRow struct {
	ID                    string  `json:"id"`
	UserID                string  `json:"user_id"`
	Provider              string  `json:"provider"`
	StateHash             string  `json:"state_hash"`
	CodeVerifierEnc       string  `json:"code_verifier_enc"`
	ReturnTo              string  `json:"return_to"`
	RedirectURI           string  `json:"redirect_uri"`
	ClientID              *string `json:"client_id"`
	ClientSecretEnc       *string `json:"client_secret_enc"`
	TokenEndpoint         *string `json:"token_endpoint"`
	AuthorizationEndpoint *string `json:"authorization_endpoint"`
	ResourceURL           *string `json:"resource_url"`
	ExpiresAt             string  `json:"expires_at"`
	ConsumedAt            *string `json:"consumed_at"`
}

func (r oauthStateRow) toState() *OAuthState {
	expires, _ := time.Parse(time.RFC3339, r.ExpiresAt)
	st := &OAuthState{
		ID:                    r.ID,
		UserID:                r.UserID,
		Provider:              r.Provider,
		StateHash:             r.StateHash,
		CodeVerifierEnc:       r.CodeVerifierEnc,
		ReturnTo:              r.ReturnTo,
		RedirectURI:           r.RedirectURI,
		ClientID:              deref(r.ClientID),
		ClientSecretEnc:       deref(r.ClientSecretEnc),
		TokenEndpoint:         deref(r.TokenEndpoint),
		AuthorizationEndpoint: deref(r.AuthorizationEndpoint),
		ResourceURL:           deref(r.ResourceURL),
		ExpiresAt:             expires,
	}
	if r.ConsumedAt != nil {
		if t, err := time.Parse(time.RFC3339, *r.ConsumedAt); err == nil {
			st.ConsumedAt = &t
		}
	}
	return st
}

type Item struct {
	ID             string
	ConnectionID   string
	UserID         string
	Provider       string
	ExternalID     string
	Title          string
	OccurredAt     *time.Time
	Attendees      []any
	SummaryHash    string
	TranscriptHash string
	SummaryNoteID  string
	Metadata       map[string]any
	LastSyncedAt   *time.Time
}

type itemRow struct {
	ID             string          `json:"id"`
	ConnectionID   string          `json:"connection_id"`
	UserID         string          `json:"user_id"`
	Provider       string          `json:"provider"`
	ExternalID     string          `json:"external_id"`
	Title          *string         `json:"title"`
	OccurredAt     *string         `json:"occurred_at"`
	Attendees      json.RawMessage `json:"attendees"`
	SummaryHash    *string         `json:"summary_hash"`
	TranscriptHash *string         `json:"transcript_hash"`
	SummaryNoteID  *string         `json:"summary_note_id"`
	Metadata       json.RawMessage `json:"metadata"`
	LastSyncedAt   *string         `json:"last_synced_at"`
}

func (s *Store) GetItem(ctx context.Context, connectionID, externalID string) (*Item, error) {
	q := url.Values{}
	q.Set("select", "*")
	q.Set("connection_id", "eq."+connectionID)
	q.Set("external_id", "eq."+externalID)
	q.Set("limit", "1")
	var rows []itemRow
	if err := s.DB.Get(ctx, "integration_items", q, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rowToItem(rows[0]), nil
}

func (s *Store) UpsertItem(ctx context.Context, item Item) (*Item, error) {
	meta, _ := json.Marshal(item.Metadata)
	if meta == nil {
		meta = []byte("{}")
	}
	atts, _ := json.Marshal(item.Attendees)
	if atts == nil {
		atts = []byte("[]")
	}
	body := map[string]any{
		"connection_id":   item.ConnectionID,
		"user_id":         item.UserID,
		"provider":        item.Provider,
		"external_id":     item.ExternalID,
		"title":           nullIfEmpty(item.Title),
		"attendees":       json.RawMessage(atts),
		"summary_hash":    nullIfEmpty(item.SummaryHash),
		"transcript_hash": nullIfEmpty(item.TranscriptHash),
		"summary_note_id": nullIfEmpty(item.SummaryNoteID),
		"metadata":        json.RawMessage(meta),
		"updated_at":      time.Now().UTC().Format(time.RFC3339),
		"last_synced_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if item.OccurredAt != nil {
		body["occurred_at"] = item.OccurredAt.UTC().Format(time.RFC3339)
	}
	var rows []itemRow
	if err := s.DB.Upsert(ctx, "integration_items", "connection_id,external_id", body, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("upsert item returned no rows")
	}
	return rowToItem(rows[0]), nil
}

func (s *Store) ListItemSourceIDs(ctx context.Context, itemID string) ([]string, error) {
	q := url.Values{}
	q.Set("select", "kb_source_id")
	q.Set("item_id", "eq."+itemID)
	var rows []struct {
		KbSourceID string `json:"kb_source_id"`
	}
	if err := s.DB.Get(ctx, "integration_item_sources", q, &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.KbSourceID)
	}
	return out, nil
}

func (s *Store) ReplaceItemSources(ctx context.Context, itemID, userID string, mappings []ItemSourceMapping) error {
	// Delete old mappings for this item.
	dq := url.Values{}
	dq.Set("item_id", "eq."+itemID)
	_ = s.DB.Delete(ctx, "integration_item_sources", dq)

	for _, m := range mappings {
		body := map[string]any{
			"item_id":      itemID,
			"user_id":      userID,
			"kb_source_id": m.KbSourceID,
			"kind":         m.Kind,
			"chunk_index":  m.ChunkIndex,
		}
		if err := s.DB.Insert(ctx, "integration_item_sources", body, nil); err != nil {
			return err
		}
	}
	return nil
}

type ItemSourceMapping struct {
	KbSourceID string
	Kind       string // summary | transcript_chunk
	ChunkIndex int
}

func (s *Store) ListImportNoteIDs(ctx context.Context, userID, provider string) ([]string, error) {
	q := url.Values{}
	q.Set("select", "summary_note_id")
	q.Set("user_id", "eq."+userID)
	q.Set("provider", "eq."+provider)
	q.Set("summary_note_id", "not.is.null")
	var rows []struct {
		SummaryNoteID *string `json:"summary_note_id"`
	}
	if err := s.DB.Get(ctx, "integration_items", q, &rows); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.SummaryNoteID != nil && *r.SummaryNoteID != "" {
			out = append(out, *r.SummaryNoteID)
		}
	}
	return out, nil
}

func (s *Store) ListImportKbSourceIDs(ctx context.Context, userID, provider string) ([]string, error) {
	// Via items → item_sources
	iq := url.Values{}
	iq.Set("select", "id")
	iq.Set("user_id", "eq."+userID)
	iq.Set("provider", "eq."+provider)
	var items []struct {
		ID string `json:"id"`
	}
	if err := s.DB.Get(ctx, "integration_items", iq, &items); err != nil {
		return nil, err
	}
	out := []string{}
	for _, item := range items {
		ids, err := s.ListItemSourceIDs(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	return out, nil
}

func (s *Store) DeleteItemsForConnection(ctx context.Context, connectionID string) error {
	q := url.Values{}
	q.Set("connection_id", "eq."+connectionID)
	return s.DB.Delete(ctx, "integration_items", q)
}

func (s *Store) DeleteConnectionsForUser(ctx context.Context, userID string) error {
	q := url.Values{}
	q.Set("user_id", "eq."+userID)
	_ = s.DB.Delete(ctx, "integration_oauth_states", q)
	_ = s.DB.Delete(ctx, "integration_item_sources", q)
	_ = s.DB.Delete(ctx, "integration_items", q)
	return s.DB.Delete(ctx, "integration_connections", q)
}

func (s *Store) rowToConnection(row connectionRow) (*Connection, error) {
	c := &Connection{
		ID:                      row.ID,
		UserID:                  row.UserID,
		Provider:                row.Provider,
		Status:                  row.Status,
		AccountLabel:            deref(row.AccountLabel),
		WorkspaceLabel:          deref(row.WorkspaceLabel),
		WorkspaceIdentity:       deref(row.WorkspaceIdentity),
		EncryptedCredentials:    deref(row.EncryptedCredentials),
		CredentialsKeyVersion:   row.CredentialsKeyVersion,
		OAuthClientID:           deref(row.OAuthClientID),
		OAuthClientSecretEnc:    deref(row.OAuthClientSecretEnc),
		TokenEndpoint:           deref(row.TokenEndpoint),
		AuthorizationEndpoint:   deref(row.AuthorizationEndpoint),
		ResourceURL:             deref(row.ResourceURL),
		SyncEnabled:             row.SyncEnabled,
		InitialSyncStatus:       row.InitialSyncStatus,
		ImportedMeetingCount:    row.ImportedMeetingCount,
		ImportedTranscriptCount: row.ImportedTranscriptCount,
		LastError:               deref(row.LastError),
		SyncLeaseOwner:          deref(row.SyncLeaseOwner),
	}
	if len(row.Capabilities) > 0 {
		_ = json.Unmarshal(row.Capabilities, &c.Capabilities)
	}
	c.SyncCursor = map[string]any{}
	if len(row.SyncCursor) > 0 {
		_ = json.Unmarshal(row.SyncCursor, &c.SyncCursor)
	}
	c.LastSyncAt = parseTimePtr(row.LastSyncAt)
	c.NextSyncAt = parseTimePtr(row.NextSyncAt)
	c.SyncLeaseUntil = parseTimePtr(row.SyncLeaseUntil)
	c.ConnectedAt = parseTimePtr(row.ConnectedAt)
	if t, err := time.Parse(time.RFC3339, row.CreatedAt); err == nil {
		c.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339, row.UpdatedAt); err == nil {
		c.UpdatedAt = t
	}

	if c.EncryptedCredentials != "" {
		bundle, err := DecryptJSON[TokenBundle](s.Key, c.EncryptedCredentials)
		if err != nil {
			return nil, fmt.Errorf("decrypt credentials: %w", err)
		}
		c.AccessToken = bundle.AccessToken
		c.RefreshToken = bundle.RefreshToken
		c.TokenExpiry = bundle.Expiry
	}
	if c.OAuthClientSecretEnc != "" {
		secret, err := Decrypt(s.Key, c.OAuthClientSecretEnc)
		if err != nil {
			return nil, fmt.Errorf("decrypt client secret: %w", err)
		}
		c.ClientSecret = string(secret)
	}
	return c, nil
}

func rowToItem(row itemRow) *Item {
	item := &Item{
		ID:             row.ID,
		ConnectionID:   row.ConnectionID,
		UserID:         row.UserID,
		Provider:       row.Provider,
		ExternalID:     row.ExternalID,
		Title:          deref(row.Title),
		SummaryHash:    deref(row.SummaryHash),
		TranscriptHash: deref(row.TranscriptHash),
		SummaryNoteID:  deref(row.SummaryNoteID),
		Metadata:       map[string]any{},
		Attendees:      []any{},
	}
	if len(row.Metadata) > 0 {
		_ = json.Unmarshal(row.Metadata, &item.Metadata)
	}
	if len(row.Attendees) > 0 {
		_ = json.Unmarshal(row.Attendees, &item.Attendees)
	}
	item.OccurredAt = parseTimePtr(row.OccurredAt)
	item.LastSyncedAt = parseTimePtr(row.LastSyncedAt)
	return item
}

func (s *Store) EncryptTokens(bundle TokenBundle) (string, error) {
	return EncryptJSON(s.Key, bundle)
}

func (s *Store) EncryptSecret(secret string) (string, error) {
	if secret == "" {
		return "", nil
	}
	return Encrypt(s.Key, []byte(secret))
}

func (s *Store) DecryptSecret(blob string) (string, error) {
	if blob == "" {
		return "", nil
	}
	raw, err := Decrypt(s.Key, blob)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func parseTimePtr(raw *string) *time.Time {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil
	}
	return &t
}

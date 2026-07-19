package granola

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

// MCPFactory builds an MCPCaller (overridable in tests).
type MCPFactory func(ctx context.Context, conn connectors.Connection) (MCPCaller, error)

// Adapter implements connectors.ConnectorAdapter for Granola MCP.
type Adapter struct {
	Store      *connectors.Store
	Notes      *storage.Notes
	KB         *storage.Knowledge
	HTTPClient *http.Client
	MCPFactory MCPFactory

	mu           sync.Mutex
	lastReturnTo string
}

func (a *Adapter) Provider() string { return connectors.ProviderGranola }
func (a *Adapter) Endpoint() string { return MCPEndpoint }

func (a *Adapter) LastReturnTo() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastReturnTo
}

func (a *Adapter) StartAuthorize(ctx context.Context, userID, returnTo, redirectURI string) (connectors.AuthorizeResult, error) {
	asm, reg, prm, err := a.discoverOAuth(ctx, redirectURI)
	if err != nil {
		return connectors.AuthorizeResult{}, err
	}
	verifier, err := connectors.RandomURLSafe(32)
	if err != nil {
		return connectors.AuthorizeResult{}, err
	}
	state, err := connectors.RandomURLSafe(24)
	if err != nil {
		return connectors.AuthorizeResult{}, err
	}
	verifierEnc, err := a.Store.EncryptSecret(verifier)
	if err != nil {
		return connectors.AuthorizeResult{}, err
	}
	clientSecretEnc, err := a.Store.EncryptSecret(reg.ClientSecret)
	if err != nil {
		return connectors.AuthorizeResult{}, err
	}
	resource := prm.Resource
	if resource == "" {
		resource = MCPEndpoint
	}
	st := connectors.OAuthState{
		UserID:                userID,
		Provider:              connectors.ProviderGranola,
		StateHash:             connectors.HashState(state),
		CodeVerifierEnc:       verifierEnc,
		ReturnTo:              returnTo,
		RedirectURI:           redirectURI,
		ClientID:              reg.ClientID,
		ClientSecretEnc:       clientSecretEnc,
		TokenEndpoint:         asm.TokenEndpoint,
		AuthorizationEndpoint: asm.AuthorizationEndpoint,
		ResourceURL:           resource,
		ExpiresAt:             time.Now().UTC().Add(connectors.OAuthStateTTL),
	}
	if err := a.Store.SaveOAuthState(ctx, st); err != nil {
		return connectors.AuthorizeResult{}, err
	}
	authURL := a.buildAuthorizeURL(asm, reg.ClientID, redirectURI, state, verifier, resource, scopesFromPRM(prm))
	return connectors.AuthorizeResult{AuthorizationURL: authURL}, nil
}

func (a *Adapter) HandleCallback(ctx context.Context, rawState, code string) (string, connectors.IntegrationStatus, error) {
	st, err := a.Store.ConsumeOAuthState(ctx, connectors.HashState(rawState))
	if err != nil {
		return "", connectors.IntegrationStatus{}, err
	}
	a.mu.Lock()
	a.lastReturnTo = st.ReturnTo
	a.mu.Unlock()

	verifier, err := a.Store.DecryptSecret(st.CodeVerifierEnc)
	if err != nil {
		return st.UserID, connectors.IntegrationStatus{}, err
	}
	clientSecret, err := a.Store.DecryptSecret(st.ClientSecretEnc)
	if err != nil {
		return st.UserID, connectors.IntegrationStatus{}, err
	}
	tok, err := a.exchangeCode(ctx, st.TokenEndpoint, st.ClientID, clientSecret, st.RedirectURI, code, verifier, st.ResourceURL)
	if err != nil {
		return st.UserID, connectors.IntegrationStatus{}, fmt.Errorf("token exchange failed")
	}
	encCreds, err := a.Store.EncryptTokens(bundleFromToken(tok))
	if err != nil {
		return st.UserID, connectors.IntegrationStatus{}, err
	}
	now := time.Now().UTC()
	next := now.Add(time.Hour)
	conn := connectors.Connection{
		UserID:                  st.UserID,
		Provider:                connectors.ProviderGranola,
		Status:                  connectors.StatusConnected,
		EncryptedCredentials:    encCreds,
		CredentialsKeyVersion:   a.Store.Key.Version,
		OAuthClientID:           st.ClientID,
		OAuthClientSecretEnc:    st.ClientSecretEnc,
		TokenEndpoint:           st.TokenEndpoint,
		AuthorizationEndpoint:   st.AuthorizationEndpoint,
		ResourceURL:             st.ResourceURL,
		SyncEnabled:             true,
		InitialSyncStatus:       connectors.InitialSyncPending,
		SyncCursor:              map[string]any{},
		ConnectedAt:             &now,
		NextSyncAt:              &next,
		AccessToken:             tok.AccessToken,
		RefreshToken:            tok.RefreshToken,
		ClientSecret:            clientSecret,
		TokenExpiry:             tok.Expiry,
	}

	caps, acct, err := a.probeConnection(ctx, conn)
	if err != nil {
		// Still store credentials so user can retry; mark error.
		conn.Status = connectors.StatusError
		conn.LastError = "connected but capability discovery failed"
	} else {
		conn.Capabilities = caps
		conn.AccountLabel = acct.Email
		conn.WorkspaceLabel = acct.Workspace
		conn.WorkspaceIdentity = workspaceIdentity(acct)
	}

	saved, err := a.Store.UpsertConnection(ctx, conn)
	if err != nil {
		return st.UserID, connectors.IntegrationStatus{}, err
	}
	return st.UserID, a.Status(ctx, saved), nil
}

func (a *Adapter) probeConnection(ctx context.Context, conn connectors.Connection) (connectors.Capabilities, AccountInfo, error) {
	caller, err := a.OpenMCP(ctx, conn)
	if err != nil {
		return connectors.Capabilities{}, AccountInfo{}, err
	}
	defer caller.Close()

	names, err := caller.ListTools(ctx)
	if err != nil {
		return connectors.Capabilities{}, AccountInfo{}, err
	}
	caps := capabilitiesFromTools(names)

	var acct AccountInfo
	if contains(names, "get_account_info") {
		raw, err := caller.CallTool(ctx, "get_account_info", map[string]any{})
		if err == nil {
			acct = parseAccountInfo(raw)
		}
	}
	return caps, acct, nil
}

func capabilitiesFromTools(names []string) connectors.Capabilities {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	caps := connectors.Capabilities{
		LiveQueryMeetings: set["query_granola_meetings"],
		LiveGetTranscript: set["get_meeting_transcript"],
		ListMeetings:      set["list_meetings"],
		GetMeetings:       set["get_meetings"],
		Transcripts:       set["get_meeting_transcript"],
		Folders:           set["list_meeting_folders"],
	}
	if caps.Transcripts {
		caps.PlanHint = "paid"
	} else {
		caps.PlanHint = "basic"
		days := 30
		caps.HistoryDays = &days
	}
	return caps
}

func (a *Adapter) DiscoverCapabilities(ctx context.Context, conn connectors.Connection) (connectors.Capabilities, error) {
	caps, _, err := a.probeConnection(ctx, conn)
	return caps, err
}

func (a *Adapter) RefreshIfNeeded(ctx context.Context, conn connectors.Connection) (connectors.Connection, error) {
	if conn.AccessToken != "" && (conn.TokenExpiry.IsZero() || time.Now().Add(2*time.Minute).Before(conn.TokenExpiry)) {
		return conn, nil
	}
	tok, err := a.refreshToken(ctx, conn)
	if err != nil {
		return conn, err
	}
	enc, err := a.Store.EncryptTokens(bundleFromToken(tok))
	if err != nil {
		return conn, err
	}
	conn.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		conn.RefreshToken = tok.RefreshToken
	}
	conn.TokenExpiry = tok.Expiry
	conn.EncryptedCredentials = enc
	_, _ = a.Store.UpsertConnection(ctx, conn)
	return conn, nil
}

func (a *Adapter) VerifyWorkspace(ctx context.Context, conn connectors.Connection) error {
	caller, err := a.OpenMCP(ctx, conn)
	if err != nil {
		return err
	}
	defer caller.Close()
	raw, err := caller.CallTool(ctx, "get_account_info", map[string]any{})
	if err != nil {
		// If tool missing, skip verification.
		if strings.Contains(strings.ToLower(err.Error()), "unknown") || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil
		}
		return err
	}
	info := parseAccountInfo(raw)
	identity := workspaceIdentity(info)
	if conn.WorkspaceIdentity != "" && identity != "" && identity != conn.WorkspaceIdentity {
		return fmt.Errorf("granola workspace changed; reconnect required to avoid mixing meetings")
	}
	if conn.WorkspaceIdentity == "" && identity != "" {
		_ = a.Store.PatchConnection(ctx, conn.ID, map[string]any{
			"workspace_identity": identity,
			"account_label":      info.Email,
			"workspace_label":    info.Workspace,
		})
	}
	return nil
}

func (a *Adapter) LiveTools(ctx context.Context, conn connectors.Connection) ([]tools.RegisteredTool, error) {
	return a.buildLiveTools(conn), nil
}

func (a *Adapter) Disconnect(ctx context.Context, conn connectors.Connection) error {
	revokeBestEffort(ctx, conn.TokenEndpoint, conn.OAuthClientID, conn.ClientSecret, conn.AccessToken)
	revokeBestEffort(ctx, conn.TokenEndpoint, conn.OAuthClientID, conn.ClientSecret, conn.RefreshToken)
	return a.Store.ClearCredentials(ctx, conn.ID)
}

func (a *Adapter) DeleteImports(ctx context.Context, conn connectors.Connection) error {
	noteIDs, err := a.Store.ListImportNoteIDs(ctx, conn.UserID, connectors.ProviderGranola)
	if err != nil {
		return err
	}
	kbIDs, err := a.Store.ListImportKbSourceIDs(ctx, conn.UserID, connectors.ProviderGranola)
	if err != nil {
		return err
	}

	// Delete mappings / items first (FKs).
	if conn.ID != "" {
		_ = a.Store.DeleteItemsForConnection(ctx, conn.ID)
	} else if a.Store.DB != nil {
		q := url.Values{}
		q.Set("user_id", "eq."+conn.UserID)
		q.Set("provider", "eq."+connectors.ProviderGranola)
		_ = a.Store.DB.Delete(ctx, "integration_item_sources", q)
		_ = a.Store.DB.Delete(ctx, "integration_items", q)
	}

	for _, id := range noteIDs {
		q := url.Values{}
		q.Set("id", "eq."+id)
		q.Set("user_id", "eq."+conn.UserID)
		_ = a.Store.DB.Delete(ctx, "notes", q)
	}
	for _, id := range kbIDs {
		q := url.Values{}
		q.Set("id", "eq."+id)
		q.Set("user_id", "eq."+conn.UserID)
		_ = a.Store.DB.Delete(ctx, "kb_sources", q)
	}
	// Also purge orphan integration kb_sources for this user.
	q := url.Values{}
	q.Set("user_id", "eq."+conn.UserID)
	q.Set("source_type", "eq.integration")
	_ = a.Store.DB.Delete(ctx, "kb_sources", q)

	if conn.ID != "" {
		_ = a.Store.PatchConnection(ctx, conn.ID, map[string]any{
			"imported_meeting_count":    0,
			"imported_transcript_count": 0,
		})
	}
	return nil
}

func (a *Adapter) Status(ctx context.Context, conn *connectors.Connection) connectors.IntegrationStatus {
	st := connectors.IntegrationStatus{
		Provider:                   connectors.ProviderGranola,
		Status:                     connectors.StatusDisconnected,
		InitialSyncStatus:          connectors.InitialSyncPending,
		RetainsImportsOnDisconnect: true,
		Capabilities:               connectors.Capabilities{},
	}
	if conn == nil {
		return st
	}
	st.Status = conn.Status
	st.AccountLabel = conn.AccountLabel
	st.WorkspaceLabel = conn.WorkspaceLabel
	st.Capabilities = conn.Capabilities
	st.InitialSyncStatus = conn.InitialSyncStatus
	st.ImportedMeetingCount = conn.ImportedMeetingCount
	st.ImportedTranscriptCount = conn.ImportedTranscriptCount
	st.SyncEnabled = conn.SyncEnabled
	st.LastError = conn.LastError
	if conn.LastSyncAt != nil {
		s := conn.LastSyncAt.UTC().Format(time.RFC3339)
		st.LastSyncAt = &s
	}
	if conn.NextSyncAt != nil {
		s := conn.NextSyncAt.UTC().Format(time.RFC3339)
		st.NextSyncAt = &s
	}
	return st
}

func contains(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

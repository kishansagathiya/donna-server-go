package google

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
)

// Adapter implements connectors.ConnectorAdapter for Google Calendar write actions.
type Adapter struct {
	Store        *connectors.Store
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client

	mu           sync.Mutex
	lastReturnTo string
}

func (a *Adapter) Provider() string { return connectors.ProviderGoogle }
func (a *Adapter) Endpoint() string { return "https://www.googleapis.com" }

func (a *Adapter) LastReturnTo() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastReturnTo
}

func (a *Adapter) StartAuthorize(ctx context.Context, userID, returnTo, redirectURI string) (connectors.AuthorizeResult, error) {
	cfg, err := a.oauthConfig(redirectURI)
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
	clientSecretEnc, err := a.Store.EncryptSecret(a.ClientSecret)
	if err != nil {
		return connectors.AuthorizeResult{}, err
	}
	st := connectors.OAuthState{
		UserID:                userID,
		Provider:              connectors.ProviderGoogle,
		StateHash:             connectors.HashState(state),
		CodeVerifierEnc:       verifierEnc,
		ReturnTo:              returnTo,
		RedirectURI:           redirectURI,
		ClientID:              a.ClientID,
		ClientSecretEnc:       clientSecretEnc,
		TokenEndpoint:         googleTokenURL,
		AuthorizationEndpoint: googleAuthURL,
		ResourceURL:           a.Endpoint(),
		ExpiresAt:             time.Now().UTC().Add(connectors.OAuthStateTTL),
	}
	if err := a.Store.SaveOAuthState(ctx, st); err != nil {
		return connectors.AuthorizeResult{}, err
	}
	authURL := cfg.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
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
	tok, err := a.exchangeCode(ctx, st.RedirectURI, code, verifier)
	if err != nil {
		return st.UserID, connectors.IntegrationStatus{}, fmt.Errorf("token exchange failed")
	}
	encCreds, err := a.Store.EncryptTokens(bundleFromToken(tok))
	if err != nil {
		return st.UserID, connectors.IntegrationStatus{}, err
	}

	email, _ := a.fetchAccountEmail(ctx, tok.AccessToken)
	now := time.Now().UTC()
	conn := connectors.Connection{
		UserID:                st.UserID,
		Provider:              connectors.ProviderGoogle,
		Status:                connectors.StatusConnected,
		AccountLabel:          email,
		EncryptedCredentials:  encCreds,
		CredentialsKeyVersion: a.Store.Key.Version,
		OAuthClientID:         st.ClientID,
		OAuthClientSecretEnc:  st.ClientSecretEnc,
		TokenEndpoint:         st.TokenEndpoint,
		AuthorizationEndpoint: st.AuthorizationEndpoint,
		ResourceURL:           st.ResourceURL,
		Capabilities: connectors.Capabilities{
			CalendarWrite: true,
			GmailSend:     true,
		},
		// Write-only provider — no hourly sync imports.
		SyncEnabled:         false,
		InitialSyncStatus:   connectors.InitialSyncCompleted,
		SyncCursor:          map[string]any{},
		ConnectedAt:         &now,
		AccessToken:         tok.AccessToken,
		RefreshToken:        tok.RefreshToken,
		ClientSecret:        a.ClientSecret,
		TokenExpiry:         tokenExpiryOr(tok, time.Hour),
	}
	saved, err := a.Store.UpsertConnection(ctx, conn)
	if err != nil {
		return st.UserID, connectors.IntegrationStatus{}, err
	}
	return st.UserID, a.Status(ctx, saved), nil
}

func (a *Adapter) RefreshIfNeeded(ctx context.Context, conn connectors.Connection) (connectors.Connection, error) {
	if conn.AccessToken != "" && time.Until(conn.TokenExpiry) > 2*time.Minute {
		return conn, nil
	}
	tok, err := a.refreshToken(ctx, conn)
	if err != nil {
		return conn, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = conn.RefreshToken
	}
	encCreds, err := a.Store.EncryptTokens(bundleFromToken(tok))
	if err != nil {
		return conn, err
	}
	conn.AccessToken = tok.AccessToken
	conn.RefreshToken = tok.RefreshToken
	conn.TokenExpiry = tokenExpiryOr(tok, time.Hour)
	conn.EncryptedCredentials = encCreds
	if err := a.Store.PatchConnection(ctx, conn.ID, map[string]any{
		"encrypted_credentials":   encCreds,
		"credentials_key_version": a.Store.Key.Version,
		"status":                  connectors.StatusConnected,
		"last_error":              nil,
	}); err != nil {
		return conn, err
	}
	return conn, nil
}

func (a *Adapter) DiscoverCapabilities(ctx context.Context, conn connectors.Connection) (connectors.Capabilities, error) {
	_ = ctx
	_ = conn
	return connectors.Capabilities{CalendarWrite: true, GmailSend: true}, nil
}

func (a *Adapter) VerifyWorkspace(ctx context.Context, conn connectors.Connection) error {
	_ = ctx
	_ = conn
	return nil
}

func (a *Adapter) LiveTools(ctx context.Context, conn connectors.Connection) ([]tools.RegisteredTool, error) {
	_ = ctx
	_ = conn
	return nil, nil
}

func (a *Adapter) RunSync(ctx context.Context, conn connectors.Connection, full bool) (connectors.SyncResult, error) {
	_ = full
	// No meeting import for Google Calendar — refresh profile email only.
	refreshed, err := a.RefreshIfNeeded(ctx, conn)
	if err != nil {
		return connectors.SyncResult{Status: connectors.StatusReauthRequired, Error: err.Error()}, err
	}
	if email, err := a.fetchAccountEmail(ctx, refreshed.AccessToken); err == nil && email != "" {
		_ = a.Store.PatchConnection(ctx, refreshed.ID, map[string]any{
			"account_label": email,
		})
	}
	return connectors.SyncResult{Status: "completed"}, nil
}

func (a *Adapter) Disconnect(ctx context.Context, conn connectors.Connection) error {
	return a.Store.ClearCredentials(ctx, conn.ID)
}

func (a *Adapter) DeleteImports(ctx context.Context, conn connectors.Connection) error {
	_ = ctx
	_ = conn
	return nil
}

func (a *Adapter) Status(ctx context.Context, conn *connectors.Connection) connectors.IntegrationStatus {
	_ = ctx
	st := connectors.IntegrationStatus{
		Provider:                   connectors.ProviderGoogle,
		Status:                     connectors.StatusDisconnected,
		Capabilities:               connectors.Capabilities{},
		InitialSyncStatus:          connectors.InitialSyncCompleted,
		SyncEnabled:                false,
		RetainsImportsOnDisconnect: false,
		Enabled:                    true,
	}
	if conn == nil {
		return st
	}
	st.Status = conn.Status
	if st.Status == "" {
		st.Status = connectors.StatusDisconnected
	}
	st.AccountLabel = conn.AccountLabel
	st.WorkspaceLabel = conn.WorkspaceLabel
	st.Capabilities = conn.Capabilities
	if conn.Status == connectors.StatusConnected || conn.Status == connectors.StatusPartial {
		if !st.Capabilities.CalendarWrite {
			st.Capabilities.CalendarWrite = true
		}
		if !st.Capabilities.GmailSend {
			st.Capabilities.GmailSend = true
		}
	}
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

// CreateCalendarEvent creates a Google Calendar event for a connected user.
func (a *Adapter) CreateCalendarEvent(ctx context.Context, conn connectors.Connection, input map[string]any) (map[string]any, error) {
	refreshed, err := a.requireAccessToken(ctx, conn)
	if err != nil {
		return nil, err
	}
	return a.createEvent(ctx, refreshed.AccessToken, input)
}

// SendEmail sends an email via Gmail for a connected user.
func (a *Adapter) SendEmail(ctx context.Context, conn connectors.Connection, input map[string]any) (map[string]any, error) {
	refreshed, err := a.requireAccessToken(ctx, conn)
	if err != nil {
		return nil, err
	}
	return a.sendEmail(ctx, refreshed.AccessToken, input)
}

func (a *Adapter) requireAccessToken(ctx context.Context, conn connectors.Connection) (connectors.Connection, error) {
	refreshed, err := a.RefreshIfNeeded(ctx, conn)
	if err != nil {
		return connectors.Connection{}, fmt.Errorf("reauth_required")
	}
	if refreshed.Status != connectors.StatusConnected && refreshed.Status != connectors.StatusPartial {
		return connectors.Connection{}, fmt.Errorf("needs_integration:google")
	}
	if strings.TrimSpace(refreshed.AccessToken) == "" {
		return connectors.Connection{}, fmt.Errorf("needs_integration:google")
	}
	return refreshed, nil
}

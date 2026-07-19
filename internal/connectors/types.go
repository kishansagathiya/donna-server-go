package connectors

import (
	"context"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
)

const (
	ProviderGranola = "granola"

	StatusDisconnected   = "disconnected"
	StatusConnecting     = "connecting"
	StatusConnected      = "connected"
	StatusSyncing        = "syncing"
	StatusReauthRequired = "reauth_required"
	StatusError          = "error"
	StatusPartial        = "partial"

	InitialSyncPending   = "pending"
	InitialSyncRunning   = "running"
	InitialSyncCompleted = "completed"
	InitialSyncPartial   = "partial"
	InitialSyncFailed    = "failed"

	ReturnToWeb    = "web"
	ReturnToMobile = "mobile"

	// MaxLiveConnectorCallsPerTurn limits Granola (and future) live MCP tools
	// inside the existing 45s tool budget.
	MaxLiveConnectorCallsPerTurn = 3

	OAuthStateTTL = 10 * time.Minute
)

// Capabilities describes what a connected provider can do for this user/plan.
type Capabilities struct {
	LiveQueryMeetings bool `json:"live_query_meetings"`
	LiveGetTranscript bool `json:"live_get_transcript"`
	ListMeetings      bool `json:"list_meetings"`
	GetMeetings       bool `json:"get_meetings"`
	Transcripts       bool `json:"transcripts"`
	Folders           bool `json:"folders"`
	HistoryDays       *int `json:"history_days,omitempty"` // e.g. 30 for Basic
	PlanHint          string `json:"plan_hint,omitempty"`  // "basic" | "paid" | ""
}

// IntegrationStatus is the shared REST/UI status payload.
type IntegrationStatus struct {
	Provider                   string       `json:"provider"`
	Status                     string       `json:"status"`
	AccountLabel               string       `json:"account_label,omitempty"`
	WorkspaceLabel             string       `json:"workspace_label,omitempty"`
	Capabilities               Capabilities `json:"capabilities"`
	InitialSyncStatus          string       `json:"initial_sync_status"`
	ImportedMeetingCount       int          `json:"imported_meeting_count"`
	ImportedTranscriptCount    int          `json:"imported_transcript_count"`
	SyncEnabled                bool         `json:"sync_enabled"`
	LastSyncAt                 *string      `json:"last_sync_at,omitempty"`
	NextSyncAt                 *string      `json:"next_sync_at,omitempty"`
	LastError                  string       `json:"last_error,omitempty"`
	RetainsImportsOnDisconnect bool         `json:"retains_imports_on_disconnect"`
	Enabled                    bool         `json:"enabled"`
}

// AuthorizeResult is returned by POST .../authorize.
type AuthorizeResult struct {
	AuthorizationURL string `json:"authorization_url"`
}

// SyncResult summarizes one sync/import job.
type SyncResult struct {
	Status           string `json:"status"` // completed | partial | failed | reauth_required
	MeetingsUpserted int    `json:"meetings_upserted"`
	TranscriptsSaved int    `json:"transcripts_saved"`
	Error            string `json:"error,omitempty"`
}

// ConnectorAdapter is the provider contract for Donna integrations.
type ConnectorAdapter interface {
	Provider() string
	// Endpoint is the only allowed MCP URL for this provider (no arbitrary URLs).
	Endpoint() string

	StartAuthorize(ctx context.Context, userID, returnTo, redirectURI string) (AuthorizeResult, error)
	HandleCallback(ctx context.Context, rawState, code string) (userID string, status IntegrationStatus, err error)
	RefreshIfNeeded(ctx context.Context, conn Connection) (Connection, error)
	DiscoverCapabilities(ctx context.Context, conn Connection) (Capabilities, error)
	VerifyWorkspace(ctx context.Context, conn Connection) error

	LiveTools(ctx context.Context, conn Connection) ([]tools.RegisteredTool, error)
	RunSync(ctx context.Context, conn Connection, full bool) (SyncResult, error)

	Disconnect(ctx context.Context, conn Connection) error
	DeleteImports(ctx context.Context, conn Connection) error
	Status(ctx context.Context, conn *Connection) IntegrationStatus
}

// Connection is the persisted integration_connections row (credentials decrypted only in-memory).
type Connection struct {
	ID                    string
	UserID                string
	Provider              string
	Status                string
	AccountLabel          string
	WorkspaceLabel        string
	WorkspaceIdentity     string
	EncryptedCredentials  string
	CredentialsKeyVersion int
	OAuthClientID         string
	OAuthClientSecretEnc  string
	TokenEndpoint         string
	AuthorizationEndpoint string
	ResourceURL           string
	Capabilities          Capabilities
	SyncEnabled           bool
	SyncCursor            map[string]any
	InitialSyncStatus     string
	ImportedMeetingCount  int
	ImportedTranscriptCount int
	LastSyncAt            *time.Time
	NextSyncAt            *time.Time
	LastError             string
	SyncLeaseOwner        string
	SyncLeaseUntil        *time.Time
	ConnectedAt           *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time

	// Decrypted in-memory only — never logged or returned.
	AccessToken  string `json:"-"`
	RefreshToken string `json:"-"`
	ClientSecret string `json:"-"`
	TokenExpiry  time.Time
}

// TokenBundle is encrypted at rest.
type TokenBundle struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
}

package connectors_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	appauth "github.com/kishansagathiya/donna/donna-server-go/internal/auth"
	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
	"github.com/kishansagathiya/donna/donna-server-go/internal/pipeline/tools"
)

type fakeAdapter struct {
	provider string
}

func (f *fakeAdapter) Provider() string { return f.provider }
func (f *fakeAdapter) Endpoint() string { return "https://mcp.granola.ai/mcp" }
func (f *fakeAdapter) StartAuthorize(ctx context.Context, userID, returnTo, redirectURI string) (connectors.AuthorizeResult, error) {
	return connectors.AuthorizeResult{AuthorizationURL: "https://auth.example/authorize?u=" + userID}, nil
}
func (f *fakeAdapter) HandleCallback(ctx context.Context, rawState, code string) (string, connectors.IntegrationStatus, error) {
	return "user-a", connectors.IntegrationStatus{Provider: f.provider}, nil
}
func (f *fakeAdapter) RefreshIfNeeded(ctx context.Context, conn connectors.Connection) (connectors.Connection, error) {
	return conn, nil
}
func (f *fakeAdapter) DiscoverCapabilities(ctx context.Context, conn connectors.Connection) (connectors.Capabilities, error) {
	return connectors.Capabilities{}, nil
}
func (f *fakeAdapter) VerifyWorkspace(ctx context.Context, conn connectors.Connection) error {
	return nil
}
func (f *fakeAdapter) LiveTools(ctx context.Context, conn connectors.Connection) ([]tools.RegisteredTool, error) {
	return nil, nil
}
func (f *fakeAdapter) RunSync(ctx context.Context, conn connectors.Connection, full bool) (connectors.SyncResult, error) {
	return connectors.SyncResult{}, nil
}
func (f *fakeAdapter) Disconnect(ctx context.Context, conn connectors.Connection) error {
	return nil
}
func (f *fakeAdapter) DeleteImports(ctx context.Context, conn connectors.Connection) error {
	return nil
}
func (f *fakeAdapter) Status(ctx context.Context, conn *connectors.Connection) connectors.IntegrationStatus {
	st := connectors.IntegrationStatus{
		Provider:                   f.provider,
		Status:                     connectors.StatusDisconnected,
		RetainsImportsOnDisconnect: true,
	}
	if conn != nil {
		st.Status = conn.Status
		st.AccountLabel = conn.AccountLabel
	}
	return st
}

func authAs(userID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), appauth.UserIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func TestIntegrationsDisabledReturnsEmpty(t *testing.T) {
	h := &connectors.Handler{Service: &connectors.Service{IntegrationsEnabled: false}}
	r := chi.NewRouter()
	connectors.RegisterRoutes(r, authAs("user-a"), h)
	req := httptest.NewRequest(http.MethodGet, "/integrations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if _, ok := body["integrations"]; !ok {
		t.Fatalf("body: %s", rec.Body.String())
	}
}

func TestAuthorizeRequiresEnabledAndAuth(t *testing.T) {
	keyRaw := make([]byte, 32)
	for i := range keyRaw {
		keyRaw[i] = 9
	}
	key, _ := connectors.ParseEncryptionKey(base64.StdEncoding.EncodeToString(keyRaw))
	reg := connectors.NewRegistry()
	reg.Register(&fakeAdapter{provider: connectors.ProviderGranola})
	svc := &connectors.Service{
		Registry:            reg,
		Store:               &connectors.Store{Key: key},
		IntegrationsEnabled: true,
		GranolaEnabled:      true,
		PublicAPIBase:       "https://api.example",
	}
	h := &connectors.Handler{Service: svc}
	r := chi.NewRouter()
	connectors.RegisterRoutes(r, authAs("user-a"), h)

	req := httptest.NewRequest(http.MethodPost, "/integrations/granola/authorize", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = http.NoBody
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("expected failure without body")
	}
}

func TestDeleteImportsRequiresConfirm(t *testing.T) {
	keyRaw := make([]byte, 32)
	key, _ := connectors.ParseEncryptionKey(base64.StdEncoding.EncodeToString(keyRaw))
	reg := connectors.NewRegistry()
	reg.Register(&fakeAdapter{provider: connectors.ProviderGranola})
	svc := &connectors.Service{
		Registry:            reg,
		Store:               &connectors.Store{Key: key},
		IntegrationsEnabled: true,
		GranolaEnabled:      true,
	}
	h := &connectors.Handler{Service: svc}
	r := chi.NewRouter()
	connectors.RegisterRoutes(r, authAs("user-a"), h)
	req := httptest.NewRequest(http.MethodDelete, "/integrations/granola/imports", strings.NewReader(`{"confirm":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

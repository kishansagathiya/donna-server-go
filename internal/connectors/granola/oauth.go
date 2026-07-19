package granola

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
)

const (
	// Fixed Donna-owned endpoint — never accept arbitrary MCP URLs.
	MCPEndpoint = "https://mcp.granola.ai/mcp"
)

// discoverOAuth loads protected-resource + auth-server metadata and registers a client via DCR.
func (a *Adapter) discoverOAuth(ctx context.Context, redirectURI string) (*oauthex.AuthServerMeta, *oauthex.ClientRegistrationResponse, *oauthex.ProtectedResourceMetadata, error) {
	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	var prm *oauthex.ProtectedResourceMetadata
	var lastErr error
	for _, metaURL := range []string{
		"https://mcp.granola.ai/.well-known/oauth-protected-resource",
		"https://mcp.granola.ai/.well-known/oauth-protected-resource/mcp",
	} {
		prm, lastErr = oauthex.GetProtectedResourceMetadata(ctx, metaURL, MCPEndpoint, client)
		if lastErr == nil && prm != nil && len(prm.AuthorizationServers) > 0 {
			break
		}
		prm = nil
	}
	if prm == nil {
		// Fallback: treat MCP host as authorization server (older MCP auth draft).
		prm = &oauthex.ProtectedResourceMetadata{
			Resource:             MCPEndpoint,
			AuthorizationServers: []string{"https://mcp.granola.ai"},
		}
	}

	issuer := prm.AuthorizationServers[0]
	asm, err := auth.GetAuthServerMetadata(ctx, issuer, client)
	if err != nil || asm == nil {
		asm, err = oauthex.GetAuthServerMeta(ctx, strings.TrimRight(issuer, "/")+"/.well-known/oauth-authorization-server", issuer, client)
	}
	if err != nil || asm == nil {
		asm = &oauthex.AuthServerMeta{
			Issuer:                issuer,
			AuthorizationEndpoint: strings.TrimRight(issuer, "/") + "/authorize",
			TokenEndpoint:         strings.TrimRight(issuer, "/") + "/token",
			RegistrationEndpoint:  strings.TrimRight(issuer, "/") + "/register",
		}
	}
	if asm.RegistrationEndpoint == "" {
		return nil, nil, nil, fmt.Errorf("granola authorization server does not advertise dynamic client registration")
	}

	meta := &oauthex.ClientRegistrationMetadata{
		ClientName:              "Donna",
		RedirectURIs:            []string{redirectURI},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
		ApplicationType:         "web",
	}
	reg, err := oauthex.RegisterClient(ctx, asm.RegistrationEndpoint, meta, client)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dynamic client registration failed: %w", err)
	}
	return asm, reg, prm, nil
}

func pkceChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (a *Adapter) buildAuthorizeURL(asm *oauthex.AuthServerMeta, clientID, redirectURI, state, verifier, resource string, scopes []string) string {
	cfg := &oauth2.Config{
		ClientID:    clientID,
		RedirectURL: redirectURI,
		Scopes:      scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  asm.AuthorizationEndpoint,
			TokenURL: asm.TokenEndpoint,
		},
	}
	opts := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(verifier),
		oauth2.AccessTypeOffline,
	}
	if resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", resource))
	}
	return cfg.AuthCodeURL(state, opts...)
}

func (a *Adapter) exchangeCode(ctx context.Context, tokenURL, clientID, clientSecret, redirectURI, code, verifier, resource string) (*oauth2.Token, error) {
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Endpoint: oauth2.Endpoint{
			TokenURL:  tokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	opts := []oauth2.AuthCodeOption{
		oauth2.VerifierOption(verifier),
	}
	if resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", resource))
	}
	return cfg.Exchange(ctx, code, opts...)
}

func (a *Adapter) refreshToken(ctx context.Context, conn connectors.Connection) (*oauth2.Token, error) {
	if conn.RefreshToken == "" || conn.TokenEndpoint == "" {
		return nil, fmt.Errorf("refresh unavailable")
	}
	cfg := &oauth2.Config{
		ClientID:     conn.OAuthClientID,
		ClientSecret: conn.ClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL:  conn.TokenEndpoint,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	src := cfg.TokenSource(ctx, &oauth2.Token{
		RefreshToken: conn.RefreshToken,
		Expiry:       conn.TokenExpiry,
	})
	tok, err := src.Token()
	if err != nil {
		return nil, err
	}
	return tok, nil
}

// staticOAuthHandler supplies a bearer token (with optional refresh) to the MCP transport.
type staticOAuthHandler struct {
	token *oauth2.Token
}

func (h *staticOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	if h.token == nil {
		return nil, nil
	}
	return oauth2.StaticTokenSource(h.token), nil
}

func (h *staticOAuthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return fmt.Errorf("reauth_required")
}

func bundleFromToken(tok *oauth2.Token) connectors.TokenBundle {
	b := connectors.TokenBundle{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
	}
	if b.Expiry.IsZero() && tok.ExpiresIn > 0 {
		b.Expiry = time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return b
}

// revokeBestEffort attempts token revocation when the AS advertises a revoke endpoint.
func revokeBestEffort(ctx context.Context, tokenEndpoint, clientID, clientSecret, token string) {
	if token == "" || tokenEndpoint == "" {
		return
	}
	// Common pattern: replace /token with /revoke
	revokeURL := strings.Replace(tokenEndpoint, "/token", "/revoke", 1)
	if revokeURL == tokenEndpoint {
		return
	}
	form := url.Values{}
	form.Set("token", token)
	form.Set("client_id", clientID)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
}

func scopesFromPRM(prm *oauthex.ProtectedResourceMetadata) []string {
	if prm == nil || len(prm.ScopesSupported) == 0 {
		return nil
	}
	return prm.ScopesSupported
}

// decodeJSON is a tiny helper for tests/mocks.
func decodeJSON(r io.Reader, dest any) error {
	return json.NewDecoder(r).Decode(dest)
}

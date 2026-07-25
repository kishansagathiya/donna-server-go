package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/kishansagathiya/donna/donna-server-go/internal/connectors"
)

const (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
	userInfoURL    = "https://www.googleapis.com/oauth2/v2/userinfo"
)

var googleScopes = []string{
	"openid",
	"email",
	// Full calendar scope is what Google's create-events guide requires for
	// reliable events.insert; calendar.events alone often 403s in practice.
	"https://www.googleapis.com/auth/calendar",
	"https://www.googleapis.com/auth/gmail.send",
}

func (a *Adapter) oauthConfig(redirectURI string) (*oauth2.Config, error) {
	clientID := strings.TrimSpace(a.ClientID)
	clientSecret := strings.TrimSpace(a.ClientSecret)
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("google_oauth_not_configured")
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURI,
		Scopes:       googleScopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:   googleAuthURL,
			TokenURL:  googleTokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}, nil
}

func (a *Adapter) exchangeCode(ctx context.Context, redirectURI, code, verifier string) (*oauth2.Token, error) {
	cfg, err := a.oauthConfig(redirectURI)
	if err != nil {
		return nil, err
	}
	return cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
}

func (a *Adapter) refreshToken(ctx context.Context, conn connectors.Connection) (*oauth2.Token, error) {
	if conn.RefreshToken == "" {
		return nil, fmt.Errorf("refresh unavailable")
	}
	clientID := conn.OAuthClientID
	if clientID == "" {
		clientID = a.ClientID
	}
	clientSecret := conn.ClientSecret
	if clientSecret == "" {
		clientSecret = a.ClientSecret
	}
	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   googleAuthURL,
			TokenURL:  googleTokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	src := cfg.TokenSource(ctx, &oauth2.Token{
		RefreshToken: conn.RefreshToken,
		Expiry:       conn.TokenExpiry,
	})
	return src.Token()
}

func (a *Adapter) fetchAccountEmail(ctx context.Context, accessToken string) (string, error) {
	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("userinfo_failed:%d", res.StatusCode)
	}
	var payload struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Email), nil
}

func bundleFromToken(tok *oauth2.Token) connectors.TokenBundle {
	return connectors.TokenBundle{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       tok.Expiry,
		TokenType:    tok.TokenType,
	}
}

func tokenExpiryOr(tok *oauth2.Token, fallback time.Duration) time.Time {
	if tok != nil && !tok.Expiry.IsZero() {
		return tok.Expiry
	}
	return time.Now().UTC().Add(fallback)
}

package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

type Config struct {
	SupabaseURL string
	JWTAudience string
}

type VerifiedUser struct {
	UserID  string
	Token   jwt.Token
}

var (
	cacheMu sync.Mutex
	caches  = map[string]*jwk.Cache{}
)

func issuer(cfg Config) string {
	return cfg.SupabaseURL + "/auth/v1"
}

func jwksURL(cfg Config) string {
	return issuer(cfg) + "/.well-known/jwks.json"
}

func getCache(cfg Config) *jwk.Cache {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	if c, ok := caches[cfg.SupabaseURL]; ok {
		return c
	}

	c := jwk.NewCache(context.Background())
	_ = c.Register(jwksURL(cfg), jwk.WithMinRefreshInterval(15*time.Minute))
	caches[cfg.SupabaseURL] = c
	return c
}

func VerifyAccessToken(ctx context.Context, token string, cfg Config) (*VerifiedUser, error) {
	cache := getCache(cfg)
	keySet, err := cache.Get(ctx, jwksURL(cfg))
	if err != nil {
		return nil, fmt.Errorf("jwks fetch: %w", err)
	}

	parsed, err := jwt.Parse(
		[]byte(token),
		jwt.WithKeySet(keySet),
		jwt.WithIssuer(issuer(cfg)),
		jwt.WithAudience(cfg.JWTAudience),
		jwt.WithValidate(true),
	)
	if err != nil {
		return nil, err
	}

	sub, ok := parsed.Get("sub")
	if !ok {
		return nil, fmt.Errorf("missing_subject")
	}

	userID, ok := sub.(string)
	if !ok || userID == "" {
		return nil, fmt.Errorf("missing_subject")
	}

	return &VerifiedUser{UserID: userID, Token: parsed}, nil
}

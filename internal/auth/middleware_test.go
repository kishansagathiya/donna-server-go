package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func TestRequireAuth_disabled(t *testing.T) {
	handler := RequireAuth(MiddlewareConfig{RequireAuth: false})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertErrorCode(t, rec, "auth_required")
}

func TestRequireAuth_missingToken(t *testing.T) {
	handler := RequireAuth(MiddlewareConfig{RequireAuth: true, Auth: Config{SupabaseURL: "http://example.com"}})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertErrorCode(t, rec, "missing_token")
}

func TestRequireAuth_invalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	handler := RequireAuth(MiddlewareConfig{
		RequireAuth: true,
		Auth:        Config{SupabaseURL: srv.URL, JWTAudience: "authenticated"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-jwt")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertErrorCode(t, rec, "invalid_token")
}

func TestRequireAuth_validToken(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	privJWK, err := jwk.FromRaw(privKey)
	if err != nil {
		t.Fatal(err)
	}
	_ = privJWK.Set(jwk.KeyIDKey, "test-key")
	_ = privJWK.Set(jwk.AlgorithmKey, jwa.RS256)

	pubJWK, err := jwk.PublicKeyOf(privJWK)
	if err != nil {
		t.Fatal(err)
	}
	_ = pubJWK.Set(jwk.KeyIDKey, "test-key")
	_ = pubJWK.Set(jwk.AlgorithmKey, jwa.RS256)

	set := jwk.NewSet()
	set.AddKey(pubJWK)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/v1/.well-known/jwks.json" {
			_ = json.NewEncoder(w).Encode(set)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{SupabaseURL: srv.URL, JWTAudience: "authenticated"}
	token, err := jwt.NewBuilder().
		Issuer(issuer(cfg)).
		Audience([]string{"authenticated"}).
		Subject("user-123").
		IssuedAt(time.Now()).
		Expiration(time.Now().Add(time.Hour)).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.RS256, privJWK))
	if err != nil {
		t.Fatal(err)
	}

	var capturedUserID string
	handler := RequireAuth(MiddlewareConfig{RequireAuth: true, Auth: cfg})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserIDFromContext(r.Context())
			if !ok {
				t.Fatal("missing user id in context")
			}
			capturedUserID = userID
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+string(signed))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if capturedUserID != "user-123" {
		t.Fatalf("userID = %q, want user-123", capturedUserID)
	}
}

func TestRequireAuth_queryTokenFallback(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	privJWK, err := jwk.FromRaw(privKey)
	if err != nil {
		t.Fatal(err)
	}
	_ = privJWK.Set(jwk.KeyIDKey, "test-key")
	_ = privJWK.Set(jwk.AlgorithmKey, jwa.RS256)

	pubJWK, err := jwk.PublicKeyOf(privJWK)
	if err != nil {
		t.Fatal(err)
	}
	_ = pubJWK.Set(jwk.KeyIDKey, "test-key")
	_ = pubJWK.Set(jwk.AlgorithmKey, jwa.RS256)

	set := jwk.NewSet()
	set.AddKey(pubJWK)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/v1/.well-known/jwks.json" {
			_ = json.NewEncoder(w).Encode(set)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{SupabaseURL: srv.URL, JWTAudience: "authenticated"}
	token, err := jwt.NewBuilder().
		Issuer(issuer(cfg)).
		Audience([]string{"authenticated"}).
		Subject("user-query").
		IssuedAt(time.Now()).
		Expiration(time.Now().Add(time.Hour)).
		Build()
	if err != nil {
		t.Fatal(err)
	}

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.RS256, privJWK))
	if err != nil {
		t.Fatal(err)
	}

	var capturedUserID string
	handler := RequireAuth(MiddlewareConfig{RequireAuth: true, Auth: cfg})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserIDFromContext(r.Context())
			if !ok {
				t.Fatal("missing user id in context")
			}
			capturedUserID = userID
			w.WriteHeader(http.StatusOK)
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/?token="+string(signed), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if capturedUserID != "user-query" {
		t.Fatalf("userID = %q, want user-query", capturedUserID)
	}
}

func TestBearerToken(t *testing.T) {
	if got := bearerToken("Bearer abc123"); got != "abc123" {
		t.Fatalf("bearerToken = %q", got)
	}
	if got := bearerToken("Basic abc"); got != "" {
		t.Fatalf("bearerToken basic = %q", got)
	}
}

func TestUserIDFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), UserIDKey, "user-1")
	userID, ok := UserIDFromContext(ctx)
	if !ok || userID != "user-1" {
		t.Fatalf("got (%q, %v)", userID, ok)
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != code {
		t.Fatalf("error = %q, want %q", body["error"], code)
	}
}

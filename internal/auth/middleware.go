package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

const UserIDKey contextKey = "userId"

type MiddlewareConfig struct {
	RequireAuth bool
	Auth        Config
}

func RequireAuth(cfg MiddlewareConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.RequireAuth {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "auth_required"})
				return
			}

			token := bearerToken(r.Header.Get("Authorization"))
			if token == "" {
				token = r.URL.Query().Get("token")
			}
			if token == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing_token"})
				return
			}

			verified, err := VerifyAccessToken(r.Context(), token, cfg.Auth)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
				return
			}

			ctx := context.WithValue(r.Context(), UserIDKey, verified.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(UserIDKey).(string)
	return userID, ok
}

func bearerToken(header string) string {
	if strings.HasPrefix(header, "Bearer ") {
		return header[7:]
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

package notes

import (
	"encoding/json"
	"net/http"
)

const webClientHeader = "X-Donna-Client"

func RequireWebClient(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(webClientHeader) != "web" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "web_client_required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

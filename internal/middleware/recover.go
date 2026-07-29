package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
)

// RecoverWithErrorLog replaces chi's Recoverer: it reports panics to the
// error hook (GitHub issue reporting) before returning a 500.
func RecoverWithErrorLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic recovered", map[string]any{
					"error": fmt.Sprint(rec),
					"path":  r.URL.Path,
					"stack": string(debug.Stack()),
				})
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// requireBearer returns middleware that enforces a fixed Bearer token on the
// Authorization header. The comparison is constant-time so a leaked timing
// signal cannot reveal the secret prefix-by-prefix.
//
// An empty configured token disables the route (all requests get 401) — this
// is intentional so a misconfigured deployment fails closed instead of open.
func requireBearer(token string) func(http.Handler) http.Handler {
	want := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(want) == 0 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			header := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			got := []byte(header[len(prefix):])
			if subtle.ConstantTimeCompare(got, want) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

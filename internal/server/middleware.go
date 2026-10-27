package server

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// requireBearer returns middleware that enforces a fixed Bearer token on the
// Authorization header, matching the scheme case-insensitively. The token
// comparison is constant-time so a leaked timing signal cannot reveal the
// secret prefix-by-prefix.
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
			// The auth scheme is a case-insensitive token (RFC 9110 §11.1).
			scheme, got, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

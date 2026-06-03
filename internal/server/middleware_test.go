package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireBearer(t *testing.T) {
	const secret = "s3cret"
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	cases := []struct {
		name       string
		header     string
		wantStatus int
		wantCalled bool
	}{
		{"missing header", "", http.StatusUnauthorized, false},
		{"wrong scheme", "Basic " + secret, http.StatusUnauthorized, false},
		{"wrong token", "Bearer wrong-token", http.StatusUnauthorized, false},
		{"empty bearer", "Bearer ", http.StatusUnauthorized, false},
		{"valid token", "Bearer " + secret, http.StatusNoContent, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodPost, "/v1/deploy", http.NoBody)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rec := httptest.NewRecorder()
			requireBearer(secret)(next).ServeHTTP(rec, req)

			if rec.Code != c.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, c.wantStatus)
			}
			if called != c.wantCalled {
				t.Errorf("next called = %v, want %v", called, c.wantCalled)
			}
		})
	}
}

func TestRequireBearer_EmptyConfiguredTokenFailsClosed(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/deploy", http.NoBody)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	requireBearer("")(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if called {
		t.Error("next handler was called with empty configured token")
	}
}

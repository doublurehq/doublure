package preview_test

import (
	"testing"

	"github.com/doublurehq/doublure/internal/preview"
)

func TestParsePRURL(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantOwner string
		wantRepo  string
		wantNum   int
		wantOK    bool
	}{
		{"valid", "https://github.com/acme/backend-1/pull/42", "acme", "backend-1", 42, true},
		{"trailing path", "https://github.com/acme/backend-1/pull/42/files", "acme", "backend-1", 42, true},
		{"non-github host", "https://gitlab.com/acme/backend-1/pull/42", "", "", 0, false},
		{"missing number", "https://github.com/acme/backend-1/pull/", "", "", 0, false},
		{"number overflow", "https://github.com/acme/backend-1/pull/99999999999999999999", "", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, repo, num, ok := preview.ParsePRURL(tc.in)
			if owner != tc.wantOwner || repo != tc.wantRepo || num != tc.wantNum || ok != tc.wantOK {
				t.Errorf("ParsePRURL(%q) = (%q, %q, %d, %v), want (%q, %q, %d, %v)",
					tc.in, owner, repo, num, ok, tc.wantOwner, tc.wantRepo, tc.wantNum, tc.wantOK)
			}
		})
	}
}

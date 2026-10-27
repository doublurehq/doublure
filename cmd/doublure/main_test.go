package main

import (
	"testing"
	"time"
)

func TestParseCleanupTTL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		want    time.Duration
		wantErr string
	}{
		{raw: "", want: 0},
		{raw: "168h", want: 168 * time.Hour},
		{raw: "0", wantErr: `CLEANUP_TTL must be a positive duration, got "0"`},
		{raw: "-1h", wantErr: `CLEANUP_TTL must be a positive duration, got "-1h"`},
		{raw: "abc", wantErr: `CLEANUP_TTL must be a positive duration, got "abc"`},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, err := parseCleanupTTL(tc.raw)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("parseCleanupTTL(%q) err = %v, want %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCleanupTTL(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("parseCleanupTTL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

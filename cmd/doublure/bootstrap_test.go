package main

import (
	"errors"
	"fmt"
	"regexp"
	"testing"
)

func TestDefaultAppName(t *testing.T) {
	t.Parallel()

	re := regexp.MustCompile(`^doublure-dev-[0-9a-f]{6}$`)
	name := defaultAppName()
	if !re.MatchString(name) {
		t.Fatalf("defaultAppName() = %q, want match %s", name, re)
	}
	if flag := bootstrapAppCmd().Flags().Lookup("name"); !re.MatchString(flag.DefValue) {
		t.Fatalf("--name default = %q, want match %s", flag.DefValue, re)
	}
}

func TestResolveWebhookURL(t *testing.T) {
	t.Parallel()

	errNgrok := errors.New("ngrok down")
	const tunnel = "https://abc.ngrok-free.app"

	tests := []resolveCase{
		{name: "bare host", flag: "https://d.example.com", want: "https://d.example.com/webhooks/github"},
		{name: "trailing slash", flag: "https://d.example.com/", want: "https://d.example.com/webhooks/github"},
		{name: "double slash", flag: "https://d.example.com//", want: "https://d.example.com/webhooks/github"},
		{
			name: "full endpoint",
			flag: "https://d.example.com/webhooks/github",
			want: "https://d.example.com/webhooks/github",
		},
		{
			name: "full endpoint trailing slash",
			flag: "https://d.example.com/webhooks/github/",
			want: "https://d.example.com/webhooks/github",
		},
		{
			name: "base path",
			flag: "https://x.example.com/doublure",
			want: "https://x.example.com/doublure/webhooks/github",
		},
		{
			name: "encoded segment",
			flag: "https://x.example.com/a%2Fb",
			want: "https://x.example.com/a%2Fb/webhooks/github",
		},
		{name: "uppercase scheme", flag: "HTTPS://d.example.com", want: "https://d.example.com/webhooks/github"},
		{name: "port", flag: "https://d.example.com:8443", want: "https://d.example.com:8443/webhooks/github"},
		{name: "ipv6", flag: "https://[::1]", want: "https://[::1]/webhooks/github"},
		{
			name: "endpoint with suffix is a base",
			flag: "https://d.example.com/webhooks/github/x",
			want: "https://d.example.com/webhooks/github/x/webhooks/github",
		},
		{name: "ngrok tunnel", flag: "", want: "https://abc.ngrok-free.app/webhooks/github"},
		{name: "ngrok error", flag: "", ngrokErr: errNgrok, wantErr: errNgrok},
		{name: "http", flag: "http://d.example.com"},
		{name: "no scheme", flag: "d.example.com"},
		{name: "opaque", flag: "https:d.example.com"},
		{name: "empty host", flag: "https://"},
		{name: "port only", flag: "https://:443"},
		{name: "empty port", flag: "https://d.example.com:"},
		{name: "credentials", flag: "https://u:p@d.example.com"},
		{name: "empty userinfo", flag: "https://@d.example.com"},
		{name: "query", flag: "https://d.example.com?x=1"},
		{name: "empty query", flag: "https://d.example.com?"},
		{name: "fragment", flag: "https://d.example.com#f"},
		{name: "empty fragment", flag: "https://d.example.com#"},
		{name: "leading space", flag: " https://d.example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			ngrok := func() (string, error) {
				calls++
				if tc.ngrokErr != nil {
					return "", tc.ngrokErr
				}
				return tunnel, nil
			}
			got, err := resolveWebhookURL(tc.flag, ngrok)

			wantCalls := 0
			if tc.flag == "" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("ngrok called %d times, want %d", calls, wantCalls)
			}
			assertResolve(t, tc, got, err)
		})
	}
}

// resolveCase is one resolveWebhookURL input. An empty want with a nil
// wantErr means the flag must be rejected with the validation error.
type resolveCase struct {
	name     string
	flag     string
	ngrokErr error
	want     string
	wantErr  error
}

func assertResolve(t *testing.T, tc resolveCase, got string, err error) {
	t.Helper()

	switch {
	case tc.wantErr != nil:
		if err != tc.wantErr { //nolint:errorlint // the ngrok error must be returned unchanged
			t.Fatalf("err = %v, want %v unchanged", err, tc.wantErr)
		}
	case tc.want == "":
		want := fmt.Sprintf(
			"--webhook-url must be an absolute https URL without credentials, query or fragment, got %q", tc.flag)
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %q", err, want)
		}
	case err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
	if got != tc.want {
		t.Fatalf("got %q, want %q", got, tc.want)
	}
}

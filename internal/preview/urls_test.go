package preview_test

import (
	"strings"
	"testing"

	"github.com/doublurehq/doublure/internal/preview"
)

const longSlug = "feature-very-long-branch-name-that-keeps-going-5bec34"

func TestURLConfig_Host(t *testing.T) {
	urls := preview.URLConfig{Domain: "127.0.0.1.nip.io"}
	cases := []struct {
		name    string
		service string
		slug    string
		want    string
	}{
		{
			name:    "63-character label unchanged",
			service: "backend-1",
			slug:    longSlug,
			want:    "backend-1-feature-very-long-branch-name-that-keeps-going-5bec34.127.0.0.1.nip.io",
		},
		{
			name:    "70-character label cut and hashed",
			service: "front-office-web",
			slug:    longSlug,
			want:    "front-office-web-feature-very-long-branch-name-that-keep-395ff3.127.0.0.1.nip.io",
		},
		{
			name:    "short label unchanged",
			service: "front",
			slug:    "feature-pricing",
			want:    "front-feature-pricing.127.0.0.1.nip.io",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := urls.Host(c.service, c.slug); got != c.want {
				t.Errorf("Host(%q, %q) = %q, want %q", c.service, c.slug, got, c.want)
			}
		})
	}
}

func TestURLConfig_HostLabelBoundary(t *testing.T) {
	const domain = "example.com"
	urls := preview.URLConfig{Domain: domain}

	// "s-" + 61 characters is a 63-character label.
	at := strings.Repeat("a", 61)
	if got, want := urls.Host("s", at), "s-"+at+"."+domain; got != want {
		t.Errorf("Host with a 63-character label = %q, want %q", got, want)
	}

	over := strings.Repeat("a", 62)
	got := urls.Host("s", over)
	label, ok := strings.CutSuffix(got, "."+domain)
	if !ok {
		t.Fatalf("Host = %q, want suffix .%s", got, domain)
	}
	if len(label) > 63 {
		t.Errorf("label length = %d, want <= 63 (%q)", len(label), label)
	}
	if label == "s-"+over[:len(label)-2] {
		t.Errorf("label %q, want a hashed suffix", label)
	}
}

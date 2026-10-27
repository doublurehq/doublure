package gitops_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

const serviceRepos = `serviceRepos:
  backend-1: https://github.com/acme/backend-1.git
  backend-2: https://github.com/acme/backend-2.git
  front: https://github.com/acme/front.git
`

func TestParseServices_Preview(t *testing.T) {
	tests := []struct {
		name    string
		preview string
		want    preview.URLConfig
	}{
		{
			name: "absent block",
			want: preview.URLConfig{},
		},
		{
			name:    "null block",
			preview: "preview:\n",
			want:    preview.URLConfig{},
		},
		{
			name:    "absent domain",
			preview: "preview:\n  tls: false\n",
			want:    preview.URLConfig{},
		},
		{
			name: "full block",
			preview: `preview:
  domain: 127.0.0.1.nip.io
  tls: false
  expose:
    front: {}
    backend-1:
      path: /docs
    backend-2:
`,
			want: preview.URLConfig{
				Domain: "127.0.0.1.nip.io",
				TLS:    false,
				Expose: map[string]string{"front": "/", "backend-1": "/docs", "backend-2": "/"},
			},
		},
		{
			name:    "tls defaults to true",
			preview: "preview:\n  domain: example.com\n  expose:\n    front: {}\n",
			want: preview.URLConfig{
				Domain: "example.com",
				TLS:    true,
				Expose: map[string]string{"front": "/"},
			},
		},
		{
			name:    "expose entry for a service outside the catalog",
			preview: "preview:\n  domain: example.com\n  expose:\n    unknown: {}\n",
			want: preview.URLConfig{
				Domain: "example.com",
				TLS:    true,
				Expose: map[string]string{"unknown": "/"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitops.ParseServices([]byte(serviceRepos + tt.preview))
			if err != nil {
				t.Fatalf("ParseServices: %v", err)
			}
			if !reflect.DeepEqual(got.URLs, tt.want) {
				t.Errorf("URLs = %+v, want %+v", got.URLs, tt.want)
			}
			if want := []string{"backend-1", "backend-2", "front"}; !reflect.DeepEqual(got.Services, want) {
				t.Errorf("Services = %v, want %v", got.Services, want)
			}
		})
	}
}

func TestParseServices_MalformedPreview(t *testing.T) {
	tests := []struct {
		name    string
		preview string
		key     string
	}{
		{"preview not a map", "preview: [a]\n", "preview:"},
		{"preview scalar", "preview: on\n", "preview:"},
		{"domain not a string", "preview:\n  domain: [a]\n", "preview.domain"},
		{"domain number", "preview:\n  domain: 42\n", "preview.domain"},
		{"domain with scheme", "preview:\n  domain: https://example.com\n", "preview.domain"},
		{"domain with path", "preview:\n  domain: example.com/x\n", "preview.domain"},
		{"domain leading dot", "preview:\n  domain: .example.com\n", "preview.domain"},
		{"domain trailing dot", "preview:\n  domain: example.com.\n", "preview.domain"},
		{"domain uppercase", "preview:\n  domain: Example.com\n", "preview.domain"},
		{"domain empty", "preview:\n  domain: \"\"\n", "preview.domain"},
		{"tls not a boolean", "preview:\n  domain: example.com\n  tls: \"false\"\n", "preview.tls"},
		{"expose not a map", "preview:\n  domain: example.com\n  expose: [front]\n", "preview.expose"},
		{
			"expose entry not null or map",
			"preview:\n  domain: example.com\n  expose:\n    front: yes\n",
			"preview.expose.front",
		},
		{
			"path not a string",
			"preview:\n  domain: example.com\n  expose:\n    front:\n      path: 1\n",
			"preview.expose.front.path",
		},
		{
			"path without leading slash",
			"preview:\n  domain: example.com\n  expose:\n    front:\n      path: docs\n",
			"preview.expose.front.path",
		},
		{"expose without domain", "preview:\n  expose:\n    front: {}\n", "preview.expose"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gitops.ParseServices([]byte(serviceRepos + tt.preview))
			if err == nil {
				t.Fatal("ParseServices: want error, got nil")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error %q does not name key %q", err, tt.key)
			}
		})
	}
}

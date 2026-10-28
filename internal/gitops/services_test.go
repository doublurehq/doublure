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

func TestParseServices_PreviewAliases(t *testing.T) {
	want := preview.URLConfig{
		Domain: "example.com",
		TLS:    false,
		Expose: map[string]string{"front": "/", "backend-1": "/docs"},
	}
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "aliased block",
			raw: `base: &p
  domain: example.com
  tls: false
  expose:
    front: {}
    backend-1:
      path: /docs
preview: *p
`,
		},
		{
			name: "aliased domain",
			raw: `domains:
  - &d example.com
preview:
  domain: *d
  tls: false
  expose:
    front: {}
    backend-1:
      path: /docs
`,
		},
		{
			name: "aliased expose",
			raw: `exposed: &e
  front: {}
  backend-1:
    path: /docs
preview:
  domain: example.com
  tls: false
  expose: *e
`,
		},
		{
			name: "merge key",
			raw: `base: &p
  domain: other.example.com
  tls: false
  expose:
    front: {}
    backend-1:
      path: /docs
preview:
  <<: *p
  domain: example.com
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitops.ParseServices([]byte(serviceRepos + tt.raw))
			if err != nil {
				t.Fatalf("ParseServices: %v", err)
			}
			if !reflect.DeepEqual(got.URLs, want) {
				t.Errorf("URLs = %+v, want %+v", got.URLs, want)
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
		{"preview not a map", "preview: [a]\n", "line 5: cannot unmarshal !!seq into gitops.previewBlock"},
		{"preview scalar", "preview: on\n", "line 5: cannot unmarshal !!str `on` into gitops.previewBlock"},
		{"domain not a string", "preview:\n  domain: [a]\n", "preview.domain"},
		{"domain number", "preview:\n  domain: 42\n", "preview.domain"},
		{"domain with scheme", "preview:\n  domain: https://example.com\n", "preview.domain"},
		{"domain with path", "preview:\n  domain: example.com/x\n", "preview.domain"},
		{"domain leading dot", "preview:\n  domain: .example.com\n", "preview.domain"},
		{"domain trailing dot", "preview:\n  domain: example.com.\n", "preview.domain"},
		{"domain uppercase", "preview:\n  domain: Example.com\n", "preview.domain"},
		{"domain empty", "preview:\n  domain: \"\"\n", "preview.domain"},
		{"tls not a boolean", "preview:\n  domain: example.com\n  tls: \"false\"\n", "preview.tls"},
		{
			"expose not a map",
			"preview:\n  domain: example.com\n  expose: [front]\n",
			"line 7: cannot unmarshal !!seq into map[string]*gitops.exposeEntry",
		},
		{
			"expose entry not null or map",
			"preview:\n  domain: example.com\n  expose:\n    front: yes\n",
			"line 8: cannot unmarshal !!str `yes` into gitops.exposeEntry",
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
		{"unknown preview key", "preview:\n  domian: example.com\n", "line 6: field domian not found"},
		{
			"unknown preview key next to valid ones",
			"preview:\n  domain: example.com\n  expsoe:\n    front: {}\n",
			"line 7: field expsoe not found",
		},
		{
			"unknown expose entry key",
			"preview:\n  domain: example.com\n  expose:\n    front:\n      paht: /docs\n",
			"line 9: field paht not found",
		},
		{
			"unknown key from a merge",
			"base: &p\n  domian: example.com\npreview:\n  <<: *p\n",
			"line 6: field domian not found",
		},
		{
			"duplicate preview key",
			"preview:\n  domain: example.com\n  domain: example.org\n",
			`line 7: mapping key "domain" already defined at line 6`,
		},
		{
			"duplicate expose entry",
			"preview:\n  domain: example.com\n  expose:\n    front: {}\n    front: {}\n",
			`line 9: mapping key "front" already defined at line 8`,
		},
		{
			"duplicate path key",
			"preview:\n  domain: example.com\n  expose:\n    front:\n      path: /a\n      path: /b\n",
			`line 10: mapping key "path" already defined at line 9`,
		},
		{
			"merge of a scalar",
			"preview:\n  <<: 1\n  domain: example.com\n",
			"map merge requires map or sequence of maps",
		},
		{"aliased domain not a string", "n: &n 42\npreview:\n  domain: *n\n", "preview.domain"},
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

func TestParseServices_Label(t *testing.T) {
	tests := []struct {
		name    string
		preview string
		want    string
	}{
		{name: "absent block", want: "preview"},
		{name: "absent key", preview: "preview:\n  tls: false\n", want: "preview"},
		{name: "empty value", preview: "preview:\n  label:\n", want: "preview"},
		{name: "null", preview: "preview:\n  label: ~\n", want: "preview"},
		{name: "set", preview: "preview:\n  label: deploy-preview\n", want: "deploy-preview"},
		{name: "set next to a domain", preview: "preview:\n  domain: example.com\n  label: Preview\n", want: "Preview"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitops.ParseServices([]byte(serviceRepos + tt.preview))
			if err != nil {
				t.Fatalf("ParseServices: %v", err)
			}
			if got.Label != tt.want {
				t.Errorf("Label = %q, want %q", got.Label, tt.want)
			}
		})
	}
}

func TestParseServices_MalformedLabel(t *testing.T) {
	const want = "services.yaml: malformed preview block: preview.label: must be a non-empty string"
	for name, preview := range map[string]string{
		"empty string": "preview:\n  label: \"\"\n",
		"number":       "preview:\n  label: 42\n",
		"boolean":      "preview:\n  label: true\n",
		"sequence":     "preview:\n  label: [preview]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := gitops.ParseServices([]byte(serviceRepos + preview))
			if err == nil || err.Error() != want {
				t.Fatalf("err = %v, want %q", err, want)
			}
		})
	}
}

func TestParseServices_Repos(t *testing.T) {
	got, err := gitops.ParseServices([]byte(serviceRepos))
	if err != nil {
		t.Fatalf("ParseServices: %v", err)
	}
	want := map[string]string{
		"backend-1": "https://github.com/acme/backend-1.git",
		"backend-2": "https://github.com/acme/backend-2.git",
		"front":     "https://github.com/acme/front.git",
	}
	if !reflect.DeepEqual(got.Repos, want) {
		t.Errorf("Repos = %v, want %v", got.Repos, want)
	}
}

func TestParseServices_RepoNotAString(t *testing.T) {
	for name, raw := range map[string]string{
		"null":     "serviceRepos:\n  backend-1:\n",
		"number":   "serviceRepos:\n  backend-1: 42\n",
		"mapping":  "serviceRepos:\n  backend-1:\n    url: https://github.com/acme/backend-1.git\n",
		"sequence": "serviceRepos:\n  backend-1: [https://github.com/acme/backend-1.git]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := gitops.ParseServices([]byte(raw))
			if want := "services.yaml: serviceRepos.backend-1 must be a string"; err == nil || err.Error() != want {
				t.Fatalf("err = %v, want %q", err, want)
			}
		})
	}
}

func TestServicesForRepo(t *testing.T) {
	const raw = `serviceRepos:
  api: https://github.com/acme/monorepo.git
  worker: https://github.com/Acme/MonoRepo/
  front: https://github.com/acme/front
  web: HTTPS://GITHUB.COM/ACME/WEB.GIT
  ssh: git@github.com:acme/ssh.git
  ghe: https://github.acme.example/acme/ghe.git
`
	catalog, err := gitops.ParseServices([]byte(raw))
	if err != nil {
		t.Fatalf("ParseServices: %v", err)
	}
	tests := []struct {
		name, owner, repo string
		want              []string
	}{
		{"two services on one repo, .git and trailing slash", "acme", "monorepo", []string{"api", "worker"}},
		{"case-insensitive owner and repo", "ACME", "MonoRepo", []string{"api", "worker"}},
		{"no .git", "acme", "front", []string{"front"}},
		{"uppercase url", "acme", "web", []string{"web"}},
		{"ssh url never matches", "acme", "ssh", nil},
		{"enterprise url never matches", "acme", "ghe", nil},
		{"unknown repo", "acme", "unknown", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := catalog.ServicesForRepo(tt.owner, tt.repo); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ServicesForRepo(%q, %q) = %v, want %v", tt.owner, tt.repo, got, tt.want)
			}
		})
	}
}

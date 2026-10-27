package gitops

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/doublurehq/doublure/internal/preview"
)

// ServiceCatalog is the parsed services.yaml of the gitops repo.
type ServiceCatalog struct {
	// Services lists the keys of serviceRepos, in file order.
	Services []string
	// URLs is the validated `preview` block; its zero value means off.
	URLs preview.URLConfig
}

// errMalformedPreview is wrapped by every preview block validation error.
var errMalformedPreview = errors.New("malformed preview block")

// domainPattern matches a lowercase bare domain: dot-separated labels of
// [a-z0-9-] that neither start nor end with a hyphen. No scheme, no path, no
// leading or trailing dot.
var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// pairStride is the step between keys in a mapping node's Content, which
// alternates key and value nodes.
const pairStride = 2

// servicesFile is services.yaml. Top-level keys other than serviceRepos and
// preview are kept in Rest so that only the preview block is strict.
type servicesFile struct {
	ServiceRepos yaml.Node      `yaml:"serviceRepos"`
	Preview      *previewBlock  `yaml:"preview"`
	Rest         map[string]any `yaml:",inline"`
}

type previewBlock struct {
	Domain domainName              `yaml:"domain"`
	TLS    *tlsFlag                `yaml:"tls"`
	Expose map[string]*exposeEntry `yaml:"expose"`
}

type exposeEntry struct {
	Path *string `yaml:"path"`
}

// domainName is preview.domain. It only accepts a YAML string, not a scalar
// yaml.v3 would convert (`domain: 42`).
type domainName string

func (d *domainName) UnmarshalYAML(n *yaml.Node) error {
	if n.Tag != "!!str" {
		return malformed("preview.domain", "must be a string")
	}
	if !domainPattern.MatchString(n.Value) {
		return malformed("preview.domain",
			"%q must be a lowercase bare domain (no scheme, no path, no leading or trailing dot)", n.Value)
	}
	*d = domainName(n.Value)
	return nil
}

// tlsFlag is preview.tls. It only accepts a YAML boolean, not the YAML 1.1
// forms yaml.v3 converts (`yes`, `on`).
type tlsFlag bool

func (t *tlsFlag) UnmarshalYAML(n *yaml.Node) error {
	var v bool
	if n.Tag != "!!bool" || n.Decode(&v) != nil {
		return malformed("preview.tls", "must be a boolean")
	}
	*t = tlsFlag(v)
	return nil
}

func malformed(key, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", errMalformedPreview, key, fmt.Sprintf(format, args...))
}

// ParseServices parses and validates the content of services.yaml.
func ParseServices(raw []byte) (*ServiceCatalog, error) {
	var doc servicesFile
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("services.yaml: %w", err)
	}
	if doc.ServiceRepos.Kind != yaml.MappingNode {
		return nil, errors.New("services.yaml: missing 'serviceRepos' mapping")
	}
	services := make([]string, 0, len(doc.ServiceRepos.Content)/pairStride)
	for i := 0; i+1 < len(doc.ServiceRepos.Content); i += pairStride {
		services = append(services, doc.ServiceRepos.Content[i].Value)
	}
	urls, err := previewURLs(doc.Preview)
	if err != nil {
		return nil, fmt.Errorf("services.yaml: %w", err)
	}
	return &ServiceCatalog{Services: services, URLs: urls}, nil
}

// previewURLs validates the decoded preview block. An absent block or an
// absent domain yields the zero URLConfig (feature off).
func previewURLs(p *previewBlock) (preview.URLConfig, error) {
	if p == nil {
		return preview.URLConfig{}, nil
	}
	if p.Domain == "" {
		if p.Expose != nil {
			return preview.URLConfig{}, malformed("preview.expose", "is set but preview.domain is absent")
		}
		return preview.URLConfig{}, nil
	}
	expose := make(map[string]string, len(p.Expose))
	for name, e := range p.Expose {
		path := "/"
		if e != nil && e.Path != nil {
			path = *e.Path
		}
		if !strings.HasPrefix(path, "/") {
			return preview.URLConfig{}, malformed("preview.expose."+name+".path",
				"must be a string starting with \"/\"")
		}
		expose[name] = path
	}
	return preview.URLConfig{Domain: string(p.Domain), TLS: p.TLS == nil || bool(*p.TLS), Expose: expose}, nil
}

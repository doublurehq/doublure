package gitops

import (
	"errors"
	"fmt"
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

const (
	tagBool = "!!bool"
	tagStr  = "!!str"
	// pairStride is the step between keys in a mapping node's Content, which
	// alternates key and value nodes.
	pairStride = 2
)

// ParseServices parses and validates the content of services.yaml.
func ParseServices(raw []byte) (*ServiceCatalog, error) {
	var doc struct {
		ServiceRepos yaml.Node `yaml:"serviceRepos"`
		Preview      yaml.Node `yaml:"preview"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse services.yaml: %w", err)
	}
	if doc.ServiceRepos.Kind != yaml.MappingNode {
		return nil, errors.New("services.yaml: missing 'serviceRepos' mapping")
	}
	services := make([]string, 0, len(doc.ServiceRepos.Content)/pairStride)
	for i := 0; i+1 < len(doc.ServiceRepos.Content); i += pairStride {
		services = append(services, doc.ServiceRepos.Content[i].Value)
	}
	urls, err := parsePreview(&doc.Preview)
	if err != nil {
		return nil, fmt.Errorf("services.yaml: %w", err)
	}
	return &ServiceCatalog{Services: services, URLs: urls}, nil
}

// isAbsent reports whether n is a missing key or an explicit YAML null.
func isAbsent(n *yaml.Node) bool {
	return n == nil || n.Kind == 0 || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

func malformed(key, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", errMalformedPreview, key, fmt.Sprintf(format, args...))
}

// mappingValues returns the key → value nodes of a mapping node.
func mappingValues(n *yaml.Node) ([]string, map[string]*yaml.Node) {
	keys := make([]string, 0, len(n.Content)/pairStride)
	vals := make(map[string]*yaml.Node, len(n.Content)/pairStride)
	for i := 0; i+1 < len(n.Content); i += pairStride {
		k := n.Content[i].Value
		keys = append(keys, k)
		vals[k] = n.Content[i+1]
	}
	return keys, vals
}

// parsePreview validates the `preview` block. An absent block or an absent
// domain yields the zero URLConfig (feature off).
func parsePreview(n *yaml.Node) (preview.URLConfig, error) {
	if isAbsent(n) {
		return preview.URLConfig{}, nil
	}
	if n.Kind != yaml.MappingNode {
		return preview.URLConfig{}, malformed("preview", "must be a map")
	}
	_, fields := mappingValues(n)

	domain, err := parseDomain(fields["domain"])
	if err != nil {
		return preview.URLConfig{}, err
	}
	tls, err := parseTLS(fields["tls"])
	if err != nil {
		return preview.URLConfig{}, err
	}
	expose, err := parseExpose(fields["expose"])
	if err != nil {
		return preview.URLConfig{}, err
	}
	if domain == "" {
		if !isAbsent(fields["expose"]) {
			return preview.URLConfig{}, malformed("preview.expose", "is set but preview.domain is absent")
		}
		return preview.URLConfig{}, nil
	}
	return preview.URLConfig{Domain: domain, TLS: tls, Expose: expose}, nil
}

func parseDomain(n *yaml.Node) (string, error) {
	if isAbsent(n) {
		return "", nil
	}
	if n.Kind != yaml.ScalarNode || n.Tag != tagStr {
		return "", malformed("preview.domain", "must be a string")
	}
	if !domainPattern.MatchString(n.Value) {
		return "", malformed("preview.domain",
			"%q must be a lowercase bare domain (no scheme, no path, no leading or trailing dot)", n.Value)
	}
	return n.Value, nil
}

func parseTLS(n *yaml.Node) (bool, error) {
	if isAbsent(n) {
		return true, nil
	}
	if n.Kind != yaml.ScalarNode || n.Tag != tagBool {
		return false, malformed("preview.tls", "must be a boolean")
	}
	var v bool
	if err := n.Decode(&v); err != nil {
		return false, malformed("preview.tls", "must be a boolean")
	}
	return v, nil
}

// parseExpose returns the map of exposed service name to link path, empty
// when expose is absent.
func parseExpose(n *yaml.Node) (map[string]string, error) {
	if isAbsent(n) {
		return map[string]string{}, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, malformed("preview.expose", "must be a map")
	}
	names, entries := mappingValues(n)
	expose := make(map[string]string, len(names))
	for _, name := range names {
		path, err := parseExposeEntry(name, entries[name])
		if err != nil {
			return nil, err
		}
		expose[name] = path
	}
	return expose, nil
}

func parseExposeEntry(name string, n *yaml.Node) (string, error) {
	key := "preview.expose." + name
	if isAbsent(n) {
		return "/", nil
	}
	if n.Kind != yaml.MappingNode {
		return "", malformed(key, "must be empty or a map")
	}
	_, fields := mappingValues(n)
	p := fields["path"]
	if isAbsent(p) {
		return "/", nil
	}
	if p.Kind != yaml.ScalarNode || p.Tag != tagStr || !strings.HasPrefix(p.Value, "/") {
		return "", malformed(key+".path", "must be a string starting with \"/\"")
	}
	return p.Value, nil
}

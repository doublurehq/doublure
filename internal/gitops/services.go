package gitops

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
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
	// tagMerge is the tag yaml.v3 gives a `<<` merge key.
	tagMerge = "!!merge"
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

// resolve follows alias nodes to the node they point at.
func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// isAbsent reports whether n is a missing key or an explicit YAML null.
func isAbsent(n *yaml.Node) bool {
	return n == nil || n.Kind == 0 || (n.Kind == yaml.ScalarNode && n.Tag == "!!null")
}

func malformed(key, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", errMalformedPreview, key, fmt.Sprintf(format, args...))
}

// mappingValues returns the keys, in order, and the key → value nodes of the
// mapping node n, named path in errors. Aliases are resolved and merge keys
// (`<<`) are expanded as YAML does: explicit keys win over merged ones, and an
// earlier merged map wins over a later one. A key set twice is an error. When
// allowed is non-nil, a key outside it is an error.
func mappingValues(path string, n *yaml.Node, allowed ...string) ([]string, map[string]*yaml.Node, error) {
	keys := make([]string, 0, len(n.Content)/pairStride)
	vals := make(map[string]*yaml.Node, len(n.Content)/pairStride)
	var merges []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += pairStride {
		k, v := resolve(n.Content[i]), resolve(n.Content[i+1])
		if k.Kind == yaml.ScalarNode && k.Tag == tagMerge {
			merges = append(merges, v)
			continue
		}
		if _, dup := vals[k.Value]; dup {
			return nil, nil, malformed(path+"."+k.Value, "duplicate key")
		}
		keys = append(keys, k.Value)
		vals[k.Value] = v
	}
	for _, m := range merges {
		var err error
		if keys, err = mergeInto(path, keys, vals, m, allowed); err != nil {
			return nil, nil, err
		}
	}
	for _, k := range keys {
		if allowed != nil && !slices.Contains(allowed, k) {
			return nil, nil, malformed(path+"."+k, "unknown key")
		}
	}
	return keys, vals, nil
}

// mergeInto adds to vals the keys of the merge value m (a map or a list of
// maps) that are not set yet, and returns keys extended with them.
func mergeInto(
	path string, keys []string, vals map[string]*yaml.Node, m *yaml.Node, allowed []string,
) ([]string, error) {
	sources := []*yaml.Node{m}
	if m.Kind == yaml.SequenceNode {
		sources = m.Content
	}
	for _, src := range sources {
		src = resolve(src)
		if src.Kind != yaml.MappingNode {
			return nil, malformed(path+".<<", "must be a map or a list of maps")
		}
		srcKeys, srcVals, err := mappingValues(path, src, allowed...)
		if err != nil {
			return nil, err
		}
		for _, k := range srcKeys {
			if _, set := vals[k]; !set {
				keys = append(keys, k)
				vals[k] = srcVals[k]
			}
		}
	}
	return keys, nil
}

// parsePreview validates the `preview` block. An absent block or an absent
// domain yields the zero URLConfig (feature off).
func parsePreview(n *yaml.Node) (preview.URLConfig, error) {
	n = resolve(n)
	if isAbsent(n) {
		return preview.URLConfig{}, nil
	}
	if n.Kind != yaml.MappingNode {
		return preview.URLConfig{}, malformed("preview", "must be a map")
	}
	_, fields, err := mappingValues("preview", n, "domain", "tls", "expose")
	if err != nil {
		return preview.URLConfig{}, err
	}

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
	names, entries, err := mappingValues("preview.expose", n)
	if err != nil {
		return nil, err
	}
	expose := make(map[string]string, len(names))
	for _, name := range names {
		path, entryErr := parseExposeEntry(name, entries[name])
		if entryErr != nil {
			return nil, entryErr
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
	_, fields, err := mappingValues(key, n, "path")
	if err != nil {
		return "", err
	}
	p := fields["path"]
	if isAbsent(p) {
		return "/", nil
	}
	if p.Kind != yaml.ScalarNode || p.Tag != tagStr || !strings.HasPrefix(p.Value, "/") {
		return "", malformed(key+".path", "must be a string starting with \"/\"")
	}
	return p.Value, nil
}

package gitops

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
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
	// Label is the pull request label that requests a preview (preview.label,
	// default "preview"). GitHub label names compare case-insensitively.
	Label string
	// Repos maps each service to its git URL, as written in serviceRepos.
	Repos map[string]string
	// Branches maps each service whose serviceCharts entry sets branch to that
	// branch. It is nil when no entry sets one.
	Branches map[string]string
}

// DefaultLabel is the preview label when preview.label is absent.
const DefaultLabel = "preview"

// ServicesForRepo returns, in catalog order, the services whose git URL is
// https://github.com/<owner>/<repo>, compared lowercased and with a trailing
// "/" then ".git" removed. Other URL forms (SSH, GitHub Enterprise) never
// match.
func (c *ServiceCatalog) ServicesForRepo(owner, repo string) []string {
	want := strings.ToLower("https://github.com/" + owner + "/" + repo)
	var out []string
	for _, name := range c.Services {
		if normalizeRepoURL(c.Repos[name]) == want {
			out = append(out, name)
		}
	}
	return out
}

// normalizeRepoURL lowercases url and removes a trailing "/" then ".git".
func normalizeRepoURL(url string) string {
	u := strings.ToLower(url)
	u = strings.TrimSuffix(u, "/")
	return strings.TrimSuffix(u, ".git")
}

// errMalformedPreview is wrapped by every preview block validation error.
var errMalformedPreview = errors.New("malformed preview block")

// domainPattern matches a lowercase bare domain: dot-separated labels of
// [a-z0-9-] that neither start nor end with a hyphen. No scheme, no path, no
// leading or trailing dot.
var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// strTag is the resolved YAML tag of a string scalar.
const strTag = "!!str"

// nullTag is the resolved YAML tag of a null scalar.
const nullTag = "!!null"

// pairStride is the step between keys in a mapping node's Content, which
// alternates key and value nodes.
const pairStride = 2

// errMalformedServiceCharts is wrapped by every serviceCharts block validation
// error raised after decoding.
var errMalformedServiceCharts = errors.New("malformed serviceCharts block")

// servicesFile is services.yaml. Top-level keys other than serviceRepos, preview
// and serviceCharts are kept in Rest so that only the preview and serviceCharts
// blocks are strict.
type servicesFile struct {
	ServiceRepos  yaml.Node                `yaml:"serviceRepos"`
	Preview       *previewBlock            `yaml:"preview"`
	ServiceCharts map[string]*serviceChart `yaml:"serviceCharts"`
	Rest          map[string]any           `yaml:",inline"`
}

// serviceChart is a serviceCharts entry. Its fields stay nodes so that a YAML
// string can be told apart from a scalar yaml.v3 would convert (`branch: 123`).
type serviceChart struct {
	Branch        yaml.Node `yaml:"branch"`
	Path          yaml.Node `yaml:"path"`
	ImageTagParam yaml.Node `yaml:"imageTagParam"`
	HostParam     yaml.Node `yaml:"hostParam"`
	ValueFiles    yaml.Node `yaml:"valueFiles"`
}

type previewBlock struct {
	Domain domainName              `yaml:"domain"`
	TLS    *tlsFlag                `yaml:"tls"`
	Expose map[string]*exposeEntry `yaml:"expose"`
	Label  *labelName              `yaml:"label"`
}

type exposeEntry struct {
	Path *string `yaml:"path"`
}

// domainName is preview.domain. It only accepts a YAML string, not a scalar
// yaml.v3 would convert (`domain: 42`).
type domainName string

func (d *domainName) UnmarshalYAML(n *yaml.Node) error {
	if n.Tag != strTag {
		return malformed("preview.domain", "must be a string")
	}
	if !domainPattern.MatchString(n.Value) {
		return malformed("preview.domain",
			"%q must be a lowercase bare domain (no scheme, no path, no leading or trailing dot)", n.Value)
	}
	*d = domainName(n.Value)
	return nil
}

// labelName is preview.label. It only accepts a non-empty YAML string; a YAML
// null leaves the field unset.
type labelName string

func (l *labelName) UnmarshalYAML(n *yaml.Node) error {
	if n.Tag != strTag || n.Value == "" {
		return malformed("preview.label", "must be a non-empty string")
	}
	*l = labelName(n.Value)
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

func malformedCharts(key, msg string) error {
	return fmt.Errorf("%w: %s: %s", errMalformedServiceCharts, key, msg)
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
	repos := make(map[string]string, len(doc.ServiceRepos.Content)/pairStride)
	for i := 0; i+1 < len(doc.ServiceRepos.Content); i += pairStride {
		name := doc.ServiceRepos.Content[i].Value
		val := doc.ServiceRepos.Content[i+1]
		if val.Kind == yaml.AliasNode && val.Alias != nil {
			val = val.Alias
		}
		if val.Kind != yaml.ScalarNode || val.Tag != strTag {
			return nil, fmt.Errorf("services.yaml: serviceRepos.%s must be a string", name)
		}
		services = append(services, name)
		repos[name] = val.Value
	}
	urls, err := previewURLs(doc.Preview)
	if err != nil {
		return nil, fmt.Errorf("services.yaml: %w", err)
	}
	label := DefaultLabel
	if doc.Preview != nil && doc.Preview.Label != nil {
		label = string(*doc.Preview.Label)
	}
	branches, err := parseServiceCharts(doc.ServiceCharts, repos)
	if err != nil {
		return nil, fmt.Errorf("services.yaml: %w", err)
	}
	return &ServiceCatalog{Services: services, URLs: urls, Label: label, Repos: repos, Branches: branches}, nil
}

// parseServiceCharts validates the decoded serviceCharts block, in sorted
// service order, and returns the branch of each entry that sets one (nil when
// none does).
func parseServiceCharts(sc map[string]*serviceChart, repos map[string]string) (map[string]string, error) {
	var branches map[string]string
	for _, name := range slices.Sorted(maps.Keys(sc)) {
		if _, ok := repos[name]; !ok {
			return nil, malformedCharts("serviceCharts."+name, "not in serviceRepos")
		}
		branch, err := chartEntry(name, sc[name])
		if err != nil {
			return nil, err
		}
		if branch == "" {
			continue
		}
		if branches == nil {
			branches = make(map[string]string)
		}
		branches[name] = branch
	}
	return branches, nil
}

// chartEntry validates the fields of one serviceCharts entry, in file-format
// order, and returns its branch ("" when unset). A null entry sets nothing.
func chartEntry(name string, c *serviceChart) (string, error) {
	if c == nil {
		return "", nil
	}
	branch, err := chartString(name, "branch", c.Branch)
	if err != nil {
		return "", err
	}
	if _, err = chartString(name, "path", c.Path); err != nil {
		return "", err
	}
	if _, err = chartString(name, "imageTagParam", c.ImageTagParam); err != nil {
		return "", err
	}
	if _, err = chartString(name, "hostParam", c.HostParam); err != nil {
		return "", err
	}
	if err = chartValueFiles(name, c.ValueFiles); err != nil {
		return "", err
	}
	return branch, nil
}

// chartString returns the value of a serviceCharts string field, "" when it is
// absent or null, and an error when it is not a non-empty YAML string.
func chartString(name, field string, n yaml.Node) (string, error) {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = *n.Alias
	}
	switch {
	case n.Kind == 0 || n.Tag == nullTag:
		return "", nil
	case n.Kind != yaml.ScalarNode || n.Tag != strTag || n.Value == "":
		return "", malformedCharts("serviceCharts."+name+"."+field, "must be a non-empty string")
	case yaml11Bool(&n):
		return "", malformedCharts("serviceCharts."+name+"."+field, quoteMsg(n.Value))
	}
	return n.Value, nil
}

// yaml11BoolRe matches the YAML 1.1 boolean forms that yaml.v3 tags !!str but
// Helm, which reads services.yaml as YAML 1.1, decodes as booleans.
var yaml11BoolRe = regexp.MustCompile(`^(y|Y|yes|Yes|YES|n|N|no|No|NO|on|On|ON|off|Off|OFF)$`)

// yaml11Bool reports whether n is a plain (unquoted) scalar that YAML 1.1
// reads as a boolean.
func yaml11Bool(n *yaml.Node) bool {
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		return false
	}
	return yaml11BoolRe.MatchString(n.Value)
}

func quoteMsg(v string) string {
	return fmt.Sprintf("%q must be quoted: YAML 1.1 reads it as a boolean", v)
}

// chartValueFiles checks that a serviceCharts valueFiles field, when set, is a
// list of non-empty YAML strings.
func chartValueFiles(name string, n yaml.Node) error {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		n = *n.Alias
	}
	if n.Kind == 0 || n.Tag == nullTag {
		return nil
	}
	bad := malformedCharts("serviceCharts."+name+".valueFiles", "must be a list of non-empty strings")
	if n.Kind != yaml.SequenceNode {
		return bad
	}
	for _, item := range n.Content {
		if item.Kind == yaml.AliasNode && item.Alias != nil {
			item = item.Alias
		}
		if item.Kind != yaml.ScalarNode || item.Tag != strTag || item.Value == "" {
			return bad
		}
		if yaml11Bool(item) {
			return malformedCharts("serviceCharts."+name+".valueFiles", quoteMsg(item.Value))
		}
	}
	return nil
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

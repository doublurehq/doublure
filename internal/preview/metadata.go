package preview

import (
	"fmt"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"
)

// iso8601 matches the timestamp shape used in service metadata. Values matching
// this pattern must be force-quoted so downstream parsers (ArgoCD multi-source
// merge, Helm) keep them as strings rather than coercing to time.Time.
var iso8601 = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})$`)

// needsQuoting reports whether a string value would round-trip to a non-string
// when re-parsed as YAML. Digits-only, floats, booleans, nulls, and ISO-8601
// timestamps get single-quoted to preserve their string identity.
func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	switch s {
	case "true", "false", "True", "False", "TRUE", "FALSE",
		"null", "Null", "NULL", "~",
		"yes", "no", "on", "off":
		return true
	}
	return iso8601.MatchString(s)
}

// Metadata is the per-service annotation bag. It marshals to YAML with keys in
// MetadataKeyOrder regardless of insertion order, so emitted values.yaml is
// byte-stable across runs. Unknown keys (forward-compat) preserve their order
// after the known ones.
type Metadata struct {
	values map[string]string
	extras []string // keys seen during unmarshal that aren't in MetadataKeyOrder
}

// NewMetadata returns an empty Metadata ready for Set/Get.
func NewMetadata() *Metadata {
	return &Metadata{values: map[string]string{}}
}

// Get returns the value for key (or "" if absent).
func (m *Metadata) Get(key string) string {
	if m == nil || m.values == nil {
		return ""
	}
	return m.values[key]
}

// Set assigns key=value, creating the underlying map if needed.
func (m *Metadata) Set(key, value string) {
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[key] = value
}

// BuildServiceMetadata constructs the canonical Metadata for a deploy event.
// On a fresh create, both created-at and updated-at equal inputs.Timestamp;
// UpdatePreviewValues overwrites updated-at while preserving the existing
// created-at.
func BuildServiceMetadata(inputs DeployInputs) *Metadata {
	m := NewMetadata()
	m.Set(MetaPRAuthor, inputs.PRAuthor)
	m.Set(MetaPRURL, inputs.PRURL)
	m.Set(MetaPRNumber, inputs.PRNumber)
	m.Set(MetaCreatedAt, inputs.Timestamp)
	m.Set(MetaUpdatedAt, inputs.Timestamp)
	m.Set(MetaVCSRefName, inputs.HeadRef)
	m.Set(MetaPipelineRunURL, inputs.WorkflowRunURL)
	return m
}

// MarshalYAML emits the metadata mapping with deterministic key order.
func (m *Metadata) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	emit := func(key string) {
		val, ok := m.values[key]
		if !ok {
			return
		}
		valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: val}
		if needsQuoting(val) {
			valNode.Style = yaml.SingleQuotedStyle
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			valNode,
		)
	}
	for _, k := range MetadataKeyOrder {
		emit(k)
	}
	for _, k := range m.extras {
		emit(k)
	}
	return node, nil
}

// UnmarshalYAML reads the mapping preserving the order of any keys not in
// MetadataKeyOrder, so a round-trip doesn't drop forward-compat fields.
func (m *Metadata) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("metadata: expected mapping node, got kind %d", node.Kind)
	}
	known := make(map[string]struct{}, len(MetadataKeyOrder))
	for _, k := range MetadataKeyOrder {
		known[k] = struct{}{}
	}
	m.values = map[string]string{}
	m.extras = nil
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1].Value
		m.values[key] = val
		if _, ok := known[key]; !ok {
			m.extras = append(m.extras, key)
		}
	}
	return nil
}

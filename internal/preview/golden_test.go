package preview_test

import (
	"bytes"
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/pixelfactoryio/previo/internal/preview"
)

// TestGoldenYAML pins the YAML emission of BuildPreviewValues to the fixture
// in testdata/values.golden.yaml. Failures indicate a drift in formatting
// (indent, key order, quoting) — either the change is intentional and the
// fixture should be regenerated, or it's a regression to investigate.
func TestGoldenYAML(t *testing.T) {
	in := preview.DeployInputs{
		ServiceName:    "backend-1",
		HeadRef:        "feature/add-pricing",
		CommitSha:      "abc1234567890",
		PRAuthor:       "amine",
		PRURL:          "https://github.com/amine7536/backend-1/pull/42",
		PRNumber:       "42",
		Timestamp:      "2026-05-26T10:00:00Z",
		WorkflowRunURL: "https://github.com/amine7536/backend-1/actions/runs/1",
	}
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha,
		[]string{"backend-1", "backend-2", "front"}, in)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(vals); err != nil {
		t.Fatalf("encode: %v", err)
	}
	_ = enc.Close()

	want, err := os.ReadFile("testdata/values.golden.yaml")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("yaml output drift\n--- want (golden) ---\n%s\n--- got ---\n%s",
			want, buf.Bytes())
	}
}

package preview_test

import (
	"slices"
	"testing"

	"github.com/doublurehq/doublure/internal/preview"
)

func TestParseLabel(t *testing.T) {
	cases := []struct {
		label string
		slug  string
		named bool
		plain bool
	}{
		{label: "preview", plain: true},
		{label: "Preview", plain: true},
		{label: "preview:pricing", slug: "pricing", named: true},
		{label: "Preview:Pricing", slug: "pricing", named: true},
		{label: "preview: Big Pricing ", slug: "big-pricing", named: true},
		{label: "preview:", named: true},
		{label: "preview:--", named: true},
		{label: "preview-x"},
		{label: "previewx:y"},
		{label: "bug"},
		{label: ""},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			slug, named, plain := preview.ParseLabel(tc.label, "preview")
			if slug != tc.slug || named != tc.named || plain != tc.plain {
				t.Errorf("ParseLabel(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tc.label, slug, named, plain, tc.slug, tc.named, tc.plain)
			}
		})
	}
}

func TestResolveTarget(t *testing.T) {
	cases := []struct {
		name        string
		labels      []string
		want        string
		named       []string
		namedLabels []string
		ignored     []string
		conflict    bool
	}{
		{name: "plain only", labels: []string{"bug", "preview"}, want: "feature-x"},
		{
			name: "named only", labels: []string{"preview:pricing"}, want: "pricing",
			named: []string{"pricing"}, namedLabels: []string{"preview:pricing"},
		},
		{
			name: "plain plus named", labels: []string{"preview", "preview:pricing"}, want: "pricing",
			named: []string{"pricing"}, namedLabels: []string{"preview:pricing"},
		},
		{
			name: "two named, different slugs", labels: []string{"preview:b", "Preview:A"}, conflict: true,
			named: []string{"a", "b"}, namedLabels: []string{"Preview:A", "preview:b"},
		},
		{
			name: "two named, same slug", labels: []string{"preview:Pricing", "preview:pricing"}, want: "pricing",
			named: []string{"pricing"}, namedLabels: []string{"preview:Pricing", "preview:pricing"},
		},
		{
			name: "mixed case", labels: []string{"Preview:Pricing"}, want: "pricing",
			named: []string{"pricing"}, namedLabels: []string{"Preview:Pricing"},
		},
		{
			name: "spaces", labels: []string{"preview: pricing "}, want: "pricing",
			named: []string{"pricing"}, namedLabels: []string{"preview: pricing "},
		},
		{name: "empty name", labels: []string{"preview:"}, ignored: []string{"preview:"}},
		{
			name: "plain plus empty name", labels: []string{"preview", "preview:"}, want: "feature-x",
			ignored: []string{"preview:"},
		},
		{name: "dash suffix is not named", labels: []string{"preview-x"}},
		{name: "longer prefix is not named", labels: []string{"previewx:y"}},
		{name: "no labels"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := preview.ResolveTarget(tc.labels, "Feature/X", "preview")
			if r.Slug != tc.want || r.Conflict != tc.conflict || !slices.Equal(r.Named, tc.named) ||
				!slices.Equal(r.NamedLabels, tc.namedLabels) || !slices.Equal(r.Ignored, tc.ignored) {
				t.Errorf("ResolveTarget(%q) = %+v, want slug %q, conflict %v, named %q, namedLabels %q, ignored %q",
					tc.labels, r, tc.want, tc.conflict, tc.named, tc.namedLabels, tc.ignored)
			}
		})
	}
}

package preview_test

import (
	"testing"

	"github.com/doublurehq/doublure/internal/preview"
)

func TestSlugify(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "feature/add-pricing", "feature-add-pricing"},
		{"mixed case", "Feature/Add-Pricing", "feature-add-pricing"},
		{"underscores", "fix_login_bug", "fix-login-bug"},
		{"runs collapse", "feature///add---pricing", "feature-add-pricing"},
		{"leading specials", "///feature", "feature"},
		{"trailing specials", "feature///", "feature"},
		{"only specials", "////", ""},
		{"empty", "", ""},
		{"alphanum only", "release2026", "release2026"},
		{"unicode dropped", "café-au-lait", "caf-au-lait"},
		{"dots and slashes", "v1.2.3/hotfix", "v1-2-3-hotfix"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := preview.Slugify(c.in)
			if got != c.want {
				t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

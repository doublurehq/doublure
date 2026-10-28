package preview_test

import (
	"strings"
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

func TestSlugify_Cap(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "long branch cut and hashed",
			in:   "feature/very-long-branch-name-that-keeps-going-and-going-past-the-helm-limit",
			want: "feature-very-long-branch-name-that-keeps-going-5bec34",
		},
		{
			name: "cut ending on a dash is trimmed",
			in:   strings.Repeat("a", 45) + "-" + strings.Repeat("b", 10),
			want: strings.Repeat("a", 45) + "-7b0017",
		},
		{
			name: "53 characters unchanged",
			in:   strings.Repeat("a", 45) + "-" + strings.Repeat("b", 7),
			want: strings.Repeat("a", 45) + "-" + strings.Repeat("b", 7),
		},
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

func TestSlugify_CapBoundary(t *testing.T) {
	at := strings.Repeat("a", preview.MaxSlugLen)
	if got := preview.Slugify(at); got != at {
		t.Errorf("Slugify(%d chars) = %q, want unchanged", len(at), got)
	}

	over := strings.Repeat("a", preview.MaxSlugLen+1)
	got := preview.Slugify(over)
	if len(got) > preview.MaxSlugLen {
		t.Errorf("len(Slugify(%d chars)) = %d, want <= %d", len(over), len(got), preview.MaxSlugLen)
	}
	if got == over[:len(got)] {
		t.Errorf("Slugify(%d chars) = %q, want a hashed suffix", len(over), got)
	}
	if again := preview.Slugify(over); again != got {
		t.Errorf("Slugify is not deterministic: %q then %q", got, again)
	}
}

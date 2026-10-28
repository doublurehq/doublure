package preview

import (
	"slices"
	"strings"
)

// Resolution is the preview a PR's labels target.
type Resolution struct {
	// Slug is the target preview, "" when none.
	Slug string
	// Named holds the distinct slugs of the valid named labels, sorted.
	Named []string
	// NamedLabels holds the names of the valid named labels, as written,
	// sorted case-insensitively.
	NamedLabels []string
	// Ignored holds the names of the named labels whose slug is empty, in
	// input order.
	Ignored []string
	// Conflict is true when the named labels target two or more previews.
	Conflict bool
}

// ParseLabel classifies label against the preview label base. It returns, in
// order, the slug, whether label is named and whether it is the plain label.
// A named label starts with "<base>:" (case-insensitively); its slug is the
// Slugify of the rest, trimmed of spaces, and is "" when that rest has no
// alphanumeric character. The plain label equals base (case-insensitively). Any
// other label returns all zero values.
func ParseLabel(label, base string) (string, bool, bool) {
	if strings.EqualFold(label, base) {
		return "", false, true
	}
	prefix := base + ":"
	if len(label) < len(prefix) || !strings.EqualFold(label[:len(prefix)], prefix) {
		return "", false, false
	}
	return Slugify(strings.TrimSpace(label[len(prefix):])), true, false
}

// ResolveTarget returns the preview targeted by labels for a PR on headRef,
// where base is the preview label. Exactly one distinct named slug wins; two or
// more are a conflict with no target. Without a valid named label, the plain
// label targets Slugify(headRef). Otherwise there is no target.
func ResolveTarget(labels []string, headRef, base string) Resolution {
	var r Resolution
	plain := false
	for _, label := range labels {
		slug, named, isPlain := ParseLabel(label, base)
		switch {
		case isPlain:
			plain = true
		case named && slug == "":
			r.Ignored = append(r.Ignored, label)
		case named:
			r.NamedLabels = append(r.NamedLabels, label)
			if !slices.Contains(r.Named, slug) {
				r.Named = append(r.Named, slug)
			}
		}
	}
	slices.Sort(r.Named)
	slices.SortStableFunc(r.NamedLabels, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})

	switch {
	case len(r.Named) == 1:
		r.Slug = r.Named[0]
	case len(r.Named) > 1:
		r.Conflict = true
	case plain:
		r.Slug = Slugify(headRef)
	}
	return r
}

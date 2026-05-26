package preview

import "strings"

// Slugify converts a VCS branch name into a DNS-safe slug used as the preview
// folder name and AppProject/Namespace identifier: lowercase, non-alphanumerics
// collapsed to a single '-', leading/trailing dashes trimmed.
func Slugify(branch string) string {
	var b strings.Builder
	b.Grow(len(branch))
	prevDash := true // treat leading state as "just emitted a dash" so we skip them
	for _, r := range strings.ToLower(branch) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := b.String()
	return strings.TrimRight(out, "-")
}

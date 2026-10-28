package preview

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// MaxSlugLen is the longest slug Slugify returns. It is Helm's release-name
// limit: Argo CD renders the preview-env chart with the slug as release name
// (the ApplicationSet's releaseName), and Helm rejects names over 53
// characters.
const MaxSlugLen = 53

// maxLabelLen is the DNS label limit, applied to the <service>-<slug> part of
// a preview host.
const maxLabelLen = 63

// hashLen is the number of hex characters of the hash appended to a cut label.
const hashLen = 6

// Slugify converts a VCS branch name into a DNS-safe slug used as the preview
// folder name and AppProject/Namespace identifier: lowercase, non-alphanumerics
// collapsed to a single '-', leading/trailing dashes trimmed. A slug longer
// than MaxSlugLen is cut and suffixed with a short hash of the full slug, so
// the result stays distinct and deterministic.
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
	return capLabel(strings.TrimRight(out, "-"), MaxSlugLen)
}

// capLabel returns s unchanged when it fits in maxLen. Otherwise it cuts s,
// trims the trailing dashes of the cut, and appends "-" and the first hashLen
// hex characters of the SHA-256 of the full s. The result is at most maxLen
// long.
func capLabel(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	return strings.TrimRight(s[:maxLen-hashLen-1], "-") + "-" + hex.EncodeToString(sum[:])[:hashLen]
}

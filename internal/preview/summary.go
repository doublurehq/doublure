package preview

import (
	"fmt"
	"regexp"
	"strings"
)

var prURLOwnerRepo = regexp.MustCompile(`^(https://github\.com/[^/]+/[^/]+)/pull/`)

// deriveCommitURL extracts owner/repo from a PR URL and returns the matching
// commit URL for sha. Returns "" if prURL is not a recognizable PR URL.
func deriveCommitURL(prURL, sha string) string {
	m := prURLOwnerRepo.FindStringSubmatch(prURL)
	if m == nil {
		return ""
	}
	return m[1] + "/commit/" + sha
}

// BuildSummary renders the markdown summary that previo posts back as a PR
// comment. Each service line shows the pinned commit (with a link if
// derivable) or a 🔄 main badge for main-tracking entries. The trailing line
// links to the gitops commit that recorded the change.
func BuildSummary(slug string, values PreviewValues, commitMessage, commitURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 🚀 Preview: `%s`\n\n", slug)
	for _, svc := range values.Services {
		sha := svc.CommitSha
		prURL := svc.Metadata.Get(MetaPRURL)
		prNumber := svc.Metadata.Get(MetaPRNumber)
		if sha != "" {
			shaShort := sha
			if len(shaShort) >= 8 {
				shaShort = shaShort[:8]
			}
			cURL := deriveCommitURL(prURL, sha)
			commitPart := "📌 `" + shaShort + "`"
			if cURL != "" {
				commitPart = fmt.Sprintf("📌 [`%s`](%s)", shaShort, cURL)
			}
			prPart := ""
			if prURL != "" {
				prPart = fmt.Sprintf(" · [PR #%s](%s)", prNumber, prURL)
			}
			fmt.Fprintf(&b, "- **%s** · %s%s\n", svc.Name, commitPart, prPart)
		} else {
			fmt.Fprintf(&b, "- **%s** · 🔄 `main`\n", svc.Name)
		}
	}
	b.WriteString("\n---\n")
	fmt.Fprintf(&b, "🔗 [%s](%s)", commitMessage, commitURL)
	return b.String()
}

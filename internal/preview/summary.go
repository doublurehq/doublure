package preview

import (
	"fmt"
	"regexp"
	"strings"
	"time"
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

// BuildSummary renders the markdown summary that doublure posts back as a PR
// comment. Each service line shows the pinned commit (with a link if
// derivable) or a 🔄 main badge for main-tracking entries, followed by a link
// to the service's preview host when urls exposes it. The trailing line links
// to the gitops commit that recorded the change.
func BuildSummary(slug string, values PreviewValues, urls URLConfig, commitMessage, commitURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 🚀 Preview: `%s`\n\n", slug)
	for _, svc := range values.Services {
		sha := svc.CommitSha
		var prURL, prNumber string
		if svc.Metadata != nil {
			prURL, prNumber = svc.Metadata.PRURL, svc.Metadata.PRNumber
		}
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
			fmt.Fprintf(&b, "- **%s** · %s%s", svc.Name, commitPart, prPart)
		} else {
			fmt.Fprintf(&b, "- **%s** · 🔄 `main`", svc.Name)
		}
		if text, url, ok := urls.Link(svc.Name, slug); ok {
			fmt.Fprintf(&b, " · [%s](%s)", text, url)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n---\n")
	fmt.Fprintf(&b, "🔗 [%s](%s)", commitMessage, commitURL)
	return b.String()
}

// BuildTeardownSummary renders the comment doublure leaves on a closed PR once
// its services were unpinned from the preview, or the preview was deleted.
func BuildTeardownSummary(slug string, removed []string, deleted bool, commitMessage, commitURL string) string {
	sentence := "Preview deleted: no open PR remains."
	if !deleted {
		names := make([]string, 0, len(removed))
		for _, name := range removed {
			names = append(names, "`"+name+"`")
		}
		sentence = fmt.Sprintf("Removed %s from this preview (PR closed).", strings.Join(names, ", "))
	}
	return fmt.Sprintf("## 🧹 Preview: `%s`\n\n%s\n\n---\n🔗 [%s](%s)", slug, sentence, commitMessage, commitURL)
}

// BuildExpirySummary renders the comment doublure leaves on a preview's open
// PRs once the cleanup sweeper deleted it for having no deploy within ttl.
func BuildExpirySummary(slug string, ttl time.Duration, commitMessage, commitURL string) string {
	return fmt.Sprintf("## 🧹 Preview: `%s`\n\nPreview deleted: no deploy for %s.\n\n---\n🔗 [%s](%s)",
		slug, ttl, commitMessage, commitURL)
}

package preview

import (
	"cmp"
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
// derivable) or a 🔄 badge naming the branch an unpinned entry tracks,
// followed by a link to the entry's written host, if any. The trailing line
// links to the gitops commit that recorded the change.
func BuildSummary(
	slug string, values PreviewValues, urls URLConfig, branches map[string]string, commitMessage, commitURL string,
) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 🚀 Preview: `%s`\n\n", slug)
	for _, svc := range values.Services {
		branch := cmp.Or(branches[svc.Name], "main")
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
			fmt.Fprintf(&b, "- **%s** · 🔄 `%s`", svc.Name, branch)
		}
		if text, url, ok := urls.Link(svc.Name, svc.Host); ok {
			fmt.Fprintf(&b, " · [%s](%s)", text, url)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n---\n")
	fmt.Fprintf(&b, "🔗 [%s](%s)", commitMessage, commitURL)
	return b.String()
}

// BuildTeardownSummary renders the comment doublure leaves on a PR once its
// services were unpinned from the preview, or the preview was deleted. reason
// says why (for example "PR closed").
func BuildTeardownSummary(slug string, removed []string, deleted bool, reason, commitMessage, commitURL string) string {
	sentence := fmt.Sprintf("Preview deleted (%s): no other PR is pinned.", reason)
	if !deleted {
		names := make([]string, 0, len(removed))
		for _, name := range removed {
			names = append(names, "`"+name+"`")
		}
		sentence = fmt.Sprintf("Removed %s from this preview (%s).", strings.Join(names, ", "), reason)
	}
	return fmt.Sprintf("## 🧹 Preview: `%s`\n\n%s\n\n---\n🔗 [%s](%s)", slug, sentence, commitMessage, commitURL)
}

// BuildExpirySummary renders the comment doublure leaves on a preview's open
// PRs once the cleanup sweeper deleted it for having no deploy within ttl.
func BuildExpirySummary(slug string, ttl time.Duration, commitMessage, commitURL string) string {
	return fmt.Sprintf("## 🧹 Preview: `%s`\n\nPreview deleted: no deploy for %s.\n\n---\n🔗 [%s](%s)",
		slug, ttl, commitMessage, commitURL)
}

// BuildLabelConflictSummary renders the comment doublure leaves on a PR that
// carries several named labels of the preview label base. labels are their
// names, as written.
func BuildLabelConflictSummary(base string, labels []string) string {
	names := make([]string, 0, len(labels))
	for _, label := range labels {
		names = append(names, "`"+label+"`")
	}
	return fmt.Sprintf("## ⚠️ Preview: conflicting labels\n\nKeep one `%s:` label. This PR has %s.",
		base, strings.Join(names, ", "))
}

// BuildServiceHeldSummary renders the comment doublure leaves on a PR it
// refused to deploy because service is already pinned in the preview slug by
// the PR at holderURL. base is the preview label.
func BuildServiceHeldSummary(slug, service, holderURL, base string) string {
	return fmt.Sprintf("## ⚠️ Preview: `%s`\n\n"+
		"`%s` is already pinned in this preview by %s. "+
		"Once that pull request leaves this preview, push to this pull request or re-add its label. "+
		"If that pull request is closed, or now carries a different `%s:` label, "+
		"it is released within the next cleanup run.",
		slug, service, holderURL, base)
}

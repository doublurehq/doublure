package preview_test

import (
	"strings"
	"testing"

	"github.com/doublurehq/doublure/internal/preview"
)

func TestBuildSummary_PinnedAndMainTracking(t *testing.T) {
	catalog := []string{"backend-1", "backend-2", "front"}
	in := sampleInputs()
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	got := preview.BuildSummary(
		"feature-add-pricing", vals,
		"chore(preview): create feature-add-pricing preview",
		"https://github.com/acme/gitops/commit/deadbeef",
	)

	mustContain := []string{
		"## 🚀 Preview: `feature-add-pricing`",
		"**backend-1** · 📌 [`abc12345`](https://github.com/acme/backend-1/commit/abc1234567890) · [PR #42](https://github.com/acme/backend-1/pull/42)",
		"**backend-2** · 🔄 `main`",
		"**front** · 🔄 `main`",
		"🔗 [chore(preview): create feature-add-pricing preview](https://github.com/acme/gitops/commit/deadbeef)",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing line:\n  want: %s\n  got:\n%s", want, got)
		}
	}
}

func TestBuildSummary_ShortShaForUnusualLengths(t *testing.T) {
	catalog := []string{"backend-1"}
	in := sampleInputs()
	in.CommitSha = "abc"
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	got := preview.BuildSummary("slug", vals, "msg", "url")
	// 3-char sha should appear in full (not truncated to 8).
	if !strings.Contains(got, "`abc`") {
		t.Errorf("expected raw short sha `abc` in summary, got:\n%s", got)
	}
}

func TestBuildSummary_MissingPRURL(t *testing.T) {
	// Service is pinned but PR URL/number are empty — render commit-only line
	// without the PR suffix and without a hyperlink.
	in := sampleInputs()
	in.PRURL = ""
	in.PRNumber = ""
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, []string{"backend-1"}, in)
	got := preview.BuildSummary("slug", vals, "m", "u")

	if !strings.Contains(got, "**backend-1** · 📌 `abc12345`\n") {
		t.Errorf("expected unlinked sha and no PR suffix, got:\n%s", got)
	}
	if strings.Contains(got, "PR #") {
		t.Errorf("expected no PR suffix when PR URL missing, got:\n%s", got)
	}
}

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
		"feature-add-pricing", vals, preview.URLConfig{},
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

// TestBuildSummary_ExactOutput pins the full rendered summary byte for byte.
func TestBuildSummary_ExactOutput(t *testing.T) {
	catalog := []string{"backend-1", "backend-2", "front"}
	in := sampleInputs()
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	got := preview.BuildSummary(
		"feature-add-pricing", vals, preview.URLConfig{},
		"chore(preview): create feature-add-pricing preview",
		"https://github.com/acme/gitops/commit/deadbeef",
	)

	want := "## 🚀 Preview: `feature-add-pricing`\n" +
		"\n" +
		"- **backend-1** · 📌 [`abc12345`](https://github.com/acme/backend-1/commit/abc1234567890) · [PR #42](https://github.com/acme/backend-1/pull/42)\n" +
		"- **backend-2** · 🔄 `main`\n" +
		"- **front** · 🔄 `main`\n" +
		"\n" +
		"---\n" +
		"🔗 [chore(preview): create feature-add-pricing preview](https://github.com/acme/gitops/commit/deadbeef)"
	if got != want {
		t.Errorf("summary mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildSummary_ShortShaForUnusualLengths(t *testing.T) {
	catalog := []string{"backend-1"}
	in := sampleInputs()
	in.CommitSha = "abc"
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	got := preview.BuildSummary("slug", vals, preview.URLConfig{}, "msg", "url")
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
	got := preview.BuildSummary("slug", vals, preview.URLConfig{}, "m", "u")

	if !strings.Contains(got, "**backend-1** · 📌 `abc12345`\n") {
		t.Errorf("expected unlinked sha and no PR suffix, got:\n%s", got)
	}
	if strings.Contains(got, "PR #") {
		t.Errorf("expected no PR suffix when PR URL missing, got:\n%s", got)
	}
}

// sharedURLFixture is the per-preview URL fixture shared with the charts CI
// render step: front exposed with no path, backend-1 with /docs, backend-2 not
// exposed.
func sharedURLFixture() preview.URLConfig {
	return preview.URLConfig{
		Domain: "127.0.0.1.nip.io",
		TLS:    false,
		Expose: map[string]string{"front": "/", "backend-1": "/docs"},
	}
}

func TestBuildSummary_PreviewURLs(t *testing.T) {
	catalog := []string{"backend-1", "backend-2", "front"}
	in := sampleInputs()
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	got := preview.BuildSummary("feature-pricing", vals, sharedURLFixture(), "msg", "url")

	want := "## 🚀 Preview: `feature-pricing`\n" +
		"\n" +
		"- **backend-1** · 📌 [`abc12345`](https://github.com/acme/backend-1/commit/abc1234567890)" +
		" · [PR #42](https://github.com/acme/backend-1/pull/42)" +
		" · [backend-1-feature-pricing.127.0.0.1.nip.io/docs](http://backend-1-feature-pricing.127.0.0.1.nip.io/docs)\n" +
		"- **backend-2** · 🔄 `main`\n" +
		"- **front** · 🔄 `main` · [front-feature-pricing.127.0.0.1.nip.io/](http://front-feature-pricing.127.0.0.1.nip.io/)\n" +
		"\n" +
		"---\n" +
		"🔗 [msg](url)"
	if got != want {
		t.Errorf("summary mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildSummary_PreviewURLsDefaultToHTTPS(t *testing.T) {
	in := sampleInputs()
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, []string{"backend-1", "front"}, in)
	urls := sharedURLFixture()
	urls.TLS = true // tls omitted defaults to true

	got := preview.BuildSummary("feature-pricing", vals, urls, "msg", "url")

	for _, want := range []string{
		"(https://front-feature-pricing.127.0.0.1.nip.io/)",
		"(https://backend-1-feature-pricing.127.0.0.1.nip.io/docs)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %s, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "http://") {
		t.Errorf("summary has an http link with tls on, got:\n%s", got)
	}
}

func TestBuildSummary_PreviewURLPathEscaping(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "space and parenthesis",
			path: "/a b)c",
			want: ` · [front-feature-pricing.example.com/a b\)c](https://front-feature-pricing.example.com/a%20b%29c)`,
		},
		{
			name: "query and fragment",
			path: "/x?q=1#f",
			want: " · [front-feature-pricing.example.com/x?q=1#f](https://front-feature-pricing.example.com/x?q=1#f)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := sampleInputs()
			vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, []string{"front"}, in)
			urls := preview.URLConfig{Domain: "example.com", TLS: true, Expose: map[string]string{"front": tt.path}}

			got := preview.BuildSummary("feature-pricing", vals, urls, "msg", "url")

			if !strings.Contains(got, tt.want+"\n") {
				t.Errorf("summary missing %q, got:\n%s", tt.want, got)
			}
		})
	}
}

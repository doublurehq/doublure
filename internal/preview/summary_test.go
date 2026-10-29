package preview_test

import (
	"strings"
	"testing"
	"time"

	"github.com/doublurehq/doublure/internal/preview"
)

func TestBuildSummary_PinnedAndMainTracking(t *testing.T) {
	catalog := []string{"backend-1", "backend-2", "front"}
	in := sampleInputs()
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	got := preview.BuildSummary(
		"feature-add-pricing", vals, preview.URLConfig{}, nil,
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

func TestBuildSummary_BranchBadge(t *testing.T) {
	vals := preview.PreviewValues{Services: []preview.ServiceEntry{
		{Name: "backend-1"},
		{Name: "backend-2", CommitSha: "abc1234567890"},
		{Name: "front"},
	}}
	branches := map[string]string{"backend-1": "master", "backend-2": "release"}

	got := preview.BuildSummary("slug", vals, preview.URLConfig{}, branches, "m", "u")

	want := "## 🚀 Preview: `slug`\n\n- **backend-1** · 🔄 `master`\n- **backend-2** · 📌 `abc12345`\n- **front** · 🔄 `main`\n\n---\n🔗 [m](u)"
	if got != want {
		t.Errorf("summary mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// TestBuildSummary_ExactOutput pins the full rendered summary byte for byte.
func TestBuildSummary_ExactOutput(t *testing.T) {
	catalog := []string{"backend-1", "backend-2", "front"}
	in := sampleInputs()
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	got := preview.BuildSummary(
		"feature-add-pricing", vals, preview.URLConfig{}, nil,
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

	got := preview.BuildSummary("slug", vals, preview.URLConfig{}, nil, "msg", "url")
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
	got := preview.BuildSummary("slug", vals, preview.URLConfig{}, nil, "m", "u")

	if !strings.Contains(got, "**backend-1** · 📌 `abc12345`\n") {
		t.Errorf("expected unlinked sha and no PR suffix, got:\n%s", got)
	}
	if strings.Contains(got, "PR #") {
		t.Errorf("expected no PR suffix when PR URL missing, got:\n%s", got)
	}
}

// sharedURLFixture is the per-preview URL fixture: front exposed with no
// path, backend-1 with /docs, backend-2 not exposed. The charts CI render step
// uses the hosts it produces for the slug feature-pricing.
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
	vals = preview.ApplyHosts(vals, "feature-pricing", sharedURLFixture())

	got := preview.BuildSummary("feature-pricing", vals, sharedURLFixture(), nil, "msg", "url")

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
	vals = preview.ApplyHosts(vals, "feature-pricing", urls)

	got := preview.BuildSummary("feature-pricing", vals, urls, nil, "msg", "url")

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
			vals = preview.ApplyHosts(vals, "feature-pricing", urls)

			got := preview.BuildSummary("feature-pricing", vals, urls, nil, "msg", "url")

			if !strings.Contains(got, tt.want+"\n") {
				t.Errorf("summary missing %q, got:\n%s", tt.want, got)
			}
		})
	}
}

func TestBuildSummary_LinksWrittenHosts(t *testing.T) {
	vals := preview.PreviewValues{Services: []preview.ServiceEntry{
		{Name: "front"},
		{Name: "backend-2", Host: "backend-2-feature-pricing.127.0.0.1.nip.io"},
	}}

	got := preview.BuildSummary("feature-pricing", vals, sharedURLFixture(), nil, "msg", "url")

	for _, want := range []string{
		"- **front** · 🔄 `main`\n",
		"- **backend-2** · 🔄 `main` · [backend-2-feature-pricing.127.0.0.1.nip.io/]" +
			"(http://backend-2-feature-pricing.127.0.0.1.nip.io/)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q, got:\n%s", want, got)
		}
	}
}

func TestBuildTeardownSummary(t *testing.T) {
	const (
		slug      = "feature-add-pricing"
		commitURL = "https://github.com/acme/gitops/commit/deadbeef"
	)
	cases := []struct {
		name    string
		removed []string
		deleted bool
		reason  string
		message string
		want    string
	}{
		{
			name:    "one removed service",
			removed: []string{"backend-1"},
			reason:  "PR closed",
			message: "chore(preview): remove backend-1 from feature-add-pricing",
			want: "## 🧹 Preview: `feature-add-pricing`\n\n" +
				"Removed `backend-1` from this preview (PR closed).\n\n" +
				"---\n" +
				"🔗 [chore(preview): remove backend-1 from feature-add-pricing](" + commitURL + ")",
		},
		{
			name:    "two removed services",
			removed: []string{"api", "worker"},
			reason:  "PR closed",
			message: "chore(preview): remove api, worker from feature-add-pricing",
			want: "## 🧹 Preview: `feature-add-pricing`\n\n" +
				"Removed `api`, `worker` from this preview (PR closed).\n\n" +
				"---\n" +
				"🔗 [chore(preview): remove api, worker from feature-add-pricing](" + commitURL + ")",
		},
		{
			name:    "deleted",
			removed: []string{"backend-1"},
			deleted: true,
			reason:  "PR closed",
			message: "chore(preview): delete feature-add-pricing preview",
			want: "## 🧹 Preview: `feature-add-pricing`\n\n" +
				"Preview deleted (PR closed): no other PR is pinned.\n\n" +
				"---\n" +
				"🔗 [chore(preview): delete feature-add-pricing preview](" + commitURL + ")",
		},
		{
			name:    "label removed",
			removed: []string{"backend-1"},
			reason:  "label `preview` removed",
			message: "chore(preview): remove backend-1 from feature-add-pricing",
			want: "## 🧹 Preview: `feature-add-pricing`\n\n" +
				"Removed `backend-1` from this preview (label `preview` removed).\n\n" +
				"---\n" +
				"🔗 [chore(preview): remove backend-1 from feature-add-pricing](" + commitURL + ")",
		},
		{
			name:    "deleted after label removed",
			removed: []string{"backend-1"},
			deleted: true,
			reason:  "label `preview` removed",
			message: "chore(preview): delete feature-add-pricing preview",
			want: "## 🧹 Preview: `feature-add-pricing`\n\n" +
				"Preview deleted (label `preview` removed): no other PR is pinned.\n\n" +
				"---\n" +
				"🔗 [chore(preview): delete feature-add-pricing preview](" + commitURL + ")",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := preview.BuildTeardownSummary(slug, tc.removed, tc.deleted, tc.reason, tc.message, commitURL)
			if got != tc.want {
				t.Errorf("summary mismatch\n got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestBuildExpirySummary(t *testing.T) {
	got := preview.BuildExpirySummary(
		"feature-add-pricing", 168*time.Hour,
		"chore(preview): expire feature-add-pricing preview",
		"https://github.com/acme/gitops/commit/deadbeef",
	)
	want := "## 🧹 Preview: `feature-add-pricing`\n\n" +
		"Preview deleted: no deploy for 168h0m0s.\n\n" +
		"---\n" +
		"🔗 [chore(preview): expire feature-add-pricing preview](https://github.com/acme/gitops/commit/deadbeef)"
	if got != want {
		t.Errorf("summary mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildLabelConflictSummary(t *testing.T) {
	got := preview.BuildLabelConflictSummary("preview", []string{"preview:a", "Preview:B"})
	want := "## ⚠️ Preview: conflicting labels\n\n" +
		"Keep one `preview:` label. This PR has `preview:a`, `Preview:B`."
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildServiceHeldSummary(t *testing.T) {
	got := preview.BuildServiceHeldSummary("pricing", "front", "https://github.com/acme/front/pull/7", "preview")
	want := "## ⚠️ Preview: `pricing`\n\n" +
		"`front` is already pinned in this preview by https://github.com/acme/front/pull/7. " +
		"Once that pull request leaves this preview, push to this pull request or re-add its label. " +
		"If that pull request is closed, or now carries a different `preview:` label, " +
		"it is released within the next cleanup run."
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

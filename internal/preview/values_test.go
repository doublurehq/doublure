package preview_test

import (
	"slices"
	"testing"
	"time"

	"github.com/doublurehq/doublure/internal/preview"
)

func sampleInputs() preview.DeployInputs {
	return preview.DeployInputs{
		ServiceName:    "backend-1",
		HeadRef:        "feature/add-pricing",
		CommitSha:      "abc1234567890",
		PRAuthor:       "octocat",
		PRURL:          "https://github.com/acme/backend-1/pull/42",
		PRNumber:       "42",
		Timestamp:      "2026-05-26T10:00:00Z",
		WorkflowRunURL: "https://github.com/acme/backend-1/actions/runs/1",
	}
}

func TestBuildPreviewValues(t *testing.T) {
	catalog := []string{"backend-1", "backend-2", "front"}
	in := sampleInputs()
	vals := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	if got := len(vals.Services); got != 3 {
		t.Fatalf("len(Services) = %d, want 3", got)
	}

	// Order must follow catalog.
	for i, want := range catalog {
		if vals.Services[i].Name != want {
			t.Errorf("Services[%d].Name = %q, want %q", i, vals.Services[i].Name, want)
		}
	}

	// Only the triggering service is pinned and carries metadata.
	if vals.Services[0].CommitSha != in.CommitSha {
		t.Errorf("Services[0].CommitSha = %q, want %q", vals.Services[0].CommitSha, in.CommitSha)
	}
	if vals.Services[0].Metadata == nil {
		t.Fatal("Services[0].Metadata is nil; want populated")
	}
	if got := vals.Services[0].Metadata.PRAuthor; got != in.PRAuthor {
		t.Errorf("metadata pr-author = %q, want %q", got, in.PRAuthor)
	}
	for i, svc := range vals.Services[1:] {
		if svc.CommitSha != "" {
			t.Errorf("Services[%d].CommitSha = %q, want empty (main-tracking)", i+1, svc.CommitSha)
		}
		if svc.Metadata != nil {
			t.Errorf("Services[%d].Metadata not nil; want nil for main-tracking entries", i+1)
		}
	}
}

func TestUpdatePreviewValues_PreservesCreatedAt(t *testing.T) {
	catalog := []string{"backend-1", "backend-2", "front"}
	in := sampleInputs()
	existing := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)
	originalCreatedAt := existing.Services[0].Metadata.CreatedAt

	in2 := in
	in2.CommitSha = "newsha000"
	in2.Timestamp = "2026-05-26T11:00:00Z"

	updated := preview.UpdatePreviewValues(existing, in2.ServiceName, in2.CommitSha, in2)

	svc := updated.Services[0]
	if svc.CommitSha != in2.CommitSha {
		t.Errorf("CommitSha = %q, want %q", svc.CommitSha, in2.CommitSha)
	}
	if got := svc.Metadata.CreatedAt; got != originalCreatedAt {
		t.Errorf("created-at = %q, want preserved %q", got, originalCreatedAt)
	}
	if got := svc.Metadata.UpdatedAt; got != in2.Timestamp {
		t.Errorf("updated-at = %q, want %q", got, in2.Timestamp)
	}
}

func TestUpdatePreviewValues_AppendsNewService(t *testing.T) {
	catalog := []string{"backend-1", "backend-2", "front"}
	in := sampleInputs()
	existing := preview.BuildPreviewValues(in.ServiceName, in.CommitSha, catalog, in)

	in2 := in
	in2.ServiceName = "extra-svc"
	in2.CommitSha = "xyz0987"
	in2.Timestamp = "2026-05-26T12:00:00Z"

	updated := preview.UpdatePreviewValues(existing, in2.ServiceName, in2.CommitSha, in2)
	if got := len(updated.Services); got != 4 {
		t.Fatalf("len = %d, want 4 after append", got)
	}
	last := updated.Services[3]
	if last.Name != "extra-svc" || last.CommitSha != "xyz0987" {
		t.Errorf("appended entry = %+v, want extra-svc/xyz0987", last)
	}
	if last.Metadata.CreatedAt != in2.Timestamp {
		t.Errorf("new entry created-at = %q, want %q",
			last.Metadata.CreatedAt, in2.Timestamp)
	}
}

func TestUpdatePreviewValues_FreshTimestampsWhenNoExistingMetadata(t *testing.T) {
	// Service exists in the values doc but as a main-tracker (no metadata yet);
	// the update should populate metadata using the new timestamp for both
	// created-at and updated-at since there's nothing to preserve.
	catalog := []string{"backend-1", "backend-2"}
	in := sampleInputs()
	existing := preview.BuildPreviewValues("backend-2", "sha-other", catalog, in)

	in2 := in
	in2.ServiceName = "backend-1"
	in2.CommitSha = "newsha"
	in2.Timestamp = "2026-05-26T13:00:00Z"

	updated := preview.UpdatePreviewValues(existing, in2.ServiceName, in2.CommitSha, in2)
	svc := updated.Services[0]
	if svc.Metadata.CreatedAt != in2.Timestamp {
		t.Errorf("created-at = %q, want fresh %q",
			svc.Metadata.CreatedAt, in2.Timestamp)
	}
}

// pinned returns an entry pinned to sha by the PR at prURL.
func pinned(name, sha, prURL string) preview.ServiceEntry {
	return preview.ServiceEntry{Name: name, CommitSha: sha, Metadata: &preview.Metadata{PRURL: prURL}}
}

func TestUnpinPR(t *testing.T) {
	const (
		pr42 = "https://github.com/acme/backend-1/pull/42"
		pr7  = "https://github.com/acme/backend-2/pull/7"
	)
	cases := []struct {
		name        string
		services    []preview.ServiceEntry
		owner, repo string
		number      int
		wantRemoved []string
		wantPinned  bool
	}{
		{
			name:       "same number in another repo",
			services:   []preview.ServiceEntry{pinned("backend-1", "a", pr42), {Name: "front"}},
			owner:      "acme",
			repo:       "backend-2",
			number:     42,
			wantPinned: true,
		},
		{
			name:       "another number in the same repo",
			services:   []preview.ServiceEntry{pinned("backend-1", "a", pr42), {Name: "front"}},
			owner:      "acme",
			repo:       "backend-1",
			number:     43,
			wantPinned: true,
		},
		{
			name:        "owner and repo differ only by case",
			services:    []preview.ServiceEntry{pinned("backend-1", "a", pr42), {Name: "front"}},
			owner:       "ACME",
			repo:        "Backend-1",
			number:      42,
			wantRemoved: []string{"backend-1"},
		},
		{
			name: "no metadata",
			services: []preview.ServiceEntry{
				{Name: "backend-1", CommitSha: "a"},
				{Name: "front"},
			},
			owner:      "acme",
			repo:       "backend-1",
			number:     42,
			wantPinned: true,
		},
		{
			name:       "unparsable pr-url",
			services:   []preview.ServiceEntry{pinned("backend-1", "a", "not-a-url/pull/42")},
			owner:      "acme",
			repo:       "backend-1",
			number:     42,
			wantPinned: true,
		},
		{
			name: "one PR pinning two services",
			services: []preview.ServiceEntry{
				pinned("api", "a", pr42),
				{Name: "front"},
				pinned("worker", "a", pr42),
			},
			owner:       "acme",
			repo:        "backend-1",
			number:      42,
			wantRemoved: []string{"api", "worker"},
		},
		{
			name: "two PRs, one closes",
			services: []preview.ServiceEntry{
				pinned("backend-1", "a", pr42),
				pinned("backend-2", "b", pr7),
				{Name: "front"},
			},
			owner:       "acme",
			repo:        "backend-1",
			number:      42,
			wantRemoved: []string{"backend-1"},
			wantPinned:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := preview.PreviewValues{Services: tc.services}
			got, removed, stillPinned := preview.UnpinPR(in, tc.owner, tc.repo, tc.number)
			if !slices.Equal(removed, tc.wantRemoved) {
				t.Errorf("removed = %v, want %v", removed, tc.wantRemoved)
			}
			if stillPinned != tc.wantPinned {
				t.Errorf("pinned = %v, want %v", stillPinned, tc.wantPinned)
			}
			assertUnpinned(t, got, tc.services, tc.wantRemoved)
		})
	}
}

// assertUnpinned checks that got keeps every entry of orig in order and that
// the removed services are bare main-tracking entries.
func assertUnpinned(t *testing.T, got preview.PreviewValues, orig []preview.ServiceEntry, removed []string) {
	t.Helper()
	if len(got.Services) != len(orig) {
		t.Fatalf("len(Services) = %d, want %d (entries are kept)", len(got.Services), len(orig))
	}
	for i, svc := range got.Services {
		if svc.Name != orig[i].Name {
			t.Errorf("Services[%d].Name = %q, want %q", i, svc.Name, orig[i].Name)
		}
		if slices.Contains(removed, svc.Name) && (svc.CommitSha != "" || svc.Metadata != nil) {
			t.Errorf("Services[%d] = %+v, want bare main-tracking entry", i, svc)
		}
	}
}

func TestUnpinPR_DoesNotMutateInput(t *testing.T) {
	const prURL = "https://github.com/acme/backend-1/pull/42"
	in := preview.PreviewValues{Services: []preview.ServiceEntry{
		pinned("backend-1", "abc", prURL),
		{Name: "front"},
	}}
	got, removed, _ := preview.UnpinPR(in, "acme", "backend-1", 42)
	if len(removed) != 1 || got.Services[0].CommitSha != "" {
		t.Fatalf("UnpinPR did not unpin: removed = %v, got = %+v", removed, got)
	}
	if in.Services[0].CommitSha != "abc" || in.Services[0].Metadata == nil || in.Services[0].Metadata.PRURL != prURL {
		t.Errorf("input mutated: %+v", in.Services[0])
	}
}

func TestLastDeploy(t *testing.T) {
	pinned := func(name, updatedAt string) preview.ServiceEntry {
		return preview.ServiceEntry{Name: name, CommitSha: "abc", Metadata: &preview.Metadata{UpdatedAt: updatedAt}}
	}
	cases := []struct {
		name   string
		vals   preview.PreviewValues
		want   string
		wantOK bool
	}{
		{
			name: "max over pins",
			vals: preview.PreviewValues{Services: []preview.ServiceEntry{
				pinned("backend-1", "2026-05-26T10:00:00Z"),
				pinned("backend-2", "2026-05-27T09:00:00Z"),
				pinned("front", "2026-05-25T12:00:00Z"),
			}},
			want:   "2026-05-27T09:00:00Z",
			wantOK: true,
		},
		{
			name: "pinless entries ignored",
			vals: preview.PreviewValues{Services: []preview.ServiceEntry{
				pinned("backend-1", "2026-05-26T10:00:00Z"),
				{Name: "backend-2", Metadata: &preview.Metadata{UpdatedAt: "2026-06-01T00:00:00Z"}},
				{Name: "front"},
			}},
			want:   "2026-05-26T10:00:00Z",
			wantOK: true,
		},
		{
			name: "unparseable updated-at ignored",
			vals: preview.PreviewValues{Services: []preview.ServiceEntry{
				pinned("backend-1", "not-a-time"),
				pinned("backend-2", "2026-05-26T10:00:00Z"),
				{Name: "front", CommitSha: "def"},
			}},
			want:   "2026-05-26T10:00:00Z",
			wantOK: true,
		},
		{
			name: "none parseable",
			vals: preview.PreviewValues{Services: []preview.ServiceEntry{
				pinned("backend-1", "2026-05-26"),
				{Name: "backend-2", CommitSha: "def"},
				{Name: "front"},
			}},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := preview.LastDeploy(tc.vals)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if s := got.Format(time.RFC3339); s != tc.want {
				t.Errorf("LastDeploy = %s, want %s", s, tc.want)
			}
		})
	}
}

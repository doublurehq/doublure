package preview_test

import (
	"testing"

	"github.com/pixelfactoryio/previo/internal/preview"
)

func sampleInputs() preview.DeployInputs {
	return preview.DeployInputs{
		ServiceName:    "backend-1",
		HeadRef:        "feature/add-pricing",
		CommitSha:      "abc1234567890",
		PRAuthor:       "amine",
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
	if got := vals.Services[0].Metadata.Get(preview.MetaPRAuthor); got != in.PRAuthor {
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
	originalCreatedAt := existing.Services[0].Metadata.Get(preview.MetaCreatedAt)

	in2 := in
	in2.CommitSha = "newsha000"
	in2.Timestamp = "2026-05-26T11:00:00Z"

	updated := preview.UpdatePreviewValues(existing, in2.ServiceName, in2.CommitSha, in2)

	svc := updated.Services[0]
	if svc.CommitSha != in2.CommitSha {
		t.Errorf("CommitSha = %q, want %q", svc.CommitSha, in2.CommitSha)
	}
	if got := svc.Metadata.Get(preview.MetaCreatedAt); got != originalCreatedAt {
		t.Errorf("created-at = %q, want preserved %q", got, originalCreatedAt)
	}
	if got := svc.Metadata.Get(preview.MetaUpdatedAt); got != in2.Timestamp {
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
	if last.Metadata.Get(preview.MetaCreatedAt) != in2.Timestamp {
		t.Errorf("new entry created-at = %q, want %q",
			last.Metadata.Get(preview.MetaCreatedAt), in2.Timestamp)
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
	if svc.Metadata.Get(preview.MetaCreatedAt) != in2.Timestamp {
		t.Errorf("created-at = %q, want fresh %q",
			svc.Metadata.Get(preview.MetaCreatedAt), in2.Timestamp)
	}
}

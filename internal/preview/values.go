package preview

import "strings"

// BuildPreviewValues constructs a fresh PreviewValues for a slug given the
// service catalog. The triggering service (serviceName) is pinned to
// commitSha and carries metadata; every other catalog service renders as a
// bare entry tracking main.
//
// The order of services in the result matches the order of the catalog slice.
func BuildPreviewValues(serviceName, commitSha string, catalog []string, inputs DeployInputs) PreviewValues {
	services := make([]ServiceEntry, 0, len(catalog))
	for _, name := range catalog {
		entry := ServiceEntry{Name: name}
		if name == serviceName {
			entry.CommitSha = commitSha
			entry.Metadata = BuildServiceMetadata(inputs)
		}
		services = append(services, entry)
	}
	return PreviewValues{Services: services}
}

// UpdatePreviewValues merges a deploy event into an existing PreviewValues:
//   - if serviceName already has an entry, overwrite commitSha and metadata
//     but preserve the original created-at;
//   - if the service is absent, append a new pinned entry.
//
// Other services are left untouched (main-tracking entries keep their state).
func UpdatePreviewValues(existing PreviewValues, serviceName, commitSha string, inputs DeployInputs) PreviewValues {
	for i := range existing.Services {
		if existing.Services[i].Name != serviceName {
			continue
		}
		md := BuildServiceMetadata(inputs)
		if old := existing.Services[i].Metadata; old != nil && old.CreatedAt != "" {
			md.CreatedAt = old.CreatedAt
		}
		existing.Services[i].CommitSha = commitSha
		existing.Services[i].Metadata = md
		return existing
	}
	existing.Services = append(existing.Services, ServiceEntry{
		Name:      serviceName,
		CommitSha: commitSha,
		Metadata:  BuildServiceMetadata(inputs),
	})
	return existing
}

// UnpinPR returns a copy of vals where every entry deployed by the PR
// owner/repo#number (matched on its metadata pr-url; owner and repo compare
// case-insensitively) is reset to a bare main-tracking entry. It also returns
// the names of the unpinned services in file order, and whether any entry is
// still pinned to a commit. vals is never mutated.
func UnpinPR(vals PreviewValues, owner, repo string, number int) (PreviewValues, []string, bool) {
	services := make([]ServiceEntry, len(vals.Services))
	copy(services, vals.Services)

	var removed []string
	pinned := false
	for i := range services {
		if belongsToPR(services[i], owner, repo, number) {
			services[i].CommitSha = ""
			services[i].Metadata = nil
			removed = append(removed, services[i].Name)
			continue
		}
		if services[i].CommitSha != "" {
			pinned = true
		}
	}
	return PreviewValues{Services: services}, removed, pinned
}

// belongsToPR reports whether entry was deployed by owner/repo#number.
func belongsToPR(entry ServiceEntry, owner, repo string, number int) bool {
	if entry.Metadata == nil {
		return false
	}
	o, r, n, ok := ParsePRURL(entry.Metadata.PRURL)
	return ok && n == number && strings.EqualFold(o, owner) && strings.EqualFold(r, repo)
}

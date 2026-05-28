package preview

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
		preservedCreatedAt := existing.Services[i].Metadata.Get(MetaCreatedAt)
		md := BuildServiceMetadata(inputs)
		if preservedCreatedAt != "" {
			md.Set(MetaCreatedAt, preservedCreatedAt)
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

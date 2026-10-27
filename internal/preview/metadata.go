package preview

// Metadata is the per-service annotation bag. yaml.v3 emits the known keys in
// field order and quotes any value that would not read back as a string, so
// the values file is byte-stable and safe for YAML 1.1 readers such as Helm.
// Every known key is always written: the chart renders a missing key as an
// empty annotation. Unknown keys are kept in Extra and written sorted after
// the known ones.
type Metadata struct {
	PRAuthor       string            `yaml:"pr-author"`
	PRURL          string            `yaml:"pr-url"`
	PRNumber       string            `yaml:"pr-number"`
	CreatedAt      string            `yaml:"created-at"`
	UpdatedAt      string            `yaml:"updated-at"`
	VCSRefName     string            `yaml:"vcs.ref.name"`
	PipelineRunURL string            `yaml:"cicd.pipeline.run.url"`
	Extra          map[string]string `yaml:",inline"`
}

// BuildServiceMetadata constructs the canonical Metadata for a deploy event.
// On a fresh create, both created-at and updated-at equal inputs.Timestamp;
// UpdatePreviewValues overwrites updated-at while preserving the existing
// created-at.
func BuildServiceMetadata(inputs DeployInputs) *Metadata {
	return &Metadata{
		PRAuthor:       inputs.PRAuthor,
		PRURL:          inputs.PRURL,
		PRNumber:       inputs.PRNumber,
		CreatedAt:      inputs.Timestamp,
		UpdatedAt:      inputs.Timestamp,
		VCSRefName:     inputs.HeadRef,
		PipelineRunURL: inputs.WorkflowRunURL,
	}
}

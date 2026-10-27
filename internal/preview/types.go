package preview

// DeployInputs collects the per-deploy values needed to build or update a
// preview environment's values.yaml.
type DeployInputs struct {
	GitopsRepo     string // "owner/repo"
	ServiceName    string
	HeadRef        string
	CommitSha      string
	PRAuthor       string
	PRURL          string
	PRNumber       string
	Timestamp      string
	WorkflowRunURL string
}

// ServiceEntry is one item in PreviewValues.Services. CommitSha and Metadata
// are populated only for the service that owns the preview; other entries
// render as a bare {name: ...} and track main.
type ServiceEntry struct {
	Name      string    `yaml:"name"`
	CommitSha string    `yaml:"commitSha,omitempty"`
	Metadata  *Metadata `yaml:"metadata,omitempty"`
}

// PreviewValues is the root document persisted at previews/<slug>/values.yaml.
type PreviewValues struct {
	Services []ServiceEntry `yaml:"services"`
}

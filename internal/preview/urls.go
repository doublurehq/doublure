package preview

// URLConfig is the parsed `preview` block of the gitops repo's services.yaml.
// The zero value means the feature is off: no hosts and no links.
type URLConfig struct {
	// Domain is the parent of every preview host. Empty means off.
	Domain string
	// TLS selects https (true) or http (false) for the PR-comment links.
	TLS bool
	// Expose maps each exposed service name to its link path (default "/").
	Expose map[string]string
}

// Enabled reports whether per-preview URLs are configured.
func (c URLConfig) Enabled() bool {
	return c.Domain != ""
}

// Host returns the per-preview host of service: <service>-<slug>.<domain>.
func (c URLConfig) Host(service, slug string) string {
	return service + "-" + slug + "." + c.Domain
}

// Link returns the link text (<host><path>) and URL (<scheme>://<host><path>)
// for service in the preview slug. The boolean is false when the feature is
// off or the service is not exposed.
func (c URLConfig) Link(service, slug string) (string, string, bool) {
	if !c.Enabled() {
		return "", "", false
	}
	path, exposed := c.Expose[service]
	if !exposed {
		return "", "", false
	}
	scheme := "https"
	if !c.TLS {
		scheme = "http"
	}
	text := c.Host(service, slug) + path
	return text, scheme + "://" + text, true
}

package preview

import "strings"

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
// A <service>-<slug> label longer than 63 characters (the DNS label limit) is
// cut and suffixed with a short hash of the full label.
func (c URLConfig) Host(service, slug string) string {
	return capLabel(service+"-"+slug, maxLabelLen) + "." + c.Domain
}

// Link returns the Markdown-safe link text (<host><path>) and URL
// (<scheme>://<host><path>, with the path percent-encoded) for the host
// written on service's entry. The path is service's Expose path, or "/" when
// service is not a key of Expose. The boolean is false only when host is
// empty.
func (c URLConfig) Link(service, host string) (string, string, bool) {
	if host == "" {
		return "", "", false
	}
	path, exposed := c.Expose[service]
	if !exposed {
		path = "/"
	}
	scheme := "https"
	if !c.TLS {
		scheme = "http"
	}
	return markdownEscape(host + path), scheme + "://" + host + escapeURLPath(path), true
}

// urlSafe lists the bytes kept as-is in a link path: unreserved and
// sub-delimiter characters plus the path, query and fragment delimiters.
// Parentheses are left out so the URL cannot close a Markdown link.
const urlSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~!$&'*+,;=:@/?"

const hexDigits = "0123456789ABCDEF"

// escapeURLPath percent-encodes every byte of p that is not valid in a URL
// path, query or fragment. The first "#" starts the fragment; existing %XX
// escapes are kept.
func escapeURLPath(p string) string {
	var b strings.Builder
	fragment := false
	for i := range len(p) {
		c := p[i]
		switch {
		case strings.IndexByte(urlSafe, c) >= 0:
			b.WriteByte(c)
		case c == '#' && !fragment:
			fragment = true
			b.WriteByte(c)
		case c == '%' && i+2 < len(p) && isHex(p[i+1]) && isHex(p[i+2]):
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0x0f])
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return strings.IndexByte("0123456789abcdefABCDEF", c) >= 0
}

// markdownEscape backslash-escapes the characters of s that could end or
// reshape Markdown link text.
func markdownEscape(s string) string {
	return strings.NewReplacer(
		`\`, `\\`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`,
		"<", `\<`, ">", `\>`, "`", "\\`", "*", `\*`, "_", `\_`, "~", `\~`, "&", `\&`,
	).Replace(s)
}

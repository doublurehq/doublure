package preview

import (
	"regexp"
	"strconv"
)

// prURLPattern extracts (owner, repo, number) from a github.com PR URL.
var prURLPattern = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/pull/(\d+)`)

// ParsePRURL extracts the owner, repo and PR number from a github.com pull
// request URL. Trailing path segments (e.g. /files) are ignored. ok is false
// when u is not a recognizable PR URL.
func ParsePRURL(u string) (string, string, int, bool) {
	m := prURLPattern.FindStringSubmatch(u)
	if m == nil {
		return "", "", 0, false
	}
	number, err := strconv.Atoi(m[3])
	if err != nil {
		return "", "", 0, false
	}
	return m[1], m[2], number, true
}

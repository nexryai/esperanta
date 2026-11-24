package config

import (
	"fmt"
	"net/url"
	"strings"
)

// GitPrefixes returns the logical HTTPS hostname and the allowed repository paths.
func (t RequestTarget) GitPrefixes() (string, []string, error) {
	if len(t.AllowedPrefixes) == 0 {
		return "", nil, fmt.Errorf("must not be empty")
	}
	var host string
	paths := make([]string, 0, len(t.AllowedPrefixes))
	for i, prefix := range t.AllowedPrefixes {
		u, err := url.Parse(prefix)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" ||
			u.RawQuery != "" || u.ForceQuery || strings.Contains(prefix, "#") ||
			u.Host != u.Hostname() || !validDomainName(CanonicalDomain(u.Host)) {
			return "", nil, fmt.Errorf("entry %d must be an HTTPS URL with a hostname and no port, credentials, query or fragment", i)
		}
		repoPath := strings.TrimSuffix(u.Path, "/")
		if u.RawPath != "" || !ValidGitPath(repoPath) {
			return "", nil, fmt.Errorf("entry %d must contain a valid repository path", i)
		}
		h := CanonicalDomain(u.Host)
		if host != "" && h != host {
			return "", nil, fmt.Errorf("entries must share the same HTTPS hostname")
		}
		host = h
		paths = append(paths, repoPath)
	}
	return host, paths, nil
}

// ValidGitPath accepts only unambiguous paths made of URL-unreserved characters.
func ValidGitPath(s string) bool {
	rest, ok := strings.CutPrefix(s, "/")
	if !ok {
		return false
	}
	for part := range strings.SplitSeq(rest, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", rune(c))) {
				return false
			}
		}
	}
	return true
}

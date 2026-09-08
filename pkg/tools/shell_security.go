package tools

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/stpinkie/rhizome/pkg/guard"
	"github.com/stpinkie/rhizome/pkg/utils"
)

// ssrfURLPattern extracts HTTP/HTTPS/FTP URLs from a raw shell command.
// It stops at shell metacharacters and quotes so it does not over-match into
// unrelated command text.
var ssrfURLPattern = regexp.MustCompile(`(?i)\b(?:https?|ftps?|sftp)://[^\s"'<>{}|\\]+`)

// ssrfTrailingPunctuation is stripped from the end of a URL match so that
// punctuation attached to the URL (e.g. a closing parenthesis or comma) does
// not break parsing.
const ssrfTrailingPunctuation = `.,;:!?)]}'` + "`"

// ssrfMetadataHosts are hostnames that resolve to cloud instance metadata
// services. They are blocked in addition to the standard private/reserved IP
// ranges checked by utils.IsObviousPrivateHost.
var ssrfMetadataHosts = map[string]bool{
	"metadata.google.internal":   true,
	"metadata.google.internal.":  true,
	"metadata.compute.internal":  true,
	"metadata.compute.internal.": true,
	"metadata.aws.internal":      true,
	"metadata.aws.internal.":     true,
}

// commandContainsBlockedSSRF checks whether a shell command contains a URL
// that points to a private, local, link-local, or cloud-metadata endpoint.
// It returns the matched URL string and true when one is found.
func commandContainsBlockedSSRF(command string) (string, bool) {
	for _, raw := range ssrfURLPattern.FindAllString(command, -1) {
		raw = strings.TrimRight(raw, ssrfTrailingPunctuation)

		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}

		host := strings.ToLower(strings.TrimSpace(u.Hostname()))
		if host == "" {
			continue
		}

		if isBlockedSSRFHost(host) {
			return raw, true
		}
	}

	return "", false
}

func isBlockedSSRFHost(host string) bool {
	host = strings.TrimSuffix(host, ".")

	if ssrfMetadataHosts[host] {
		return true
	}

	if utils.IsObviousPrivateHost(host, nil, nil) {
		return true
	}

	return false
}

// commandContainsPromptInjection checks a shell command for obvious
// prompt-injection substrings. It returns the matched pattern and true when
// one is found. The shared patterns live in pkg/guard so the tool-call JSON
// extraction path and the registry arg scan can reuse them.
func commandContainsPromptInjection(command string) (string, bool) {
	return guard.ContainsPromptInjection(command)
}

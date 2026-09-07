package tools

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

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

// promptInjectionPatterns matches common instruction-injection phrases that may
// appear in an LLM-generated tool argument. These are conservative whole-phrase
// patterns; the goal is to catch obvious attempts, not to enumerate every
// possible adversarial encoding.
var promptInjectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bignore\s+(?:all\s+)?(?:previous|the|your)\s+instructions\b`),
	regexp.MustCompile(`(?i)\bdisregard\s+(?:all\s+)?(?:previous|the|your)\s+instructions\b`),
	regexp.MustCompile(`(?i)\bnew\s+instructions?\s*:?`),
	regexp.MustCompile(`(?i)\byou\s+are\s+now\b`),
	regexp.MustCompile(`(?i)\bdeveloper\s+mode\b`),
	regexp.MustCompile(`(?i)\bignore\s+your\s+safety\b`),
	regexp.MustCompile(`(?i)\bdo\s+not\s+follow\s+(?:the|your)\s+instructions\b`),
}

// commandContainsPromptInjection checks a shell command for obvious
// prompt-injection substrings. It returns the matched pattern and true when
// one is found.
func commandContainsPromptInjection(command string) (string, bool) {
	for _, re := range promptInjectionPatterns {
		if match := re.FindString(command); match != "" {
			return match, true
		}
	}

	return "", false
}

// ssrferrorf formats a guard error message for a blocked SSRF target.
func ssrferrorf(target string) string {
	return fmt.Sprintf("Command blocked by safety guard (SSRF target: %s)", target)
}

// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package guard provides shared input-safety checks (prompt-injection phrase
// detection) used by tool argument validation, shell command screening, and
// tool-call JSON extraction.
//
// The package is intentionally a leaf: it must not import other Rhizome
// packages so that both low-level (providers) and higher-level (tools)
// packages can depend on it without creating import cycles.
package guard

import (
	"fmt"
	"regexp"
)

// InjectionPatterns matches common instruction-injection phrases that may
// appear in an LLM-generated tool argument or in tool-call JSON extracted from
// free-text model output. These are conservative whole-phrase patterns; the
// goal is to catch obvious attempts, not to enumerate every possible
// adversarial encoding.
var InjectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bignore\s+(?:all\s+)?(?:previous|the|your)\s+instructions\b`),
	regexp.MustCompile(`(?i)\bdisregard\s+(?:all\s+)?(?:previous|the|your)\s+instructions\b`),
	regexp.MustCompile(`(?i)\bnew\s+instructions?\s*:?`),
	regexp.MustCompile(`(?i)\byou\s+are\s+now\b`),
	regexp.MustCompile(`(?i)\bdeveloper\s+mode\b`),
	regexp.MustCompile(`(?i)\bignore\s+your\s+safety\b`),
	regexp.MustCompile(`(?i)\bdo\s+not\s+follow\s+(?:the|your)\s+instructions\b`),
}

// ContainsPromptInjection checks a string for obvious prompt-injection phrases.
// It returns the matched text and true when one is found.
func ContainsPromptInjection(s string) (string, bool) {
	for _, re := range InjectionPatterns {
		if match := re.FindString(s); match != "" {
			return match, true
		}
	}
	return "", false
}

// ScanArgsForInjection recursively scans string values in a tool-argument map
// for prompt-injection phrases. It returns the dotted argument path, the matched
// text, and true on the first match found.
func ScanArgsForInjection(args map[string]any) (path, match string, found bool) {
	for key, val := range args {
		if p, m, ok := scanValueForInjection(key, val); ok {
			return p, m, true
		}
	}
	return "", "", false
}

func scanValueForInjection(path string, val any) (string, string, bool) {
	switch v := val.(type) {
	case string:
		if match, ok := ContainsPromptInjection(v); ok {
			return path, match, true
		}
	case map[string]any:
		for key, elem := range v {
			if p, m, ok := scanValueForInjection(path+"."+key, elem); ok {
				return p, m, true
			}
		}
	case []any:
		for i, elem := range v {
			if p, m, ok := scanValueForInjection(fmt.Sprintf("%s[%d]", path, i), elem); ok {
				return p, m, true
			}
		}
	}
	return "", "", false
}

// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// Package redact provides shared secret-masking utilities used by the logger,
// the sensitive-data filter for tool results, and any other code path that may
// emit user credentials or tokens.
//
// The package is intentionally a leaf: it must not import other Rhizome
// packages so that low-level packages (logger, config) can depend on it
// without creating import cycles.
package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
)

// FilteredPlaceholder is the wholesale redaction marker used for values that
// are credential-bearing by position (e.g. under an api_key map key) rather
// than by matching a secret pattern.
const FilteredPlaceholder = "[FILTERED]"

var (
	// botTokenRe matches the bot ID prefix and the secret part of a Telegram bot token.
	// Groups: 1 = "bot<id>:", 2 = first 4 chars of secret, 3 = last 4 chars.
	botTokenRe = regexp.MustCompile(`(bot\d+:)([A-Za-z0-9_-]{4})[A-Za-z0-9_-]{12,}([A-Za-z0-9_-]{4})`)

	// bearerTokenRe matches a "Bearer <token>" style credential, either as a header
	// value or a bare token.
	bearerTokenRe = regexp.MustCompile(
		`(?i)(\bbearer\s+|Authorization:\s*Bearer\s+)([A-Za-z0-9_.-]{4})[A-Za-z0-9_.-]{4,}([A-Za-z0-9_.-]{4})`,
	)

	// apiKeyRe matches key=value or key: value style API keys.
	apiKeyRe = regexp.MustCompile(
		`(?i)(\bapi[_-]?key\s*[:=]\s*)([A-Za-z0-9_.-]{4})[A-Za-z0-9_.-]{4,}([A-Za-z0-9_.-]{4})`,
	)

	// genericTokenRe matches generic "token" or "auth" key-value pairs.
	genericTokenRe = regexp.MustCompile(
		`(?i)(\b(?:token|auth[_-]?token)\s*[:=]\s*)([A-Za-z0-9_.-]{4})[A-Za-z0-9_.-]{4,}([A-Za-z0-9_.-]{4})`,
	)

	// secretPrefixRe matches common secret prefixes (sk-, sk-or-v1-, rk-, etc).
	secretPrefixRe = regexp.MustCompile(
		`(?i)(\b(?:sk|rk|pk|ek)-(?:or-[a-zA-Z0-9]+-)?)([A-Za-z0-9_.-]{4})[A-Za-z0-9_.-]{4,}([A-Za-z0-9_.-]{4})`,
	)
)

// extraMasker optionally masks configured secrets (SecureString values) on top
// of the generic patterns. Registered via SetExtraMasker, typically from
// config.SetGlobal so every downstream masking call covers configured values.
var extraMasker atomic.Value // stores func(string) string

// SetExtraMasker registers an additional masking function applied after the
// generic patterns. Passing nil clears it. Typically wired to
// Config.SensitiveDataReplacer().Replace.
func SetExtraMasker(fn func(string) string) {
	if fn == nil {
		extraMasker.Store((func(string) string)(nil))
		return
	}
	extraMasker.Store(fn)
}

// Mask applies the optional extra masker (configured secrets) followed by
// generic secret-pattern masking. Safe to call on any user- or tool-supplied
// string before it reaches logs or the LLM.
func Mask(s string) string {
	var extra func(string) string
	if fn, ok := extraMasker.Load().(func(string) string); ok {
		extra = fn
	}
	return MaskWith(s, extra)
}

// MaskWith applies the given extra masking function (configured secrets)
// first — so known credentials become "[FILTERED]" rather than a partially
// pattern-masked form — then applies the generic secret patterns. It does not
// consult the globally-registered extra masker, so callers that already hold
// their masker (e.g. Config.SensitiveDataReplacer) do not apply it twice.
func MaskWith(s string, extra func(string) string) string {
	if s == "" {
		return s
	}

	if extra != nil {
		s = extra(s)
	}
	return maskSecrets(s)
}

// maskSecrets replaces any embedded credentials in s with a redacted placeholder
// that keeps the prefix and the first and last 4 characters of the secret for
// identification. It covers Telegram bot tokens, Bearer/Basic Authorization
// headers, API keys, and common secret prefixes.
func maskSecrets(s string) string {
	// Handle Authorization headers first so the scheme (Bearer/Basic) is kept
	// and already-redacted values are not masked again by later passes.
	s = maskAuthorizationHeader(s)

	s = botTokenRe.ReplaceAllString(s, "${1}${2}****${3}")
	s = bearerTokenRe.ReplaceAllString(s, "${1}${2}****${3}")
	s = apiKeyRe.ReplaceAllString(s, "${1}${2}****${3}")
	s = genericTokenRe.ReplaceAllString(s, "${1}${2}****${3}")
	s = secretPrefixRe.ReplaceAllString(s, "${1}${2}****${3}")

	return s
}

// secretishKeyRe matches map/JSON key names that conventionally carry
// credentials; values under such keys are redacted wholesale instead of
// relying on the value patterns above.
var secretishKeyRe = regexp.MustCompile(
	`(?i)(?:^|[^a-z])(?:api[_-]?key|key|token|secret|passw(?:or)?d|credentials?|auth)`)

// IsSecretishKey reports whether a map or JSON key name looks like it holds a
// credential (api_key, token, password, auth, …).
func IsSecretishKey(key string) bool {
	return secretishKeyRe.MatchString(key)
}

// jsonSecretKVRe matches `"<key>": "<value>"` members whose key name is
// credential-bearing so the value can be redacted without breaking the JSON.
var jsonSecretKVRe = regexp.MustCompile(
	`"([^"\\]*(?i:api[_-]?key|token|secret|passw(?:or)?d|credentials?|auth|key)[^"\\]*)"` +
		`(\s*:\s*)"(?:[^"\\]|\\.)*"`)

// maskJSON redacts credential-bearing `"key": "value"` members and applies
// the generic patterns to a serialized JSON document. Replacements stay
// inside string literals, so the output remains valid JSON.
func maskJSON(raw []byte) []byte {
	masked := jsonSecretKVRe.ReplaceAll(raw, []byte(`"${1}"${2}"[FILTERED]"`))
	return []byte(Mask(string(masked)))
}

func maskKeyedValue(key string, val any) any {
	if s, ok := val.(string); ok {
		if s != "" && IsSecretishKey(key) {
			return FilteredPlaceholder
		}
		return Mask(s)
	}
	return MaskAny(val)
}

// MaskAny deep-masks a log-field value: strings pass through Mask, maps and
// slices are masked recursively, and values under secret-looking map keys are
// redacted wholesale. Types it cannot walk directly are masked through their
// JSON encoding so the logged shape is preserved (the returned
// json.RawMessage still renders as the original object via event.Interface).
func MaskAny(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return Mask(t)
	case error:
		return Mask(t.Error())
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = maskKeyedValue(k, val)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(t))
		for k, val := range t {
			if val != "" && IsSecretishKey(k) {
				out[k] = FilteredPlaceholder
			} else {
				out[k] = Mask(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = MaskAny(val)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, val := range t {
			out[i] = Mask(val)
		}
		return out
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return Mask(fmt.Sprintf("%+v", t))
		}
		masked := maskJSON(raw)
		if bytes.Equal(masked, raw) {
			return t // nothing secret found — keep the original value/shape
		}
		if json.Valid(masked) {
			return json.RawMessage(masked)
		}
		return Mask(string(raw))
	}
}

// maskAuthorizationHeader redacts the credential portion of any "Authorization"
// header. It preserves the scheme (e.g., Bearer, Basic) and keeps the first and
// last 4 characters of the token for identification. Already-redacted values
// (containing "****") are left untouched.
func maskAuthorizationHeader(s string) string {
	const header = "Authorization:"
	offset := 0
	for {
		idx := strings.Index(s[offset:], header)
		if idx == -1 {
			break
		}
		idx += offset
		valStart := idx + len(header)
		if valStart >= len(s) {
			break
		}
		valEnd := strings.IndexAny(s[valStart:], "\r\n")
		if valEnd == -1 {
			valEnd = len(s) - valStart
		}
		value := strings.TrimSpace(s[valStart : valStart+valEnd])

		if len(value) > 12 && !strings.Contains(value, "****") {
			fields := strings.Fields(value)
			if len(fields) >= 2 {
				token := fields[len(fields)-1]
				if len(token) > 12 {
					redacted := token[:4] + "****" + token[len(token)-4:]
					value = value[:len(value)-len(token)] + redacted
				}
			} else if len(fields) == 1 {
				token := fields[0]
				if len(token) > 12 {
					value = token[:4] + "****" + token[len(token)-4:]
				}
			}
			s = s[:valStart] + " " + value + s[valStart+valEnd:]
		}
		offset = valStart
	}
	return s
}

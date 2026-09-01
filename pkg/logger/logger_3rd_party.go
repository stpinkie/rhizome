// this file is for compatible with 3rd party loggers, should not be called in Rhizome project

package logger

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// botTokenRe matches the bot ID prefix and the secret part of a Telegram bot token.
	// Groups: 1 = "bot<id>:", 2 = first 4 chars of secret, 3 = last 4 chars.
	botTokenRe = regexp.MustCompile(`(bot\d+:)([A-Za-z0-9_-]{4})[A-Za-z0-9_-]{12,}([A-Za-z0-9_-]{4})`)

	// bearerTokenRe matches a "Bearer <token>" style credential, either as a header
	// value or a bare token.
	bearerTokenRe = regexp.MustCompile(`(?i)(\bbearer\s+|Authorization:\s*Bearer\s+)([A-Za-z0-9_.-]{4})[A-Za-z0-9_.-]{4,}([A-Za-z0-9_.-]{4})`)

	// apiKeyRe matches key=value or key: value style API keys.
	apiKeyRe = regexp.MustCompile(`(?i)(\bapi[_-]?key\s*[:=]\s*)([A-Za-z0-9_.-]{4})[A-Za-z0-9_.-]{4,}([A-Za-z0-9_.-]{4})`)

	// genericTokenRe matches generic "token" or "auth" key-value pairs.
	genericTokenRe = regexp.MustCompile(`(?i)(\b(?:token|auth[_-]?token)\s*[:=]\s*)([A-Za-z0-9_.-]{4})[A-Za-z0-9_.-]{4,}([A-Za-z0-9_.-]{4})`)

	// secretPrefixRe matches common secret prefixes (sk-, sk-or-v1-, rk-, etc).
	secretPrefixRe = regexp.MustCompile(`(?i)(\b(?:sk|rk|pk|ek)-(?:or-[a-zA-Z0-9]+-)?)([A-Za-z0-9_.-]{4})[A-Za-z0-9_.-]{4,}([A-Za-z0-9_.-]{4})`)
)

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

// Logger implements common Logger interface
type Logger struct {
	component string
	levels    map[int]LogLevel
}

// Debug logs debug messages
func (b *Logger) Debug(v ...any) {
	logMessage(DEBUG, b.component, maskSecrets(fmt.Sprint(v...)), nil)
}

// Info logs info messages
func (b *Logger) Info(v ...any) {
	logMessage(INFO, b.component, maskSecrets(fmt.Sprint(v...)), nil)
}

// Warn logs warning messages
func (b *Logger) Warn(v ...any) {
	logMessage(WARN, b.component, maskSecrets(fmt.Sprint(v...)), nil)
}

// Error logs error messages
func (b *Logger) Error(v ...any) {
	logMessage(ERROR, b.component, maskSecrets(fmt.Sprint(v...)), nil)
}

// Debugf logs formatted debug messages
func (b *Logger) Debugf(format string, v ...any) {
	logMessage(DEBUG, b.component, maskSecrets(fmt.Sprintf(format, v...)), nil)
}

// Infof logs formatted info messages
func (b *Logger) Infof(format string, v ...any) {
	logMessage(INFO, b.component, maskSecrets(fmt.Sprintf(format, v...)), nil)
}

// Warnf logs formatted warning messages
func (b *Logger) Warnf(format string, v ...any) {
	logMessage(WARN, b.component, maskSecrets(fmt.Sprintf(format, v...)), nil)
}

// Warningf logs formatted warning messages
func (b *Logger) Warningf(format string, v ...any) {
	logMessage(WARN, b.component, maskSecrets(fmt.Sprintf(format, v...)), nil)
}

// Errorf logs formatted error messages
func (b *Logger) Errorf(format string, v ...any) {
	logMessage(ERROR, b.component, maskSecrets(fmt.Sprintf(format, v...)), nil)
}

// Fatalf logs formatted fatal messages and exits
func (b *Logger) Fatalf(format string, v ...any) {
	logMessage(FATAL, b.component, maskSecrets(fmt.Sprintf(format, v...)), nil)
}

// Log logs a message at a given level with caller information
// the func name must be this because 3rd party loggers expect this
// msgL: message level (DEBUG, INFO, WARN, ERROR, FATAL)
// caller: unused parameter reserved for compatibility
// format: format string
// a: format arguments
//
//nolint:goprintffuncname
func (b *Logger) Log(msgL, caller int, format string, a ...any) {
	level := LogLevel(msgL)
	if b.levels != nil {
		if lvl, ok := b.levels[msgL]; ok {
			level = lvl
		}
	}
	logMessage(level, b.component, maskSecrets(fmt.Sprintf(format, a...)), nil)
}

// Sync flushes log buffer (no-op for this implementation)
func (b *Logger) Sync() error {
	return nil
}

// WithLevels sets log levels mapping for this logger
func (b *Logger) WithLevels(levels map[int]LogLevel) *Logger {
	b.levels = levels
	return b
}

// NewLogger creates a new logger instance with optional component name
func NewLogger(component string) *Logger {
	return &Logger{component: component}
}

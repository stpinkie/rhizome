package common

import "strings"

// knownModelPrefixes are provider-name prefixes that appear as the leading
// segment of model identifiers (e.g. "deepseek" in "deepseek/deepseek-chat").
// On endpoints whose model registry uses unprefixed names, a leading known
// provider segment should be stripped before the request is sent.
var knownModelPrefixes = map[string]struct{}{
	"litellm":     {},
	"nearai":      {},
	"venice":      {},
	"moonshot":    {},
	"nvidia":      {},
	"groq":        {},
	"ollama":      {},
	"deepseek":    {},
	"google":      {},
	"openrouter":  {},
	"siliconflow": {},
	"zhipu":       {},
	"mistral":     {},
	"vivgrid":     {},
	"minimax":     {},
	"novita":      {},
	"lmstudio":    {},
	// Note: "openai" is deliberately absent. Model ids like "openai/gpt-4o"
	// appear verbatim in some registries, and OpenAI's own endpoint already
	// receives unprefixed names via protocol extraction.
}

// IsKnownModelPrefix reports whether prefix is a known provider-name prefix.
func IsKnownModelPrefix(prefix string) bool {
	_, ok := knownModelPrefixes[strings.ToLower(strings.TrimSpace(prefix))]
	return ok
}

// StripModelPrefix removes the leading "provider/" segment from model when it
// matches providerName or is a known provider prefix. When apiBase points at a
// host whose model registry already includes upstream provider prefixes
// (OpenRouter being the canonical example), the model is returned unchanged.
func StripModelPrefix(model, providerName, apiBase string) string {
	if strings.Contains(strings.ToLower(apiBase), "openrouter.ai") {
		return model
	}

	before, after, ok := strings.Cut(model, "/")
	if !ok {
		return model
	}

	prefix := strings.ToLower(strings.TrimSpace(before))
	if prefix == "" {
		return model
	}
	if prefix != strings.ToLower(strings.TrimSpace(providerName)) && !IsKnownModelPrefix(prefix) {
		return model
	}
	return after
}

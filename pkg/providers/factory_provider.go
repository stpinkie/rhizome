// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	anthropicmessages "github.com/stpinkie/rhizome/pkg/providers/anthropic_messages"
	"github.com/stpinkie/rhizome/pkg/providers/azure"
	"github.com/stpinkie/rhizome/pkg/providers/bedrock"
	"github.com/stpinkie/rhizome/pkg/providers/gemini"
	openaicompat "github.com/stpinkie/rhizome/pkg/providers/openai_compat"
)

// createClaudeAuthProvider creates a Claude provider using OAuth credentials from auth store.
func createClaudeAuthProvider(apiBase, userAgent string) (LLMProvider, error) {
	cred, err := getCredential("anthropic")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for anthropic. Run: rhizome auth login --provider anthropic")
	}
	return anthropicmessages.NewProviderWithTokenSource(
		cred.AccessToken,
		createClaudeTokenSource(),
		apiBase,
		userAgent,
		0,
	), nil
}

// createCodexAuthProvider creates a Codex provider using OAuth credentials from auth store.
func createCodexAuthProvider() (LLMProvider, error) {
	cred, err := getCredential("openai")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for openai. Run: rhizome auth login --provider openai")
	}
	return NewCodexProviderWithTokenSource(cred.AccessToken, cred.AccountID, createCodexTokenSource()), nil
}

// ExtractProtocol extracts the effective protocol and model identifier from a
// model configuration.
//
// The explicit Provider field takes precedence. When Provider is empty, the
// protocol is inferred from Model. Plain model names default to "openai".
// Provider-prefixed models strip the first slash-separated segment from the
// returned model ID.
//
// The returned protocol is normalized to the provider's canonical spelling.
// Examples:
//   - Model "openai/gpt-4o" -> ("openai", "gpt-4o")
//   - Model "nvidia/z-ai/glm-5.1" -> ("nvidia", "z-ai/glm-5.1")
//   - Provider "nvidia", Model "z-ai/glm-5.1" -> ("nvidia", "z-ai/glm-5.1")
//   - Provider "openai", Model "openai/gpt-4o" -> ("openai", "openai/gpt-4o")
//   - Model "gpt-4o" -> ("openai", "gpt-4o")
func ExtractProtocol(cfg *config.ModelConfig) (protocol, modelID string) {
	if cfg == nil {
		return "", ""
	}

	model := strings.TrimSpace(cfg.Model)
	if provider := strings.TrimSpace(cfg.Provider); provider != "" {
		return NormalizeProvider(provider), model
	}
	return SplitModelProviderAndID(model, "openai")
}

// ResolveAPIBase returns the configured API base, or the protocol default when
// the model uses an HTTP-based provider family with a known default endpoint.
func ResolveAPIBase(cfg *config.ModelConfig) string {
	if cfg == nil {
		return ""
	}
	if apiBase := strings.TrimSpace(cfg.APIBase); apiBase != "" {
		return strings.TrimRight(apiBase, "/")
	}
	protocol, _ := ExtractProtocol(cfg)
	return strings.TrimRight(getDefaultAPIBase(protocol), "/")
}

// CreateProviderFromConfig creates a provider based on the ModelConfig.
// It uses ExtractProtocol to determine which provider to create.
// OpenAI-compatible providers are constructed from catalog metadata; special
// protocols (Anthropic Messages, Gemini, Azure, Bedrock, CLI, OAuth) have
// explicit constructors.
// Returns the provider, the effective model ID from ExtractProtocol, and any error.
func CreateProviderFromConfig(cfg *config.ModelConfig) (LLMProvider, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("config is nil")
	}

	if cfg.Model == "" {
		return nil, "", fmt.Errorf("model is required")
	}

	protocol, modelID := ExtractProtocol(cfg)
	authMethod := strings.ToLower(strings.TrimSpace(cfg.AuthMethod))

	userAgent := cfg.UserAgent
	if userAgent == "" {
		userAgent = fmt.Sprintf("Rhizome/%s", config.Version)
	}

	// OAuth/token auth is protocol-specific and must be resolved before the
	// catalog-driven path. OpenAI uses Codex; Anthropic uses Claude via the
	// Anthropic Messages protocol.
	if authMethod == "oauth" || authMethod == "token" {
		switch protocol {
		case "openai":
			provider, err := createCodexAuthProvider()
			if err != nil {
				return nil, "", err
			}
			return finalizeProviderFromConfig(provider, modelID, cfg)
		case "anthropic", "anthropic-messages":
			apiBase := ResolveAPIBase(cfg)
			provider, err := createClaudeAuthProvider(apiBase, userAgent)
			if err != nil {
				return nil, "", err
			}
			return finalizeProviderFromConfig(provider, modelID, cfg)
		}
	}

	option, ok := modelProviderOptionForName(protocol)
	if !ok {
		return nil, "", fmt.Errorf("unknown protocol %q in model %q", protocol, cfg.Model)
	}

	switch option.ProtocolFamily {
	case "openai-compatible":
		return createOpenAICompatibleProvider(cfg, option, modelID, userAgent)
	case "anthropic-messages":
		return createAnthropicMessagesProvider(cfg, option, modelID, userAgent)
	case "gemini":
		return createGeminiProvider(cfg, option, modelID, userAgent)
	case "azure":
		return createAzureProvider(cfg, modelID, userAgent)
	case "bedrock":
		return createBedrockProvider(cfg, modelID)
	case "oauth":
		return createOAuthProvider(protocol, modelID, cfg)
	case "cli":
		return createCLIProvider(cfg, protocol, modelID)
	case "asr":
		return nil, "", fmt.Errorf("provider %q is not an LLM chat provider", protocol)
	default:
		return nil, "", fmt.Errorf("unknown protocol family %q for provider %q", option.ProtocolFamily, protocol)
	}
}

func createOpenAICompatibleProvider(cfg *config.ModelConfig, option ModelProviderOption, modelID, userAgent string) (LLMProvider, string, error) {
	if cfg.APIKey() == "" && cfg.APIBase == "" && !option.EmptyAPIKeyAllowed {
		return nil, "", fmt.Errorf("api_key or api_base is required for HTTP-based protocol %q", option.ID)
	}

	apiBase := ResolveAPIBase(cfg)

	extraBody := mergeExtraBody(option.ExtraBodyDefaults, cfg.ExtraBody)

	var timeout time.Duration
	if cfg.RequestTimeout > 0 {
		timeout = time.Duration(cfg.RequestTimeout) * time.Second
	}

	provider := openaicompat.NewProvider(
		cfg.APIKey(),
		apiBase,
		cfg.Proxy,
		openaicompat.WithMaxTokensField(cfg.MaxTokensField),
		openaicompat.WithUserAgent(userAgent),
		openaicompat.WithRequestTimeout(timeout),
		openaicompat.WithExtraBody(extraBody),
		openaicompat.WithCustomHeaders(cfg.CustomHeaders),
		openaicompat.WithProviderName(option.ID),
		openaicompat.WithStripModelPrefix(option.StripModelPrefix),
	)

	return finalizeProviderFromConfig(provider, modelID, cfg)
}

func createAnthropicMessagesProvider(cfg *config.ModelConfig, option ModelProviderOption, modelID, userAgent string) (LLMProvider, string, error) {
	if cfg.APIKey() == "" {
		return nil, "", fmt.Errorf("api_key is required for %q protocol (model: %s)", option.ID, cfg.Model)
	}
	apiBase := ResolveAPIBase(cfg)
	if apiBase == "" {
		apiBase = "https://api.anthropic.com/v1"
	}

	var timeout time.Duration
	if cfg.RequestTimeout > 0 {
		timeout = time.Duration(cfg.RequestTimeout) * time.Second
	}

	provider := anthropicmessages.NewProviderWithTimeout(
		cfg.APIKey(),
		apiBase,
		userAgent,
		int(timeout.Seconds()),
	)
	return finalizeProviderFromConfig(provider, modelID, cfg)
}

func createGeminiProvider(cfg *config.ModelConfig, option ModelProviderOption, modelID, userAgent string) (LLMProvider, string, error) {
	if cfg.APIKey() == "" && cfg.APIBase == "" {
		return nil, "", fmt.Errorf("api_key or api_base is required for gemini protocol (model: %s)", cfg.Model)
	}
	apiBase := ResolveAPIBase(cfg)

	var timeout time.Duration
	if cfg.RequestTimeout > 0 {
		timeout = time.Duration(cfg.RequestTimeout) * time.Second
	}

	provider := gemini.NewGeminiProvider(
		cfg.APIKey(),
		apiBase,
		cfg.Proxy,
		userAgent,
		int(timeout.Seconds()),
		cfg.ExtraBody,
		cfg.CustomHeaders,
	)
	return finalizeProviderFromConfig(provider, modelID, cfg)
}

func createAzureProvider(cfg *config.ModelConfig, modelID, userAgent string) (LLMProvider, string, error) {
	if cfg.APIBase == "" {
		return nil, "", fmt.Errorf(
			"api_base is required for azure protocol (e.g., https://your-resource.openai.azure.com)",
		)
	}
	if cfg.APIKey() != "" {
		return finalizeProviderFromConfig(azure.NewProviderWithTimeout(
			cfg.APIKey(),
			cfg.APIBase,
			cfg.Proxy,
			userAgent,
			cfg.RequestTimeout,
		), modelID, cfg)
	}
	provider, err := azure.NewProviderWithIdentityAndTimeout(
		cfg.APIBase,
		cfg.Proxy,
		userAgent,
		cfg.RequestTimeout,
	)
	if err != nil {
		return nil, "", err
	}
	return finalizeProviderFromConfig(provider, modelID, cfg)
}

func createBedrockProvider(cfg *config.ModelConfig, modelID string) (LLMProvider, string, error) {
	var opts []bedrock.Option
	if cfg.APIBase != "" {
		if !strings.Contains(cfg.APIBase, "://") {
			opts = append(opts, bedrock.WithRegion(cfg.APIBase))
		} else {
			opts = append(opts, bedrock.WithBaseEndpoint(cfg.APIBase))
		}
	}
	initTimeout := config.Global().LLMProviderInit()
	if cfg.RequestTimeout > 0 {
		reqTimeout := time.Duration(cfg.RequestTimeout) * time.Second
		opts = append(opts, bedrock.WithRequestTimeout(reqTimeout))
		if reqTimeout > initTimeout {
			initTimeout = reqTimeout
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()
	provider, err := bedrock.NewProvider(ctx, opts...)
	if err != nil {
		return nil, "", fmt.Errorf("creating bedrock provider: %w", err)
	}
	return finalizeProviderFromConfig(provider, modelID, cfg)
}

func createOAuthProvider(protocol, modelID string, cfg *config.ModelConfig) (LLMProvider, string, error) {
	switch protocol {
	case "antigravity":
		return finalizeProviderFromConfig(NewAntigravityProvider(), modelID, cfg)
	default:
		return nil, "", fmt.Errorf("unsupported oauth protocol %q", protocol)
	}
}

func createCLIProvider(cfg *config.ModelConfig, protocol, modelID string) (LLMProvider, string, error) {
	switch protocol {
	case "claude-cli":
		workspace := cfg.Workspace
		if workspace == "" {
			workspace = "."
		}
		return finalizeProviderFromConfig(NewClaudeCliProvider(workspace), modelID, cfg)
	case "codex-cli":
		workspace := cfg.Workspace
		if workspace == "" {
			workspace = "."
		}
		return finalizeProviderFromConfig(NewCodexCliProvider(workspace), modelID, cfg)
	case "github-copilot":
		apiBase := cfg.APIBase
		if apiBase == "" {
			apiBase = "localhost:4321"
		}
		connectMode := cfg.ConnectMode
		if connectMode == "" {
			connectMode = "grpc"
		}
		provider, err := NewGitHubCopilotProvider(apiBase, connectMode, modelID)
		if err != nil {
			return nil, "", err
		}
		return finalizeProviderFromConfig(provider, modelID, cfg)
	default:
		return nil, "", fmt.Errorf("unsupported cli protocol %q", protocol)
	}
}

func mergeExtraBody(defaults, overrides map[string]any) map[string]any {
	merged := make(map[string]any)
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func finalizeProviderFromConfig(
	provider LLMProvider,
	modelID string,
	cfg *config.ModelConfig,
) (LLMProvider, string, error) {
	wrapped, err := wrapProviderWithToolSchemaTransform(provider, cfg.ToolSchemaTransform)
	if err != nil {
		return nil, "", err
	}
	return wrapped, modelID, nil
}

func isEmptyAPIKeyAllowed(protocol string) bool {
	option, ok := modelProviderOptionForName(protocol)
	return ok && option.EmptyAPIKeyAllowed
}

// IsEmptyAPIKeyAllowedForProtocol reports whether a protocol allows requests
// without api_key when using its default local endpoint.
func IsEmptyAPIKeyAllowedForProtocol(protocol string) bool {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	return isEmptyAPIKeyAllowed(protocol)
}

// IsHTTPAPIProtocol reports whether a provider uses an HTTP API base in the
// model configuration path. This excludes providers such as Bedrock, CLI
// bridges, and managed OAuth providers even if they do not require an
// explicit api_key field.
func IsHTTPAPIProtocol(protocol string) bool {
	protocol = NormalizeProvider(protocol)
	option, ok := modelProviderOptionsByName[protocol]
	if !ok {
		return false
	}
	switch option.ProtocolFamily {
	case "openai-compatible", "anthropic-messages", "gemini", "azure", "asr":
		return true
	}
	return false
}

// DefaultAPIBaseForProtocol returns the configured default API base for a protocol.
// It returns empty string if the protocol has no default base.
func DefaultAPIBaseForProtocol(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	return getDefaultAPIBase(protocol)
}

// getDefaultAPIBase returns the default API base URL for a given protocol.
func getDefaultAPIBase(protocol string) string {
	option, ok := modelProviderOptionForName(protocol)
	if !ok {
		return ""
	}
	return option.DefaultAPIBase
}

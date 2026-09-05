package oauthprovider

import (
	"context"
	"fmt"

	"github.com/stpinkie/rhizome/pkg/auth"
	anthropicmessages "github.com/stpinkie/rhizome/pkg/providers/anthropic_messages"
)

type ClaudeProvider struct {
	delegate *anthropicmessages.Provider
}

func NewClaudeProvider(token string) *ClaudeProvider {
	return NewClaudeProviderWithBaseURL(token, "")
}

func NewClaudeProviderWithBaseURL(token, apiBase string) *ClaudeProvider {
	return NewClaudeProviderWithTokenSourceAndBaseURL(token, nil, apiBase)
}

func NewClaudeProviderWithTokenSource(token string, tokenSource func() (string, error)) *ClaudeProvider {
	return NewClaudeProviderWithTokenSourceAndBaseURL(token, tokenSource, "")
}

func NewClaudeProviderWithTokenSourceAndBaseURL(
	token string, tokenSource func() (string, error), apiBase string,
) *ClaudeProvider {
	delegate := anthropicmessages.NewProviderWithTokenSource(token, tokenSource, apiBase, "", 0)
	return &ClaudeProvider{delegate: delegate}
}

func (p *ClaudeProvider) Chat(
	ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]any,
) (*LLMResponse, error) {
	return p.delegate.Chat(ctx, messages, tools, model, options)
}

func (p *ClaudeProvider) GetDefaultModel() string {
	return p.delegate.GetDefaultModel()
}

func CreateClaudeTokenSource(getCredential func(string) (*auth.AuthCredential, error)) func() (string, error) {
	return func() (string, error) {
		cred, err := getCredential("anthropic")
		if err != nil {
			return "", fmt.Errorf("loading auth credentials: %w", err)
		}
		if cred == nil {
			return "", fmt.Errorf("no credentials for anthropic. Run: rhizome auth login --provider anthropic")
		}
		return cred.AccessToken, nil
	}
}

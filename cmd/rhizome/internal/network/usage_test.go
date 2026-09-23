package network

import (
	"testing"

	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func TestFormatUsageLine(t *testing.T) {
	if got := formatUsageLine(nil); got != "" {
		t.Fatalf("nil usage should render empty, got %q", got)
	}
	got := formatUsageLine(&toolshared.RemoteUsage{
		LLMCalls:         2,
		PromptTokens:     120,
		CompletionTokens: 30,
		TotalTokens:      150,
		DurationMS:       42,
	})
	want := "2 llm calls, 120 prompt + 30 completion = 150 tokens, 42 ms"
	if got != want {
		t.Fatalf("formatUsageLine = %q, want %q", got, want)
	}
}

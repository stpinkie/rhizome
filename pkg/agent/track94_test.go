package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/providers"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// usageSeqProvider answers with a tool call first (forcing a second LLM call
// in the same turn) and reports usage on every response.
type usageSeqProvider struct {
	callCount int
	mu        sync.Mutex
}

func (p *usageSeqProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.callCount++
	count := p.callCount
	p.mu.Unlock()

	if count == 1 {
		return &providers.LLMResponse{
			Content: "calling a tool",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_1",
				Name:      "no_such_tool_track94",
				Arguments: map[string]any{},
			}},
			FinishReason: "tool_calls",
			Usage: &providers.UsageInfo{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		}, nil
	}
	return &providers.LLMResponse{
		Content:      "final answer",
		FinishReason: "stop",
		Usage: &providers.UsageInfo{
			PromptTokens:     20,
			CompletionTokens: 7,
			TotalTokens:      27,
		},
	}, nil
}

func (p *usageSeqProvider) GetDefaultModel() string { return "usage-seq-model" }

// TestProcessRemoteDispatchUsageSinkAccumulates drives a remote dispatch
// through a two-LLM-call turn and asserts the sink receives the *summed*
// usage (not just the last call) plus a wall duration.
func TestProcessRemoteDispatchUsageSinkAccumulates(t *testing.T) {
	provider := &usageSeqProvider{}
	al, _, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	var sink toolshared.RemoteUsage
	text, _, err := al.ProcessRemoteDispatch(context.Background(), RemoteDispatchRequest{
		Prompt:    "hi",
		UsageSink: &sink,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, text)

	assert.Equal(t, 2, sink.LLMCalls, "usage should sum both LLM calls")
	assert.Equal(t, 30, sink.PromptTokens)
	assert.Equal(t, 12, sink.CompletionTokens)
	assert.Equal(t, 42, sink.TotalTokens)
	assert.GreaterOrEqual(t, sink.DurationMS, int64(0))
}

// TestProcessRemoteDispatchNoUsageSink proves the common path is unaffected
// when no sink is supplied.
func TestProcessRemoteDispatchNoUsageSink(t *testing.T) {
	provider := &usageSeqProvider{}
	al, _, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()

	text, _, err := al.ProcessRemoteDispatch(context.Background(), RemoteDispatchRequest{
		Prompt: "hi",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, text)
}

// TestTurnStateAddUsageAccumulates is the unit-level check on the per-turn
// accumulator itself.
func TestTurnStateAddUsageAccumulates(t *testing.T) {
	ts := &turnState{}
	ts.AddUsage(nil)
	assert.Equal(t, toolshared.RemoteUsage{}, ts.Usage())

	ts.AddUsage(&providers.UsageInfo{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7})
	ts.AddUsage(&providers.UsageInfo{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4})

	u := ts.Usage()
	assert.Equal(t, 2, u.LLMCalls)
	assert.Equal(t, 8, u.PromptTokens)
	assert.Equal(t, 3, u.CompletionTokens)
	assert.Equal(t, 11, u.TotalTokens)
}

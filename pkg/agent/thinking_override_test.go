package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/bus"
)

// TestProcessMessageThinkingLevelOverrideRejectsUnknown mirrors the
// model-override refusal: an unconfigured level fails the turn.
func TestProcessMessageThinkingLevelOverrideRejectsUnknown(t *testing.T) {
	cfg := cfgWithModelList(t)
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	_, err := al.ProcessInbound(context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "acp",
			ChatID:   "sess-1",
			ChatType: "direct",
			SenderID: "acp",
		},
		Content:               "hi",
		ThinkingLevelOverride: "ludicrous",
	})
	if err == nil || !strings.Contains(err.Error(), "not a configured level") {
		t.Fatalf("expected not-configured error from processMessage, got %v", err)
	}
}

// TestProcessMessageThinkingLevelOverrideApplies proves a valid override
// reaches the LLM call — the provider records the per-turn thinking_level
// the pipeline built for it.
func TestProcessMessageThinkingLevelOverrideApplies(t *testing.T) {
	cfg := cfgWithModelList(t)
	provider := &thinkingRecordingProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)

	_, err := al.ProcessInbound(context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "acp",
			ChatID:   "sess-1",
			ChatType: "direct",
			SenderID: "acp",
		},
		Content:               "hi",
		ThinkingLevelOverride: "high",
	})
	if err != nil {
		t.Fatalf("ProcessInbound: %v", err)
	}
	if provider.lastOptions["thinking_level"] != "high" {
		t.Fatalf("provider saw thinking_level = %v, want high",
			provider.lastOptions["thinking_level"])
	}
}

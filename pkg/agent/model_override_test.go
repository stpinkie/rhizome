package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
)

// cfgWithModelList returns a config whose model_list holds a default entry
// plus an alternate the override can select.
func cfgWithModelList(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.ModelList = config.SecureModelList{
		{ModelName: "test-model", Model: "test/test-model-id", Enabled: true},
		{ModelName: "alt-model", Model: "test/alt-model-id", Enabled: true},
	}
	return cfg
}

func TestApplyModelOverrideRebuildsCopy(t *testing.T) {
	cfg := cfgWithModelList(t)
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	base := al.registry.GetDefaultAgent()
	if base == nil {
		t.Fatal("no default agent")
	}
	agentCopy := *base
	if err := al.applyModelOverride(base, &agentCopy, "alt-model"); err != nil {
		t.Fatalf("applyModelOverride: %v", err)
	}
	if agentCopy.Model != "alt-model" {
		t.Fatalf("copy model = %q", agentCopy.Model)
	}
	if len(agentCopy.Candidates) == 0 {
		t.Fatal("candidates should be rebuilt for the override")
	}
	// The copy shares the session store — no ephemeral-session swap.
	if agentCopy.Sessions != base.Sessions {
		t.Fatal("copy should share the session store")
	}
	// The base instance is untouched.
	if base.Model != "test-model" {
		t.Fatalf("base model mutated: %q", base.Model)
	}
}

func TestApplyModelOverrideRejectsUnknown(t *testing.T) {
	cfg := cfgWithModelList(t)
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	base := al.registry.GetDefaultAgent()
	agentCopy := *base
	err := al.applyModelOverride(base, &agentCopy, "not-configured")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected not-configured error, got %v", err)
	}
}

func TestProcessMessageModelOverrideRejectsUnknown(t *testing.T) {
	cfg := cfgWithModelList(t)
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})

	_, err := al.ProcessInbound(context.Background(), bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "acp",
			ChatID:   "sess-1",
			ChatType: "direct",
			SenderID: "acp",
		},
		Content:       "hi",
		ModelOverride: "not-configured",
	})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected not-configured error from processMessage, got %v", err)
	}
}

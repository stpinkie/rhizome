package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/providers"
)

func TestInferSkillNamesFromToolCall_ReadFileSkillMarkdown(t *testing.T) {
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "three-one")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: three-one\ndescription: test\n---\n# Three One\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cb := NewContextBuilder(workspace)
	ts := &turnState{
		workspace: workspace,
		agent: &AgentInstance{
			Workspace:      workspace,
			ContextBuilder: cb,
		},
	}

	got := inferSkillNamesFromToolCall(ts, "read_file", map[string]any{
		"path": filepath.Join(workspace, "skills", "three-one", "SKILL.md"),
	})
	if len(got) != 1 || got[0] != "three-one" {
		t.Fatalf("inferSkillNamesFromToolCall = %v, want [three-one]", got)
	}
}

func TestInferSkillNamesFromToolCall_NonSkillFileIgnored(t *testing.T) {
	workspace := t.TempDir()
	ts := &turnState{workspace: workspace}

	got := inferSkillNamesFromToolCall(ts, "read_file", map[string]any{
		"path": filepath.Join(workspace, "README.md"),
	})
	if len(got) != 0 {
		t.Fatalf("inferSkillNamesFromToolCall = %v, want empty", got)
	}
}

func TestAsyncToolFollowUp_CarriesOriginSessionKey(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	provider := &toolCallProvider{
		toolCalls: []providers.ToolCall{
			{
				ID:   "call_async_1",
				Type: "function",
				Name: "async_followup",
				Function: &providers.FunctionCall{
					Name:      "async_followup",
					Arguments: "{}",
				},
				Arguments: map[string]any{},
			},
		},
		finalResp: "async launched",
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)
	defer al.Close()
	al.RegisterTool(&asyncFollowUpTool{
		name:         "async_followup",
		followUpText: "background result",
	})

	const sessionKey = "agent:main:telegram:direct:alice"
	if _, err := al.runAgentLoop(context.Background(), al.GetRegistry().GetDefaultAgent(), processOptions{
		SessionKey:      sessionKey,
		Channel:         "telegram",
		ChatID:          "alice",
		UserMessage:     "run async tool",
		DefaultResponse: defaultResponse,
	}); err != nil {
		t.Fatalf("runAgentLoop failed: %v", err)
	}

	select {
	case msg := <-msgBus.InboundChan():
		if msg.Channel != "system" {
			t.Fatalf("follow-up channel = %q, want system", msg.Channel)
		}
		if msg.ChatID != "telegram:alice" {
			t.Fatalf("follow-up chat ID = %q, want telegram:alice", msg.ChatID)
		}
		if msg.SessionKey != sessionKey {
			t.Fatalf("follow-up session key = %q, want %q", msg.SessionKey, sessionKey)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for async follow-up")
	}
}

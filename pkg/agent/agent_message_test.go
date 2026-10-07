package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/providers"
	"github.com/stpinkie/rhizome/pkg/session"
)

func newSystemMessageTestLoop(t *testing.T, agents ...config.AgentConfig) (*AgentLoop, *recordingProvider) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: agents,
		},
	}
	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	return al, provider
}

func asyncResultMessage(sessionKey, chatID, content string) bus.InboundMessage {
	return bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "system",
			ChatID:   chatID,
			ChatType: "direct",
			SenderID: "async:spawn",
		},
		Content:    content,
		SessionKey: sessionKey,
	}
}

func messagesContain(msgs []providers.Message, marker string) bool {
	for _, msg := range msgs {
		if strings.Contains(msg.Content, marker) {
			return true
		}
	}
	return false
}

func TestProcessSystemMessage_UsesOriginSession(t *testing.T) {
	al, provider := newSystemMessageTestLoop(t)
	agent := al.GetRegistry().GetDefaultAgent()
	ctx := context.Background()

	const (
		aliceKey = "agent:main:telegram:direct:alice"
		bobKey   = "agent:main:discord:direct:bob"
	)
	if _, err := al.processMessage(ctx, asyncResultMessage(aliceKey, "telegram:alice", "ALICE-SECRET")); err != nil {
		t.Fatalf("processMessage(alice) error = %v", err)
	}
	if _, err := al.processMessage(ctx, asyncResultMessage(bobKey, "discord:bob", "result for bob")); err != nil {
		t.Fatalf("processMessage(bob) error = %v", err)
	}

	if messagesContain(provider.lastMessages, "ALICE-SECRET") {
		t.Fatal("bob's follow-up turn saw alice's async result")
	}
	if !messagesContain(agent.Sessions.GetHistory(aliceKey), "ALICE-SECRET") {
		t.Fatalf("alice's session history does not contain her async result: %+v",
			agent.Sessions.GetHistory(aliceKey))
	}
	if history := agent.Sessions.GetHistory(session.BuildMainSessionKey(agent.ID)); len(history) != 0 {
		t.Fatalf("main session history = %+v, want empty", history)
	}
	for _, key := range []string{aliceKey, bobKey} {
		if ts := al.getActiveTurnState(key); ts != nil {
			t.Fatalf("session %q still has an active turn %q after processing", key, ts.turnID)
		}
	}
}

func TestProcessSystemMessage_UsesSessionAgent(t *testing.T) {
	al, _ := newSystemMessageTestLoop(t,
		config.AgentConfig{ID: "main", Default: true},
		config.AgentConfig{ID: "support"},
	)
	support, ok := al.GetRegistry().GetAgent("support")
	if !ok {
		t.Fatal("expected support agent")
	}

	const key = "agent:support:telegram:direct:alice"
	if _, err := al.processMessage(
		context.Background(),
		asyncResultMessage(key, "telegram:alice", "SUPPORT-RESULT"),
	); err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}

	if !messagesContain(support.Sessions.GetHistory(key), "SUPPORT-RESULT") {
		t.Fatalf("support session history does not contain the async result: %+v",
			support.Sessions.GetHistory(key))
	}
}

func TestProcessSystemMessage_NoSessionKeyUsesMainSession(t *testing.T) {
	al, provider := newSystemMessageTestLoop(t)
	agent := al.GetRegistry().GetDefaultAgent()

	if _, err := al.processMessage(
		context.Background(),
		asyncResultMessage("", "telegram:alice", "LEGACY-RESULT"),
	); err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}

	if !messagesContain(provider.lastMessages, "LEGACY-RESULT") {
		t.Fatal("LLM did not receive the async result")
	}
	if !messagesContain(agent.Sessions.GetHistory(session.BuildMainSessionKey(agent.ID)), "LEGACY-RESULT") {
		t.Fatal("main session history does not contain the async result")
	}
}

func TestProcessSystemMessage_BusySessionQueuesSteering(t *testing.T) {
	al, provider := newSystemMessageTestLoop(t)
	agent := al.GetRegistry().GetDefaultAgent()

	const key = "agent:main:telegram:direct:alice"
	active := &turnState{turnID: "user-turn", phase: TurnPhaseRunning}
	al.activeTurnStates.Store(key, active)

	resp, err := al.processMessage(
		context.Background(),
		asyncResultMessage(key, "telegram:alice", "QUEUED-RESULT"),
	)
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if resp != "" {
		t.Fatalf("processMessage() response = %q, want empty", resp)
	}

	if provider.lastMessages != nil {
		t.Fatal("LLM was called while the origin session had an active turn")
	}
	if got := al.pendingSteeringCountForScope(key); got != 1 {
		t.Fatalf("pending steering for %q = %d, want 1", key, got)
	}
	if got := al.getActiveTurnState(key); got != active {
		t.Fatalf("active turn for %q = %v, want the user's turn", key, got)
	}
	if history := agent.Sessions.GetHistory(session.BuildMainSessionKey(agent.ID)); len(history) != 0 {
		t.Fatalf("main session history = %+v, want empty", history)
	}
}

// Rhizome - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/tools"
)

// drainOutbound waits for one outbound message, failing the test on timeout.
func drainOutbound(t *testing.T, msgBus *bus.MessageBus) bus.OutboundMessage {
	t.Helper()
	select {
	case outbound := <-msgBus.OutboundChan():
		return outbound
	case <-time.After(2 * time.Second):
		t.Fatal("expected an outbound message, got none")
	}
	return bus.OutboundMessage{}
}

// assertNoOutbound fails the test if a message shows up on the outbound bus.
func assertNoOutbound(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()
	select {
	case outbound := <-msgBus.OutboundChan():
		t.Fatalf("expected no outbound message, got %+v", outbound)
	case <-time.After(150 * time.Millisecond):
	}
}

// registerMessageToolThatSent registers a message tool on the default agent and
// records one send to channel/chatID, so the "already sent to this chat"
// suppression path becomes active.
func registerMessageToolThatSent(t *testing.T, al *AgentLoop, channel, chatID string) {
	t.Helper()

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	mt := tools.NewMessageTool()
	mt.SetSendCallback(func(
		ctx context.Context,
		channel, chatID, content, replyToMessageID string,
		mediaParts []bus.MediaPart,
	) error {
		return nil
	})
	defaultAgent.Tools.Register(mt)

	result := mt.Execute(
		tools.WithToolSessionContext(context.Background(), "main", "session-1", nil),
		map[string]any{
			"content": "ack",
			"channel": channel,
			"chat_id": chatID,
		},
	)
	if result == nil || result.IsError {
		t.Fatalf("message tool execute failed: %+v", result)
	}
}

// A turn that dies mid-way must still tell the user, even when the message tool
// already delivered something to this chat during the same round. Otherwise the
// user sees a partial reply and then silence, with no way to tell whether the
// turn finished or died.
func TestPublishTurnFailureNotice_NotSuppressedByMessageTool(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	registerMessageToolThatSent(t, al, "telegram", "-100123")

	al.publishTurnFailureNotice(
		context.Background(),
		"telegram",
		"-100123",
		"session-1",
		"Error processing message: provider exploded",
	)

	outbound := drainOutbound(t, msgBus)
	if outbound.Content != "Error processing message: provider exploded" {
		t.Fatalf("outbound content = %q, want the failure notice", outbound.Content)
	}
	if outbound.Channel != "telegram" || outbound.ChatID != "-100123" {
		t.Fatalf("unexpected outbound target: %+v", outbound)
	}
	if outbound.Context.Raw[metadataKeyOutboundKind] != outboundKindFinal {
		t.Fatalf("outbound kind = %q, want %q",
			outbound.Context.Raw[metadataKeyOutboundKind], outboundKindFinal)
	}
}

// The regular response path keeps its suppression behavior, so this change does
// not make normal turns chattier.
func TestPublishResponseIfNeeded_StillSuppressedByMessageTool(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	registerMessageToolThatSent(t, al, "telegram", "-100123")

	al.PublishResponseIfNeeded(context.Background(), "telegram", "-100123", "session-1", "final reply")

	assertNoOutbound(t, msgBus)
}

// Turn failures commonly happen while the surrounding context is already being
// torn down (provider timeout, shutdown). The notice must survive that, because
// that is exactly when it matters most.
func TestPublishTurnFailureNotice_SurvivesCanceledContext(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	al.publishTurnFailureNotice(ctx, "telegram", "-100123", "session-1", "turn died")

	outbound := drainOutbound(t, msgBus)
	if outbound.Content != "turn died" {
		t.Fatalf("outbound content = %q, want turn died", outbound.Content)
	}
}

// The regular response path still honors context cancellation.
func TestPublishResponseIfNeeded_RespectsCanceledContext(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	al.PublishResponseIfNeeded(ctx, "telegram", "-100123", "session-1", "final reply")

	assertNoOutbound(t, msgBus)
}

// Internal channels have no human reader, so a failure notice there is pure
// noise.
func TestPublishTurnFailureNotice_SkipsInternalChannels(t *testing.T) {
	for _, channel := range []string{"cli", "system", "subagent"} {
		t.Run(channel, func(t *testing.T) {
			al, _, msgBus, _, cleanup := newTestAgentLoop(t)
			defer cleanup()

			al.publishTurnFailureNotice(context.Background(), channel, "chat-1", "session-1", "turn died")

			assertNoOutbound(t, msgBus)
		})
	}
}

// Without a channel/chatID there is nowhere to deliver, and publishing would
// fail with ErrMissingOutboundContext.
func TestPublishTurnFailureNotice_SkipsMissingDestination(t *testing.T) {
	cases := []struct {
		name          string
		channel, chat string
	}{
		{"empty channel", "", "chat-1"},
		{"empty chat", "telegram", ""},
		{"both empty", "", ""},
		{"blank channel", "   ", "chat-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _, msgBus, _, cleanup := newTestAgentLoop(t)
			defer cleanup()

			al.publishTurnFailureNotice(context.Background(), tc.channel, tc.chat, "session-1", "turn died")

			assertNoOutbound(t, msgBus)
		})
	}
}

// An empty notice carries no information and must not produce an empty bubble.
func TestPublishTurnFailureNotice_SkipsEmptyNotice(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	al.publishTurnFailureNotice(context.Background(), "telegram", "-100123", "session-1", "   ")

	assertNoOutbound(t, msgBus)
}

// maybePublishError routes through the failure notice path, so a real turn error
// reaches the user even when the message tool already sent to this chat.
func TestMaybePublishError_DeliversNoticeDespiteMessageToolSend(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	registerMessageToolThatSent(t, al, "telegram", "-100123")

	if !al.maybePublishError(
		context.Background(),
		"telegram",
		"-100123",
		"session-1",
		errors.New("LLM call failed: 503 No compatible route"),
	) {
		t.Fatal("maybePublishError = false, want true for a non-cancellation error")
	}

	outbound := drainOutbound(t, msgBus)
	if outbound.Content == "" {
		t.Fatal("outbound content is empty, want a formatted error notice")
	}
	if !strings.Contains(outbound.Content, "No compatible route") {
		t.Fatalf("outbound content = %q, want it to include the original error", outbound.Content)
	}
}

// context.Canceled means the user (or a /stop) ended the turn on purpose, so no
// notice is warranted.
func TestMaybePublishError_SilentOnContextCanceled(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if al.maybePublishError(context.Background(), "telegram", "-100123", "session-1", context.Canceled) {
		t.Fatal("maybePublishError = true, want false for context.Canceled")
	}

	assertNoOutbound(t, msgBus)
}

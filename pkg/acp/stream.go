package acp

import (
	"context"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/bus"
)

// sessionStreamer adapts the bus.Streamer seam to ACP session/update
// notifications. The agent pipeline hands it *accumulated* content; the
// streamer diffs against what was already forwarded and emits the suffix as
// an agent_message_chunk (or agent_thought_chunk for reasoning).
type sessionStreamer struct {
	srv  *Server
	sess *acpSession
	ctx  context.Context // turn context for notifications

	mu          sync.Mutex
	sent        int
	thoughtSent int
	didPublish  bool
	cancelled   bool
}

var (
	_ bus.Streamer          = (*sessionStreamer)(nil)
	_ bus.ReasoningStreamer = (*sessionStreamer)(nil)
)

// Update forwards the newly-added suffix of the accumulated content.
func (s *sessionStreamer) Update(ctx context.Context, accumulated string) error {
	s.mu.Lock()
	if s.cancelled || len(accumulated) <= s.sent {
		s.mu.Unlock()
		return nil
	}
	delta := accumulated[s.sent:]
	s.sent = len(accumulated)
	s.didPublish = true
	s.mu.Unlock()

	return s.notify(ctx, acpsdk.UpdateAgentMessageText(delta))
}

// UpdateReasoning forwards reasoning deltas as agent_thought_chunk updates.
func (s *sessionStreamer) UpdateReasoning(ctx context.Context, accumulated string) error {
	s.mu.Lock()
	if s.cancelled || len(accumulated) <= s.thoughtSent {
		s.mu.Unlock()
		return nil
	}
	delta := accumulated[s.thoughtSent:]
	s.thoughtSent = len(accumulated)
	s.mu.Unlock()

	return s.notify(ctx, acpsdk.UpdateAgentThoughtText(delta))
}

// Finalize flushes any remaining suffix of the final content.
func (s *sessionStreamer) Finalize(ctx context.Context, content string) error {
	s.mu.Lock()
	if s.cancelled || len(content) <= s.sent {
		s.mu.Unlock()
		return nil
	}
	delta := content[s.sent:]
	s.sent = len(content)
	s.didPublish = true
	s.mu.Unlock()

	return s.notify(ctx, acpsdk.UpdateAgentMessageText(delta))
}

// FinalizeReasoning flushes any remaining suffix of the final reasoning
// content.
func (s *sessionStreamer) FinalizeReasoning(ctx context.Context, content string) error {
	s.mu.Lock()
	if s.cancelled || len(content) <= s.thoughtSent {
		s.mu.Unlock()
		return nil
	}
	delta := content[s.thoughtSent:]
	s.thoughtSent = len(content)
	s.mu.Unlock()

	return s.notify(ctx, acpsdk.UpdateAgentThoughtText(delta))
}

// Cancel drops subsequent updates (the turn is being torn down).
func (s *sessionStreamer) Cancel(_ context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelled = true
}

// published reports whether any content reached the client this turn.
func (s *sessionStreamer) published() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.didPublish
}

func (s *sessionStreamer) notify(ctx context.Context, upd acpsdk.SessionUpdate) error {
	notifyCtx := ctx
	if notifyCtx == nil {
		notifyCtx = s.ctx
	}
	if notifyCtx == nil {
		notifyCtx = context.Background()
	}
	return s.srv.notify(notifyCtx, s.sess.id, upd)
}

// GetStreamer implements bus.StreamDelegate. It is installed on the message
// bus only inside the `rhizome acp` process, so other channels' streaming is
// unaffected; anything outside channel "acp" is declined.
func (s *Server) GetStreamer(
	ctx context.Context,
	channel, chatID, sessionKey string,
) (bus.Streamer, bool) {
	if channel != ChannelName {
		return nil, false
	}
	sess := s.sessionByChatID(chatID)
	if sess == nil {
		return nil, false
	}
	return sess.streamer(s), true
}

var _ bus.StreamDelegate = (*Server)(nil)

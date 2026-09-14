package acp

import (
	"context"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/events"
)

// pumpToolEvents forwards agent tool-execution runtime events to the ACP
// client as tool_call / tool_call_update notifications. It runs until the
// subscription context is cancelled.
func (s *Server) pumpToolEvents(ctx context.Context, ch <-chan events.Event, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			s.forwardToolEvent(ctx, evt)
		}
	}
}

// forwardToolEvent maps one runtime event onto an ACP session update when it
// belongs to an ACP session (matched via the event scope's session key).
func (s *Server) forwardToolEvent(ctx context.Context, evt events.Event) {
	sess := s.sessionByKey(evt.Scope.SessionKey)
	if sess == nil || sess.isClosed() {
		return
	}

	switch evt.Kind {
	case events.KindAgentToolExecStart:
		p, ok := payloadAs[agent.ToolExecStartPayload](evt.Payload)
		if !ok {
			return
		}
		opts := []acpsdk.ToolCallStartOpt{
			acpsdk.WithStartKind(toolKindFor(p.Tool)),
			acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
			acpsdk.WithStartRawInput(p.Arguments),
		}
		if locs := toolLocations(p.Arguments); len(locs) > 0 {
			opts = append(opts, acpsdk.WithStartLocations(locs))
		}
		s.sendToolUpdate(ctx, sess, acpsdk.StartToolCall(acpsdk.ToolCallId(callID(p.CallID, p.Tool)), p.Tool, opts...))

	case events.KindAgentToolExecEnd:
		p, ok := payloadAs[agent.ToolExecEndPayload](evt.Payload)
		if !ok {
			return
		}
		status := acpsdk.ToolCallStatusCompleted
		if p.IsError {
			status = acpsdk.ToolCallStatusFailed
		}
		s.sendToolUpdate(ctx, sess, acpsdk.UpdateToolCall(
			acpsdk.ToolCallId(callID(p.CallID, p.Tool)),
			acpsdk.WithUpdateStatus(status),
		))

	case events.KindAgentToolExecSkipped:
		p, ok := payloadAs[agent.ToolExecSkippedPayload](evt.Payload)
		if !ok {
			return
		}
		s.sendToolUpdate(ctx, sess, acpsdk.UpdateToolCall(
			acpsdk.ToolCallId(callID(p.CallID, p.Tool)),
			acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusFailed),
			acpsdk.WithUpdateRawOutput(p.Reason),
		))
	}
}

func (s *Server) sendToolUpdate(ctx context.Context, sess *acpSession, upd acpsdk.SessionUpdate) {
	if err := s.notify(ctx, sess.id, upd); err != nil {
		s.log.Warn("acp: tool update failed", "session_id", string(sess.id), "error", err)
	}
}

// sessionByKey finds the ACP session bound to a Rhizome session key.
func (s *Server) sessionByKey(key string) *acpSession {
	if key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range s.sessions {
		if sess.key == key {
			return sess
		}
	}
	return nil
}

// payloadAs extracts a typed payload tolerating value or pointer forms.
func payloadAs[T any](payload any) (T, bool) {
	var zero T
	switch v := payload.(type) {
	case T:
		return v, true
	case *T:
		if v == nil {
			return zero, false
		}
		return *v, true
	default:
		return zero, false
	}
}

// callID prefers the provider tool-call id; falls back to a stable
// name-based id so an update never targets an empty toolCallId.
func callID(callID, tool string) string {
	if callID != "" {
		return callID
	}
	return "tool-" + tool
}

// toolLocations surfaces file-path arguments as ACP tool-call locations so
// editors can deep-link the touched files.
func toolLocations(args map[string]any) []acpsdk.ToolCallLocation {
	if len(args) == 0 {
		return nil
	}
	for _, key := range []string{"path", "file", "file_path", "filepath", "target"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return []acpsdk.ToolCallLocation{{Path: s}}
			}
		}
	}
	return nil
}

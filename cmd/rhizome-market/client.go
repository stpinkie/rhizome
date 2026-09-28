// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"strconv"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"
)

// marketClientHandler is the client side of the spawned-agent connection
// for one market session. The sell-side posture is deliberately hard:
// nothing is advertised at initialize (pkg/acp's bound spawn sends empty
// ClientCapabilities), every permission request is denied, and fs/terminal
// calls get a plain method-not-found — the only traffic allowed through is
// session/update, relayed verbatim to the buyer's connection and
// accumulated for the receipt's result_sha256.
type marketClientHandler struct {
	sess     *marketSession
	upstream func(context.Context, acpsdk.SessionNotification) error
}

// SessionUpdate relays the agent's progress to the buyer and records the
// assistant text the receipt hashes over.
func (h *marketClientHandler) SessionUpdate(
	ctx context.Context,
	params acpsdk.SessionNotification,
) error {
	if text := agentTextDelta(params.Update); text != "" {
		h.sess.mu.Lock()
		h.sess.result.WriteString(text)
		h.sess.mu.Unlock()
	}
	if h.upstream != nil {
		// Relay under the buyer-visible session id (the escrow address),
		// not the spawned agent's internal one.
		params.SessionId = acpsdk.SessionId(h.sess.ID)
		return h.upstream(ctx, params)
	}
	return nil
}

// RequestPermission always denies: market agents run under
// permission_policy=deny — the Cancelled outcome is the protocol's
// terminal refusal.
func (h *marketClientHandler) RequestPermission(
	_ context.Context,
	_ acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{Outcome: "cancelled"},
		},
	}, nil
}

// agentTextDelta extracts assistant-message text from a session update —
// only agent_message_chunk content is metered into the result hash.
func agentTextDelta(u acpsdk.SessionUpdate) string {
	if u.AgentMessageChunk == nil || u.AgentMessageChunk.Content.Text == nil {
		return ""
	}
	return u.AgentMessageChunk.Content.Text.Text
}

func errNoCap(verb string) error {
	return &acpsdk.RequestError{
		Code:    -32601,
		Message: "market sessions serve no client capabilities (fs/terminal denied)",
		Data:    map[string]any{"verb": verb},
	}
}

func (h *marketClientHandler) ReadTextFile(
	context.Context, acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, errNoCap("fs/read_text_file")
}

func (h *marketClientHandler) WriteTextFile(
	context.Context, acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, errNoCap("fs/write_text_file")
}

func (h *marketClientHandler) CreateTerminal(
	context.Context, acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, errNoCap("terminal/create")
}

func (h *marketClientHandler) KillTerminal(
	context.Context, acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, errNoCap("terminal/kill")
}

func (h *marketClientHandler) TerminalOutput(
	context.Context, acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, errNoCap("terminal/output")
}

func (h *marketClientHandler) ReleaseTerminal(
	context.Context, acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, errNoCap("terminal/release")
}

func (h *marketClientHandler) WaitForTerminalExit(
	context.Context, acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, errNoCap("terminal/wait_for_exit")
}

var _ acpsdk.Client = (*marketClientHandler)(nil)

// promptText extracts the text from a buyer's prompt — the task_hash
// commits to text content only. resource_link blocks are accepted as
// inert URI references the spawned agent may dereference (the buyer's
// export_allow_attachments already gated them); embedded image/resource
// blocks are refused — their bytes aren't covered by the task hash.
func promptText(blocks []acpsdk.ContentBlock) (string, error) {
	var b strings.Builder
	for i, blk := range blocks {
		switch {
		case blk.Text != nil:
			b.WriteString(blk.Text.Text)
		case blk.ResourceLink != nil:
			// URI references pass through to the agent unchanged — they
			// carry no bytes, so the task-hash text binding is unaffected.
		default:
			return "", &gateError{
				code: "unsupported_block",
				msg: "market sessions accept text and resource_link blocks only " +
					"(block " + strconv.Itoa(i) + " carries an embedded/unsupported variant)",
			}
		}
	}
	return b.String(), nil
}

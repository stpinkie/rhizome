package acp

import (
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/logger"
	toolfs "github.com/stpinkie/rhizome/pkg/tools/fs"
)

const (
	acpInitTimeout      = 30 * time.Second
	closeSessionTimeout = 5 * time.Second
	// Max bytes the fs.read_text_file bridge will return, matching the agent
	// file tools' cap so external agents see the same limits.
	acpMaxReadFileSize = toolfs.MaxReadFileSize
)

// sessionBuffer accumulates streamed text for one ACP session.
type sessionBuffer struct {
	text strings.Builder
}

// clientHandler implements acpsdk.Client for one external agent process:
// collects streamed output, answers fs.* requests through the workspace
// sandbox, resolves permission requests by policy, and bridges terminal/*
// onto the guarded exec path only when acp.client.terminal_policy=allow.
type clientHandler struct {
	agentID   string
	policy    string
	workspace string
	restrict  bool
	allowRead []*regexp.Regexp
	allowWr   []*regexp.Regexp
	term      *terminalBridge // nil when terminal_policy=deny

	mu       sync.Mutex
	sessions map[acpsdk.SessionId]*sessionBuffer
}

func (h *clientHandler) beginSession(id acpsdk.SessionId) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[id] = &sessionBuffer{}
}

func (h *clientHandler) dropSession(id acpsdk.SessionId) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, id)
}

func (h *clientHandler) sessionText(id acpsdk.SessionId) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if b, ok := h.sessions[id]; ok {
		return b.text.String()
	}
	return ""
}

// SessionUpdate accumulates agent output chunks.
func (h *clientHandler) SessionUpdate(_ context.Context, params acpsdk.SessionNotification) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	buf, ok := h.sessions[params.SessionId]
	if !ok {
		buf = &sessionBuffer{}
		h.sessions[params.SessionId] = buf
	}
	u := params.Update
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		buf.text.WriteString(u.AgentMessageChunk.Content.Text.Text)
	case u.AgentThoughtChunk != nil && u.AgentThoughtChunk.Content.Text != nil:
		// Reasoning is part of the agent's answer for our purposes.
		buf.text.WriteString(u.AgentThoughtChunk.Content.Text.Text)
	case u.ToolCall != nil:
		logger.InfoCF("acp", "external agent tool call",
			map[string]any{
				"agent_id": h.agentID,
				"title":    u.ToolCall.Title,
				"kind":     string(u.ToolCall.Kind),
			})
	}
	return nil
}

// RequestPermission answers the external agent's tool-permission requests
// according to acp.client.permission_policy.
func (h *clientHandler) RequestPermission(
	_ context.Context,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	var kind acpsdk.ToolKind
	if params.ToolCall.Kind != nil {
		kind = *params.ToolCall.Kind
	}
	decision := "deny"
	switch h.policy {
	case ClientPolicyAllow:
		decision = "allow"
	case ClientPolicyAllowReadOnly:
		if isReadOnlyToolKind(kind) {
			decision = "allow"
		}
	}

	if decision == "allow" {
		if opt := pickPermissionOption(params.Options,
			acpsdk.PermissionOptionKindAllowOnce,
			acpsdk.PermissionOptionKindAllowAlways,
		); opt != nil {
			return acpsdk.RequestPermissionResponse{
				Outcome: acpsdk.RequestPermissionOutcome{
					Selected: &acpsdk.RequestPermissionOutcomeSelected{
						Outcome:  "selected",
						OptionId: opt.OptionId,
					},
				},
			}, nil
		}
		// No allow option offered — fall through to reject/cancel.
	}

	if opt := pickPermissionOption(params.Options,
		acpsdk.PermissionOptionKindRejectOnce,
		acpsdk.PermissionOptionKindRejectAlways,
	); opt != nil {
		return acpsdk.RequestPermissionResponse{
			Outcome: acpsdk.RequestPermissionOutcome{
				Selected: &acpsdk.RequestPermissionOutcomeSelected{
					Outcome:  "selected",
					OptionId: opt.OptionId,
				},
			},
		}, nil
	}
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{Outcome: "cancelled"},
		},
	}, nil
}

func pickPermissionOption(
	opts []acpsdk.PermissionOption,
	kinds ...acpsdk.PermissionOptionKind,
) *acpsdk.PermissionOption {
	for _, k := range kinds {
		for i := range opts {
			if opts[i].Kind == k {
				return &opts[i]
			}
		}
	}
	return nil
}

func isReadOnlyToolKind(k acpsdk.ToolKind) bool {
	switch k {
	case acpsdk.ToolKindRead, acpsdk.ToolKindSearch, acpsdk.ToolKindThink, acpsdk.ToolKindFetch:
		return true
	default:
		return false
	}
}

// ReadTextFile serves fs/read_text_file through the workspace sandbox.
func (h *clientHandler) ReadTextFile(
	_ context.Context,
	params acpsdk.ReadTextFileRequest,
) (acpsdk.ReadTextFileResponse, error) {
	path, err := h.sandboxPath(params.Path)
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, acpsdk.NewInvalidParams(err.Error())
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path validated by workspace sandbox
	if err != nil {
		return acpsdk.ReadTextFileResponse{}, acpsdk.NewInternalError(err.Error())
	}
	content := string(data)
	if len(content) > acpMaxReadFileSize {
		content = content[:acpMaxReadFileSize] +
			"\n\n[... truncated by rhizome fs bridge ...]"
	}
	// line/limit slicing, matching the read_file tool's semantics loosely.
	if params.Line != nil || params.Limit != nil {
		content = sliceLines(content, params.Line, params.Limit)
	}
	return acpsdk.ReadTextFileResponse{Content: content}, nil
}

// WriteTextFile serves fs/write_text_file through the workspace sandbox.
func (h *clientHandler) WriteTextFile(
	_ context.Context,
	params acpsdk.WriteTextFileRequest,
) (acpsdk.WriteTextFileResponse, error) {
	if h.policy == ClientPolicyAllowReadOnly {
		return acpsdk.WriteTextFileResponse{},
			acpsdk.NewInvalidParams("write_text_file disabled by acp.client.permission_policy=allow-read-only")
	}
	path, err := h.sandboxPathWrite(params.Path)
	if err != nil {
		return acpsdk.WriteTextFileResponse{}, acpsdk.NewInvalidParams(err.Error())
	}
	if err := os.WriteFile(path, []byte(params.Content), 0o600); err != nil {
		return acpsdk.WriteTextFileResponse{}, acpsdk.NewInternalError(err.Error())
	}
	return acpsdk.WriteTextFileResponse{}, nil
}

func (h *clientHandler) sandboxPath(p string) (string, error) {
	return toolfs.ValidatePathWithAllowPaths(p, h.workspace, h.restrict, h.allowRead)
}

func (h *clientHandler) sandboxPathWrite(p string) (string, error) {
	return toolfs.ValidatePathWithAllowPaths(p, h.workspace, h.restrict, h.allowWr)
}

func sliceLines(content string, line, limit *int) string {
	lines := strings.Split(content, "\n")
	start := 0
	if line != nil && *line > 1 {
		start = *line - 1
		if start > len(lines) {
			return ""
		}
	}
	lines = lines[start:]
	if limit != nil && *limit >= 0 && *limit < len(lines) {
		lines = lines[:*limit]
	}
	return strings.Join(lines, "\n")
}

// Terminal methods bridge onto the guarded exec path only when
// acp.client.terminal_policy=allow; otherwise they report method-not-found
// and ClientCapabilities.Terminal is never advertised.
func (h *clientHandler) CreateTerminal(
	ctx context.Context,
	params acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	if h.term == nil {
		return acpsdk.CreateTerminalResponse{}, acpsdk.NewMethodNotFound("terminal/create")
	}
	return h.term.create(ctx, params)
}

func (h *clientHandler) KillTerminal(
	ctx context.Context,
	params acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	if h.term == nil {
		return acpsdk.KillTerminalResponse{}, acpsdk.NewMethodNotFound("terminal/kill")
	}
	return h.term.kill(ctx, params)
}

func (h *clientHandler) TerminalOutput(
	ctx context.Context,
	params acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	if h.term == nil {
		return acpsdk.TerminalOutputResponse{}, acpsdk.NewMethodNotFound("terminal/output")
	}
	return h.term.output(ctx, params)
}

func (h *clientHandler) ReleaseTerminal(
	ctx context.Context,
	params acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	if h.term == nil {
		return acpsdk.ReleaseTerminalResponse{}, acpsdk.NewMethodNotFound("terminal/release")
	}
	return h.term.release(ctx, params)
}

func (h *clientHandler) WaitForTerminalExit(
	ctx context.Context,
	params acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	if h.term == nil {
		return acpsdk.WaitForTerminalExitResponse{}, acpsdk.NewMethodNotFound("terminal/wait_for_exit")
	}
	return h.term.waitForExit(ctx, params)
}

// drainStderr forwards the child agent's stderr to the file logger.
func drainStderr(agentID string, r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			for line := range strings.Lines(strings.TrimRight(string(buf[:n]), "\n")) {
				if strings.TrimSpace(line) != "" {
					logger.WarnCF("acp", "external agent stderr",
						map[string]any{"agent_id": agentID, "line": line})
				}
			}
		}
		if err != nil {
			return
		}
	}
}

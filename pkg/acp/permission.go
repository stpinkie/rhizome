package acp

import (
	"context"
	"fmt"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
)

// ACP permission option ids surfaced to the client.
const (
	optAllowOnce    = "allow_once"
	optAllowAlways  = "allow_always"
	optRejectOnce   = "reject_once"
	optRejectAlways = "reject_always"
)

// toolApprover bridges the agent loop's ApproveTool hook to ACP
// session/request_permission prompts. It only claims requests that arrive
// on the "acp" channel; every other turn is passed through untouched.
type toolApprover struct {
	srv *Server
}

var _ agent.ToolApprover = (*toolApprover)(nil)

// ApproveTool decides whether a tool call may proceed. Under the "prompt"
// policy the client is asked via session/request_permission; allow_always
// and reject_always answers are cached per tool for the session. A missing
// connection, transport error, or cancellation fails closed (denied).
func (a *toolApprover) ApproveTool(
	ctx context.Context,
	req *agent.ToolApprovalRequest,
) (agent.ApprovalDecision, error) {
	var inbound *bus.InboundContext
	if req != nil && req.Context != nil {
		inbound = req.Context.Inbound
	}
	if inbound == nil || inbound.Channel != ChannelName {
		return agent.ApprovalDecision{Approved: true}, nil
	}

	sess := a.srv.sessionByChatID(inbound.ChatID)
	if sess == nil {
		// Not an ACP-owned chat — abstain.
		return agent.ApprovalDecision{Approved: true}, nil
	}

	// The session's mode override (session/set_mode) takes precedence over
	// the server-wide permission_policy.
	policy := sess.effectivePolicy(a.srv.policy)
	switch policy {
	case PermissionAllow:
		return agent.ApprovalDecision{Approved: true}, nil
	case PermissionDeny:
		reason := "denied by acp.server.permission_policy=deny"
		if m := sess.sessionMode(); m != "" {
			reason = fmt.Sprintf("denied by acp session mode %q", m)
		}
		return agent.ApprovalDecision{
			Approved: false,
			Reason:   reason,
		}, nil
	}

	// PermissionPrompt (default).
	if approved, ok := sess.cachedDecision(req.Tool); ok {
		return agent.ApprovalDecision{Approved: approved, Reason: "cached acp decision"}, nil
	}

	conn, err := a.srv.connOrErr()
	if err != nil {
		//nolint:nilerr // an unreachable client fails closed as a deny decision, not a hook error.
		return agent.ApprovalDecision{
			Approved: false,
			Reason:   "acp client unavailable for permission request",
		}, nil
	}

	resp, err := conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
		SessionId: sess.id,
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: acpsdk.ToolCallId(permissionCallID(req)),
			Title:      acpsdk.Ptr(req.Tool),
			Kind:       acpsdk.Ptr(toolKindFor(req.Tool)),
			RawInput:   req.Arguments,
		},
		Options: []acpsdk.PermissionOption{
			{
				Kind:     acpsdk.PermissionOptionKindAllowOnce,
				Name:     "Allow once",
				OptionId: acpsdk.PermissionOptionId(optAllowOnce),
			},
			{
				Kind:     acpsdk.PermissionOptionKindAllowAlways,
				Name:     "Always allow " + req.Tool,
				OptionId: acpsdk.PermissionOptionId(optAllowAlways),
			},
			{
				Kind:     acpsdk.PermissionOptionKindRejectOnce,
				Name:     "Reject once",
				OptionId: acpsdk.PermissionOptionId(optRejectOnce),
			},
			{
				Kind:     acpsdk.PermissionOptionKindRejectAlways,
				Name:     "Always reject " + req.Tool,
				OptionId: acpsdk.PermissionOptionId(optRejectAlways),
			},
		},
	})
	if err != nil {
		return agent.ApprovalDecision{
			Approved: false,
			Reason:   fmt.Sprintf("acp permission request failed: %v", err),
		}, nil
	}

	switch {
	case resp.Outcome.Cancelled != nil:
		return agent.ApprovalDecision{Approved: false, Reason: "permission prompt cancelled"}, nil
	case resp.Outcome.Selected != nil:
		return a.selected(sess, req.Tool, string(resp.Outcome.Selected.OptionId)), nil
	default:
		return agent.ApprovalDecision{Approved: false, Reason: "empty permission outcome"}, nil
	}
}

// selected maps a chosen permission option to an approval decision and
// caches *_always answers on the session.
func (a *toolApprover) selected(
	sess *acpSession,
	tool, optionID string,
) agent.ApprovalDecision {
	switch optionID {
	case optAllowOnce:
		return agent.ApprovalDecision{Approved: true}
	case optAllowAlways:
		sess.cacheDecision(tool, true)
		return agent.ApprovalDecision{Approved: true}
	case optRejectAlways:
		sess.cacheDecision(tool, false)
		return agent.ApprovalDecision{Approved: false, Reason: "rejected (always) via acp"}
	case optRejectOnce:
		return agent.ApprovalDecision{Approved: false, Reason: "rejected via acp"}
	default:
		return agent.ApprovalDecision{
			Approved: false,
			Reason:   fmt.Sprintf("unrecognized acp permission option %q", optionID),
		}
	}
}

// permissionCallID returns the provider tool-call id when available so the
// permission prompt and the tool_call updates share one correlation id.
func permissionCallID(req *agent.ToolApprovalRequest) string {
	if req.CallID != "" {
		return req.CallID
	}
	return req.Tool
}

// toolKindFor maps a Rhizome tool name onto a coarse ACP ToolKind.
func toolKindFor(tool string) acpsdk.ToolKind {
	switch tool {
	case "read_file", "list_dir", "search_files":
		return acpsdk.ToolKindRead
	case "write_file", "edit_file", "append_file":
		return acpsdk.ToolKindEdit
	case "exec_command", "run_command", "shell", "bash":
		return acpsdk.ToolKindExecute
	case "web_search", "web_fetch", "search":
		return acpsdk.ToolKindSearch
	default:
		return acpsdk.ToolKindOther
	}
}

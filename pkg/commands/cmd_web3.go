package commands

import (
	"context"
	"fmt"
	"strings"
)

// web3Command provides channel-side resolution of the web3 signing queue —
// gated to scopes in tools.web3.approval_channels (channel or
// channel:chat_id). Every resolution goes through the same pending store
// as CLI/UI approvals and is recorded as resolved_by "channel:<scope>".
func web3Command() Definition {
	return Definition{
		Name:        "web3",
		Description: "Web3 signing approvals (allowlisted scopes only)",
		SubCommands: []SubCommand{
			{
				Name: "pending", Description: "List requests awaiting approval",
				Handler: web3PendingHandler(),
			},
			{
				Name: "approve", Description: "Approve and broadcast", ArgsUsage: "<id>",
				Handler: web3ResolveHandler(true),
			},
			{
				Name: "reject", Description: "Reject a request", ArgsUsage: "<id>",
				Handler: web3ResolveHandler(false),
			},
		},
	}
}

// web3ScopeAllowed checks the request's channel+chat against
// tools.web3.approval_channels ("channel" or "channel:chat_id").
func web3ScopeAllowed(rt *Runtime, req Request) bool {
	if rt == nil || rt.Config == nil {
		return false
	}
	scope := req.Channel + ":" + req.ChatID
	for _, allowed := range rt.Config.Tools.Web3.ApprovalChannels {
		allowed = strings.TrimSpace(allowed)
		if allowed == req.Channel || allowed == scope {
			return true
		}
	}
	return false
}

func web3PendingHandler() Handler {
	return func(_ context.Context, req Request, rt *Runtime) error {
		if !web3ScopeAllowed(rt, req) {
			return req.Reply(unavailableMsg)
		}
		if rt.ListWeb3Pending == nil {
			return req.Reply(unavailableMsg)
		}
		entries, err := rt.ListWeb3Pending()
		if err != nil {
			return req.Reply("web3 pending failed: " + err.Error())
		}
		if len(entries) == 0 {
			return req.Reply("No pending signing requests.")
		}
		var b strings.Builder
		b.WriteString("Pending approvals:\n")
		for _, e := range entries {
			fmt.Fprintf(&b, "• %s — %s (expires %s)\n",
				e.ID, e.Summary, e.ExpiresAt.Local().Format("15:04"))
		}
		b.WriteString("\nResolve with /web3 approve <id> or /web3 reject <id>")
		return req.Reply(b.String())
	}
}

func web3ResolveHandler(approve bool) Handler {
	verb := "reject"
	if approve {
		verb = "approve"
	}
	return func(ctx context.Context, req Request, rt *Runtime) error {
		if !web3ScopeAllowed(rt, req) {
			return req.Reply(unavailableMsg)
		}
		if rt.ResolveWeb3 == nil {
			return req.Reply(unavailableMsg)
		}
		id := nthToken(req.Text, 2)
		if id == "" {
			return req.Reply(fmt.Sprintf("Usage: /web3 %s <id>", verb))
		}
		res, err := rt.ResolveWeb3(ctx, id, approve,
			fmt.Sprintf("channel:%s:%s", req.Channel, req.ChatID))
		if err != nil {
			return req.Reply(fmt.Sprintf("web3 %s failed: %s", verb, err.Error()))
		}
		switch res.Status {
		case "sent":
			return req.Reply(fmt.Sprintf("Approved %s — broadcast %s", res.ID, res.TxHash))
		case "done":
			return req.Reply(fmt.Sprintf("Approved %s — signature produced", res.ID))
		case "rejected":
			return req.Reply("Rejected " + res.ID)
		default:
			return req.Reply(fmt.Sprintf("%s %s — %s", verb, res.ID, res.Status))
		}
	}
}

package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
)

func web3Runtime(allow []string) *Runtime {
	cfg := &config.Config{}
	cfg.Tools.Web3.ApprovalChannels = allow
	return &Runtime{Config: cfg}
}

func web3Request(text, channel, chatID string, reply *string) Request {
	return Request{
		Text: text, Channel: channel, ChatID: chatID,
		Reply: func(s string) error { *reply = s; return nil },
	}
}

func TestWeb3Command_ScopeDenied(t *testing.T) {
	ex := NewExecutor(NewRegistry([]Definition{web3Command()}),
		web3Runtime([]string{"telegram:ops"}))

	var reply string
	res := ex.Execute(context.Background(),
		web3Request("/web3 pending", "telegram", "random", &reply))
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v", res.Outcome)
	}
	if reply != unavailableMsg {
		t.Fatalf("reply=%q, want unavailable", reply)
	}
}

func TestWeb3Command_PendingListsEntries(t *testing.T) {
	rt := web3Runtime([]string{"telegram"})
	rt.ListWeb3Pending = func() ([]Web3PendingInfo, error) {
		return []Web3PendingInfo{{
			ID: "p1", Kind: "send", Summary: "send 0.5 ETH",
			ExpiresAt: time.Now().Add(time.Hour),
		}}, nil
	}
	ex := NewExecutor(NewRegistry([]Definition{web3Command()}), rt)

	var reply string
	res := ex.Execute(context.Background(),
		web3Request("/web3 pending", "telegram", "anyone", &reply))
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v", res.Outcome)
	}
	if !strings.Contains(reply, "p1") || !strings.Contains(reply, "send 0.5 ETH") {
		t.Fatalf("reply=%q", reply)
	}
}

func TestWeb3Command_ScopeChatIDMatch(t *testing.T) {
	rt := web3Runtime([]string{"telegram:ops"})
	rt.ListWeb3Pending = func() ([]Web3PendingInfo, error) { return nil, nil }
	ex := NewExecutor(NewRegistry([]Definition{web3Command()}), rt)

	var reply string
	ex.Execute(context.Background(),
		web3Request("/web3 pending", "telegram", "ops", &reply))
	if !strings.Contains(reply, "No pending") {
		t.Fatalf("reply=%q", reply)
	}

	// Same channel, different chat → denied.
	reply = ""
	ex.Execute(context.Background(),
		web3Request("/web3 pending", "telegram", "other", &reply))
	if reply != unavailableMsg {
		t.Fatalf("reply=%q, want unavailable", reply)
	}
}

func TestWeb3Command_ApproveResolves(t *testing.T) {
	var gotID, gotBy string
	var gotApprove bool
	rt := web3Runtime([]string{"cli"})
	rt.ResolveWeb3 = func(_ context.Context, id string, approve bool, by string) (*Web3ResolveResult, error) {
		gotID, gotApprove, gotBy = id, approve, by
		return &Web3ResolveResult{ID: id, Status: "sent", TxHash: "0xabc"}, nil
	}
	ex := NewExecutor(NewRegistry([]Definition{web3Command()}), rt)

	var reply string
	res := ex.Execute(context.Background(),
		web3Request("/web3 approve p7", "cli", "", &reply))
	if res.Outcome != OutcomeHandled {
		t.Fatalf("outcome=%v", res.Outcome)
	}
	if gotID != "p7" || !gotApprove || gotBy != "channel:cli:" {
		t.Fatalf("resolve args = %q %v %q", gotID, gotApprove, gotBy)
	}
	if !strings.Contains(reply, "0xabc") {
		t.Fatalf("reply=%q, want tx hash", reply)
	}
}

func TestWeb3Command_RejectAndUsage(t *testing.T) {
	var gotApprove bool
	rt := web3Runtime([]string{"cli"})
	rt.ResolveWeb3 = func(_ context.Context, _ string, approve bool, _ string) (*Web3ResolveResult, error) {
		gotApprove = approve
		return &Web3ResolveResult{ID: "p9", Status: "rejected"}, nil
	}
	ex := NewExecutor(NewRegistry([]Definition{web3Command()}), rt)

	var reply string
	ex.Execute(context.Background(), web3Request("/web3 reject p9", "cli", "", &reply))
	if gotApprove || !strings.Contains(reply, "Rejected") {
		t.Fatalf("reject path: approve=%v reply=%q", gotApprove, reply)
	}

	// Missing id → usage.
	reply = ""
	ex.Execute(context.Background(), web3Request("/web3 approve", "cli", "", &reply))
	if !strings.Contains(reply, "Usage:") {
		t.Fatalf("reply=%q, want usage", reply)
	}

	// Bare /web3 → group usage.
	reply = ""
	ex.Execute(context.Background(), web3Request("/web3", "cli", "", &reply))
	if !strings.Contains(reply, "Usage:") {
		t.Fatalf("reply=%q, want usage", reply)
	}
}

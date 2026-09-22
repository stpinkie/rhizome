// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

func TestPending_SubmitResolveFlow(t *testing.T) {
	s := OpenPendingStore(t.TempDir())
	e, err := s.Submit(&PendingEntry{
		Kind: KindSend, From: testKeyOneAddr,
		To:       "0x00000000000000000000000000000000000000ff",
		ValueWei: "1000", Summary: "send 1000 wei",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID == "" || e.Status != StatusPending {
		t.Fatalf("bad submit: %+v", e)
	}
	if _, err := s.Resolve(e.ID, true, "cli"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusApproved || got.ResolvedBy != "cli" {
		t.Fatalf("after approve: %+v", got)
	}
	// Double-resolve must fail.
	if _, err := s.Resolve(e.ID, false, "cli"); err == nil {
		t.Fatal("re-resolving an approved entry must fail")
	}
}

func TestPending_ExpiryDeniesByDefault(t *testing.T) {
	s := OpenPendingStore(t.TempDir())
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	e, err := s.Submit(&PendingEntry{
		Kind: KindSend, From: testKeyOneAddr, Summary: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Advance past expiry.
	s.now = func() time.Time { return now.Add(defaultApprovalTTL + time.Second) }
	got, err := s.Get(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusExpired {
		t.Fatalf("expired entry status = %s", got.Status)
	}
	if _, err := s.Resolve(e.ID, true, "cli"); err == nil {
		t.Fatal("approving an expired entry must fail")
	}
}

func TestPending_RejectFlow(t *testing.T) {
	s := OpenPendingStore(t.TempDir())
	e, _ := s.Submit(&PendingEntry{Kind: KindSend, From: testKeyOneAddr, Summary: "x"})
	if _, err := s.Resolve(e.ID, false, "channel:telegram:123"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(e.ID)
	if got.Status != StatusRejected || got.ResolvedBy != "channel:telegram:123" {
		t.Fatalf("after reject: %+v", got)
	}
}

// ExecuteApproved end-to-end: approved send → fill → sign → broadcast via
// a stubbed JSON-RPC endpoint, ledger records the spend.
func TestPending_ExecuteApprovedSends(t *testing.T) {
	useScryptWallet(t)
	dir := t.TempDir()
	wallets := OpenWalletStore(dir)
	if _, err := wallets.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}

	var sawSend bool
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		switch method {
		case "eth_chainId":
			return "0xaa36a7", nil // sepolia
		case "eth_getTransactionCount":
			return "0x0", nil
		case "eth_estimateGas":
			return "0x5208", nil
		case "eth_getBlockByNumber":
			return map[string]any{
				"number": "0x1", "hash": "0xabc", "parentHash": "0x0",
				"timestamp": "0x1", "gasUsed": "0x0", "gasLimit": "0x0",
				"baseFeePerGas": "0x3b9aca00", "transactions": []any{},
			}, nil
		case "eth_maxPriorityFeePerGas":
			return "0x3b9aca00", nil
		case "eth_sendRawTransaction":
			sawSend = true
			return "0xfeed00000000000000000000000000000000000000000000000000000000beef", nil
		default:
			t.Fatalf("unexpected method %q", method)
			return nil, nil
		}
	})
	defer srv.Close()
	provider := NewStaticProvider(srv.URL, "")
	ledger := OpenSpendLedger(dir)

	s := OpenPendingStore(dir)
	e, err := s.Submit(&PendingEntry{
		Kind: KindSend, ChainID: 11155111, From: testKeyOneAddr,
		To:       "0x00000000000000000000000000000000000000ff",
		ValueWei: "42", Summary: "send 42 wei",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(e.ID, true, "cli"); err != nil {
		t.Fatal(err)
	}
	done, err := s.ExecuteApproved(context.Background(), e.ID, wallets, provider, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusSent || done.TxHash == "" {
		t.Fatalf("after execute: %+v", done)
	}
	if !sawSend {
		t.Fatal("eth_sendRawTransaction was never called")
	}
	spent, err := ledger.SpendSince(testKeyOneAddr, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if spent.Cmp(big.NewInt(42)) != 0 {
		t.Fatalf("ledger spend = %s, want 42", spent)
	}
}

func TestPending_ExecuteApprovedSign(t *testing.T) {
	useScryptWallet(t)
	dir := t.TempDir()
	wallets := OpenWalletStore(dir)
	if _, err := wallets.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}
	s := OpenPendingStore(dir)
	e, err := s.Submit(&PendingEntry{
		Kind: KindSign, From: testKeyOneAddr,
		Message: "0x48656c6c6f", Summary: "sign 'Hello'",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve(e.ID, true, "cli"); err != nil {
		t.Fatal(err)
	}
	// Provider is unused for sign — pass a dead one.
	done, err := s.ExecuteApproved(context.Background(), e.ID, wallets,
		NewStaticProvider("http://127.0.0.1:1", ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusDone || len(done.Result) != 132 { // 0x + 130 hex
		t.Fatalf("sign result: %+v", done)
	}
}

func TestPending_ChainMovedRefuses(t *testing.T) {
	useScryptWallet(t)
	dir := t.TempDir()
	wallets := OpenWalletStore(dir)
	if _, err := wallets.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}
	// Endpoint reports mainnet while the approved request was for sepolia.
	srv := rpcServer(t, func(method string, _ json.RawMessage, _ uint64) (any, *RPCError) {
		if method == "eth_chainId" {
			return "0x1", nil
		}
		return "0x0", nil
	})
	defer srv.Close()
	s := OpenPendingStore(dir)
	e, _ := s.Submit(&PendingEntry{
		Kind: KindSend, ChainID: 11155111, From: testKeyOneAddr,
		To: "0x00000000000000000000000000000000000000ff", ValueWei: "1",
		Summary: "x",
	})
	if _, err := s.Resolve(e.ID, true, "cli"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ExecuteApproved(context.Background(), e.ID, wallets,
		NewStaticProvider(srv.URL, ""), nil)
	if err == nil {
		t.Fatal("chain-moved execute must refuse")
	}
	if got.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
}

func TestPending_BoundsQueue(t *testing.T) {
	s := OpenPendingStore(t.TempDir())
	for range pendingMaxQueued {
		if _, err := s.Submit(&PendingEntry{
			Kind: KindSend, From: testKeyOneAddr, Summary: "x",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Submit(&PendingEntry{
		Kind: KindSend, From: testKeyOneAddr, Summary: "overflow",
	}); err == nil {
		t.Fatal("101st live pending entry must be refused")
	}
}

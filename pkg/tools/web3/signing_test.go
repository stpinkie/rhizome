// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/tools"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// testSigning builds a SigningDeps over a temp home with one generated key
// as the allowed signer.
func testSigning(t *testing.T, responses map[string]any) (*SigningDeps, *web3.SigningStack) {
	t.Helper()
	t.Setenv("RHIZOME_WALLET_KEYSOURCE", "scrypt")
	t.Setenv("RHIZOME_WALLET_PASSPHRASE", "test-passphrase")
	home := t.TempDir()

	stack, err := web3.OpenSigningStack(home, &config.Web3SigningConfig{Enabled: true})
	if err != nil {
		t.Fatalf("open stack: %v", err)
	}
	entry, err := stack.Wallets.Generate("test")
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	stack.Policy.FromAddresses = []string{entry.Address}

	var provider *web3.Provider
	if responses != nil {
		srv := scriptedRPC(t, responses)
		t.Cleanup(srv.Close)
		provider = web3.NewStaticProvider(srv.URL, "")
	}
	return &SigningDeps{Provider: provider, Stack: stack}, stack
}

func signToolByName(t *testing.T, deps *SigningDeps, name string) tools.Tool {
	t.Helper()
	return toolByName(t, SigningTools(deps), name)
}

func TestSigningGatedWhenPolicyDisabled(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	stack.Policy.Enabled = false
	for _, name := range []string{"web3_send", "web3_sign", "web3_approve"} {
		expectErr(t, signToolByName(t, deps, name), map[string]any{
			"to": testAddr, "token": testAddr, "spender": testAddr,
			"amount": "1", "message": "hi",
		}, "disabled")
	}
	// Read tools still work when signing is off.
	out := runTool(t, signToolByName(t, deps, "web3_wallet"), nil)
	if out["count"].(float64) != 1 {
		t.Fatalf("wallet count = %v", out)
	}
}

func TestWeb3SendQueuesPending(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{"eth_chainId": "0xaa36a7"})
	tool := signToolByName(t, deps, "web3_send")
	out := runTool(t, tool, map[string]any{
		"to": testAddr, "value": "1000000000000000000", "reason": "test send",
	})
	id, _ := out["pending_id"].(string)
	if id == "" || out["status"] != "pending" {
		t.Fatalf("result = %v", out)
	}
	e, err := stack.Pending.Get(id)
	if err != nil {
		t.Fatalf("pending entry: %v", err)
	}
	if e.Kind != web3.KindSend || e.ChainID != 11155111 || e.To != testAddr {
		t.Fatalf("entry = %+v", e)
	}
	if e.ValueWei != "1000000000000000000" || !strings.Contains(e.Summary, "test send") {
		t.Fatalf("entry = %+v", e)
	}
}

func TestWeb3SendPolicyDeniesUnlistedFrom(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	stack.Policy.FromAddresses = []string{"0x0000000000000000000000000000000000000001"}
	tool := signToolByName(t, deps, "web3_send")
	expectErr(t, tool, map[string]any{"to": testAddr, "value": "1"}, "from_addresses")
	entries, _ := stack.Pending.List()
	if len(entries) != 0 {
		t.Fatal("denied send must not queue a pending entry")
	}
}

func TestWeb3SendChainCap(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	stack.Policy.ChainIDs = []uint64{11155111} // endpoint is mainnet
	tool := signToolByName(t, deps, "web3_send")
	expectErr(t, tool, map[string]any{"to": testAddr}, "chain")
}

func TestWeb3SendHookVeto(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	hook := &captureHook{}
	deps.Hook = hook
	tool := signToolByName(t, deps, "web3_send")
	expectErr(t, tool, map[string]any{"to": testAddr}, "hook")
	if hook.last == nil || hook.last.Tool != "web3_send" || hook.last.To != testAddr {
		t.Fatalf("hook saw %+v", hook.last)
	}
	entries, _ := stack.Pending.List()
	if len(entries) != 0 {
		t.Fatal("hook-vetoed send must not queue")
	}
}

type captureHook struct{ last *Web3ApprovalAction }

func (h *captureHook) ApproveWeb3Action(
	_ context.Context, a *Web3ApprovalAction,
) (bool, string) {
	h.last = a
	return false, "test veto"
}

func TestWeb3ApproveEncodesERC20(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	tool := signToolByName(t, deps, "web3_approve")
	out := runTool(t, tool, map[string]any{
		"token": testAddr, "spender": "0x1111111111111111111111111111111111111111",
		"amount": "500",
	})
	e, err := stack.Pending.Get(out["pending_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.Data, "0x095ea7b3") {
		t.Fatalf("approve selector wrong: %s", e.Data)
	}
	// spender is word 1 of calldata, amount word 2 — 500 = 0x1f4.
	if !strings.HasSuffix(e.Data, "1f4") {
		t.Fatalf("amount encoding wrong: %s", e.Data)
	}
	if e.Kind != web3.KindApprove || e.ValueWei != "0" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestWeb3SignQueuesSignKind(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	tool := signToolByName(t, deps, "web3_sign")
	out := runTool(t, tool, map[string]any{"message": "hello rhizome"})
	e, err := stack.Pending.Get(out["pending_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != web3.KindSign || e.Message != "0x68656c6c6f207268697a6f6d65" {
		t.Fatalf("entry = %+v", e)
	}
}

func TestWeb3WalletBalances(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{
		"eth_getBalance": "0xde0b6b3a7640000",
	})
	def, derr := stack.Wallets.Default()
	if derr != nil {
		t.Fatal(derr)
	}
	out := runTool(t, signToolByName(t, deps, "web3_wallet"), nil)
	addrs := out["addresses"].([]any)
	if len(addrs) != 1 {
		t.Fatalf("addresses = %v", addrs)
	}
	first := addrs[0].(map[string]any)
	if first["address"] != def || first["balance_wei"] != "1000000000000000000" {
		t.Fatalf("address entry = %v", first)
	}
}

func TestWeb3SendStatusByID(t *testing.T) {
	deps, _ := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	send := signToolByName(t, deps, "web3_send")
	out := runTool(t, send, map[string]any{"to": testAddr})
	id := out["pending_id"].(string)

	status := signToolByName(t, deps, "web3_send_status")
	got := runTool(t, status, map[string]any{"id": id})
	entry := got["entry"].(map[string]any)
	if entry["status"] != "pending" {
		t.Fatalf("status = %v", entry["status"])
	}
}

func TestSigningEmitFiresOnSubmit(t *testing.T) {
	deps, _ := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	var kinds []string
	deps.Emit = func(kind string, _ map[string]any) { kinds = append(kinds, kind) }
	runTool(t, signToolByName(t, deps, "web3_send"), map[string]any{"to": testAddr})
	if len(kinds) != 1 || kinds[0] != "web3.pending" {
		t.Fatalf("emitted %v", kinds)
	}
}

func TestPolicyFromConfigParsesWeiCaps(t *testing.T) {
	p, err := web3.PolicyFromConfig(&config.Web3SigningConfig{
		Enabled:           true,
		MaxValueWeiPerTx:  "1000000000000000000",
		MaxValueWeiPerDay: "5000000000000000000",
		ChainIDs:          []uint64{1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxValueWeiPerTx.String() != "1000000000000000000" ||
		p.MaxValueWeiPerDay.String() != "5000000000000000000" {
		t.Fatalf("caps = %v/%v", p.MaxValueWeiPerTx, p.MaxValueWeiPerDay)
	}
	if _, err := web3.PolicyFromConfig(&config.Web3SigningConfig{
		MaxValueWeiPerTx: "not-a-number",
	}); err == nil {
		t.Fatal("expected malformed wei to error")
	}
	if _, err := web3.PolicyFromConfig(&config.Web3SigningConfig{
		MaxValueWeiPerTx: "-5",
	}); err == nil {
		t.Fatal("expected negative wei to error")
	}
}

func TestSigningToolsRespectTTL(t *testing.T) {
	deps, stack := testSigning(t, map[string]any{"eth_chainId": "0x1"})
	deps.ApproveTTL = time.Hour
	out := runTool(t, signToolByName(t, deps, "web3_send"), map[string]any{"to": testAddr})
	e, _ := stack.Pending.Get(out["pending_id"].(string))
	if d := time.Until(e.ExpiresAt); d < 50*time.Minute || d > 61*time.Minute {
		t.Fatalf("expires_at delta = %v, want ~1h", d)
	}
}

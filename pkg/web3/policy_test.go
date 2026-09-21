// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"math/big"
	"testing"
	"time"
)

func testPolicy() *SigningPolicy {
	return &SigningPolicy{
		Enabled:       true,
		FromAddresses: []string{testKeyOneAddr},
	}
}

func TestPolicy_DisabledDenies(t *testing.T) {
	p := &SigningPolicy{Enabled: false, FromAddresses: []string{testKeyOneAddr}}
	d := p.Evaluate(&SignRequest{Kind: "send", From: testKeyOneAddr}, nil, time.Now())
	if d.Allowed {
		t.Fatal("disabled policy must deny")
	}
}

func TestPolicy_EmptyFromAddressesDenies(t *testing.T) {
	// Empty signer list = nothing may sign — the cold-key protection.
	p := &SigningPolicy{Enabled: true}
	d := p.Evaluate(&SignRequest{Kind: "send", From: testKeyOneAddr}, nil, time.Now())
	if d.Allowed {
		t.Fatal("empty from_addresses must deny")
	}
}

func TestPolicy_UnlistedFromDenies(t *testing.T) {
	p := testPolicy()
	d := p.Evaluate(&SignRequest{
		Kind: "send", From: "0x00000000000000000000000000000000000000ff",
	}, nil, time.Now())
	if d.Allowed {
		t.Fatal("unlisted from must deny")
	}
}

func TestPolicy_ChainGate(t *testing.T) {
	p := testPolicy()
	p.ChainIDs = []uint64{11155111}
	req := &SignRequest{Kind: "send", From: testKeyOneAddr, ChainID: 1}
	if d := p.Evaluate(req, nil, time.Now()); d.Allowed {
		t.Fatal("mainnet send under sepolia-only policy must deny")
	}
	req.ChainID = 11155111
	if d := p.Evaluate(req, nil, time.Now()); !d.Allowed {
		t.Fatalf("sepolia send should pass: %s", d.Reason)
	}
}

func TestPolicy_ContractAndMethodAllowlists(t *testing.T) {
	p := testPolicy()
	p.AllowContracts = []string{"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"} // USDC
	p.AllowMethods = []string{"0xa9059cbb"}                                   // transfer

	// Contract send to a non-allowlisted contract → deny.
	req := &SignRequest{
		Kind: "contract", From: testKeyOneAddr,
		To:   "0x00000000000000000000000000000000000000ff",
		Data: mustHex(t, "a9059cbb00000000000000000000000000000000000000000000000000000000000000ff"),
	}
	if d := p.Evaluate(req, nil, time.Now()); d.Allowed {
		t.Fatal("non-allowlisted contract must deny")
	}

	// Allowlisted contract but non-allowlisted method → deny.
	req.To = "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48" // lowercase OK
	req.Data = mustHex(t, "095ea7b300000000000000000000000000000000000000000000000000000000000000ff")
	if d := p.Evaluate(req, nil, time.Now()); d.Allowed {
		t.Fatal("non-allowlisted method must deny")
	}

	// Allowlisted contract + method → allow.
	req.Data = mustHex(t, "a9059cbb00000000000000000000000000000000000000000000000000000000000000ff")
	if d := p.Evaluate(req, nil, time.Now()); !d.Allowed {
		t.Fatalf("allowlisted contract+method denied: %s", d.Reason)
	}

	// Plain ETH send (no calldata) is unaffected by contract allowlist.
	req.To = "0x00000000000000000000000000000000000000ff"
	req.Data = nil
	if d := p.Evaluate(req, nil, time.Now()); !d.Allowed {
		t.Fatalf("plain send should pass contract gate: %s", d.Reason)
	}
}

func TestPolicy_ValueCaps(t *testing.T) {
	p := testPolicy()
	p.MaxValueWeiPerTx = big.NewInt(1000)
	p.MaxValueWeiPerDay = big.NewInt(2000)
	ledger := OpenSpendLedger(t.TempDir())
	now := time.Now().UTC()

	req := &SignRequest{Kind: "send", From: testKeyOneAddr, ValueWei: big.NewInt(500)}
	if d := p.Evaluate(req, ledger, now); !d.Allowed {
		t.Fatalf("first send should pass: %s", d.Reason)
	}
	if err := ledger.Record(SpendEntry{
		TS: now, Kind: "send", From: testKeyOneAddr, ValueWei: "1500",
	}); err != nil {
		t.Fatal(err)
	}
	// 1500 spent + 500 new = 2000 == cap → allowed.
	if d := p.Evaluate(req, ledger, now); !d.Allowed {
		t.Fatalf("at-cap send should pass: %s", d.Reason)
	}
	// Per-tx cap.
	req.ValueWei = big.NewInt(1001)
	if d := p.Evaluate(req, ledger, now); d.Allowed {
		t.Fatal("over per-tx cap must deny")
	}
	// Daily cap.
	req.ValueWei = big.NewInt(501)
	if d := p.Evaluate(req, ledger, now); d.Allowed {
		t.Fatal("over daily cap must deny")
	}
}

func TestSpendLedger_RoundTripAndRotation(t *testing.T) {
	l := OpenSpendLedger(t.TempDir())
	now := time.Now().UTC()
	for range 3 {
		if err := l.Record(SpendEntry{
			TS: now, Kind: "send", From: testKeyOneAddr, ValueWei: "100",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Yesterday's spend must not count toward today.
	yesterday := now.Add(-25 * time.Hour)
	if err := l.Record(SpendEntry{
		TS: yesterday, Kind: "send", From: testKeyOneAddr, ValueWei: "99999",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := l.SpendSince(testKeyOneAddr, now.Truncate(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "300" {
		t.Fatalf("daily spend = %s, want 300", got)
	}
}

// --- Track 78: registry-aware allowlist entries ---

const usdcAddr = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"

func registryPolicy(t *testing.T) *SigningPolicy {
	t.Helper()
	reg := OpenABIRegistry(t.TempDir())
	if _, err := reg.Add("usdc", testERC20ishABI, usdcAddr, nil); err != nil {
		t.Fatal(err)
	}
	p := testPolicy()
	p.Registry = reg
	return p
}

func transferData() []byte {
	m, _ := mustABI(testERC20ishABI).Method("transfer", 2)
	data, _ := m.PackArgs([]any{
		"0x00000000000000000000000000000000000000ff", "5",
	})
	return data
}

func TestPolicy_LabelContractAllowlist(t *testing.T) {
	p := registryPolicy(t)
	p.AllowContracts = []string{"usdc"}

	ok := &SignRequest{
		Kind: "contract", From: testKeyOneAddr,
		To: usdcAddr, Data: transferData(),
	}
	if d := p.Evaluate(ok, nil, time.Now()); !d.Allowed {
		t.Fatalf("label-allowlisted contract denied: %s", d.Reason)
	}
	bad := &SignRequest{
		Kind: "contract", From: testKeyOneAddr,
		To: "0x00000000000000000000000000000000000000ff", Data: transferData(),
	}
	if d := p.Evaluate(bad, nil, time.Now()); d.Allowed {
		t.Fatal("other contract must not match the label")
	}
}

func TestPolicy_LabelMethodAllowlist(t *testing.T) {
	p := registryPolicy(t)
	p.AllowMethods = []string{"usdc:transfer"}

	ok := &SignRequest{
		Kind: "contract", From: testKeyOneAddr,
		To: usdcAddr, Data: transferData(),
	}
	if d := p.Evaluate(ok, nil, time.Now()); !d.Allowed {
		t.Fatalf("label:method should allow: %s", d.Reason)
	}
	// Same selector on a different contract must NOT match the bound label.
	bad := &SignRequest{
		Kind: "contract", From: testKeyOneAddr,
		To: "0x00000000000000000000000000000000000000ff", Data: transferData(),
	}
	if d := p.Evaluate(bad, nil, time.Now()); d.Allowed {
		t.Fatal("usdc:transfer must not bless transfer() on other contracts")
	}
	// balanceOf is not in the list.
	bal, _ := mustABI(testERC20ishABI).Method("balanceOf", 1)
	d2, _ := bal.PackArgs([]any{testKeyOneAddr})
	other := &SignRequest{
		Kind: "contract", From: testKeyOneAddr,
		To: usdcAddr, Data: d2,
	}
	if d := p.Evaluate(other, nil, time.Now()); d.Allowed {
		t.Fatal("balanceOf must not match usdc:transfer")
	}
}

func TestPolicy_UnknownLabelEntriesDeny(t *testing.T) {
	p := testPolicy()
	p.Registry = OpenABIRegistry(t.TempDir()) // empty registry
	p.AllowContracts = []string{"ghost"}
	p.AllowMethods = []string{"ghost:transfer"}
	req := &SignRequest{
		Kind: "contract", From: testKeyOneAddr,
		To: usdcAddr, Data: transferData(),
	}
	if d := p.Evaluate(req, nil, time.Now()); d.Allowed {
		t.Fatal("unresolvable allowlist entries must not pass")
	}
}

func TestPolicy_RawSelectorStillWorksWithRegistry(t *testing.T) {
	p := registryPolicy(t)
	p.AllowMethods = []string{"0xa9059cbb"}
	req := &SignRequest{
		Kind: "contract", From: testKeyOneAddr,
		To: usdcAddr, Data: transferData(),
	}
	if d := p.Evaluate(req, nil, time.Now()); !d.Allowed {
		t.Fatalf("raw selector entry should still match: %s", d.Reason)
	}
}

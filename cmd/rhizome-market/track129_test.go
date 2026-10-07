// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors
//
// Track 129: decentralized arbitration — the evidence bundle dispute()
// commits, graduated-rail evidence submission + kleros auto-escalation,
// and the operator evidence/escalate verbs.

package main

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/settlement"
	"github.com/stpinkie/rhizome/pkg/web3"
)

const (
	testAdapter    = "0x6000000000000000000000000000000000000006"
	testArbitrator = "0x7000000000000000000000000000000000000007"
	testToken      = "0x4000000000000000000000000000000000000004"
	testFactory    = "0x5000000000000000000000000000000000000005"
)

// klerosFixture wires a purchaseMgr to a graduated rail whose sessions
// bind the adapter — the kleros:<court> resolved shape over FakeChain.
type klerosFixture struct {
	pm   *purchaseMgr
	rail *settlement.GraduatedRail
	fc   *settlement.FakeChain
	snd  *settlement.DirectSender
}

func newKlerosFixture(t *testing.T, autoRuling int64) *klerosFixture {
	t.Helper()
	cfg := settlement.RailConfig{
		Kind:              settlement.RailKindRhizome,
		ChainID:           11155111,
		Factory:           testFactory,
		Token:             testToken,
		Arbiter:           testAdapter, // resolved: adapter in the slot
		ArbiterKind:       settlement.ArbiterKindKleros,
		ArbiterAdapter:    testAdapter,
		DisputeWindowSecs: 7200,
	}
	fc := settlement.NewFakeGraduatedChain(t, cfg)
	fc.DeployToken(cfg.Token)
	fc.DeployKleros(testAdapter, testArbitrator, big.NewInt(7))
	fc.SetArbiterRuling(autoRuling)
	mint := new(big.Int).Exp(big.NewInt(10), big.NewInt(27), nil)
	fc.Mint(cfg.Token, testBuyer, mint)
	snd := settlement.NewDirectSender(
		web3.NewClient(fc.Endpoint(), "", nil), testBuyer)
	rail, err := settlement.NewGraduatedRail(cfg, snd)
	if err != nil {
		t.Fatalf("NewGraduatedRail: %v", err)
	}
	pm := newPurchaseMgr(t.TempDir(), t.TempDir(), newAuditLogger(""))
	pm.setConfig(&marketConfig{
		buyerAddress: testBuyer, buyAutoRelease: true,
	}, rail, nil)
	return &klerosFixture{pm: pm, rail: rail, fc: fc, snd: snd}
}

func TestTrack129_EvidenceBundle(t *testing.T) {
	p := &purchase{
		SessionID: "0x" + strings.Repeat("ab", 32),
		TaskHash:  "0x42",
		Terms:     purchaseTerms{Amount: "500", Token: testToken},
		Receipt:   &receipt{SessionID: "0xsess", ResultSHA256: "0x99"},
	}
	js, hash := buildEvidence(p, "quality issue")
	if js == "" {
		t.Fatal("bundle must marshal")
	}
	var got struct {
		SessionID string          `json:"session_id"`
		TermsHash string          `json:"terms_hash"`
		Reason    string          `json:"reason"`
		Receipt   json.RawMessage `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(js), &got); err != nil {
		t.Fatalf("bundle unmarshal: %v", err)
	}
	if got.SessionID != p.SessionID {
		t.Fatalf("session_id = %q", got.SessionID)
	}
	if !strings.HasPrefix(got.TermsHash, "0x") || len(got.TermsHash) != 66 {
		t.Fatalf("terms_hash = %q, want 0x+64", got.TermsHash)
	}
	if got.Reason != "quality issue" {
		t.Fatalf("reason = %q", got.Reason)
	}
	if len(got.Receipt) == 0 {
		t.Fatal("receipt must embed in the bundle")
	}
	// Deterministic — the same purchase hashes identically twice (the
	// arbiter and both parties must agree on the commitment).
	_, hash2 := buildEvidence(p, "quality issue")
	if hash != hash2 {
		t.Fatal("evidence hash must be deterministic")
	}
	// A different reason → different commitment.
	_, hash3 := buildEvidence(p, "other")
	if hash == hash3 {
		t.Fatal("distinct reasons must produce distinct hashes")
	}
	// Missing receipt still produces a bundle — terms hash alone.
	p2 := &purchase{
		SessionID: p.SessionID, TaskHash: "0x42",
		Terms: purchaseTerms{Amount: "500", Token: testToken},
	}
	js2, _ := buildEvidence(p2, "")
	var got2 map[string]any
	if err := json.Unmarshal([]byte(js2), &got2); err != nil {
		t.Fatal(err)
	}
	if _, has := got2["receipt"]; has {
		t.Fatal("receipt-less bundle must omit the field")
	}
}

func TestTrack129_DisputeSubmitsAndEscalates(t *testing.T) {
	f := newKlerosFixture(t, -1) // manual ruling — escalation stays pending
	ctx := context.Background()
	amount := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	amount.Mul(amount, big.NewInt(5))

	// An Open session + a disputable purchase — dispute() must drive the
	// full evidence→escalate flow.
	if _, err := f.rail.Open(ctx, "corr-d2", settlement.Terms{
		Buyer: testBuyer, Seller: testSeller, Token: testToken,
		Amount: amount, TaskHash: [32]byte{0x7},
		TerminationTime: time.Now().Unix() + 7200,
	}); err != nil {
		t.Fatal(err)
	}
	sid := f.rail.SessionID("corr-d2")
	p := &purchase{
		PurchaseID: "pur-d2", SessionID: sid,
		State: purchaseDisputable, TaskHash: "0x42",
		Terms:   purchaseTerms{Amount: amount.String(), Token: testToken},
		Receipt: &receipt{SessionID: sid, ResultSHA256: "0x99"},
	}
	f.pm.mu.Lock()
	f.pm.byID[p.PurchaseID] = p
	f.pm.mu.Unlock()

	if _, err := f.pm.dispute(ctx, p.PurchaseID, "bad output"); err != nil {
		t.Fatalf("dispute: %v", err)
	}
	if p.State != purchaseDisputed {
		t.Fatalf("state = %s, want disputed", p.State)
	}
	// dispute + submitEvidence + createDispute = 3 txs recorded.
	if len(p.TxHashes) != 3 {
		t.Fatalf("tx hashes = %v, want 3 (dispute/evidence/escalate)", p.TxHashes)
	}
	// The fake's manual-ruling posture leaves the session locked pending
	// the arbitrator — escalate a second time must revert.
	if _, err := f.pm.escalate(ctx, p.PurchaseID); err == nil {
		t.Fatal("double escalation must revert on the adapter")
	}
}

func TestTrack129_EscalateGuards(t *testing.T) {
	f := newKlerosFixture(t, -1)
	ctx := context.Background()
	amount := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

	t.Run("escalate requires disputed state", func(t *testing.T) {
		p := &purchase{PurchaseID: "pur-e1", State: purchaseCompleted}
		f.pm.mu.Lock()
		f.pm.byID[p.PurchaseID] = p
		f.pm.mu.Unlock()
		if _, err := f.pm.escalate(ctx, p.PurchaseID); err == nil {
			t.Fatal("escalate on completed must fail")
		}
	})

	t.Run("escalate unknown purchase", func(t *testing.T) {
		if _, err := f.pm.escalate(ctx, "nope"); err == nil {
			t.Fatal("escalate must refuse unknown ids")
		}
	})

	t.Run("escalate on non-kleros rail refuses", func(t *testing.T) {
		// Same session, but pm's rail is designated-arbiter → not kleros.
		cfg := settlement.RailConfig{
			Kind: settlement.RailKindRhizome, ChainID: 11155111,
			Factory: testFactory, Token: testToken,
			Arbiter:           "0x3000000000000000000000000000000000000003",
			DisputeWindowSecs: 7200,
		}
		snd := settlement.NewDirectSender(
			web3.NewClient(f.fc.Endpoint(), "", nil), testBuyer)
		rail2, err := settlement.NewGraduatedRail(cfg, snd)
		if err != nil {
			t.Fatal(err)
		}
		pm2 := newPurchaseMgr(t.TempDir(), t.TempDir(), newAuditLogger(""))
		pm2.setConfig(&marketConfig{buyerAddress: testBuyer}, rail2, nil)
		p := &purchase{
			PurchaseID: "pur-e2", State: purchaseDisputed,
			SessionID: f.rail.SessionID("corr-x"),
		}
		pm2.mu.Lock()
		pm2.byID[p.PurchaseID] = p
		pm2.mu.Unlock()
		if _, err := pm2.escalate(ctx, p.PurchaseID); err == nil {
			t.Fatal("designated-arbiter rail must refuse escalate")
		}
	})

	t.Run("evidence surfaces the bundle", func(t *testing.T) {
		p := &purchase{
			PurchaseID: "pur-e3", SessionID: "0x" + strings.Repeat("cd", 32),
			TaskHash: "0x42", Terms: purchaseTerms{Amount: amount.String(), Token: testToken},
		}
		f.pm.mu.Lock()
		f.pm.byID[p.PurchaseID] = p
		f.pm.mu.Unlock()
		bundle, err := f.pm.evidence(p.PurchaseID)
		if err != nil {
			t.Fatalf("evidence: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(bundle, &got); err != nil {
			t.Fatal(err)
		}
		if got["session_id"] != p.SessionID {
			t.Fatalf("bundle session_id = %v", got["session_id"])
		}
	})
}

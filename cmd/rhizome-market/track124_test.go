// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"bufio"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"
)

// fakePeerScoreDaemon listens on loopback, captures the peer_score hello,
// and answers with resp. Returns the captured hello channel + addr.
func fakePeerScoreDaemon(t *testing.T, resp bridgeResponse) (chan bridgeHello, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan bridgeHello, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var h bridgeHello
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		if err := json.Unmarshal(line[:len(line)-1], &h); err != nil {
			return
		}
		got <- h
		rl, _ := json.Marshal(resp)
		_, _ = conn.Write(append(rl, '\n'))
	}()
	return got, ln.Addr().String()
}

func TestReportOutcome_RoundTrip(t *testing.T) {
	got, addr := fakePeerScoreDaemon(t, bridgeResponse{OK: true})
	t.Setenv("RHIZOME_BRIDGE_ADDR", addr)
	t.Setenv("RHIZOME_BRIDGE_TOKEN", "modtok")

	err := reportOutcome("peerXYZ", "market_buy", "completed", "sess-1", "abcd1234")
	if err != nil {
		t.Fatalf("reportOutcome: %v", err)
	}
	select {
	case h := <-got:
		if h.Action != "peer_score" || h.Token != "modtok" || h.Peer != "peerXYZ" ||
			h.Op != "market_buy" || h.Outcome != "completed" ||
			h.Ref != "sess-1" || h.ValueHash != "abcd1234" {
			t.Fatalf("hello = %+v", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no hello received")
	}
}

func TestReportOutcome_Refused(t *testing.T) {
	_, addr := fakePeerScoreDaemon(t, bridgeResponse{OK: false, Error: "no score store"})
	t.Setenv("RHIZOME_BRIDGE_ADDR", addr)
	t.Setenv("RHIZOME_BRIDGE_TOKEN", "tok")

	err := reportOutcome("peer", "market_sell", "failed", "s", "v")
	if err == nil || !strings.Contains(err.Error(), "no score store") {
		t.Fatalf("expected refusal error, got %v", err)
	}
}

func TestReportOutcome_NoBridgeAddr(t *testing.T) {
	t.Setenv("RHIZOME_BRIDGE_ADDR", "")
	if err := reportOutcome("p", "market_buy", "completed", "s", "v"); err == nil {
		t.Fatal("expected error without RHIZOME_BRIDGE_ADDR")
	}
}

func TestPurchaseOutcomeMap(t *testing.T) {
	cases := map[string]string{
		purchaseCompleted:  "completed",
		purchaseFailed:     "failed",
		purchaseDisputed:   "disputed",
		purchaseResolved:   "resolved",
		purchaseRefunded:   "refunded",
		purchaseQueued:     "",
		purchaseSession:    "",
		purchaseDisputable: "",
	}
	for state, want := range cases {
		if got := purchaseOutcome(state); got != want {
			t.Errorf("purchaseOutcome(%q) = %q, want %q", state, got, want)
		}
	}
}

func TestSessionOutcomeMap(t *testing.T) {
	cases := []struct {
		state, reason, want string
	}{
		{sessionCompleted, "", "completed"},
		{sessionClosed, "", "completed"},
		{sessionFailed, "buyer gone", "failed"},
		{sessionFailed, "session expired", "expired"},
		{sessionOpen, "", ""},
		{sessionActive, "", ""},
	}
	for _, tc := range cases {
		if got := sessionOutcome(tc.state, tc.reason); got != tc.want {
			t.Errorf("sessionOutcome(%q, %q) = %q, want %q",
				tc.state, tc.reason, got, tc.want)
		}
	}
}

func TestValueHash_CommitsWithoutAmounts(t *testing.T) {
	h1 := valueHash("100", "USDC", "taskhash")
	h2 := valueHash("100", "USDC", "taskhash")
	h3 := valueHash("200", "USDC", "taskhash")
	if h1 != h2 {
		t.Fatal("valueHash not deterministic")
	}
	if h1 == h3 {
		t.Fatal("valueHash ignores amount")
	}
	if len(h1) != 64 {
		t.Fatalf("valueHash len = %d, want 64 hex chars", len(h1))
	}
}

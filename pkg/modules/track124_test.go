// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"bufio"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// peerScoreCall captures one recorded outcome for assertions.
type peerScoreCall struct {
	pid       peer.ID
	op        string
	outcome   string
	ref       string
	valueHash string
}

// peerScoreFixture stands up a Bridge on a single node with mod-b declaring
// peer_score (no protocol claim needed — actions are request/response) and
// a capture recorder.
func peerScoreFixture(t *testing.T) (addr, token string, calls *[]peerScoreCall) {
	t.Helper()
	calls = &[]peerScoreCall{}

	spec := testSpec("mod-ps")
	spec.BridgeActions = []string{"peer_score"}
	registerTestSpec(t, spec)
	mgr, _, _ := newTestManager(t, &config.Config{Modules: map[string]config.ModuleConfig{
		"mod-ps": {Enabled: true},
	}})

	nodeA := bridgeTestNode(t, t.Context(), nil)
	b, err := NewBridge(nodeA.Host(), mgr, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	b.SetPeerScoreRecorder(func(pid peer.ID, op, outcome, ref, vh string) error {
		*calls = append(*calls, peerScoreCall{pid, op, outcome, ref, vh})
		return nil
	})
	b.Start()

	token, err = mgr.bridgeToken("mod-ps")
	if err != nil || token == "" {
		t.Fatalf("token not minted for bridge_actions module: %q %v", token, err)
	}
	return b.Addr(), token, calls
}

// peerScoreRoundTrip sends one peer_score hello and reads the response line.
func peerScoreRoundTrip(t *testing.T, addr string, hello bridgeHello) bridgeResponse {
	t.Helper()
	conn := dialBridge(t, addr, hello)
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("response read: %v", err)
	}
	var resp bridgeResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("response parse: %v (%q)", err, line)
	}
	return resp
}

func TestPeerScoreActionRoundTrip(t *testing.T) {
	addr, tok, calls := peerScoreFixture(t)
	pidStr := testutil.NewIdentity(t).PeerID
	wantPID, err := peer.Decode(pidStr)
	if err != nil {
		t.Fatalf("peer decode: %v", err)
	}

	resp := peerScoreRoundTrip(t, addr, bridgeHello{
		Token: tok, Action: "peer_score", Peer: pidStr,
		Op: "market_buy", Outcome: "completed", Ref: "sess-1",
		ValueHash: "abcd1234",
	})
	if !resp.OK {
		t.Fatalf("peer_score refused: %s", resp.Error)
	}
	if len(*calls) != 1 {
		t.Fatalf("recorded calls = %d, want 1", len(*calls))
	}
	c := (*calls)[0]
	if c.pid != wantPID || c.op != "market_buy" || c.outcome != "completed" ||
		c.ref != "sess-1" || c.valueHash != "abcd1234" {
		t.Fatalf("recorded call = %+v", c)
	}
}

func TestPeerScoreRefusals(t *testing.T) {
	addr, tok, calls := peerScoreFixture(t)
	pid := testutil.NewIdentity(t).PeerID

	// Bad token — conn closes with no response line.
	conn := dialBridge(t, addr, bridgeHello{
		Token: "bogus", Action: "peer_score", Peer: pid,
		Op: "market_buy", Outcome: "completed",
	})
	expectClosed(t, conn)

	// Bad peer id.
	resp := peerScoreRoundTrip(t, addr, bridgeHello{
		Token: tok, Action: "peer_score", Peer: "not-a-peer",
		Op: "market_buy", Outcome: "completed",
	})
	if resp.OK {
		t.Fatal("bad peer id accepted")
	}

	if len(*calls) != 0 {
		t.Fatalf("refused calls recorded: %+v", *calls)
	}
}

// refusedFixture stands up a bridge for one enabled module with the given
// spec mutations (protocols-only vs actions-only) and returns addr+token.
func refusedFixture(
	t *testing.T, modID string, mutate func(*ModuleSpec), recorder bool,
) (addr, token string) {
	t.Helper()
	spec := testSpec(modID)
	mutate(&spec)
	registerTestSpec(t, spec)
	mgr, _, _ := newTestManager(t, &config.Config{Modules: map[string]config.ModuleConfig{
		modID: {Enabled: true},
	}})
	nodeA := bridgeTestNode(t, t.Context(), nil)
	b, err := NewBridge(nodeA.Host(), mgr, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if recorder {
		b.SetPeerScoreRecorder(func(peer.ID, string, string, string, string) error {
			return nil
		})
	}
	b.Start()
	tok, _ := mgr.bridgeToken(modID)
	return b.Addr(), tok
}

func peerScoreHello(t *testing.T, tok string) bridgeHello {
	return bridgeHello{
		Token: tok, Action: "peer_score",
		Peer: testutil.NewIdentity(t).PeerID,
		Op:   "market_buy", Outcome: "completed",
	}
}

func TestPeerScoreUndeclaredModuleRefused(t *testing.T) {
	// Protocols but no bridge_actions — peer_score must refuse.
	addr, tok := refusedFixture(t, "mod-na", func(s *ModuleSpec) {
		s.Protocols = []string{"/acme/x/1.0.0"}
	}, true)

	resp := peerScoreRoundTrip(t, addr, peerScoreHello(t, tok))
	if resp.OK {
		t.Fatal("peer_score accepted for module without bridge_actions")
	}
}

func TestPeerScoreNoRecorderRefused(t *testing.T) {
	addr, tok := refusedFixture(t, "mod-nr", func(s *ModuleSpec) {
		s.BridgeActions = []string{"peer_score"}
	}, false)

	resp := peerScoreRoundTrip(t, addr, peerScoreHello(t, tok))
	if resp.OK {
		t.Fatal("peer_score accepted with no recorder installed")
	}
}

// AnyEnabledProtocolModules must include bridge_actions-only modules or the
// daemon never binds the bridge for them.
func TestAnyEnabledBridgeActionModules(t *testing.T) {
	spec := testSpec("mod-act")
	spec.BridgeActions = []string{"peer_score"}
	registerTestSpec(t, spec)
	mgr, _, _ := newTestManager(t, &config.Config{Modules: map[string]config.ModuleConfig{
		"mod-act": {Enabled: true},
	}})
	if !mgr.AnyEnabledProtocolModules() {
		t.Fatal("bridge_actions module not seen as bridge user")
	}
}

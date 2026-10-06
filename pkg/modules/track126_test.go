// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"context"
	"fmt"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// fakeDHT captures provide/find calls and serves canned AddrInfos.
type fakeDHT struct {
	provided []string
	infos    []peer.AddrInfo
	findErr  error
	provErr  error
}

func (f *fakeDHT) Provide(_ context.Context, ns string) error {
	if f.provErr != nil {
		return f.provErr
	}
	f.provided = append(f.provided, ns)
	return nil
}

func (f *fakeDHT) FindProviders(_ context.Context, ns string) ([]peer.AddrInfo, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	return f.infos, nil
}

// dhtFixture stands up a Bridge for mod-dht declaring dht_provide+dht_find
// with a fake querier installed.
func dhtFixture(t *testing.T, actions []string, q DHTQuerier) (addr, token string) {
	t.Helper()
	spec := testSpec("mod-dht")
	spec.BridgeActions = actions
	registerTestSpec(t, spec)
	mgr, _, _ := newTestManager(t, &config.Config{Modules: map[string]config.ModuleConfig{
		"mod-dht": {Enabled: true},
	}})
	nodeA := bridgeTestNode(t, t.Context(), nil)
	b, err := NewBridge(nodeA.Host(), mgr, nil)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if q != nil {
		b.SetDHTQuerier(q)
	}
	b.Start()
	token, _ = mgr.bridgeToken("mod-dht")
	return b.Addr(), token
}

func dhtHello(tok, action, ns string) bridgeHello {
	return bridgeHello{Token: tok, Action: action, NS: ns}
}

func TestDHTProvideRoundTrip(t *testing.T) {
	fake := &fakeDHT{}
	addr, tok := dhtFixture(t, []string{"dht_provide", "dht_find"}, fake)

	resp := peerScoreRoundTrip(t, addr, dhtHello(tok, "dht_provide", "rhizome-market-v1"))
	if !resp.OK {
		t.Fatalf("dht_provide refused: %s", resp.Error)
	}
	if len(fake.provided) != 1 || fake.provided[0] != "rhizome-market-v1" {
		t.Fatalf("provided = %v", fake.provided)
	}
}

func TestDHTFindRoundTrip(t *testing.T) {
	pid, err := peer.Decode(testutil.NewIdentity(t).PeerID)
	if err != nil {
		t.Fatalf("peer decode: %v", err)
	}
	ma, err := multiaddr.NewMultiaddr("/ip4/127.0.0.1/tcp/4242")
	if err != nil {
		t.Fatalf("multiaddr: %v", err)
	}
	fake := &fakeDHT{infos: []peer.AddrInfo{{ID: pid, Addrs: []multiaddr.Multiaddr{ma}}}}
	addr, tok := dhtFixture(t, []string{"dht_provide", "dht_find"}, fake)

	resp := peerScoreRoundTrip(t, addr, dhtHello(tok, "dht_find", "rhizome-market-v1"))
	if !resp.OK {
		t.Fatalf("dht_find refused: %s", resp.Error)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("peers = %v, want 1", resp.Peers)
	}
	p := resp.Peers[0]
	if p.ID != pid.String() || len(p.Addrs) != 1 || p.Addrs[0] != ma.String() {
		t.Fatalf("peer = %+v", p)
	}
}

func TestDHTRefusals(t *testing.T) {
	fake := &fakeDHT{}
	addr, tok := dhtFixture(t, []string{"dht_provide", "dht_find"}, fake)

	cases := []bridgeHello{
		// Namespace must carry the rhizome-market- prefix.
		dhtHello(tok, "dht_provide", "other-ns"),
		dhtHello(tok, "dht_find", ""),
		dhtHello(tok, "dht_find", "rhizome-market-"), // prefix only, no tier
		// Overlong namespaces are refused (>64 chars).
		dhtHello(tok, "dht_find", "rhizome-market-"+
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
	}
	for i, h := range cases {
		resp := peerScoreRoundTrip(t, addr, h)
		if resp.OK {
			t.Fatalf("case %d accepted ns %q", i, h.NS)
		}
	}
	if len(fake.provided) != 0 {
		t.Fatalf("refused provide recorded: %v", fake.provided)
	}
}

func TestDHTUndeclaredModuleRefused(t *testing.T) {
	// peer_score only — dht actions must refuse.
	fake := &fakeDHT{}
	addr, tok := dhtFixture(t, []string{"peer_score"}, fake)

	resp := peerScoreRoundTrip(t, addr, dhtHello(tok, "dht_provide", "rhizome-market-v1"))
	if resp.OK {
		t.Fatal("dht_provide accepted for undeclared module")
	}
	if len(fake.provided) != 0 {
		t.Fatalf("refused provide recorded: %v", fake.provided)
	}
}

func TestDHTNoQuerierRefused(t *testing.T) {
	// Host without DHT: no querier installed — clean refusal, not a hang.
	addr, tok := dhtFixture(t, []string{"dht_provide", "dht_find"}, nil)

	resp := peerScoreRoundTrip(t, addr, dhtHello(tok, "dht_provide", "rhizome-market-v1"))
	if resp.OK {
		t.Fatal("dht_provide accepted with no querier")
	}
	resp = peerScoreRoundTrip(t, addr, dhtHello(tok, "dht_find", "rhizome-market-v1"))
	if resp.OK {
		t.Fatal("dht_find accepted with no querier")
	}
}

func TestDHTQuerierErrorsSurface(t *testing.T) {
	fake := &fakeDHT{
		provErr: fmt.Errorf("no peers in table"),
		findErr: fmt.Errorf("query timeout"),
	}
	addr, tok := dhtFixture(t, []string{"dht_provide", "dht_find"}, fake)

	resp := peerScoreRoundTrip(t, addr, dhtHello(tok, "dht_provide", "rhizome-market-v1"))
	if resp.OK {
		t.Fatal("provide error swallowed")
	}
	resp = peerScoreRoundTrip(t, addr, dhtHello(tok, "dht_find", "rhizome-market-v1"))
	if resp.OK {
		t.Fatal("find error swallowed")
	}
}

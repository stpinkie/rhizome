package network

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	circuitv2 "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

const testMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

func testIdentity(t *testing.T, index uint32) *identity.Derived {
	t.Helper()
	id, _, err := identity.FromMnemonic(testMnemonic, index)
	if err != nil {
		t.Fatalf("identity %d: %v", index, err)
	}
	return id
}

// TestRelayTraversal exercises the relay transport path end-to-end: node R runs
// a circuit relay v2 service, node B reserves a slot on R, and node A dials B
// over the relayed /p2p-circuit address.
//
// The AutoRelay reservation path itself is exercised implicitly (B has
// NATTraversal enabled), but its advertised /p2p-circuit addrs cannot be
// asserted here: upstream cleanupAddressSet strips non-public relay addrs from
// the advertised set, and loopback is the only address family available in
// tests.
func TestRelayTraversal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idR := testIdentity(t, 10)
	idA := testIdentity(t, 11)
	idB := testIdentity(t, 12)

	nodeR, err := NewNode(ctx, idR.Libp2pPrivKey, Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		RelayService:      true,
		ForceReachability: "public",
	})
	if err != nil {
		t.Fatalf("new relay node: %v", err)
	}
	defer nodeR.Close()

	relayAddrs := nodeR.BootstrapAddrs()
	if len(relayAddrs) == 0 {
		t.Fatalf("relay node has no listen addrs")
	}

	nodeB, err := NewNode(ctx, idB.Libp2pPrivKey, Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{relayAddrs[0]},
		NATTraversal:      true,
		StaticRelays:      relayAddrs,
		ForceReachability: "private",
	})
	if err != nil {
		t.Fatalf("new private node: %v", err)
	}
	defer nodeB.Close()

	nodeA, err := NewNode(ctx, idA.Libp2pPrivKey, Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		NATTraversal:      true,
		ForceReachability: "public",
	})
	if err != nil {
		t.Fatalf("new dialer node: %v", err)
	}
	defer nodeA.Close()

	// B reserves a relay slot on R directly. AutoRelay does the same thing in
	// production once it detects the node is private.
	relayMaddrs, err := MultiaddrStrings(relayAddrs)
	if err != nil {
		t.Fatalf("parse relay addr: %v", err)
	}
	relayInfo, err := peer.AddrInfosFromP2pAddrs(relayMaddrs...)
	if err != nil {
		t.Fatalf("parse relay addr: %v", err)
	}
	// The relay service registers the hop protocol asynchronously once the
	// (forced) public reachability is applied, and the advertisement must
	// reach B's peerstore over identify. Reserve before that wins a
	// "protocols not supported" race under load.
	hopProtocol := protocol.ID("/libp2p/circuit/relay/0.2.0/hop")
	protoDeadline := time.Now().Add(10 * time.Second)
	for {
		protos, err := nodeB.Host().Peerstore().SupportsProtocols(relayInfo[0].ID, hopProtocol)
		if err == nil && len(protos) > 0 {
			break
		}
		if time.Now().After(protoDeadline) {
			t.Fatalf("relay never advertised %s", hopProtocol)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The reservation itself may still lose a negotiation race, so retry it
	// within a bounded window — reservations are idempotent.
	var rsvp *circuitv2.Reservation
	rsvpDeadline := time.Now().Add(15 * time.Second)
	for {
		rsvpCtx, rsvpCancel := context.WithTimeout(ctx, 5*time.Second)
		rsvp, err = circuitv2.Reserve(rsvpCtx, nodeB.Host(), relayInfo[0])
		rsvpCancel()
		if err == nil {
			break
		}
		if time.Now().After(rsvpDeadline) {
			t.Fatalf("reserve relay slot on R: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if rsvp == nil || rsvp.Expiration.IsZero() {
		t.Fatalf("empty relay reservation")
	}

	// A dials B through the relay: <R addr>/p2p/<R>/p2p-circuit/p2p/<B>.
	circuitAddr := fmt.Sprintf("%s/p2p-circuit/p2p/%s", relayAddrs[0], nodeB.PeerID())
	dialCtx, dialCancel := context.WithTimeout(ctx, 15*time.Second)
	err = nodeA.Connect(dialCtx, circuitAddr)
	dialCancel()
	if err != nil {
		t.Fatalf("dial B via relay: %v", err)
	}

	// A relayed connection reports Connectedness "Limited" until (and unless)
	// hole punching upgrades it to a direct "Connected" link.
	connected := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if IsConnectednessUp(nodeA.Connectedness(nodeB.ID())) {
			connected = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !connected {
		t.Fatalf("node A is not connected to B via relay")
	}
}

// TestRelayPeerSource verifies the AutoRelay peer source yields static relays
// and known peers without duplicates and without the node's own ID.
func TestRelayPeerSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idR := testIdentity(t, 20)
	idA := testIdentity(t, 21)
	idB := testIdentity(t, 22)

	nodeR, err := NewNode(ctx, idR.Libp2pPrivKey, Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	if err != nil {
		t.Fatalf("new node R: %v", err)
	}
	defer nodeR.Close()
	relayAddrs := nodeR.BootstrapAddrs()

	nodeA, err := NewNode(ctx, idA.Libp2pPrivKey, Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	if err != nil {
		t.Fatalf("new node A: %v", err)
	}
	defer nodeA.Close()
	addrsA := nodeA.BootstrapAddrs()

	relayMaddrs, err := MultiaddrStrings(relayAddrs)
	if err != nil {
		t.Fatalf("parse relay addrs: %v", err)
	}
	relayInfos, err := peer.AddrInfosFromP2pAddrs(relayMaddrs...)
	if err != nil {
		t.Fatalf("parse relay addrs: %v", err)
	}

	nodeB, err := NewNode(ctx, idB.Libp2pPrivKey, Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
		NATTraversal:   true,
		StaticRelays:   relayAddrs,
	})
	if err != nil {
		t.Fatalf("new node B: %v", err)
	}
	defer nodeB.Close()

	// Give B a moment to register A as a known peer.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(nodeB.Peers()) == 0 {
		time.Sleep(100 * time.Millisecond)
	}

	src := nodeB.relaySource
	if src == nil {
		t.Fatalf("node B has no relay peer source")
	}
	if len(src.static) != len(relayInfos) {
		t.Fatalf("expected %d static relays, got %d", len(relayInfos), len(src.static))
	}

	out := src.peers(ctx, 10)
	seen := map[string]int{}
	count := 0
	for ai := range out {
		seen[ai.ID.String()]++
		count++
	}
	if count == 0 {
		t.Fatalf("peer source yielded no candidates")
	}
	if seen[nodeB.PeerID()] > 0 {
		t.Fatalf("peer source yielded the node's own ID")
	}
	if seen[nodeR.PeerID()] != 1 {
		t.Fatalf("expected relay %s exactly once, got %d", nodeR.PeerID(), seen[nodeR.PeerID()])
	}
	if _, ok := seen[nodeA.PeerID()]; !ok && len(nodeB.Peers()) > 0 {
		t.Fatalf("expected connected peer %s among candidates", nodeA.PeerID())
	}
	for id, n := range seen {
		if n > 1 {
			t.Fatalf("peer %s yielded %d times", id, n)
		}
	}
	if strings.Count(fmt.Sprint(src.static[0].Addrs), "tcp") == 0 {
		t.Fatalf("static relay has no usable addrs: %v", src.static[0])
	}
}

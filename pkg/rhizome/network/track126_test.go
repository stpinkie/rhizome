// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package network

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
)

// TestDHTNamespacedProvideFindConnect is the Track 126 loopback fixture:
// node A provides the market rendezvous namespace, node B finds it via
// FindProvidersNS, then connects — the same path the market module's
// dht_provide/dht_find bridge actions ride.
func TestDHTNamespacedProvideFindConnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer hA.Close()

	hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer hB.Close()

	// A is the DHT server (reachable), B a client bootstrapping through A.
	dA, err := NewDiscovery(hA, DHTConfig{
		Enabled:    true,
		Server:     true,
		Rendezvous: "rhizome-test-node-tier",
	})
	require.NoError(t, err)
	require.NoError(t, dA.Start(ctx))
	defer dA.Stop()

	addrsA := hA.Addrs()
	require.NotEmpty(t, addrsA)
	bootstrap := fmt.Sprintf("%s/p2p/%s", addrsA[0].String(), hA.ID().String())

	// Both nodes serve the DHT protocol — client-mode peers never land in
	// each other's routing tables, so Provide would never find a route.
	dB, err := NewDiscovery(hB, DHTConfig{
		Enabled:        true,
		Server:         true,
		Rendezvous:     "rhizome-test-node-tier",
		BootstrapPeers: []string{bootstrap},
	})
	require.NoError(t, err)
	require.NoError(t, dB.Start(ctx))
	defer dB.Stop()

	// Give B's bootstrap a beat so provide lands on a routed peer.
	require.Eventually(t, func() bool {
		return hB.Network().Connectedness(hA.ID()).String() == "Connected"
	}, 15*time.Second, 100*time.Millisecond)

	ns := "rhizome-market-v1"
	// A's routing table fills asynchronously once B connects — provide
	// retries like the daemon's provide loop does.
	require.Eventually(t, func() bool {
		return dA.ProvideNS(ctx, ns) == nil
	}, 30*time.Second, 500*time.Millisecond)

	// B finds A under the market namespace, then connects to its addrs.
	var found []string
	require.Eventually(t, func() bool {
		infos, err := dB.FindProvidersNS(ctx, ns)
		if err != nil {
			return false
		}
		for _, info := range infos {
			if info.ID == hA.ID() {
				for _, a := range info.Addrs {
					found = append(found, a.String())
				}
				return true
			}
		}
		return false
	}, 30*time.Second, 500*time.Millisecond)
	require.NotEmpty(t, found)

	// Connect explicitly — the AddrInfo is enough to dial.
	require.NoError(t, hB.Connect(ctx, peer.AddrInfo{ID: hA.ID(), Addrs: hA.Addrs()}))
	require.Equal(t, "Connected", hB.Network().Connectedness(hA.ID()).String())

	// Disabled DHT surfaces a clean error, not a hang.
	var nilD *Discovery
	require.Error(t, nilD.ProvideNS(ctx, ns))
	_, err = nilD.FindProvidersNS(ctx, ns)
	require.Error(t, err)
}

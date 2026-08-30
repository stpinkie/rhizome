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

func TestDHTDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer hA.Close()

	hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	defer hB.Close()

	rendezvous := "rhizome-test-rendezvous"

	dA, err := NewDiscovery(hA, DHTConfig{
		Enabled:    true,
		Server:     true,
		Rendezvous: rendezvous,
	})
	require.NoError(t, err)
	require.NoError(t, dA.Start(ctx))
	defer dA.Stop()

	found := make(chan peer.ID, 1)
	dA.OnFound = func(info peer.AddrInfo) { found <- info.ID }

	addrsA := hA.Addrs()
	require.NotEmpty(t, addrsA)
	bootstrap := fmt.Sprintf("%s/p2p/%s", addrsA[0].String(), hA.ID().String())

	dB, err := NewDiscovery(hB, DHTConfig{
		Enabled:        true,
		Server:         false,
		Rendezvous:     rendezvous,
		BootstrapPeers: []string{bootstrap},
	})
	require.NoError(t, err)
	require.NoError(t, dB.Start(ctx))
	defer dB.Stop()

	// Wait for B to find A and for A to find B.
	select {
	case id := <-found:
		require.Equal(t, hB.ID().String(), id.String())
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for DHT discovery")
	}
}

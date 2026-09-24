package acp

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/p2putil"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// remoteE2EMesh builds an isolated libp2p node + mesh pair. Peers are wired
// explicitly and trust is granted by the test.
func remoteE2EMesh(t *testing.T, ctx context.Context, bootstrap []string) (*rnet.Node, *mesh.Mesh) {
	t.Helper()
	id := testutil.NewIdentity(t)
	node, err := rnet.NewNode(ctx, id.Libp2pPrivKey, rnet.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    bootstrap,
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	t.Cleanup(func() { node.Close() })

	m := mesh.NewMesh(node, nil, id, config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		RemoteTimeout:       30 * time.Second,
	}, func(context.Context, agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		return toolshared.NewToolResult("n/a"), nil, nil
	})
	if err := m.Start(ctx); err != nil {
		t.Fatalf("mesh.Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop() })
	return node, m
}

// remoteE2EServer wires a RemoteMux behind the same trusted-peer-gated
// /rhizome/acp/1.0.0 stream handler the gateway installs.
func remoteE2EServer(t *testing.T, m *mesh.Mesh, mux *RemoteMux) {
	t.Helper()
	if err := mux.Start(); err != nil {
		t.Fatalf("mux.Start: %v", err)
	}
	t.Cleanup(mux.Close)
	m.Host().SetStreamHandler(protocol.ID(RemoteProtocolID), func(s network.Stream) {
		if !m.IsTrusted(s.Conn().RemotePeer()) {
			_ = s.Reset()
			return
		}
		mux.Serve(s)
	})
}

// remoteE2EDialer replicates the gateway's trust-gated RemoteDialer.
func remoteE2EDialer(m *mesh.Mesh) RemoteDialer {
	return func(ctx context.Context, _, remote string) (io.ReadWriteCloser, error) {
		var pid peer.ID
		if decoded, err := peer.Decode(remote); err == nil {
			pid = decoded
		} else {
			ai, err := peer.AddrInfoFromString(remote)
			if err != nil {
				return nil, err
			}
			pid = ai.ID
			m.Host().Peerstore().AddAddrs(pid, ai.Addrs, peerstore.TempAddrTTL)
		}
		if !m.IsTrusted(pid) {
			return nil, fmt.Errorf("peer %s is not trusted", pid)
		}
		return p2putil.OpenProtocolStream(
			ctx, m.Host(), pid, protocol.ID(RemoteProtocolID), 10*time.Second,
		)
	}
}

// TestRemoteACPOverLibp2p drives the full acp.remote path over real libp2p
// streams: a remote-bound client agent on node A dials a trusted RemoteMux
// on node B and completes an initialize + session/new + prompt round-trip.
func TestRemoteACPOverLibp2p(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeB, meshB := remoteE2EMesh(t, ctx, nil)
	addrsB := nodeB.BootstrapAddrs()
	if len(addrsB) == 0 {
		t.Fatal("nodeB has no listen addrs")
	}
	nodeA, meshA := remoteE2EMesh(t, ctx, []string{addrsB[0]})

	waitConnected(t, nodeA, nodeB)

	// Mutual trust — the dialer and the stream gate both require it.
	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	mux := NewRemoteMux(newFakeRunner(), Options{Policy: PermissionAllow})
	remoteE2EServer(t, meshB, mux)

	reg := remoteBoundRegistry(t, t.TempDir(), nodeB.ID().String())
	mgr := NewClientManager(&config.Config{}, func() *agent.AgentRegistry { return reg })
	if mgr == nil {
		t.Fatal("manager should exist for remote-bound agent")
	}
	defer mgr.Close()
	mgr.SetRemoteDialer(remoteE2EDialer(meshA))

	out, err := mgr.RunAgent(ctx, "ext", "say hi")
	if err != nil {
		t.Fatalf("RunAgent over libp2p acp.remote: %v", err)
	}
	if out != "final response" {
		t.Fatalf("got %q", out)
	}
}

// TestRemoteACPUntrustedPeerRefused verifies the trust gate on the serve
// side: an untrusted peer's stream is reset before any ACP traffic flows.
func TestRemoteACPUntrustedPeerRefused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeB, meshB := remoteE2EMesh(t, ctx, nil)
	nodeA, meshA := remoteE2EMesh(t, ctx, []string{nodeB.BootstrapAddrs()[0]})
	waitConnected(t, nodeA, nodeB)

	// nodeA trusts nodeB (so its dialer proceeds) but nodeB does NOT trust
	// nodeA back — the inbound gate must refuse the stream.
	meshA.TrustPeer(nodeB.ID())

	mux := NewRemoteMux(newFakeRunner(), Options{Policy: PermissionAllow})
	remoteE2EServer(t, meshB, mux)

	dial := remoteE2EDialer(meshA)
	rw, err := dial(ctx, "ext", nodeB.ID().String())
	if err != nil {
		return // refusal at stream-open is also acceptable
	}
	defer rw.Close()
	// The stream opened (handler runs post-open) but must be dead: the
	// remote reset it before any ACP bytes were consumed.
	s := rw.(network.Stream)
	_ = s.CloseWrite()
	_ = s.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	if _, err := s.Read(buf); err == nil {
		t.Fatal("untrusted peer's stream should be reset")
	}
}

func waitConnected(t *testing.T, a, b *rnet.Node) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if rnet.IsConnectednessUp(a.Connectedness(b.ID())) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("nodes never connected")
}

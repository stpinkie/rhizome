package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// stubRunner is a minimal acp.AgentRunner for the remote-serve path.
type stubRunner struct {
	msgs []bus.InboundMessage
	ev   *events.EventBus
	reg  *agent.AgentRegistry
}

func newStubRunner() *stubRunner {
	return &stubRunner{
		ev:  events.NewBus(),
		reg: agent.NewAgentRegistry(&config.Config{}, nil),
	}
}

func (s *stubRunner) ProcessInbound(
	_ context.Context, msg bus.InboundMessage,
) (string, error) {
	s.msgs = append(s.msgs, msg)
	return "remote-turn:" + msg.Content, nil
}

func (s *stubRunner) RuntimeEvents() events.EventChannel { return s.ev.Channel() }
func (s *stubRunner) MountHook(agent.HookRegistration) error {
	return nil
}
func (s *stubRunner) GetRegistry() *agent.AgentRegistry { return s.reg }

func remoteMeshNode(
	t *testing.T, ctx context.Context, bootstrap []string,
) (*rnet.Node, *mesh.Mesh) {
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

func waitRemoteConnected(t *testing.T, a, b *rnet.Node) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if rnet.IsConnectednessUp(a.Connectedness(b.ID())) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("nodes never connected")
}

// TestRemoteACPProductionWiring exercises the real gateway path end to end:
// startRemoteACP registers the trusted stream handler on nodeB's mesh host,
// and remoteACPDialer dials it from nodeA's remote-bound agent — a full
// initialize + session/new + prompt turn over the production wire.
func TestRemoteACPProductionWiring(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeB, meshB := remoteMeshNode(t, ctx, nil)
	nodeA, meshA := remoteMeshNode(t, ctx, []string{nodeB.BootstrapAddrs()[0]})
	waitRemoteConnected(t, nodeA, nodeB)
	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	// Serve on nodeB through the production startRemoteACP.
	runner := newStubRunner()
	msgBus := bus.NewMessageBus()
	mux, err := startRemoteACP(
		&config.Config{}, t.TempDir(), runner, msgBus, nil, meshB,
	)
	if err != nil {
		t.Fatalf("startRemoteACP: %v", err)
	}
	defer mux.Close()

	// Client on nodeA through the production remoteACPDialer.
	reg := agent.NewAgentRegistry(&config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "ext",
				Workspace: t.TempDir(),
				ACP:       &config.ACPAgentConfig{Remote: nodeB.ID().String()},
			}},
		},
	}, nil)
	mgr := acp.NewClientManager(&config.Config{}, func() *agent.AgentRegistry { return reg })
	if mgr == nil {
		t.Fatal("manager should exist for remote-bound agent")
	}
	defer mgr.Close()
	mgr.SetRemoteDialer(remoteACPDialer(meshA))

	out, err := mgr.RunAgent(ctx, "ext", "hello-remote")
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if out != "remote-turn:hello-remote" {
		t.Fatalf("got %q", out)
	}
	if len(runner.msgs) != 1 {
		t.Fatalf("runner got %d messages", len(runner.msgs))
	}
}

// TestRemoteACPDialerTrustGate exercises the dialer's trust check: an
// untrusted remote binding must fail before any stream is opened.
func TestRemoteACPDialerTrustGate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nodeB, _ := remoteMeshNode(t, ctx, nil)
	_, meshA := remoteMeshNode(t, ctx, []string{nodeB.BootstrapAddrs()[0]})

	dial := remoteACPDialer(meshA)
	_, err := dial(ctx, "ext", nodeB.ID().String())
	if err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("untrusted dial should refuse, got: %v", err)
	}
}

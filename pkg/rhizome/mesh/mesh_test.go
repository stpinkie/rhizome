package mesh

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	"github.com/stpinkie/rhizome/pkg/skills"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func TestMeshRemoteCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA := testutil.NewIdentity(t)
	idB := testutil.NewIdentity(t)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{addrsA[0]},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeB.Close()

	// Wait for the bootstrap connection to come up.
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond, "nodeB should connect to nodeA")

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("hello from remote"), nil
	}

	cfg := config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		RemoteTimeout:       30 * time.Second,
	}

	meshA := NewMesh(nodeA, nil, idA, cfg, nilUsageRun(runFunc))
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()

	meshB := NewMesh(nodeB, nil, idB, cfg, nilUsageRun(runFunc))
	require.NoError(t, meshB.Start(ctx))
	defer meshB.Stop()

	// Trust each other.
	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	result, err := meshA.CallRemote(ctx, nodeB.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "say hello",
	})
	require.NoError(t, err)
	assert.Equal(t, "hello from remote", result.ForLLM)
}

// TestWorkerRoleAdvertised verifies the worker tier is signed into the
// capability manifest, survives verifyCapability on the receiving peer, and
// surfaces through PeerCapabilities → PeerCapability.Role → status output.
func TestWorkerRoleAdvertised(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		RemoteTimeout:       30 * time.Second,
		Role:                config.MeshRoleWorker,
	})
	ctx := context.Background()

	// Local build: full nodes omit the field (wire compat), workers emit it.
	full := f.meshA.localCapability()
	assert.Empty(t, full.Role)
	raw, err := json.Marshal(full)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"role"`)

	worker := f.meshB.localCapability()
	assert.Equal(t, config.MeshRoleWorker, worker.Role)

	// The signed manifest verifies at the receiving peer and the role is
	// stored with the capability.
	announced, err := f.meshA.TrustAndDiscover(ctx, f.nodeB.ID())
	require.NoError(t, err)
	assert.Equal(t, config.MeshRoleWorker, announced.Role)

	require.Eventually(t, func() bool {
		stored, ok := f.meshA.PeerCapabilities(f.nodeB.ID())
		return ok && stored.Role == config.MeshRoleWorker
	}, 10*time.Second, 100*time.Millisecond, "stored capability must carry role=worker")

	// Status surface: PeerCapability.Role flows into the peers list.
	require.Eventually(t, func() bool {
		status := f.meshA.NetworkStatus(t.TempDir())
		for _, p := range status.Peers {
			if p.PeerID == f.nodeB.ID().String() {
				return p.Capability.Role == config.MeshRoleWorker
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond)
}

func TestMeshUntrustedPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA := testutil.NewIdentity(t)
	idB := testutil.NewIdentity(t)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{addrsA[0]},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeB.Close()

	// Wait for the bootstrap connection to come up.
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond, "nodeB should connect to nodeA")

	cfg := config.MeshConfig{Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second}
	meshA := NewMesh(nodeA, nil, idA, cfg, nil)
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()

	meshB := NewMesh(
		nodeB,
		nil,
		idB,
		cfg,
		nilUsageRun(func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("hello from remote"), nil
		}),
	)
	require.NoError(t, meshB.Start(ctx))
	defer meshB.Stop()

	// A does not trust B and B does not trust A.
	_, err = meshA.CallRemote(ctx, nodeB.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "say hello",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not trusted")
}

func TestMeshInvalidRequestSignature(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA := testutil.NewIdentity(t)
	idB := testutil.NewIdentity(t)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{addrsA[0]},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeB.Close()

	// Wait for the bootstrap connection to come up.
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond, "nodeB should connect to nodeA")

	cfg := config.MeshConfig{Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second}
	meshA := NewMesh(nodeA, nil, idA, cfg, nil)
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()

	meshB := NewMesh(
		nodeB,
		nil,
		idB,
		cfg,
		nilUsageRun(func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("hello from remote"), nil
		}),
	)
	require.NoError(t, meshB.Start(ctx))
	defer meshB.Stop()

	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	// Send a request with a bogus signature directly through the agentrpc transport.
	req := agentrpc.Request{
		CorrelationID: newCorrelationID(),
		TargetAgentID: "main",
		SystemPrompt:  "say hello",
		Timeout:       cfg.RemoteTimeout,
		Signature:     []byte("not-a-valid-signature"),
	}

	resp, err := meshA.rpc.Call(ctx, nodeB.ID(), req)
	require.NoError(t, err)
	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Error, "verify request")
	require.NotEmpty(t, resp.Signature, "error response should still be signed")
}

func TestMeshCapabilityExchange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA := testutil.NewIdentity(t)
	idB := testutil.NewIdentity(t)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{addrsA[0]},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeB.Close()

	// Wait for the bootstrap connection to come up.
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond, "nodeB should connect to nodeA")

	cfg := config.MeshConfig{Enabled: true}
	meshA := NewMesh(nodeA, nil, idA, cfg, nil)
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()

	meshB := NewMesh(nodeB, nil, idB, cfg, nil)
	require.NoError(t, meshB.Start(ctx))
	defer meshB.Stop()

	// Send B's capability to A.
	capability := Capability{PeerID: nodeB.PeerID(), Agents: []string{"main"}}
	err = meshB.cap.Send(ctx, nodeA.ID(), capability)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, ok := meshA.PeerCapabilities(nodeB.ID())
		return ok
	}, 5*time.Second, 100*time.Millisecond, "capability should arrive")

	got, ok := meshA.PeerCapabilities(nodeB.ID())
	require.True(t, ok)
	assert.Equal(t, nodeB.PeerID(), got.PeerID)
	assert.Equal(t, []string{"main"}, got.Agents)
}

func TestMeshCapabilityAdvertisesModelsAndSkills(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id := testutil.NewIdentity(t)

	node, err := network.NewNode(ctx, id.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer node.Close()

	cfg := config.MeshConfig{
		Enabled:         true,
		AdvertiseModels: true,
		AdvertiseSkills: true,
	}
	m := NewMesh(node, nil, id, cfg, nil)

	m.SetModelList(config.SecureModelList{
		{ModelName: "enabled-model", Model: "openai/gpt-5", Enabled: true},
		{ModelName: "disabled-model", Model: "openai/gpt-4", Enabled: false},
		{ModelName: "unnamed-model", Model: "openai/gpt-3"},
	})

	tmp := t.TempDir()
	skillDir := filepath.Join(tmp, "skills", "demo-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(skillDir, "SKILL.md"),
			[]byte("---\nname: demo-skill\ndescription: A demo skill for testing.\n---\n# Demo\n"),
			0o644,
		),
	)

	m.SetSkillsLoader(skills.NewSkillsLoader(tmp, "", ""))

	capability := m.localCapability()
	assert.Equal(t, []string{"enabled-model"}, capability.Models)
	assert.Equal(t, []string{"demo-skill"}, capability.Skills)
	assert.Equal(t, []string{"main"}, capability.Agents)
}

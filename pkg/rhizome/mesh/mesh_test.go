package mesh

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/skills"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func TestMeshRemoteCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		1,
	)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
	})
	require.NoError(t, err)
	defer nodeB.Close()

	// Wait for nodes to connect.
	time.Sleep(500 * time.Millisecond)

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("hello from remote"), nil
	}

	cfg := config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		RemoteTimeout:       30 * time.Second,
	}

	meshA := NewMesh(nodeA, nil, idA, cfg, runFunc)
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()

	meshB := NewMesh(nodeB, nil, idB, cfg, runFunc)
	require.NoError(t, meshB.Start(ctx))
	defer meshB.Stop()

	// Trust each other.
	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	result, err := meshA.CallRemote(ctx, nodeB.ID(), "main", "say hello", false)
	require.NoError(t, err)
	assert.Equal(t, "hello from remote", result.ForLLM)
}

func TestMeshUntrustedPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		1,
	)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
	})
	require.NoError(t, err)
	defer nodeB.Close()

	time.Sleep(500 * time.Millisecond)

	cfg := config.MeshConfig{Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second}
	meshA := NewMesh(nodeA, nil, idA, cfg, nil)
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()

	meshB := NewMesh(nodeB, nil, idB, cfg, func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("hello from remote"), nil
	})
	require.NoError(t, meshB.Start(ctx))
	defer meshB.Stop()

	// A does not trust B and B does not trust A.
	_, err = meshA.CallRemote(ctx, nodeB.ID(), "main", "say hello", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not trusted")
}

func TestMeshInvalidRequestSignature(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		1,
	)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
	})
	require.NoError(t, err)
	defer nodeB.Close()

	time.Sleep(500 * time.Millisecond)

	cfg := config.MeshConfig{Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second}
	meshA := NewMesh(nodeA, nil, idA, cfg, nil)
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()

	meshB := NewMesh(nodeB, nil, idB, cfg, func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("hello from remote"), nil
	})
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

	idA, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		1,
	)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	require.NoError(t, err)
	defer nodeA.Close()

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
	})
	require.NoError(t, err)
	defer nodeB.Close()

	time.Sleep(500 * time.Millisecond)

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

	id, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	require.NoError(t, err)

	node, err := network.NewNode(ctx, id.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
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
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo-skill\ndescription: A demo skill for testing.\n---\n# Demo\n"), 0o644))

	m.SetSkillsLoader(skills.NewSkillsLoader(tmp, "", ""))

	cap := m.localCapability()
	assert.Equal(t, []string{"enabled-model"}, cap.Models)
	assert.Equal(t, []string{"demo-skill"}, cap.Skills)
	assert.Equal(t, []string{"main"}, cap.Agents)
}

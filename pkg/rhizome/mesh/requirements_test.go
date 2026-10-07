package mesh

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	"github.com/stpinkie/rhizome/pkg/skills"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func TestCapabilityServesRequirements(t *testing.T) {
	capable := Capability{
		Agents:          []string{"main"},
		Models:          []string{"llama3"},
		Skills:          []string{"web-search"},
		ShareableSkills: []string{"summarize"},
		Allows:          map[string]bool{"spawn": true},
	}

	for _, tc := range []struct {
		name string
		req  Requirements
		want bool
	}{
		{"empty", Requirements{}, true},
		{"model match", Requirements{Models: []string{"llama3"}}, true},
		{"model any-of", Requirements{Models: []string{"qwen", "llama3"}}, true},
		{"model miss", Requirements{Models: []string{"qwen"}}, false},
		{"skill match", Requirements{Skills: []string{"web-search"}}, true},
		{"skill via shareable", Requirements{Skills: []string{"summarize"}}, true},
		{"skill miss", Requirements{Skills: []string{"code-review"}}, false},
		{"model ok skill miss", Requirements{
			Models: []string{"llama3"}, Skills: []string{"code-review"},
		}, false},
		{"model miss skill ok", Requirements{
			Models: []string{"qwen"}, Skills: []string{"web-search"},
		}, false},
		{"both match", Requirements{
			Models: []string{"llama3"}, Skills: []string{"web-search"},
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, capabilityServes(capable, "main", "spawn", tc.req))
		})
	}

	// A manifest that omits a class entirely never satisfies a requirement
	// for it — advertise_models/advertise_skills off is not a match.
	bare := Capability{Agents: []string{"*"}, Allows: map[string]bool{"spawn": true}}
	assert.False(t, capabilityServes(bare, "main", "spawn", Requirements{Models: []string{"llama3"}}))
	assert.False(t, capabilityServes(bare, "main", "spawn", Requirements{Skills: []string{"web-search"}}))
	assert.True(t, capabilityServes(bare, "main", "spawn", Requirements{}))

	// Requirements do not relax the agent/op checks.
	assert.False(t, capabilityServes(capable, "other", "spawn", Requirements{}))
	assert.False(t, capabilityServes(capable, "main", "delegate", Requirements{
		Models: []string{"llama3"},
	}))
}

func TestRequirementsStrings(t *testing.T) {
	assert.True(t, Requirements{}.Empty())
	assert.Equal(t, "", reqSuffix(Requirements{}))
	assert.Equal(t,
		" satisfying requirements (models=[a b], skills=[x])",
		reqSuffix(Requirements{Models: []string{"a", "b"}, Skills: []string{"x"}}))
}

func TestMeshPickPeerWithRequirements(t *testing.T) {
	ctx := context.Background()
	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok"), nil
	}
	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		AdvertiseModels:  true,
		SkillShare:       []string{"summarize"},
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)

	// B really advertises the model and the shareable skill — announces
	// then converge on the truth instead of a planted manifest that a
	// late-arriving announce could clobber.
	ws := t.TempDir()
	skillDir := filepath.Join(ws, "skills", "summarize")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: summarize\ndescription: test skill\n---\n"), 0o644))
	meshB.SetSkillsLoader(skills.NewSkillsLoader(ws, "", ""))
	meshB.SetModelList(config.SecureModelList{
		{ModelName: "llama3", Model: "test/llama3", Enabled: true},
	})

	req := Requirements{Models: []string{"llama3"}}
	var pid peer.ID
	require.Eventually(t, func() bool {
		meshB.Advertise(ctx)
		var err error
		pid, _, err = meshA.PickPeerWith("main", "spawn", req)
		return err == nil
	}, 15*time.Second, 100*time.Millisecond)
	assert.Equal(t, meshB.node.ID(), pid)

	// A skill advertised only under shareable_skills still satisfies.
	_, _, err := meshA.PickPeerWith("main", "spawn", Requirements{Skills: []string{"summarize"}})
	require.NoError(t, err)

	// A missing model rejects the pick; the error names the requirement.
	_, _, err = meshA.PickPeerWith("main", "spawn", Requirements{Models: []string{"qwen"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "models=[qwen]")

	// Unconstrained pick still works on the same manifest.
	_, _, err = meshA.PickPeer("main", "spawn")
	require.NoError(t, err)
}

func TestSubmitCandidatesRequirements(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok"), nil
	}
	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		AdvertiseModels:  true,
		RemoteTimeout:    30 * time.Second,
	}
	meshA, meshB := newTaskTestMeshes(t, runFunc, cfg)
	meshB.SetModelList(config.SecureModelList{
		{ModelName: "llama3", Model: "test/llama3", Enabled: true},
	})

	// Third node connected to A — advertises no models.
	idC := testutil.NewIdentity(t)
	nodeC, err := network.NewNode(ctx, idC.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeC.Close() })

	meshC := NewMesh(nodeC, nil, idC, cfg, nilUsageRun(runFunc))
	require.NoError(t, meshC.Start(ctx))
	t.Cleanup(func() { _ = meshC.Stop() })

	addrsC := nodeC.BootstrapAddrs()
	require.NotEmpty(t, addrsC)
	require.NoError(t, meshA.Connect(ctx, addrsC[0]))
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(meshA.node.Connectedness(nodeC.ID()))
	}, 10*time.Second, 50*time.Millisecond)
	meshA.TrustPeer(nodeC.ID())

	// B carries the required model for real; C omits the class. Re-announce
	// until the manifests land at A — every announce carries the truth, so
	// the view stays correct once it converges (no planted-caps race).
	req := Requirements{Models: []string{"llama3"}}
	var candidates []RankedPeer
	require.Eventually(t, func() bool {
		meshB.Advertise(ctx)
		meshC.Advertise(ctx)
		candidates = meshA.submitCandidates("", "main", "spawn", req)
		return len(candidates) == 1 && candidates[0].PID == meshB.node.ID()
	}, 15*time.Second, 100*time.Millisecond)

	// Without a preferred peer only B qualifies.
	assert.Equal(t, meshB.node.ID(), candidates[0].PID)

	// The preferred peer is never filtered — C leads even though it lacks
	// the model — and only B remains as a fallback.
	candidates = meshA.submitCandidates(nodeC.ID(), "main", "spawn", req)
	require.Len(t, candidates, 2)
	assert.Equal(t, nodeC.ID(), candidates[0].PID)
	assert.Equal(t, meshB.node.ID(), candidates[1].PID)

	// An unsatisfiable requirement leaves the preferred peer alone on the
	// list — no fallback survives.
	candidates = meshA.submitCandidates(nodeC.ID(), "main", "spawn",
		Requirements{Models: []string{"qwen"}})
	require.Len(t, candidates, 1)
	assert.Equal(t, nodeC.ID(), candidates[0].PID)
}

func TestMeshFanoutRequirements(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok-" + req.TargetAgentID), nil
	}
	// Worker B really advertises llama3 — announces then converge on the
	// truth instead of clobbering a planted manifest at an unlucky moment.
	meshA, meshB, _ := newFanoutTestMeshesCfg(t, func(role string, c *config.MeshConfig) {
		if role == "workerB" {
			c.AdvertiseModels = true
		}
	}, runFunc, runFunc)
	meshB.SetModelList(config.SecureModelList{
		{ModelName: "llama3", Model: "test/llama3", Enabled: true},
	})

	// Re-announce until A sees the model; every announce carries llama3 so
	// the last write wins forever once it lands.
	require.Eventually(t, func() bool {
		meshB.Advertise(ctx)
		c, ok := meshA.PeerCapabilities(meshB.node.ID())
		return ok && slices.Contains(c.Models, "llama3")
	}, 15*time.Second, 100*time.Millisecond)

	res, err := meshA.FanoutTask(ctx, FanoutRequest{
		AgentID:  "main",
		Task:     "hi",
		Strategy: FanoutStrategyAll,
		Wait:     15 * time.Second,
		Requires: Requirements{Models: []string{"llama3"}},
	})
	require.NoError(t, err)
	require.Len(t, res.Branches, 1, "only the matching peer should get a branch")
	assert.Equal(t, meshB.node.ID().String(), res.Branches[0].PeerID)
	assert.Equal(t, string(agenttask.StatusDone), res.Branches[0].Status)

	// An unsatisfiable requirement fails the fan-out outright.
	_, err = meshA.FanoutTask(ctx, FanoutRequest{
		AgentID:  "main",
		Task:     "hi",
		Requires: Requirements{Models: []string{"qwen"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no capable trusted peer")
	assert.Contains(t, err.Error(), "models=[qwen]")
}

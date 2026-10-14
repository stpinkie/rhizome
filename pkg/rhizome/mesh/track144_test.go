package mesh

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func ptr[T any](v T) *T { return &v }

// track144EconCfg builds a seller-side economy config: unit "credits",
// price_sheet{min_charge, per_task}. The caller fixture auto-populates
// bill_peers when enabled.
func track144EconCfg(minCharge, perTask string) config.MeshEconomyConfig {
	return config.MeshEconomyConfig{
		Enabled:     true,
		Unit:        "credits",
		AcceptUnits: []string{"credits"},
		PriceSheet: config.EconPriceSheet{
			MinCharge: minCharge,
			PerTask:   perTask,
		},
	}
}

// track144CallerEcon mirrors track139CallerEcon but with a caller-selectable
// per-task config cap so tests can exercise flag-vs-config precedence.
func track144CallerEcon(maxCostPerTask string) config.MeshEconomyConfig {
	e := track139CallerEcon()
	e.MaxCostPerTask = maxCostPerTask
	return e
}

// addEconPeerNode spins up an additional mesh peer connected to meshA with
// mutual trust and task-protocol readiness. When econCfg is non-nil the
// peer advertises those economy terms; the call returns only after the
// peer's real capability manifest (and advert) has landed at meshA — the
// announce-driven path, so nothing races a planted capability.
func addEconPeerNode(
	t *testing.T,
	meshA *Mesh,
	econCfg *config.MeshEconomyConfig,
) *Mesh {
	t.Helper()
	ctx := context.Background()

	id := testutil.NewIdentity(t)
	node, err := network.NewNode(ctx, id.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = node.Close() })

	cfg := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}
	if econCfg != nil {
		cfg.Economy = *econCfg
	}
	m := NewMesh(node, nil, id, cfg, nilUsageRun(
		func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("ok from " + req.CorrelationID), nil
		}))
	require.NoError(t, m.Start(ctx))
	t.Cleanup(func() { _ = m.Stop() })

	addrs := node.BootstrapAddrs()
	require.NotEmpty(t, addrs)
	require.NoError(t, meshA.Connect(ctx, addrs[0]))
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(meshA.node.Connectedness(node.ID()))
	}, 10*time.Second, 50*time.Millisecond)
	meshA.TrustPeer(node.ID())
	m.TrustPeer(meshA.node.ID())

	require.Eventually(t, func() bool {
		protos, _ := meshA.host.Peerstore().SupportsProtocols(node.ID(), agenttask.ProtocolID)
		return len(protos) > 0
	}, 60*time.Second, 100*time.Millisecond, "task protocol should be advertised")

	// Drive announces until meshA holds the peer's manifest; priced peers
	// must additionally have their economy advert visible.
	require.Eventually(t, func() bool {
		m.Advertise(ctx)
		c, ok := meshA.PeerCapabilities(node.ID())
		if !ok {
			return false
		}
		if econCfg == nil {
			return true
		}
		return c.Economy != nil && c.Economy.Unit == "credits"
	}, 15*time.Second, 100*time.Millisecond, "peer capability must propagate")

	return m
}

// waitEconAdvert waits until caller holds the callee's economy advert via
// the real announce path (a calle-side Advertise drives it).
func waitEconAdvert(t *testing.T, caller, callee *Mesh) {
	t.Helper()
	require.Eventually(t, func() bool {
		callee.Advertise(context.Background())
		c, ok := caller.PeerCapabilities(callee.node.ID())
		return ok && c.Economy != nil && c.Economy.Unit == "credits"
	}, 15*time.Second, 100*time.Millisecond, "callee economy advert must propagate")
}

func submitFailures(t *testing.T, m *Mesh, pid peer.ID) int {
	t.Helper()
	sc, ok := m.scoreStore.Get(pid)
	if !ok {
		return 0
	}
	return sc.OpStats[string(agenttask.OpSubmit)].Failures
}

func TestTrack144MaxCostForResolvesMin(t *testing.T) {
	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track144CallerEcon("5"),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track144EconCfg("0.005", "0.01"),
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB,
		nilUsageRun(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("ok"), nil
		}))
	waitEconAdvert(t, meshA, meshB)

	advPid := meshB.node.ID()

	// Flag vs config: the tighter of the two always wins.
	assert.Equal(t, "4", meshA.MaxCostFor(advPid, "4"), "flag tighter than config")
	assert.Equal(t, "5", meshA.MaxCostFor(advPid, "9"), "config tighter than flag")
	assert.Equal(t, "5", meshA.MaxCostFor(advPid, ""), "config alone")
	assert.Equal(t, "5", meshA.MaxCostFor(advPid, "junk"),
		"unparseable flag treated as unset (entry points validate earlier)")

	// A peer without an economy advert can never be price-bound.
	freePid := testutil.NewIdentity(t).PeerID
	meshA.SetCapability(peer.ID(freePid), Capability{
		PeerID: freePid, Agents: []string{"main"}, Allows: map[string]bool{"spawn": true},
	})
	assert.Equal(t, "", meshA.MaxCostFor(peer.ID(freePid), "4"))

	// Negotiated terms carry the resolved cap verbatim.
	terms := meshA.econTermsFor(advPid, "4")
	require.NotNil(t, terms)
	assert.Equal(t, "4", terms.MaxCharge)
	terms = meshA.econTermsFor(advPid, "9")
	require.NotNil(t, terms)
	assert.Equal(t, "5", terms.MaxCharge)
	assert.Nil(t, meshA.econTermsFor(peer.ID(freePid), "4"))

	// meshB has its own economy enabled but is the seller here; disable the
	// caller-side gate by pointing a fresh check at meshB's view of A — A's
	// advert exists (its own economy emits one), yet meshB resolves nothing
	// because MaxCostFor keys off the *caller's* enabled flag.
	// Simpler invariant: a mesh with economy disabled never resolves a cap.
	meshC := addEconPeerNode(t, meshA, nil)
	assert.Equal(t, "", meshC.MaxCostFor(meshA.node.ID(), "4"),
		"caller economy disabled → no cap resolves")
}

func TestTrack144RankedCandidatesCostFilter(t *testing.T) {
	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		TaskFailover:     true,
		Economy:          track144CallerEcon("5"),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track144EconCfg("6", "10"), // min_charge 6 > cap 5
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB,
		nilUsageRun(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("ok from " + req.CorrelationID), nil
		}))
	waitForTaskProtocol(t, meshA, meshB)
	waitEconAdvert(t, meshA, meshB)

	cheap := addEconPeerNode(t, meshA, ptr(track144EconCfg("0.005", "0.01")))
	free := addEconPeerNode(t, meshA, nil)

	pidB := meshB.node.ID()
	hasPeer := func(list []RankedPeer, pid peer.ID) bool {
		for _, c := range list {
			if c.PID == pid {
				return true
			}
		}
		return false
	}

	// Config cap alone filters the over-floor candidate.
	cands := meshA.PickPeerRankedWithCost("main", "spawn", Requirements{}, nil, "")
	assert.False(t, hasPeer(cands, pidB), "min_charge 6 exceeds cap 5")
	assert.True(t, hasPeer(cands, cheap.node.ID()))
	assert.True(t, hasPeer(cands, free.node.ID()))

	// The flag can only tighten: a looser flag does not rescue B.
	cands = meshA.PickPeerRankedWithCost("main", "spawn", Requirements{}, nil, "100")
	assert.False(t, hasPeer(cands, pidB), "flag 100 vs config 5 → still capped at 5")

	// A tighter flag drops the cheap-but-over peer too.
	cands = meshA.PickPeerRankedWithCost("main", "spawn", Requirements{}, nil, "0.004")
	assert.False(t, hasPeer(cands, pidB))
	assert.False(t, hasPeer(cands, cheap.node.ID()), "min_charge 0.005 exceeds cap 0.004")
	assert.True(t, hasPeer(cands, free.node.ID()), "free peers are never filtered")
}

func TestTrack144CostRankBonusOrdering(t *testing.T) {
	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		TaskFailover:     true,
		Economy:          track144CallerEcon(""), // uncapped — pure ranking
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track144EconCfg("1", "2"), // est = max(1, 2) = 2
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB,
		nilUsageRun(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("ok"), nil
		}))
	waitForTaskProtocol(t, meshA, meshB)
	waitEconAdvert(t, meshA, meshB)

	pricy := addEconPeerNode(t, meshA, ptr(track144EconCfg("1", "8"))) // est 8
	free := addEconPeerNode(t, meshA, nil)                             // cost zero

	cands := meshA.PickPeerRankedWithCost("main", "spawn", Requirements{}, nil, "")
	require.Len(t, cands, 3)

	score := map[peer.ID]int64{}
	for _, c := range cands {
		score[c.PID] = c.Score
	}
	// Cheapest priced peer (est 2) and the free peer get the full bonus;
	// the pricey peer (est 8) gets 256·2/8 = 64.
	const base = int64(1 << 20) // connected; no role bonus without RoleAware
	assert.Equal(t, base+costRankBonusMax, score[meshB.node.ID()], "cheapest priced peer")
	assert.Equal(t, base+costRankBonusMax, score[free.node.ID()], "free peer = cost zero")
	assert.Equal(t, base+64, score[pricy.node.ID()], "4x price → quarter bonus")

	// Ordering: both cheapest-priced and free outrank the pricey peer.
	assert.Equal(t, pricy.node.ID(), cands[2].PID, "pricey peer ranks last")
	assert.NotEqual(t, pricy.node.ID(), cands[0].PID)
}

func TestTrack144MaxChargeCarriesResolvedCap(t *testing.T) {
	ctx := context.Background()
	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		TaskFailover:     false, // surface the preferred peer's rejection directly
		TaskRetries:      1,
		Economy:          track144CallerEcon("5"),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track144EconCfg("6", "0.01"), // above any cap in play
	}

	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB,
		nilUsageRun(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("ok"), nil
		}))
	waitForTaskProtocol(t, meshA, meshB)
	waitEconAdvert(t, meshA, meshB)

	for _, tc := range []struct {
		name    string
		maxCost string
		wantCap string
	}{
		{"flag tightens config", "4", "max_charge 4"},
		{"config tightens flag", "9", "max_charge 5"},
		{"config alone", "", "max_charge 5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
				TargetAgentID: "main",
				SystemPrompt:  "capped task",
				Async:         true,
				MaxCost:       tc.maxCost,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), econ.RejectPriceFloor)
			assert.Contains(t, err.Error(), tc.wantCap,
				"callee observed the min(flag, config) cap on the wire")
		})
	}
}

func TestTrack144PriceFloorPreFilterAndSteering(t *testing.T) {
	ctx := context.Background()
	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		TaskFailover:     true,
		TaskRetries:      3, // prove the rejection skips same-peer retries
		Economy:          track144CallerEcon("5"),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track144EconCfg("6", "0.01"), // over the caller cap
	}

	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB,
		nilUsageRun(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("ok"), nil
		}))
	waitForTaskProtocol(t, meshA, meshB)
	waitEconAdvert(t, meshA, meshB)

	free := addEconPeerNode(t, meshA, nil)
	pidB := meshB.node.ID()

	// (a) No preferred peer: B is filtered before dispatch — it must not
	// be sent a request at all, so no submit failure is recorded for it.
	usedPeer, taskID, err := meshA.SubmitRemoteTaskWithPeer(ctx, "", RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "filtered task",
		Async:         true,
	})
	require.NoError(t, err)
	assert.Equal(t, free.node.ID(), usedPeer)
	assert.NotEmpty(t, taskID)
	assert.Equal(t, 0, submitFailures(t, meshA, pidB),
		"over-cap peer filtered pre-send — zero submit attempts recorded")

	// (b) Explicit preferred peer: never filtered, but the wire cap binds —
	// B rejects with price_floor and the failover ladder continues to C.
	// TaskRetries=3 with a single failure recorded proves the same-peer
	// retry loop was skipped.
	usedPeer, taskID, err = meshA.SubmitRemoteTaskWithPeer(ctx, pidB, RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "explicit task",
		Async:         true,
		MaxCost:       "4",
	})
	require.NoError(t, err)
	assert.Equal(t, free.node.ID(), usedPeer)
	assert.NotEmpty(t, taskID)
	assert.Equal(t, 1, submitFailures(t, meshA, pidB),
		"price_floor rejection tried the peer once, then moved on")
}

func TestTrack144FanoutMaxCostFiltersBranches(t *testing.T) {
	ctx := context.Background()
	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		TaskFailover:     true,
		Economy:          track144CallerEcon(""),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track144EconCfg("6", "10"),
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB,
		nilUsageRun(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("ok"), nil
		}))
	waitForTaskProtocol(t, meshA, meshB)
	waitEconAdvert(t, meshA, meshB)

	cheap := addEconPeerNode(t, meshA, ptr(track144EconCfg("0.005", "0.01")))
	free := addEconPeerNode(t, meshA, nil)

	res, err := meshA.FanoutTask(ctx, FanoutRequest{
		AgentID: "main",
		Task:    "fan me out",
		MaxCost: "5",
	})
	require.NoError(t, err)
	require.Len(t, res.Branches, 2, "over-cap peer never becomes a branch")
	for _, b := range res.Branches {
		assert.NotEqual(t, meshB.node.ID().String(), b.PeerID)
		assert.True(t,
			b.PeerID == cheap.node.ID().String() || b.PeerID == free.node.ID().String())
	}
	assert.Equal(t, 0, submitFailures(t, meshA, meshB.node.ID()),
		"filtered candidate received no submit")
}

func TestTrack144InvalidMaxCostRejected(t *testing.T) {
	ctx := context.Background()
	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track144CallerEcon("5"),
	}
	meshA, meshB := newEconTestMeshes(t, cfgA,
		config.MeshConfig{Enabled: true, AllowRemoteSpawn: true, RemoteTimeout: 30 * time.Second},
		nilUsageRun(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
			return toolshared.NewToolResult("ok"), nil
		}))

	call := RemoteCall{TargetAgentID: "main", SystemPrompt: "x", MaxCost: "abc"}
	_, err := meshA.CallRemote(ctx, meshB.node.ID(), call)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "invalid max_cost"))

	_, _, err = meshA.SubmitRemoteTaskWithPeer(ctx, meshB.node.ID(), call)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "invalid max_cost"))

	_, err = meshA.FanoutTask(ctx, FanoutRequest{
		AgentID: "main", Task: "x", MaxCost: "1.2.3",
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "invalid max_cost"))
}

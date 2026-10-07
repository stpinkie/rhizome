package mesh

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// newEconTestMeshes wires two connected, mutually-trusted meshes with
// independent configs. runB is the callee-side runner (full usage-reporting
// signature); meshA runs a nil-usage stub. When calleeEcon is enabled the
// callee's bill_peers is populated with meshA's peer id automatically.
func newEconTestMeshes(
	t *testing.T,
	cfgA, cfgB config.MeshConfig,
	runB func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error),
) (*Mesh, *Mesh) {
	t.Helper()
	// Keep the peer-adverts journal (written on every capability announce)
	// inside a scratch home — never the operator's real RHIZOME_HOME.
	t.Setenv("RHIZOME_HOME", t.TempDir())
	ctx := context.Background()

	idA := testutil.NewIdentity(t)
	idB := testutil.NewIdentity(t)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeA.Close() })

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{addrsA[0]},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeB.Close() })

	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond, "nodeB should connect to nodeA")

	// Billing is scoped by explicit peer allowlist: the callee charges the
	// fixture's caller when economy is enabled.
	if cfgB.Economy.Enabled && len(cfgB.Economy.BillPeers) == 0 {
		cfgB.Economy.BillPeers = []string{nodeA.ID().String()}
	}

	stubRun := func(context.Context, agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		return toolshared.NewToolResult("caller-side"), nil, nil
	}

	meshA := NewMesh(nodeA, nil, idA, cfgA, stubRun)
	require.NoError(t, meshA.Start(ctx))
	t.Cleanup(func() { _ = meshA.Stop() })

	meshB := NewMesh(nodeB, nil, idB, cfgB, runB)
	require.NoError(t, meshB.Start(ctx))
	t.Cleanup(func() { _ = meshB.Stop() })

	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	return meshA, meshB
}

// track139CallerEcon is the buyer-side config: economy enabled, willing to
// pay up to 5 credits per task in the callee's unit.
func track139CallerEcon() config.MeshEconomyConfig {
	return config.MeshEconomyConfig{
		Enabled:        true,
		Unit:           "credits",
		AcceptUnits:    []string{"credits"},
		MaxCostPerTask: "5",
	}
}

// track139CalleeEcon is the seller-side sheet: 0.01/task + 0.002 per 1k
// prompt tokens, floor 0.005, settling in credits.
func track139CalleeEcon() config.MeshEconomyConfig {
	return config.MeshEconomyConfig{
		Enabled: true,
		Unit:    "credits",
		PriceSheet: config.EconPriceSheet{
			PerTask:           "0.01",
			Per1KPromptTokens: "0.002",
			MinCharge:         "0.005",
		},
		AcceptUnits: []string{"credits"},
	}
}

// discoverEconAdvert pulls the callee's real manifest so the caller's stored
// capability carries the economy advert — negotiation keys off it.
func discoverEconAdvert(t *testing.T, caller, callee *Mesh) {
	t.Helper()
	_, err := caller.TrustAndDiscover(context.Background(), callee.node.ID())
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		stored, ok := caller.PeerCapabilities(callee.node.ID())
		return ok && stored.Economy != nil && stored.Economy.Unit == "credits"
	}, 10*time.Second, 100*time.Millisecond, "callee economy advert must propagate")
}

func TestTrack139TaskChargeEndToEnd(t *testing.T) {
	ctx := context.Background()

	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CallerEcon(),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CalleeEcon(),
	}
	runB := func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		return toolshared.NewToolResult("billed result"), &toolshared.RemoteUsage{
			LLMCalls:         1,
			PromptTokens:     1000,
			CompletionTokens: 100,
			TotalTokens:      1100,
			DurationMS:       250,
		}, nil
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, runB)
	waitForTaskProtocol(t, meshA, meshB)
	discoverEconAdvert(t, meshA, meshB)

	taskID, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "billable task",
	})
	require.NoError(t, err)
	require.NotEmpty(t, taskID)

	resp, err := meshA.RemoteTaskResult(ctx, meshB.node.ID(), taskID, 15*time.Second)
	require.NoError(t, err)
	require.Equal(t, agenttask.StatusDone, resp.Status)

	// sheet·usage = 0.01 + 0.002 = 0.012; above the 0.005 floor, under the cap.
	require.NotNil(t, resp.Charge, "billed task must return a signed charge")
	assert.Equal(t, "credits", resp.Charge.Unit)
	assert.Equal(t, "0.012", resp.Charge.Amount)
	assert.Equal(t, econ.SheetDigest(cfgB.Economy.Advert()), resp.Charge.SheetDigest)
	require.NotNil(t, resp.Charge.Usage, "receipt carries metered usage")
	assert.Equal(t, 1000, resp.Charge.Usage.PromptTokens)
	assert.False(t, resp.Charge.Truncated)

	// The callee's task record persisted both the negotiated terms and the
	// charge; list responses surface it.
	stored, ok := meshB.tasks.getOwned(taskID, meshA.node.ID())
	require.True(t, ok)
	require.NotNil(t, stored.Econ)
	assert.Equal(t, "credits", stored.Econ.Unit)
	require.NotNil(t, stored.Charge)
	assert.Equal(t, "0.012", stored.Charge.Amount)

	listed, err := meshA.ListRemoteTasks(ctx, meshB.node.ID())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.NotNil(t, listed[0].Charge)
	assert.Equal(t, "0.012", listed[0].Charge.Amount)
}

func TestTrack139FailedRunEmitsNoCharge(t *testing.T) {
	ctx := context.Background()

	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CallerEcon(),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CalleeEcon(),
	}
	runB := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		return nil, nil, context.DeadlineExceeded // billed work that fails is never charged
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, runB)
	waitForTaskProtocol(t, meshA, meshB)
	discoverEconAdvert(t, meshA, meshB)

	taskID, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "failing task",
	})
	require.NoError(t, err)

	resp, err := meshA.RemoteTaskResult(ctx, meshB.node.ID(), taskID, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, agenttask.StatusError, resp.Status)
	assert.Nil(t, resp.Charge, "failed work is never billed")

	stored, ok := meshB.tasks.getOwned(taskID, meshA.node.ID())
	require.True(t, ok)
	assert.Nil(t, stored.Charge)
	assert.NotNil(t, stored.Econ, "terms still record on the billed task")
}

func TestTrack139SyncNegotiatesEcon(t *testing.T) {
	ctx := context.Background()

	var gotReq agentrpc.Request
	cfgA := config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		RemoteTimeout:       30 * time.Second,
		Economy:             track139CallerEcon(),
	}
	cfgB := config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		RemoteTimeout:       30 * time.Second,
		Economy:             track139CalleeEcon(),
	}
	runB := func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		gotReq = req
		return toolshared.NewToolResult("sync billed"), &toolshared.RemoteUsage{
			LLMCalls: 1, PromptTokens: 2000, TotalTokens: 2000, DurationMS: 100,
		}, nil
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, runB)
	discoverEconAdvert(t, meshA, meshB)

	result, err := meshA.CallRemote(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "sync billable",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "sync billed", result.ForLLM)

	// The callee observed the negotiated terms inside the signed request.
	require.NotNil(t, gotReq.Econ, "negotiated request must carry econ terms")
	assert.True(t, gotReq.Econ.Accept)
	assert.Equal(t, "credits", gotReq.Econ.Unit)
	assert.Equal(t, "5", gotReq.Econ.MaxCharge)
	// Billing forces metering regardless of want_usage.
	assert.True(t, gotReq.WantUsage, "billed run must be metered")
}

func TestTrack139HandleRequestCharge(t *testing.T) {
	cfgB := config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		RemoteTimeout:       30 * time.Second,
		Economy:             track139CalleeEcon(),
	}
	f := newSecurityMeshFixture(t, cfgB)
	f.meshB.cfg.Economy.BillPeers = []string{f.nodeA.ID().String()}

	req := f.signedRequest(t, "econ-c1", "econ-n1", time.Now().Unix())
	req.Econ = &econ.Terms{Accept: true, Unit: "credits", MaxCharge: "5"}
	// Re-sign: the terms must be inside the signed payload.
	req.Signature = nil
	payload, err := json.Marshal(req)
	require.NoError(t, err)
	req.Signature = identity.Sign(f.idA.PrivateKey, payload)

	resp, err := f.meshB.HandleRequest(f.nodeA.ID(), req)
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Status)
	require.NotNil(t, resp.Charge, "billed caller gets the signed charge")
	assert.Equal(t, "credits", resp.Charge.Unit)
	assert.Equal(t, econ.SheetDigest(cfgB.Economy.Advert()), resp.Charge.SheetDigest)
	assert.NotEmpty(t, resp.Charge.Amount)
}

func TestTrack139BilledCallerOmitsEconRejected(t *testing.T) {
	ctx := context.Background()

	// Caller's economy is off → it sends no terms; the callee bills this
	// peer → unaccepted billing is refused, not run free.
	cfgA := config.MeshConfig{Enabled: true, AllowRemoteSpawn: true, RemoteTimeout: 30 * time.Second}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CalleeEcon(),
	}
	runB := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		return toolshared.NewToolResult("nope"), nil, nil
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, runB)
	waitForTaskProtocol(t, meshA, meshB)

	_, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "free ride attempt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), econ.RejectEcon)
}

func TestTrack139PriceFloorRejected(t *testing.T) {
	ctx := context.Background()

	caller := track139CallerEcon()
	caller.MaxCostPerTask = "0.001" // below the callee's 0.005 floor

	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          caller,
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CalleeEcon(),
	}
	runB := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		return toolshared.NewToolResult("nope"), nil, nil
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, runB)
	waitForTaskProtocol(t, meshA, meshB)
	discoverEconAdvert(t, meshA, meshB)

	_, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "under-floor task",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), econ.RejectPriceFloor)
}

func TestTrack139UnacceptedUnitRejected(t *testing.T) {
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CalleeEcon(),
	}
	f := newSecurityMeshFixture(t, cfgB)
	f.meshB.cfg.Economy.BillPeers = []string{f.nodeA.ID().String()}

	req := agenttask.Request{
		Op:            agenttask.OpSubmit,
		CorrelationID: "bad-unit",
		TargetAgentID: "main",
		SystemPrompt:  "wrong unit",
		Econ:          &econ.Terms{Accept: true, Unit: "rubles"},
	}
	require.NoError(t, f.meshA.signTaskRequest(&req))

	resp := f.meshB.HandleTaskRequest(f.nodeA.ID(), req)
	assert.Equal(t, agenttask.StatusRejected, resp.Status)
	assert.Contains(t, resp.Error, econ.RejectEcon)
	assert.Contains(t, resp.Error, "not accepted")
}

func TestTrack139UnbilledCallerEconInert(t *testing.T) {
	ctx := context.Background()

	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CallerEcon(),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CalleeEcon(),
	}
	runB := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		return toolshared.NewToolResult("unbilled"), nil, nil
	}
	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, runB)
	waitForTaskProtocol(t, meshA, meshB)
	discoverEconAdvert(t, meshA, meshB)

	// Caller not on the callee's bill_peers: the submitted terms are
	// inert — the task runs unbilled and no charge is emitted.
	meshB.cfg.Economy.BillPeers = nil

	taskID, err := meshA.SubmitRemoteTask(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "unbilled task",
	})
	require.NoError(t, err)

	resp, err := meshA.RemoteTaskResult(ctx, meshB.node.ID(), taskID, 15*time.Second)
	require.NoError(t, err)
	assert.Equal(t, agenttask.StatusDone, resp.Status)
	assert.Nil(t, resp.Charge, "unbilled caller must not be charged")

	stored, ok := meshB.tasks.getOwned(taskID, meshA.node.ID())
	require.True(t, ok)
	assert.Nil(t, stored.Econ, "unbilled task must not record terms")
	assert.Nil(t, stored.Charge)
}

func TestTrack139ChargeVerification(t *testing.T) {
	// B really advertises economy so its periodic announce does not clobber
	// the planted capability with a no-economy manifest.
	calleeEcon := track139CalleeEcon()
	cfgB := config.MeshConfig{Enabled: true, RemoteTimeout: 30 * time.Second, Economy: calleeEcon}
	t.Setenv("RHIZOME_HOME", t.TempDir())
	f := newSecurityMeshFixture(t, cfgB)

	adv := calleeEcon.Advert()
	f.meshA.SetCapability(f.nodeB.ID(), Capability{
		PeerID:  f.nodeB.PeerID(),
		Economy: adv,
	})

	goodUsage := &toolshared.RemoteUsage{PromptTokens: 1000, TotalTokens: 1000, DurationMS: 10}
	amount, truncated, err := econ.Compute(adv.PriceSheet, goodUsage, "")
	require.NoError(t, err)
	assert.False(t, truncated)
	good := &econ.Charge{
		Unit: "credits", Amount: amount, Usage: goodUsage,
		SheetDigest: econ.SheetDigest(adv),
	}

	t.Run("digest tamper", func(t *testing.T) {
		bad := *good
		bad.SheetDigest = "deadbeef"
		err := f.meshA.verifyCharge(f.nodeB.ID(), &econ.Terms{Accept: true, Unit: "credits"}, &bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), econ.MismatchCharge)
	})

	t.Run("inflated amount", func(t *testing.T) {
		bad := *good
		bad.Amount = "99"
		err := f.meshA.verifyCharge(f.nodeB.ID(), &econ.Terms{Accept: true, Unit: "credits"}, &bad)
		require.Error(t, err)
		assert.Contains(t, err.Error(), econ.MismatchCharge)
	})

	t.Run("unsolicited on unnegotiated call", func(t *testing.T) {
		err := f.meshA.verifyCharge(f.nodeB.ID(), nil, good)
		require.Error(t, err)
		assert.Contains(t, err.Error(), econ.MismatchCharge)
	})

	t.Run("unsolicited on journaled task", func(t *testing.T) {
		f.meshA.recordEconSent("task-no-econ", nil)
		err := f.meshA.verifyTaskCharge(f.nodeB.ID(), "task-no-econ", good)
		require.Error(t, err)
		assert.Contains(t, err.Error(), econ.MismatchCharge)
	})

	t.Run("unit disagrees with sent terms", func(t *testing.T) {
		f.meshA.recordEconSent("task-econ", &econ.Terms{Accept: true, Unit: "other"})
		err := f.meshA.verifyTaskCharge(f.nodeB.ID(), "task-econ", good)
		require.Error(t, err)
		assert.Contains(t, err.Error(), econ.MismatchCharge)
	})

	t.Run("honest charge verifies", func(t *testing.T) {
		require.NoError(t, econ.VerifyCharge(adv, good))
		f.meshA.recordEconSent("task-good", &econ.Terms{Accept: true, Unit: "credits"})
		require.NoError(t, f.meshA.verifyTaskCharge(f.nodeB.ID(), "task-good", good))
	})
}

func TestTrack139EconRejectionFailsOver(t *testing.T) {
	ctx := context.Background()

	runFunc := func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, *toolshared.RemoteUsage, error) {
		return toolshared.NewToolResult("ok from " + req.CorrelationID), nil, nil
	}
	cfgA := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		TaskFailover:     true,
		TaskRetries:      1,
		Economy:          track139CallerEcon(),
	}
	cfgB := config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
		Economy:          track139CalleeEcon(),
	}
	cfgB.Economy.PriceSheet.MinCharge = "6" // caller's max_cost_per_task is 5 → price_floor:

	meshA, meshB := newEconTestMeshes(t, cfgA, cfgB, runFunc)
	waitForTaskProtocol(t, meshA, meshB)

	// Third node C: no economy, never billed — the failover target.
	idC := testutil.NewIdentity(t)
	nodeC, err := network.NewNode(ctx, idC.Libp2pPrivKey, network.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeC.Close() })
	meshC := NewMesh(nodeC, nil, idC, config.MeshConfig{
		Enabled:          true,
		AllowRemoteSpawn: true,
		RemoteTimeout:    30 * time.Second,
	}, nilUsageRun(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("ok from " + req.CorrelationID), nil
	}))
	require.NoError(t, meshC.Start(ctx))
	t.Cleanup(func() { _ = meshC.Stop() })

	addrsC := nodeC.BootstrapAddrs()
	require.NotEmpty(t, addrsC)
	require.NoError(t, meshA.Connect(ctx, addrsC[0]))
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(meshA.node.Connectedness(nodeC.ID()))
	}, 10*time.Second, 50*time.Millisecond)
	meshA.TrustPeer(nodeC.ID())
	meshC.TrustPeer(meshA.node.ID())

	require.Eventually(t, func() bool {
		protos, _ := meshA.host.Peerstore().SupportsProtocols(nodeC.ID(), agenttask.ProtocolID)
		return len(protos) > 0
	}, 60*time.Second, 100*time.Millisecond, "task protocol should be advertised by C")

	// Plant capability entries: B advertises economy (so A negotiates and
	// gets price_floor-rejected); C advertises no economy.
	meshA.SetCapability(meshB.node.ID(), Capability{
		PeerID: meshB.node.PeerID(), Agents: []string{"main"},
		Allows: map[string]bool{"spawn": true}, Economy: cfgB.Economy.Advert(),
	})
	meshA.SetCapability(nodeC.ID(), Capability{
		PeerID: nodeC.PeerID(), Agents: []string{"main"},
		Allows: map[string]bool{"spawn": true},
	})

	usedPeer, taskID, err := meshA.SubmitRemoteTaskWithPeer(ctx, meshB.node.ID(), RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "failover task",
		Async:         true,
	})
	require.NoError(t, err)
	assert.Equal(t, nodeC.ID(), usedPeer,
		"price_floor rejection is pre-execution — safe to fail over")
	assert.NotEmpty(t, taskID)
}

// TestTrack139UnnegotiatedWireCompat pins byte-identical encodings when no
// economy fields are negotiated — the same fixture posture Track 94 set for
// want_usage/usage.
func TestTrack139UnnegotiatedWireCompat(t *testing.T) {
	type oldTaskRequest struct {
		Op            string `json:"op"`
		TaskID        string `json:"task_id,omitempty"`
		CorrelationID string `json:"correlation_id,omitempty"`
		TargetAgentID string `json:"target_agent_id,omitempty"`
		SystemPrompt  string `json:"system_prompt,omitempty"`
		Timeout       int64  `json:"timeout,omitempty"`
		Nonce         string `json:"nonce,omitempty"`
		Timestamp     int64  `json:"timestamp,omitempty"`
	}
	oldBytes, err := json.Marshal(oldTaskRequest{
		Op: "submit", CorrelationID: "c1", TargetAgentID: "main",
		SystemPrompt: "hi", Nonce: "n1", Timestamp: 7,
	})
	require.NoError(t, err)

	newBytes, err := json.Marshal(agenttask.Request{
		Op: agenttask.OpSubmit, CorrelationID: "c1", TargetAgentID: "main",
		SystemPrompt: "hi", Nonce: "n1", Timestamp: 7,
	})
	require.NoError(t, err)
	assert.Equal(t, string(oldBytes), string(newBytes),
		"unnegotiated task request must marshal byte-identical")

	var req agenttask.Request
	require.NoError(t, json.Unmarshal(oldBytes, &req))
	assert.Nil(t, req.Econ)

	// Synchronous protocol: same contract.
	type oldRPCRequest struct {
		CorrelationID string `json:"correlation_id"`
		TargetAgentID string `json:"target_agent_id"`
		SystemPrompt  string `json:"system_prompt"`
		Nonce         string `json:"nonce,omitempty"`
		Timestamp     int64  `json:"timestamp,omitempty"`
	}
	oldRPC, err := json.Marshal(oldRPCRequest{
		CorrelationID: "c1", TargetAgentID: "main", SystemPrompt: "hi",
		Nonce: "n1", Timestamp: 7,
	})
	require.NoError(t, err)
	newRPC, err := json.Marshal(agentrpc.Request{
		CorrelationID: "c1", TargetAgentID: "main", SystemPrompt: "hi",
		Nonce: "n1", Timestamp: 7,
	})
	require.NoError(t, err)
	assert.Equal(t, string(oldRPC), string(newRPC),
		"unnegotiated rpc request must marshal byte-identical")

	// Responses without a charge emit no charge key.
	for _, raw := range []string{
		mustMarshalT139(t, agenttask.Response{TaskID: "t1", Status: agenttask.StatusDone}),
		mustMarshalT139(t, agentrpc.Response{CorrelationID: "c1", Status: "ok"}),
		mustMarshalT139(t, agenttask.TaskInfo{TaskID: "t1", Status: agenttask.StatusDone}),
	} {
		assert.NotContains(t, raw, `"charge"`)
		assert.NotContains(t, raw, `"econ"`)
	}
}

func mustMarshalT139(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestTrack139StoreRoundTrip pins Econ+Charge persistence through the
// task-store journal.
func TestTrack139StoreRoundTrip(t *testing.T) {
	path := t.TempDir() + "/mesh-tasks.jsonl"
	s := NewTaskStoreWithPath(path)

	owner, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	task, _, err := s.Submit(owner, agenttask.Request{
		CorrelationID: "econ-1", TargetAgentID: "main",
		Econ: &econ.Terms{Accept: true, Unit: "credits", MaxCharge: "5"},
	})
	require.NoError(t, err)
	s.Start(task.ID, func() {})
	s.Finish(task.ID, agenttask.StatusDone, toolshared.NewToolResult("ok"), "", nil,
		&econ.Charge{Unit: "credits", Amount: "0.012", SheetDigest: "abc123"})
	s.flushSave()

	s2 := NewTaskStoreWithPath(path)
	_, err = s2.Load()
	require.NoError(t, err)

	loaded, ok := s2.getOwned(task.ID, owner)
	require.True(t, ok)
	require.NotNil(t, loaded.Econ, "terms must survive reload")
	assert.Equal(t, "credits", loaded.Econ.Unit)
	assert.Equal(t, "5", loaded.Econ.MaxCharge)
	require.NotNil(t, loaded.Charge, "charge must survive reload")
	assert.Equal(t, "0.012", loaded.Charge.Amount)
	assert.Equal(t, "abc123", loaded.Charge.SheetDigest)
}

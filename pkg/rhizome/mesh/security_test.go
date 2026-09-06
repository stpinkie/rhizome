package mesh

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// securityMeshFixture wires two connected, mutually-trusted mesh nodes.
type securityMeshFixture struct {
	meshA, meshB *Mesh
	nodeA, nodeB *network.Node
	idA          *identity.Derived
	ranB         chan struct{}
}

func newSecurityMeshFixture(t *testing.T, cfgB config.MeshConfig) *securityMeshFixture {
	t.Helper()
	ctx := context.Background()

	idA, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 10)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 11)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	require.NoError(t, err)
	t.Cleanup(func() { nodeA.Close() })

	addrsA := nodeA.BootstrapAddrs()
	require.NotEmpty(t, addrsA)

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{addrsA[0]},
	})
	require.NoError(t, err)
	t.Cleanup(func() { nodeB.Close() })

	// Wait for the bootstrap connection to come up.
	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond, "nodeB should connect to nodeA")

	ran := make(chan struct{}, 8)
	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		ran <- struct{}{}
		return toolshared.NewToolResult("ok"), nil
	}

	meshA := NewMesh(nodeA, nil, idA, config.MeshConfig{Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second}, runFunc)
	require.NoError(t, meshA.Start(ctx))
	t.Cleanup(func() { meshA.Stop() })

	meshB := NewMesh(nodeB, nil, idB, cfgB, runFunc)
	require.NoError(t, meshB.Start(ctx))
	t.Cleanup(func() { meshB.Stop() })

	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	return &securityMeshFixture{meshA: meshA, meshB: meshB, nodeA: nodeA, nodeB: nodeB, idA: idA, ranB: ran}
}

// signedRequest builds and signs an agentrpc request as peer A would.
func (f *securityMeshFixture) signedRequest(t *testing.T, corrID, nonce string, ts int64) agentrpc.Request {
	t.Helper()
	req := agentrpc.Request{
		CorrelationID: corrID,
		TargetAgentID: "main",
		SystemPrompt:  "hi",
		Nonce:         nonce,
		Timestamp:     ts,
	}
	payload, err := json.Marshal(req)
	require.NoError(t, err)
	req.Signature = identity.Sign(f.idA.PrivateKey, payload)
	return req
}

func TestMeshReplayedNonceRejected(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second,
	})
	ctx := context.Background()

	now := time.Now().Unix()
	// Same nonce, different correlation ids → both reach the handler.
	first := f.signedRequest(t, newCorrelationID(), "nonce-1", now)
	resp, err := f.meshA.rpc.Call(ctx, f.nodeB.ID(), first)
	require.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)

	replay := f.signedRequest(t, newCorrelationID(), "nonce-1", now)
	resp, err = f.meshA.rpc.Call(ctx, f.nodeB.ID(), replay)
	require.NoError(t, err)
	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Error, "replay")
}

func TestMeshStaleTimestampRejected(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second,
	})
	ctx := context.Background()

	stale := f.signedRequest(t, newCorrelationID(), "nonce-stale", time.Now().Add(-time.Hour).Unix())
	resp, err := f.meshA.rpc.Call(ctx, f.nodeB.ID(), stale)
	require.NoError(t, err)
	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Error, "skew")
}

func TestMeshLegacyRequestWithoutNonceRejected(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second,
	})
	f.meshB.SetAuditPath(auditPath)

	ctx := context.Background()
	// Legacy-style request: signed but no nonce/timestamp → rejected.
	req := agentrpc.Request{
		CorrelationID: newCorrelationID(),
		TargetAgentID: "main",
		SystemPrompt:  "hi",
	}
	payload, err := json.Marshal(req)
	require.NoError(t, err)
	req.Signature = identity.Sign(f.idA.PrivateKey, payload)

	resp, err := f.meshA.rpc.Call(ctx, f.nodeB.ID(), req)
	require.NoError(t, err)
	assert.Equal(t, "error", resp.Status)
	assert.Contains(t, resp.Error, "nonce/timestamp")

	data, err := os.ReadFile(auditPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "rejected")
}

func TestMeshACLDeniesAgent(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second,
		ACL: []config.MeshACLRule{{
			PeerID: "", // filled below
			Agents: []string{"allowed-agent"},
		}},
	})
	// Fill the peer id now that the fixture exists.
	f.meshB.cfg.ACL[0].PeerID = f.nodeA.ID().String()

	// Directly exercise the ACL helper for the denied agent and op.
	err := f.meshB.checkRemoteAllowed(f.nodeA.ID(), "delegate", "forbidden-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed to run agent")

	require.NoError(t, f.meshB.checkRemoteAllowed(f.nodeA.ID(), "delegate", "allowed-agent"))

	// Delegate disabled for the peer entirely.
	deny := false
	f.meshB.cfg.ACL[0] = config.MeshACLRule{PeerID: f.nodeA.ID().String(), AllowDelegate: &deny}
	err = f.meshB.checkRemoteAllowed(f.nodeA.ID(), "delegate", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
	// Spawn still falls back to the global flag (unset → denied).
	err = f.meshB.checkRemoteAllowed(f.nodeA.ID(), "spawn", "")
	require.Error(t, err)
}

func TestMeshRateLimited(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second,
		RateLimitPerPeer: 1, // 1 req/min, burst 1
	})
	ctx := context.Background()

	_, err := f.meshA.CallRemote(ctx, f.nodeB.ID(), RemoteCall{TargetAgentID: "main", SystemPrompt: "first"})
	require.NoError(t, err)

	_, err = f.meshA.CallRemote(ctx, f.nodeB.ID(), RemoteCall{TargetAgentID: "main", SystemPrompt: "second"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate_limited")
}

func TestMeshSpoofedCapabilityRejected(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})
	ctx := context.Background()

	// Capability claiming a different peer id is dropped.
	spoofed := Capability{PeerID: f.nodeA.PeerID(), Agents: []string{"evil"}, Timestamp: time.Now().Unix()}
	require.NoError(t, f.meshB.cap.Send(ctx, f.nodeA.ID(), spoofed))

	// Consistently absent over a short window.
	time.Sleep(700 * time.Millisecond)
	got, ok := f.meshA.PeerCapabilities(f.nodeB.ID())
	if ok {
		assert.NotEqual(t, []string{"evil"}, got.Agents)
	}
}

func TestMeshSignedCapabilityRoundTrip(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})

	capB := f.meshB.localCapability()
	require.NotEmpty(t, capB.Signature, "local capability should be signed")
	require.NoError(t, f.meshA.verifyCapability(f.nodeB.ID(), &capB))

	// Tampering invalidates the signature.
	tampered := capB
	tampered.Agents = []string{"backdoor"}
	require.Error(t, f.meshA.verifyCapability(f.nodeB.ID(), &tampered))

	// Unsigned manifests are accepted only when require_signed_caps is off;
	// the peer_id cannot be spoofed either way.
	unsigned := Capability{PeerID: f.nodeB.PeerID(), Timestamp: time.Now().Unix()}
	require.NoError(t, f.meshA.verifyCapability(f.nodeB.ID(), &unsigned))
	f.meshA.cfg.RequireSignedCaps = true
	require.Error(t, f.meshA.verifyCapability(f.nodeB.ID(), &unsigned))
	spoof := Capability{PeerID: f.nodeA.PeerID(), Timestamp: time.Now().Unix()}
	require.Error(t, f.meshA.verifyCapability(f.nodeB.ID(), &spoof))
}

func TestMeshAuditLogWritten(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "mesh-audit.jsonl")
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled: true, AllowRemoteDelegate: true, RemoteTimeout: 30 * time.Second,
	})
	f.meshB.SetAuditPath(auditPath)

	ctx := context.Background()
	_, err := f.meshA.CallRemote(ctx, f.nodeB.ID(), RemoteCall{TargetAgentID: "main", SystemPrompt: "audit me"})
	require.NoError(t, err)

	data, err := os.ReadFile(auditPath)
	require.NoError(t, err)

	var entry map[string]any
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	found := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["op"] == "delegate" && entry["status"] == "ok" {
			found = true
			assert.Equal(t, f.nodeA.ID().String(), entry["peer_id"])
			assert.Equal(t, "main", entry["agent_id"])
		}
	}
	assert.True(t, found, fmt.Sprintf("no ok delegate audit entry in %q", string(data)))
}

// writeAuditLines writes count JSONL entries of roughly entryBytes each to a
// temp file and returns its path. Each entry is valid JSON with an "op" and
// "seq" field so tests can verify ordering and count.
func writeAuditLines(t *testing.T, count, entryBytes int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mesh-audit.jsonl")

	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	w := bufio.NewWriter(f)
	for i := 0; i < count; i++ {
		// Build a valid JSON object, then pad with a "pad" field so each
		// line is approximately entryBytes long. This forces the audit tail
		// reader to grow its read window past the 512 KiB initial size.
		entry := map[string]any{
			"op":  "submit",
			"seq": i,
			"pad": strings.Repeat("x", entryBytes),
		}
		raw, err := json.Marshal(entry)
		require.NoError(t, err)
		_, err = w.Write(raw)
		require.NoError(t, err)
		require.NoError(t, w.WriteByte('\n'))
	}
	require.NoError(t, w.Flush())
	return path
}

func TestReadAuditTailGrowsWindow(t *testing.T) {
	// 800 entries of ~1.5 KiB each ≈ 1.2 MB, well over the 512 KiB initial
	// window. Requesting all 800 forces the reader to grow the window.
	const count = 800
	const entryBytes = 1500
	path := writeAuditLines(t, count, entryBytes)

	entries, err := ReadAuditTail(path, count)
	require.NoError(t, err)
	require.Len(t, entries, count, "should return all %d entries after growing the window", count)

	// Entries are newest-last; verify ordering and that the last entry is
	// the highest seq.
	var last map[string]any
	require.NoError(t, json.Unmarshal(entries[len(entries)-1], &last))
	assert.Equal(t, float64(count-1), last["seq"], "last entry should be the highest seq")

	var first map[string]any
	require.NoError(t, json.Unmarshal(entries[0], &first))
	assert.Equal(t, float64(0), first["seq"], "first entry should be seq 0")
}

func TestReadAuditTailSmallFile(t *testing.T) {
	// 3 small entries; window is larger than the file, no growth needed.
	path := writeAuditLines(t, 3, 0)

	entries, err := ReadAuditTail(path, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 3, "should return all 3 entries when requesting more than available")
}

func TestReadAuditTailMissingFile(t *testing.T) {
	entries, err := ReadAuditTail(filepath.Join(t.TempDir(), "does-not-exist.jsonl"), 10)
	require.NoError(t, err, "missing file should not be an error")
	assert.Nil(t, entries, "missing file should return nil entries")
}

func TestReadAuditTailEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	require.NoError(t, os.WriteFile(path, []byte{}, 0o600))

	entries, err := ReadAuditTail(path, 10)
	require.NoError(t, err)
	assert.Nil(t, entries, "empty file should return nil entries")
}

func TestReadAuditTailSkipsInvalidLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mesh-audit.jsonl")
	lines := []string{
		`{"op":"a","seq":0}`,
		`not-json`,
		`{"op":"b","seq":1}`,
		``,
		`{"op":"c","seq":2}`,
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))

	entries, err := ReadAuditTail(path, 10)
	require.NoError(t, err)
	require.Len(t, entries, 3, "should skip invalid and empty lines")
	var last map[string]any
	require.NoError(t, json.Unmarshal(entries[2], &last))
	assert.Equal(t, "c", last["op"])
}

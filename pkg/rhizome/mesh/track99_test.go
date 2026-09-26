package mesh

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentmanifest"
)

// TestTrack99ModuleAdvertsAnnounced verifies a serving node's adverts ride
// inside the signed manifest: localCapability emits ModuleAdverts +
// allows.market_serve, the manifest verifies at the receiving peer, and the
// stored capability carries the advert bytes.
func TestTrack99ModuleAdvertsAnnounced(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled:             true,
		AllowRemoteDelegate: true,
		RemoteTimeout:       30 * time.Second,
	})
	ctx := context.Background()

	advert := json.RawMessage(`{"offers":[{"id":"web-search","price_sheet":{"per_task":"0.01 USDC"}}],"payout":{"chain_id":8453,"asset":"USDC"}}`)
	f.meshB.SetModuleAdvertProvider(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"rhizome-market": advert}
	})

	capB := f.meshB.localCapability()
	require.Len(t, capB.ModuleAdverts, 1)
	assert.JSONEq(t, string(advert), string(capB.ModuleAdverts["rhizome-market"]))
	assert.True(t, capB.Allows["market_serve"], "serving manifest must set market_serve")
	require.NotEmpty(t, capB.Signature)

	// The signed manifest verifies at the receiving peer and adverts persist
	// in the stored capability.
	announced, err := f.meshA.TrustAndDiscover(ctx, f.nodeB.ID())
	require.NoError(t, err)
	require.Len(t, announced.ModuleAdverts, 1)
	assert.JSONEq(t, string(advert), string(announced.ModuleAdverts["rhizome-market"]))

	require.Eventually(t, func() bool {
		stored, ok := f.meshA.PeerCapabilities(f.nodeB.ID())
		if !ok {
			return false
		}
		return stored.Allows["market_serve"] && len(stored.ModuleAdverts) == 1
	}, 10*time.Second, 100*time.Millisecond, "stored capability must carry adverts + market_serve")
}

// TestTrack99NonServingManifestByteIdentical pins emit-when-set: without a
// provider (or an empty provider result) the manifest marshals identically
// to a build that predates the field — no module_adverts bytes on the wire.
func TestTrack99NonServingManifestByteIdentical(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})

	// No provider wired at all.
	c := f.meshB.localCapability()
	assert.Nil(t, c.ModuleAdverts)
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "module_adverts")

	// Provider wired but returning nothing (no serving modules).
	f.meshB.SetModuleAdvertProvider(func() map[string]json.RawMessage { return nil })
	c2 := f.meshB.localCapability()
	assert.Nil(t, c2.ModuleAdverts)
	assert.NotContains(t, c2.Allows, "market_serve")
	raw2, err := json.Marshal(c2)
	require.NoError(t, err)
	assert.NotContains(t, string(raw2), "module_adverts")

	// Round-trips verify exactly as pre-field manifests do.
	var decoded Capability
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NoError(t, f.meshA.verifyCapability(f.nodeB.ID(), &decoded))
}

// TestTrack99OldBuildDropFailsVerify pins the Role-class rejection: an old
// build lacks the ModuleAdverts field, so decoding a serving manifest and
// re-marshaling it (which is what verifyCapability does for the signature
// check) produces different bytes — the signature fails and the whole
// manifest is rejected. Unlike Allows map keys, struct fields do not
// survive old builds.
func TestTrack99OldBuildDropFailsVerify(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})

	f.meshB.SetModuleAdvertProvider(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"rhizome-market": json.RawMessage(`{"serving":true}`)}
	})
	capB := f.meshB.localCapability()
	require.NotEmpty(t, capB.ModuleAdverts)

	wire, err := json.Marshal(capB)
	require.NoError(t, err)

	// Mirrors Capability before ModuleAdverts existed.
	type oldCapability struct {
		PeerID          string                   `json:"peer_id"`
		Models          []string                 `json:"models,omitempty"`
		Skills          []string                 `json:"skills,omitempty"`
		Agents          []string                 `json:"agents,omitempty"`
		Timestamp       int64                    `json:"timestamp"`
		Allows          map[string]bool          `json:"allows,omitempty"`
		ActiveTasks     int                      `json:"active_tasks,omitempty"`
		ShareableSkills []string                 `json:"shareable_skills,omitempty"`
		AgentManifests  []agentmanifest.Manifest `json:"agent_manifests,omitempty"`
		Role            string                   `json:"role,omitempty"`
		Signature       []byte                   `json:"signature,omitempty"`
	}

	var oldCap oldCapability
	require.NoError(t, json.Unmarshal(wire, &oldCap))
	assert.True(t, oldCap.Allows["market_serve"], "map keys survive decode")
	oldBytes, err := json.Marshal(oldCap)
	require.NoError(t, err)
	assert.NotContains(t, string(oldBytes), "module_adverts", "old build drops the field")

	// The old build then ships those bytes to a peer: decoding back into the
	// current struct loses the adverts, and verification fails on signature.
	var decoded Capability
	require.NoError(t, json.Unmarshal(oldBytes, &decoded))
	require.Empty(t, decoded.ModuleAdverts)
	require.Error(t, f.meshA.verifyCapability(f.nodeB.ID(), &decoded),
		"field drop must fail signature verification")
}

// TestTrack99OversizedAdvertDropped verifies the receive-side bound: an
// oversized advert entry is dropped post-verification while the rest of the
// manifest (and any conforming adverts) still verifies and stores.
func TestTrack99OversizedAdvertDropped(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})

	small := json.RawMessage(`{"ok":true}`)
	fat := make([]byte, moduleAdvertMaxBytes+1)
	for i := range fat {
		fat[i] = 'x'
	}
	// {"pad":"xxx…"} — a valid-JSON object exceeding the 16 KiB bound.
	fatAdvert := json.RawMessage(append(append([]byte(`{"pad":"`), fat...), []byte(`"}`)...))

	capB := f.meshB.localCapability()
	capB.ModuleAdverts = map[string]json.RawMessage{
		"rhizome-market": small,
		"fat-module":     fatAdvert,
	}
	f.meshB.signCapability(&capB)

	wire, err := json.Marshal(capB)
	require.NoError(t, err)
	var decoded Capability
	require.NoError(t, json.Unmarshal(wire, &decoded))
	require.NoError(t, f.meshA.verifyCapability(f.nodeB.ID(), &decoded),
		"oversized adverts must drop, not fail the manifest")
	assert.Contains(t, decoded.ModuleAdverts, "rhizome-market")
	assert.NotContains(t, decoded.ModuleAdverts, "fat-module")
}

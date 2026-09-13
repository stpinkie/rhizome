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
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

// TestAgentManifestAdvertisedInCapability verifies that manifests built from
// the configured source are signed, embedded in the capability, delivered to
// a trusted peer intact, persisted via the sink, and surfaced in status.
func TestAgentManifestAdvertisedInCapability(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})
	ctx := context.Background()

	var sinked []agentmanifest.Manifest
	f.meshA.SetManifestSink(func(m agentmanifest.Manifest) {
		sinked = append(sinked, m)
	})
	f.meshA.SetAgentManifestSource(func() []agentmanifest.Input {
		return []agentmanifest.Input{{
			AgentID: "main",
			Name:    "Main",
			Persona: "coordinator agent",
			Models:  []string{"gpt-4"},
			Skills:  []string{"fs"},
		}}
	})

	announced, err := f.meshB.TrustAndDiscover(ctx, f.nodeA.ID())
	require.NoError(t, err)
	require.Len(t, announced.AgentManifests, 1)

	mf := announced.AgentManifests[0]
	assert.Equal(t, "main", mf.AgentID)
	assert.Equal(t, "Main", mf.Name)
	assert.Equal(t, "coordinator agent", mf.Persona)
	assert.Equal(t, f.nodeA.ID().String(), mf.PeerID)
	require.NoError(t, mf.Verify(f.idA.PublicKey))
	// AdvertiseModels/AdvertiseSkills are off in the fixture config, so the
	// manifest must not leak model or skill names.
	assert.Empty(t, mf.Models)
	assert.Empty(t, mf.Skills)

	// The sink fires once per capability build; every emitted manifest must be
	// signed and bound to node A.
	require.NotEmpty(t, sinked)
	for _, s := range sinked {
		assert.Equal(t, f.nodeA.ID().String(), s.PeerID)
		require.NoError(t, s.Verify(f.idA.PublicKey))
	}

	// The stored capability may be a later advertisement than the one
	// TrustAndDiscover returned (each build re-signs with a fresh IssuedAt),
	// so fingerprints can legitimately differ. What must hold is that the
	// stored manifest is present, authentic, and bound to node A.
	require.Eventually(t, func() bool {
		stored, ok := f.meshB.PeerCapabilities(f.nodeA.ID())
		if !ok || len(stored.AgentManifests) != 1 {
			return false
		}
		sm := stored.AgentManifests[0]
		return sm.AgentID == "main" &&
			sm.PeerID == f.nodeA.ID().String() &&
			sm.Verify(f.idA.PublicKey) == nil &&
			agentManifestIndex(stored)["main"] == sm.Fingerprint()
	}, 10*time.Second, 100*time.Millisecond, "stored manifest must verify and be bound to node A")
}

// TestAgentManifestForgeryDropped verifies that embedded manifests signed by
// the wrong key or bound to a different peer id are dropped while authentic
// manifests survive verification.
func TestAgentManifestForgeryDropped(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})

	idB, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 11)
	require.NoError(t, err)

	good := agentmanifest.Manifest{AgentID: "good", Name: "Good"}
	require.NoError(t, good.Sign(f.nodeA.ID().String(), f.idA.PrivateKey))

	// Claims to be A's agent but signed with B's key.
	forgedSig := agentmanifest.Manifest{AgentID: "evil", Name: "Evil"}
	require.NoError(t, forgedSig.Sign(f.nodeA.ID().String(), idB.PrivateKey))

	// Signed by A but bound to B's peer id.
	wrongPeer := agentmanifest.Manifest{AgentID: "liar"}
	require.NoError(t, wrongPeer.Sign(f.nodeB.ID().String(), f.idA.PrivateKey))

	c := f.meshA.localCapability()
	c.AgentManifests = []agentmanifest.Manifest{good, forgedSig, wrongPeer}
	c.Signature = nil
	payload, err := json.Marshal(c)
	require.NoError(t, err)
	c.Signature = identity.Sign(f.idA.PrivateKey, payload)

	require.NoError(t, f.meshB.verifyCapability(f.nodeA.ID(), &c))
	require.Len(t, c.AgentManifests, 1)
	assert.Equal(t, "good", c.AgentManifests[0].AgentID)
}

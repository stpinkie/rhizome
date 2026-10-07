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
	"github.com/stpinkie/rhizome/pkg/rhizome/peeradverts"
)

// track138Economy returns a valid economy config for fixture wiring.
func track138Economy() config.MeshEconomyConfig {
	return config.MeshEconomyConfig{
		Enabled: true,
		Unit:    "credits",
		PriceSheet: config.EconPriceSheet{
			PerTask:           "0.01",
			Per1KPromptTokens: "0.0002",
			MinCharge:         "0.001",
		},
		AcceptUnits: []string{"credits"},
		Payout: &config.EconPayout{
			ChainID: "84532", Address: "0xabc123", Asset: "USDC",
		},
	}
}

// TestTrack138EconomyAnnounced verifies the economy advert rides inside
// the signed manifest: localCapability emits Economy when enabled, the
// manifest verifies at the receiving peer, and the stored capability plus
// the peer-adverts journal carry the terms.
func TestTrack138EconomyAnnounced(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RHIZOME_HOME", home)

	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled: true,
		Economy: track138Economy(),
	})
	ctx := context.Background()

	capB := f.meshB.localCapability()
	require.NotNil(t, capB.Economy, "enabled economy must advertise")
	assert.Equal(t, "credits", capB.Economy.Unit)
	assert.Equal(t, "0.01", capB.Economy.PriceSheet.PerTask)
	require.NotNil(t, capB.Economy.Payout)
	assert.Equal(t, "0xabc123", capB.Economy.Payout.Address)
	require.NotEmpty(t, capB.Signature)

	// Buyer-private fields must not appear in the advertised bytes.
	raw, err := json.Marshal(capB.Economy)
	require.NoError(t, err)
	for _, priv := range []string{
		"bill_peers", "max_cost_per_task", "max_cost_per_day",
		"settle_threshold", "settle_backend",
	} {
		assert.NotContains(t, string(raw), priv)
	}

	// The signed manifest verifies at the receiving peer.
	announced, err := f.meshA.TrustAndDiscover(ctx, f.nodeB.ID())
	require.NoError(t, err)
	require.NotNil(t, announced.Economy)
	assert.Equal(t, "credits", announced.Economy.Unit)

	require.Eventually(t, func() bool {
		stored, ok := f.meshA.PeerCapabilities(f.nodeB.ID())
		return ok && stored.Economy != nil && stored.Economy.Unit == "credits"
	}, 10*time.Second, 100*time.Millisecond, "stored capability must carry economy")

	// The journal row carries the raw economy advert for module readers.
	require.Eventually(t, func() bool {
		rows, err := peeradverts.Load(home)
		if err != nil {
			return false
		}
		for _, r := range rows {
			if r.PeerID == f.nodeB.ID().String() && len(r.Economy) > 0 {
				var decoded config.EconAdvert
				if json.Unmarshal(r.Economy, &decoded) != nil {
					return false
				}
				return decoded.Unit == "credits"
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "journal must record economy advert")
}

// TestTrack138EconomyEmitWhenSet pins emit-when-set: without
// mesh.economy.enabled the manifest carries no economy bytes and verifies
// identically to a pre-field build.
func TestTrack138EconomyEmitWhenSet(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{Enabled: true})

	c := f.meshB.localCapability()
	assert.Nil(t, c.Economy)
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), `"economy"`)

	var decoded Capability
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.NoError(t, f.meshA.verifyCapability(f.nodeB.ID(), &decoded))
}

// TestTrack138OldBuildDropFailsVerify pins the Role/ModuleAdverts-class
// rejection for Economy: an old build lacks the field, so decoding an
// economy manifest and re-marshaling it produces different bytes — the
// signature fails and the whole manifest is rejected.
func TestTrack138OldBuildDropFailsVerify(t *testing.T) {
	f := newSecurityMeshFixture(t, config.MeshConfig{
		Enabled: true,
		Economy: track138Economy(),
	})

	capB := f.meshB.localCapability()
	require.NotNil(t, capB.Economy)
	wire, err := json.Marshal(capB)
	require.NoError(t, err)
	assert.Contains(t, string(wire), `"economy"`)

	// Mirrors Capability before Economy existed (ModuleAdverts present —
	// economy lands after the module-advert field).
	type oldCapability struct {
		PeerID          string                     `json:"peer_id"`
		Models          []string                   `json:"models,omitempty"`
		Skills          []string                   `json:"skills,omitempty"`
		Agents          []string                   `json:"agents,omitempty"`
		Timestamp       int64                      `json:"timestamp"`
		Allows          map[string]bool            `json:"allows,omitempty"`
		ActiveTasks     int                        `json:"active_tasks,omitempty"`
		ShareableSkills []string                   `json:"shareable_skills,omitempty"`
		AgentManifests  []agentmanifest.Manifest   `json:"agent_manifests,omitempty"`
		Role            string                     `json:"role,omitempty"`
		ModuleAdverts   map[string]json.RawMessage `json:"module_adverts,omitempty"`
		Signature       []byte                     `json:"signature,omitempty"`
	}

	var oldCap oldCapability
	require.NoError(t, json.Unmarshal(wire, &oldCap))
	oldBytes, err := json.Marshal(oldCap)
	require.NoError(t, err)
	assert.NotContains(t, string(oldBytes), "economy", "old build drops the field")

	var decoded Capability
	require.NoError(t, json.Unmarshal(oldBytes, &decoded))
	require.Nil(t, decoded.Economy)
	require.Error(t, f.meshA.verifyCapability(f.nodeB.ID(), &decoded),
		"field drop must fail signature verification")
}

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
)

// TestMeshSkillOfferTriggersPull exercises the consentful push: A offers
// "demo-skill" to B, B accepts the notification and runs the normal pull
// path (blob fetch + guard scan + install under its global skills root).
func TestMeshSkillOfferTriggersPull(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meshA, meshB := newSkillTestPair(t, ctx)

	require.NoError(t, meshA.OfferSkill(ctx, meshB.host.ID(), "demo-skill"))

	dir := filepath.Join(meshB.skillsLoader.GlobalSkillsDir(), "demo-skill")
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "SKILL.md"))
		return err == nil
	}, 15*time.Second, 100*time.Millisecond)

	// The install carries the same mesh:<peer> provenance as a manual pull.
	data, err := os.ReadFile(filepath.Join(dir, ".skill-origin.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"origin_kind": "mesh"`)
	assert.Contains(t, string(data), "mesh:"+meshA.host.ID().String())
}

func TestMeshSkillOfferNotShared(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meshA, meshB := newSkillTestPair(t, ctx)

	// "other-skill" is not in A's skill_share — the offer fails fast
	// because B's pull would reject anyway.
	err := meshA.OfferSkill(ctx, meshB.host.ID(), "other-skill")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not shared")
}

func TestMeshSkillOfferValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meshA, meshB := newSkillTestPair(t, ctx)

	err := meshA.OfferSkill(ctx, meshB.host.ID(), "../escape")
	require.Error(t, err)

	// Offering to an untrusted peer fails at the skillCall trust gate.
	meshA.UntrustPeer(meshB.host.ID())
	err = meshA.OfferSkill(ctx, meshB.host.ID(), "demo-skill")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not trusted")
}

// TestMeshBlobsAccessor covers the `mesh blobs` data path.
func TestMeshBlobsAccessor(t *testing.T) {
	bare := &Mesh{}
	_, err := bare.Blobs()
	require.Error(t, err)

	m := &Mesh{cfg: config.DefaultMeshConfig()}
	m.SetBlobDir(t.TempDir())
	entries, err := m.Blobs()
	require.NoError(t, err)
	assert.Empty(t, entries)

	// A staged resumable transfer shows up as a partial entry.
	w, err := m.blobStore.NewResumableWriter(hash64(), 8)
	require.NoError(t, err)
	_, err = w.Write([]byte("abc"))
	require.NoError(t, err)
	w.Suspend()

	entries, err = m.Blobs()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, int64(3), entries[0].Partial)
}

package agentmanifest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

func testIdentity(t *testing.T, index uint32) *identity.Derived {
	t.Helper()
	id, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", index)
	require.NoError(t, err)
	return id
}

func TestManifestSignVerifyRoundTrip(t *testing.T) {
	id := testIdentity(t, 10)
	m := Manifest{AgentID: "main", Name: "Main", Models: []string{"gpt-4"}, Skills: []string{"fs"}}
	require.NoError(t, m.Sign(id.PeerID, id.PrivateKey))

	assert.Equal(t, id.PeerID, m.PeerID)
	assert.False(t, m.IssuedAt.IsZero())
	require.NoError(t, m.Verify(id.PublicKey))
}

func TestManifestVerifyRejectsTampering(t *testing.T) {
	id := testIdentity(t, 10)
	m := Manifest{AgentID: "main", Skills: []string{"fs"}}
	require.NoError(t, m.Sign(id.PeerID, id.PrivateKey))

	m.Skills = append(m.Skills, "shell")
	assert.Error(t, m.Verify(id.PublicKey))
}

func TestManifestVerifyRejectsWrongKey(t *testing.T) {
	idA := testIdentity(t, 10)
	idB := testIdentity(t, 11)
	m := Manifest{AgentID: "main"}
	require.NoError(t, m.Sign(idA.PeerID, idA.PrivateKey))

	assert.Error(t, m.Verify(idB.PublicKey))
}

func TestManifestVerifyRejectsUnsigned(t *testing.T) {
	id := testIdentity(t, 10)
	m := Manifest{AgentID: "main", PeerID: id.PeerID}
	assert.Error(t, m.Verify(id.PublicKey))
}

func TestManifestFingerprintStable(t *testing.T) {
	id := testIdentity(t, 10)
	m1 := Manifest{AgentID: "main", Name: "Main"}
	require.NoError(t, m1.Sign(id.PeerID, id.PrivateKey))
	fp1 := m1.Fingerprint()
	assert.Contains(t, fp1, "sha256:")

	m2 := Manifest{AgentID: "main", Name: "Other"}
	require.NoError(t, m2.Sign(id.PeerID, id.PrivateKey))
	assert.NotEqual(t, fp1, m2.Fingerprint())
}

func TestManifestSaveLoadAll(t *testing.T) {
	id := testIdentity(t, 10)
	dir := t.TempDir()

	m := Manifest{AgentID: "main", Name: "Main", Models: []string{"gpt-4"}}
	require.NoError(t, m.Sign(id.PeerID, id.PrivateKey))
	require.NoError(t, m.SaveTo(dir))

	loaded := LoadAll(dir)
	require.Len(t, loaded, 1)
	assert.Equal(t, "main", loaded[0].AgentID)
	assert.Equal(t, m.Fingerprint(), loaded[0].Fingerprint())
	require.NoError(t, loaded[0].Verify(id.PublicKey))
}

func TestManifestSaveToRejectsBadID(t *testing.T) {
	m := Manifest{AgentID: "../escape"}
	assert.Error(t, m.SaveTo(t.TempDir()))
}

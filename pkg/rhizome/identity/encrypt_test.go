package identity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSaveAndLoadEncryptedWithPassphrase(t *testing.T) {
	d, _, err := FromMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 0)
	require.NoError(t, err)

	dir := t.TempDir()
	passphrase := "test-passphrase-1234"

	require.NoError(t, SaveEncryptedWithPassphrase(dir, d, "test-node", passphrase))

	loaded, name, err := LoadWithProvider(dir, &ScryptProvider{Passphrase: passphrase})
	require.NoError(t, err)
	require.Equal(t, "test-node", name)
	require.Equal(t, d.PeerID, loaded.PeerID)
	require.Equal(t, d.PrivateKey, loaded.PrivateKey)
}

func TestLoadUnencryptedStillWorks(t *testing.T) {
	d, _, err := FromMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 0)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, Save(dir, d, "plain-node"))

	loaded, name, err := Load(dir)
	require.NoError(t, err)
	require.Equal(t, "plain-node", name)
	require.Equal(t, d.PeerID, loaded.PeerID)
}

func TestLoadEncryptedWithoutProviderFails(t *testing.T) {
	d, _, err := FromMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 0)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, SaveEncryptedWithPassphrase(dir, d, "enc-node", "hunter2"))

	_, _, err = Load(dir)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrIdentityEncrypted)
}

func TestEncryptedFileStoredInNodeJson(t *testing.T) {
	d, _, err := FromMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 0)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, SaveEncryptedWithPassphrase(dir, d, "enc-node", "hunter2"))

	path := filepath.Join(dir, "node.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), `"encrypted": true`)
	require.Contains(t, string(data), `"key_source": "scrypt"`)
}

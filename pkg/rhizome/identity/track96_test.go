package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/scrypt"
)

const track96Mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

// writeLegacyAESIdentity crafts a node.json the pre-Track-96 code would have
// written: AES-256-GCM nonce-prefixed ciphertext and no cipher marker.
func writeLegacyAESIdentity(t *testing.T, dir, passphrase string) *Derived {
	t.Helper()
	d, _, err := FromMnemonic(track96Mnemonic, 0)
	require.NoError(t, err)

	salt := make([]byte, 32)
	_, err = io.ReadFull(rand.Reader, salt)
	require.NoError(t, err)
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, keyLen)
	require.NoError(t, err)

	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	ct := gcm.Seal(nonce, nonce, d.PrivateKey, nil)

	ni := NodeIdentity{
		NodeIndex:  d.NodeIndex,
		NodeName:   "legacy-node",
		PeerID:     d.PeerID,
		PublicKey:  base64.StdEncoding.EncodeToString(d.PublicKey),
		Encrypted:  true,
		KeySource:  "scrypt",
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
		// Cipher deliberately unset — the legacy write shape.
	}
	data, err := json.MarshalIndent(ni, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "node.json"), data, 0o600))
	return d
}

func TestTrack96EncryptedWriteMarksXChaCha(t *testing.T) {
	d, _, err := FromMnemonic(track96Mnemonic, 0)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, SaveEncryptedWithPassphrase(dir, d, "new-node", "hunter2"))

	var ni NodeIdentity
	data, err := os.ReadFile(filepath.Join(dir, "node.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &ni))
	require.Equal(t, cipherXChaCha20Poly1305, ni.Cipher)

	loaded, name, err := LoadWithProvider(dir, &ScryptProvider{Passphrase: "hunter2"})
	require.NoError(t, err)
	require.Equal(t, "new-node", name)
	require.Equal(t, d.PrivateKey, loaded.PrivateKey)
}

func TestTrack96LegacyAESRecordLoads(t *testing.T) {
	dir := t.TempDir()
	d := writeLegacyAESIdentity(t, dir, "hunter2")

	loaded, name, err := LoadWithProvider(dir, &ScryptProvider{Passphrase: "hunter2"})
	require.NoError(t, err)
	require.Equal(t, "legacy-node", name)
	require.Equal(t, d.PeerID, loaded.PeerID)
	require.Equal(t, d.PrivateKey, loaded.PrivateKey)
}

func TestTrack96UnknownCipherRejected(t *testing.T) {
	dir := t.TempDir()
	d := writeLegacyAESIdentity(t, dir, "hunter2")

	path := filepath.Join(dir, "node.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var ni NodeIdentity
	require.NoError(t, json.Unmarshal(data, &ni))
	ni.Cipher = "rot13"
	data, err = json.MarshalIndent(ni, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	_, _, err = LoadWithProvider(dir, &ScryptProvider{Passphrase: "hunter2"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "rot13")
	_ = d
}

func TestTrack96DecryptDispatchesByMarker(t *testing.T) {
	key, err := generateKey()
	require.NoError(t, err)
	plaintext := []byte("ed25519-private-key-material-0123")

	// New writes: encrypt() seals XChaCha20-Poly1305; the marker selects it.
	_, ct, err := encrypt(plaintext, key)
	require.NoError(t, err)
	got, err := decrypt(ct, key, cipherXChaCha20Poly1305)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	// Legacy path: an AES-256-GCM blob opens under the empty marker.
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	aesCT := gcm.Seal(nonce, nonce, plaintext, nil)
	got, err = decrypt(aesCT, key, "")
	require.NoError(t, err)
	require.Equal(t, plaintext, got)

	// Cross-cipher reads fail — the marker is authoritative.
	_, err = decrypt(ct, key, "")
	require.Error(t, err)
	_, err = decrypt(aesCT, key, cipherXChaCha20Poly1305)
	require.Error(t, err)
}

package credential

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupSSHKey writes a fake SSH key under dir and points RHIZOME_SSH_KEY_PATH
// at it so Encrypt/Resolve accept it.
func setupSSHKey(t *testing.T, dir string) string {
	t.Helper()
	keyPath := filepath.Join(dir, "rhizome_ed25519.key")
	if err := os.WriteFile(keyPath, []byte("fake-ssh-key-material\n"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv("RHIZOME_SSH_KEY_PATH", keyPath)
	return keyPath
}

func TestEncrypt_WritesEnc2(t *testing.T) {
	dir := t.TempDir()
	setupSSHKey(t, dir)

	enc, err := Encrypt("passphrase", "", "sk-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !strings.HasPrefix(enc, Enc2Scheme) {
		t.Fatalf("expected %q prefix, got %q", Enc2Scheme, enc)
	}
}

func TestResolve_Enc2RoundTrip(t *testing.T) {
	dir := t.TempDir()
	setupSSHKey(t, dir)

	const passphrase = "passphrase-v2"
	const plaintext = "sk-enc2-secret"

	enc, err := Encrypt(passphrase, "", plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	t.Setenv("RHIZOME_KEY_PASSPHRASE", passphrase)
	r := NewResolver(t.TempDir())
	got, err := r.Resolve(enc)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != plaintext {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

// TestResolve_DualScheme verifies a legacy enc:// blob (AES-256-GCM) still
// decrypts alongside enc2:// — the transparent read path that makes the
// write-side upgrade one-way.
func TestResolve_DualScheme(t *testing.T) {
	dir := t.TempDir()
	setupSSHKey(t, dir)

	const passphrase = "dual-pass"
	const plaintext = "sk-legacy-secret"

	legacy, err := encryptV1(passphrase, "", plaintext)
	if err != nil {
		t.Fatalf("encryptV1: %v", err)
	}
	if !strings.HasPrefix(legacy, EncScheme) {
		t.Fatalf("expected %q prefix, got %q", EncScheme, legacy)
	}

	t.Setenv("RHIZOME_KEY_PASSPHRASE", passphrase)
	r := NewResolver(t.TempDir())
	got, err := r.Resolve(legacy)
	if err != nil {
		t.Fatalf("Resolve(enc://): %v", err)
	}
	if got != plaintext {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

// TestResolve_Enc2BlobLayout pins the wire format: salt(16) | nonce(24) | ct.
func TestResolve_Enc2BlobLayout(t *testing.T) {
	dir := t.TempDir()
	setupSSHKey(t, dir)

	enc, err := Encrypt("passphrase", "", "x")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	raw := strings.TrimPrefix(enc, Enc2Scheme)
	blob, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("enc2 payload not base64: %v", err)
	}
	// salt(16) + nonce(24) + plaintext(1) + poly1305 tag(16) = 57
	if len(blob) != saltLen+nonceLenV2+1+16 {
		t.Fatalf("enc2 blob length = %d, want %d", len(blob), saltLen+nonceLenV2+1+16)
	}
}

func TestResolve_Enc2WrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	setupSSHKey(t, dir)

	enc, err := Encrypt("correct", "", "sk-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	t.Setenv("RHIZOME_KEY_PASSPHRASE", "wrong")
	r := NewResolver(t.TempDir())
	_, err = r.Resolve(enc)
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got %v", err)
	}
}

func TestResolve_Enc2ShortPayload(t *testing.T) {
	t.Setenv("RHIZOME_KEY_PASSPHRASE", "p")
	t.Setenv("RHIZOME_SSH_KEY_PATH", "")
	r := NewResolver(t.TempDir())
	_, err := r.Resolve(Enc2Scheme + "dG9vc2hvcnQ=") // "tooshort"
	if err == nil {
		t.Fatal("expected error for too-short enc2:// payload")
	}
}

func TestIsEncryptedRef(t *testing.T) {
	cases := map[string]bool{
		"enc://abc":    true,
		"enc2://abc":   true,
		"file://k.key": false,
		"sk-plain":     false,
		"":             false,
	}
	for raw, want := range cases {
		if got := IsEncryptedRef(raw); got != want {
			t.Errorf("IsEncryptedRef(%q) = %v, want %v", raw, got, want)
		}
		if got := IsCredentialRef(raw); got != (want || raw == "file://k.key") {
			t.Errorf("IsCredentialRef(%q) = %v", raw, got)
		}
	}
}

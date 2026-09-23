// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/scrypt"
)

// writeLegacyWalletFile crafts a keys.json the pre-Track-96 code would have
// written: one AES-256-GCM entry with no cipher marker, scrypt key source.
func writeLegacyWalletFile(t *testing.T, dir, passphrase, privKeyHex, addr string) {
	t.Helper()
	salt := make([]byte, 32)
	_, err := io.ReadFull(rand.Reader, salt)
	if err != nil {
		t.Fatal(err)
	}
	master, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, walletKeyLen)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(privKeyHex, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	aead, err := newAEAD(master, "") // legacy AES-256-GCM
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	f := walletFile{
		Version:   walletFileVer,
		KeySource: "scrypt",
		Salt:      base64.StdEncoding.EncodeToString(salt),
		Default:   addr,
		Keys: []walletKey{{
			Address:    addr,
			Ciphertext: base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, raw, nil)),
			Nonce:      base64.StdEncoding.EncodeToString(nonce),
			CreatedAt:  time.Now().UTC(),
		}},
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, walletFileName), data, walletFilePerms); err != nil {
		t.Fatal(err)
	}
}

func readWalletFile(t *testing.T, dir string) walletFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, walletFileName))
	if err != nil {
		t.Fatal(err)
	}
	var f walletFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestTrack96WalletNewEntryMarkedXChaCha(t *testing.T) {
	useScryptWallet(t)
	dir := t.TempDir()
	s := OpenWalletStore(dir)
	if _, err := s.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}
	f := readWalletFile(t, dir)
	if len(f.Keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(f.Keys))
	}
	if f.Keys[0].Cipher != cipherXChaCha20Poly1305 {
		t.Fatalf("cipher marker = %q, want %q", f.Keys[0].Cipher, cipherXChaCha20Poly1305)
	}
	// Roundtrips.
	got, err := s.Reveal(testKeyOneAddr)
	if err != nil || got != testKeyOne {
		t.Fatalf("reveal = %q, %v", got, err)
	}
}

func TestTrack96WalletLegacyEntryReads(t *testing.T) {
	dir := t.TempDir()
	writeLegacyWalletFile(t, dir, "test-passphrase", testKeyOne, testKeyOneAddr)
	t.Setenv(keySourceEnv, "scrypt")
	t.Setenv(passphraseEnv, "test-passphrase")

	s := OpenWalletStore(dir)
	got, err := s.Reveal(testKeyOneAddr)
	if err != nil {
		t.Fatalf("legacy entry unreadable: %v", err)
	}
	if got != testKeyOne {
		t.Fatalf("reveal = %s, want the imported key", got)
	}
	// The file itself is untouched — reads never migrate records.
	f := readWalletFile(t, dir)
	if f.Keys[0].Cipher != "" {
		t.Fatalf("read path mutated cipher marker to %q", f.Keys[0].Cipher)
	}
}

func TestTrack96WalletAddKeyResealsLegacy(t *testing.T) {
	dir := t.TempDir()
	writeLegacyWalletFile(t, dir, "test-passphrase", testKeyOne, testKeyOneAddr)
	useScryptWallet(t)

	s := OpenWalletStore(dir)
	// Import a second key — addKey holds the master key and must re-seal the
	// legacy entry alongside the new one.
	if _, err := s.Generate("second"); err != nil {
		t.Fatal(err)
	}
	f := readWalletFile(t, dir)
	if len(f.Keys) != 2 {
		t.Fatalf("keys = %d, want 2", len(f.Keys))
	}
	for i, k := range f.Keys {
		if k.Cipher != cipherXChaCha20Poly1305 {
			t.Fatalf("key %d cipher marker = %q, want migrated", i, k.Cipher)
		}
	}
	// The migrated legacy key still decrypts.
	got, err := s.Reveal(testKeyOneAddr)
	if err != nil || got != testKeyOne {
		t.Fatalf("migrated entry reveal = %q, %v", got, err)
	}
}

func TestTrack96WalletUnknownCipherFails(t *testing.T) {
	useScryptWallet(t)
	dir := t.TempDir()
	s := OpenWalletStore(dir)
	if _, err := s.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}
	f := readWalletFile(t, dir)
	f.Keys[0].Cipher = "rot13"
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, walletFileName), data, walletFilePerms); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reveal(testKeyOneAddr); err == nil {
		t.Fatal("expected error for unknown cipher marker")
	}
}

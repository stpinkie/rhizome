// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// useScryptWallet pins the wallet to the passphrase path — tests must
// never touch the real OS keyring.
func useScryptWallet(t *testing.T) {
	t.Helper()
	t.Setenv(keySourceEnv, "scrypt")
	t.Setenv(passphraseEnv, "test-passphrase")
}

// privkey 0x01 derives the well-known 0x7E5F... address (generator point).
const (
	testKeyOne     = "0x0000000000000000000000000000000000000000000000000000000000000001"
	testKeyOneAddr = "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"
)

func TestWallet_ImportDerivesKnownAddress(t *testing.T) {
	useScryptWallet(t)
	s := OpenWalletStore(t.TempDir())
	e, err := s.Import(testKeyOne, "first")
	if err != nil {
		t.Fatal(err)
	}
	if e.Address != testKeyOneAddr {
		t.Fatalf("derived %s, want %s", e.Address, testKeyOneAddr)
	}
	if !IsAddress(e.Address) {
		t.Fatalf("address %q fails IsAddress", e.Address)
	}
}

func TestWallet_RevealRoundTrip(t *testing.T) {
	useScryptWallet(t)
	s := OpenWalletStore(t.TempDir())
	if _, err := s.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Reveal(testKeyOneAddr)
	if err != nil {
		t.Fatal(err)
	}
	if got != testKeyOne {
		t.Fatalf("reveal = %s, want the imported key", got)
	}
	// Reveal by label-free case-insensitive address too.
	got, err = s.Reveal(strings.ToLower(testKeyOneAddr))
	if err != nil || got != testKeyOne {
		t.Fatalf("lowercase reveal failed: %v", err)
	}
}

func TestWallet_StoredEncryptedAtRest(t *testing.T) {
	useScryptWallet(t)
	dir := t.TempDir()
	s := OpenWalletStore(dir)
	if _, err := s.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, walletFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), strings.TrimPrefix(testKeyOne, "0x")) {
		t.Fatal("plaintext private key found in wallet store")
	}
	var f walletFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f.KeySource != "scrypt" || f.Salt == "" {
		t.Fatalf("expected scrypt store with salt, got %+v", f)
	}
	// POSIX mode bits are only honored on Unix — Windows maps to a
	// read-only bit and reports 0666 either way.
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, walletFileName))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("keys.json mode %o, want 0600", st.Mode().Perm())
		}
	}
}

func TestWallet_LockedWithoutPassphrase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(keySourceEnv, "scrypt")
	t.Setenv(passphraseEnv, "test-passphrase")
	s := OpenWalletStore(dir)
	if _, err := s.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}
	// Reopen without the passphrase → signing must fail closed.
	t.Setenv(passphraseEnv, "")
	s2 := OpenWalletStore(dir)
	if _, err := s2.PrivateKey(testKeyOneAddr); err == nil {
		t.Fatal("PrivateKey without passphrase must fail")
	}
	// Wrong passphrase decrypts to garbage-free error, not a bad key.
	t.Setenv(passphraseEnv, "wrong")
	if _, err := s2.PrivateKey(testKeyOneAddr); err == nil {
		t.Fatal("PrivateKey with wrong passphrase must fail")
	}
}

func TestWallet_DuplicateImportRejected(t *testing.T) {
	useScryptWallet(t)
	s := OpenWalletStore(t.TempDir())
	if _, err := s.Import(testKeyOne, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Import(testKeyOne, ""); err == nil {
		t.Fatal("duplicate import must fail")
	}
	if _, err := s.Import("0x"+strings.Repeat("00", 32), ""); err == nil {
		t.Fatal("zero key must be rejected")
	}
}

func TestWallet_DefaultRenameRemove(t *testing.T) {
	useScryptWallet(t)
	s := OpenWalletStore(t.TempDir())
	e1, _ := s.Import(testKeyOne, "one")
	e2, err := s.Generate("two")
	if err != nil {
		t.Fatal(err)
	}
	def, err := s.Default()
	if err != nil || def != e1.Address {
		t.Fatalf("default = %q, want first key %s", def, e1.Address)
	}
	if err := s.SetDefault(e2.Address); err != nil {
		t.Fatal(err)
	}
	if def, _ := s.Default(); def != e2.Address {
		t.Fatalf("default = %q, want %s", def, e2.Address)
	}
	if err := s.Rename(e2.Address, "renamed"); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 2 || list[1].Label != "renamed" {
		t.Fatalf("list = %+v", list)
	}
	if err := s.Remove(e2.Address); err != nil {
		t.Fatal(err)
	}
	if def, _ := s.Default(); def != "" {
		t.Fatalf("default should clear after remove, got %q", def)
	}
	if s.Has(e2.Address) {
		t.Fatal("removed address still present")
	}
}

func TestChecksumAddress_EIP55Vectors(t *testing.T) {
	// EIP-55 spec vectors.
	cases := map[string]string{
		"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed": "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xfb6916095ca1df60bb79ce92ce3ea74c37c5d359": "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xdbf03b407c01e7cd3cbea99509d93f8dddc8c6fb": "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
		"0xd1220a0cf47c7b9be7a2e6ba89f429762e7b9adb": "0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
	}
	for in, want := range cases {
		got, err := NormalizeAddress(in)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("checksum %s = %s, want %s", in, got, want)
		}
	}
}

// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package web3

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

// Wallet key custody mirrors the pkg/rhizome/identity KeyProvider posture:
// a random 32-byte master key lives in the OS keyring (account
// "web3-wallet"); on hosts without a keyring (headless Linux) the master
// key is scrypt-derived from RHIZOME_WALLET_PASSPHRASE with a per-wallet
// salt stored in the file header. Individual keys are AEAD-sealed under
// the master key with a random nonce each — XChaCha20-Poly1305 for new
// writes, AES-256-GCM readable for legacy entries (see walletKey.Cipher).
//
// The store file never holds plaintext key material. Private keys are
// decrypted on demand for signing and revealed only through the explicit
// Reveal path — callers are expected to mask output and never log keys.

const (
	keyringService = "github.com/stpinkie/rhizome"
	keyringAccount = "web3-wallet"
	walletKeyLen   = 32
	scryptN        = 32768
	scryptR        = 8
	scryptP        = 1
	walletFileName = "keys.json"
	walletFileVer  = 1
	maxWalletKeys  = 64
	//nolint:gosec // G101: environment variable name, not a credential.
	passphraseEnv   = "RHIZOME_WALLET_PASSPHRASE"
	keySourceEnv    = "RHIZOME_WALLET_KEYSOURCE" // "keyring"|"scrypt" override
	walletFilePerms = 0o600
	walletDirPerms  = 0o700
	// Per-entry AEAD markers — see walletKey. "" = legacy AES-256-GCM.
	cipherAES256GCM         = "aes-256-gcm"
	cipherXChaCha20Poly1305 = "xchacha20poly1305"
)

// ErrWalletLocked is returned when the store is passphrase-protected and
// no passphrase is available.
var ErrWalletLocked = errors.New(
	"wallet is passphrase-encrypted; set " + passphraseEnv,
)

// WalletEntry is the public view of one stored key — never key material.
type WalletEntry struct {
	Address   string    `json:"address"` // EIP-55 checksummed
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// walletKey is the on-disk encrypted form of one key. Cipher is a per-entry
// AEAD marker: "" means a legacy AES-256-GCM entry, "xchacha20poly1305" is
// the current write cipher. Entries may mix within one file — the marker is
// per-entry precisely so a walletFile.Version bump is unnecessary — and
// legacy entries are re-sealed opportunistically on writes that already hold
// the master key (addKey). A downgraded binary cannot open migrated entries:
// the one-way door mirrors the enc://→enc2:// posture.
type walletKey struct {
	Address    string    `json:"address"`
	Label      string    `json:"label,omitempty"`
	Ciphertext string    `json:"ciphertext"`       // base64 AEAD ct
	Nonce      string    `json:"nonce"`            // base64
	Cipher     string    `json:"cipher,omitempty"` // "" = aes-256-gcm legacy
	CreatedAt  time.Time `json:"created_at"`
}

type walletFile struct {
	Version   int         `json:"version"`
	KeySource string      `json:"key_source"`        // "keyring" | "scrypt"
	Salt      string      `json:"salt,omitempty"`    // base64, scrypt only
	Default   string      `json:"default,omitempty"` // checksummed address
	Keys      []walletKey `json:"keys"`
}

// WalletStore manages the encrypted key file at <dir>/keys.json.
// Safe for concurrent use.
type WalletStore struct {
	dir string
	mu  sync.Mutex
}

// OpenWalletStore returns a store rooted at dir (typically
// <RHIZOME_HOME>/web3). The file need not exist yet.
func OpenWalletStore(dir string) *WalletStore {
	return &WalletStore{dir: dir}
}

func (s *WalletStore) path() string {
	return filepath.Join(s.dir, walletFileName)
}

// List returns all stored keys (public fields only).
func (s *WalletStore) List() ([]WalletEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readFile()
	if err != nil {
		return nil, err
	}
	out := make([]WalletEntry, 0, len(f.Keys))
	for _, k := range f.Keys {
		out = append(out, WalletEntry{Address: k.Address, Label: k.Label, CreatedAt: k.CreatedAt})
	}
	return out, nil
}

// Default returns the default-signing address ("" if unset).
func (s *WalletStore) Default() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readFile()
	if err != nil {
		return "", err
	}
	return f.Default, nil
}

// Has reports whether addr is stored (case-insensitive compare).
func (s *WalletStore) Has(addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readFile()
	if err != nil {
		return false
	}
	return findKey(f, addr) != nil
}

// Generate creates a new random key, persists it, and returns the entry.
// The first key becomes the default.
func (s *WalletStore) Generate(label string) (*WalletEntry, error) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	defer priv.Zero()
	return s.addKey(priv.Serialize(), label)
}

// Import stores an existing 0x-hex private key. Returns an error if the
// address is already stored.
func (s *WalletStore) Import(privKeyHex, label string) (*WalletEntry, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(privKeyHex), "0x")
	keyBytes, err := hex.DecodeString(raw)
	if err != nil || len(keyBytes) != 32 {
		return nil, fmt.Errorf("expected a 32-byte hex private key")
	}
	defer zeroBytes(keyBytes)
	if new(big.Int).SetBytes(keyBytes).Sign() == 0 {
		return nil, fmt.Errorf("private key cannot be zero")
	}
	return s.addKey(keyBytes, label)
}

// Remove deletes a key by address; clears the default if it pointed there.
func (s *WalletStore) Remove(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readFile()
	if err != nil {
		return err
	}
	kept := f.Keys[:0]
	found := false
	for _, k := range f.Keys {
		if strings.EqualFold(k.Address, addr) {
			found = true
			continue
		}
		kept = append(kept, k)
	}
	if !found {
		return fmt.Errorf("no wallet key with address %s", addr)
	}
	f.Keys = kept
	if strings.EqualFold(f.Default, addr) {
		f.Default = ""
	}
	return s.writeFile(f)
}

// SetDefault marks addr as the default-signing address.
func (s *WalletStore) SetDefault(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readFile()
	if err != nil {
		return err
	}
	k := findKey(f, addr)
	if k == nil {
		return fmt.Errorf("no wallet key with address %s", addr)
	}
	f.Default = k.Address
	return s.writeFile(f)
}

// Rename sets a key's label.
func (s *WalletStore) Rename(addr, label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readFile()
	if err != nil {
		return err
	}
	k := findKey(f, addr)
	if k == nil {
		return fmt.Errorf("no wallet key with address %s", addr)
	}
	k.Label = label
	return s.writeFile(f)
}

// Reveal returns the 0x-hex private key for addr. This is the ONLY export
// path — callers must gate it (CLI --confirm, masked output) and must
// never route the result through logs, tools, or the network.
func (s *WalletStore) Reveal(addr string) (string, error) {
	priv, err := s.PrivateKey(addr)
	if err != nil {
		return "", err
	}
	defer priv.Zero()
	return "0x" + hex.EncodeToString(priv.Serialize()), nil
}

// PrivateKey decrypts and returns the signing key for addr. Callers
// should Zero() it when done.
func (s *WalletStore) PrivateKey(addr string) (*secp256k1.PrivateKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readFile()
	if err != nil {
		return nil, err
	}
	k := findKey(f, addr)
	if k == nil {
		return nil, fmt.Errorf("no wallet key with address %s", addr)
	}
	master, err := s.masterKey(f)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(master)
	ct, err := base64.StdEncoding.DecodeString(k.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decode ciphertext: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(k.Nonce)
	if err != nil {
		return nil, fmt.Errorf("decode nonce: %w", err)
	}
	raw, err := walletDecrypt(nonce, ct, master, k.Cipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt key for %s: %w", k.Address, err)
	}
	defer zeroBytes(raw)
	if len(raw) != 32 {
		return nil, fmt.Errorf("corrupt key material for %s", k.Address)
	}
	priv := secp256k1.PrivKeyFromBytes(raw)
	return priv, nil
}

// addKey encrypts and persists a raw 32-byte private key.
func (s *WalletStore) addKey(raw []byte, label string) (*WalletEntry, error) {
	priv := secp256k1.PrivKeyFromBytes(raw)
	addr := DeriveAddress(priv)
	priv.Zero()

	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.readFile()
	if err != nil {
		return nil, err
	}
	if findKey(f, addr) != nil {
		return nil, fmt.Errorf("address %s is already in the wallet", addr)
	}
	if len(f.Keys) >= maxWalletKeys {
		return nil, fmt.Errorf("wallet is full (%d keys)", maxWalletKeys)
	}
	if f.Version == 0 {
		// First key — establish the master-key source.
		if err := s.initMasterKey(f); err != nil {
			return nil, err
		}
	}
	master, err := s.masterKey(f)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(master)
	nonce, ct, err := walletEncrypt(raw, master)
	if err != nil {
		return nil, fmt.Errorf("encrypt key: %w", err)
	}
	// The master key is already in hand — re-seal legacy entries under the
	// current cipher so the file migrates on this write. Entries that fail
	// to open (e.g. a changed passphrase) are left as-is; PrivateKey
	// surfaces that error at use time.
	for i := range f.Keys {
		resealWalletKey(&f.Keys[i], master)
	}
	entry := walletKey{
		Address:    addr,
		Label:      label,
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Cipher:     cipherXChaCha20Poly1305,
		CreatedAt:  time.Now().UTC(),
	}
	f.Keys = append(f.Keys, entry)
	if f.Default == "" {
		f.Default = addr
	}
	if err := s.writeFile(f); err != nil {
		return nil, err
	}
	return &WalletEntry{Address: addr, Label: label, CreatedAt: entry.CreatedAt}, nil
}

// readFile loads keys.json; a missing file returns an empty walletFile.
func (s *WalletStore) readFile() (*walletFile, error) {
	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return &walletFile{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read wallet store: %w", err)
	}
	var f walletFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse wallet store: %w", err)
	}
	if f.Version > walletFileVer {
		return nil, fmt.Errorf("wallet store version %d is newer than supported %d", f.Version, walletFileVer)
	}
	return &f, nil
}

func (s *WalletStore) writeFile(f *walletFile) error {
	if f.Version == 0 {
		f.Version = walletFileVer
	}
	if err := os.MkdirAll(s.dir, walletDirPerms); err != nil {
		return fmt.Errorf("create wallet dir: %w", err)
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal wallet store: %w", err)
	}
	if err := fileutil.WriteFileAtomic(s.path(), data, walletFilePerms); err != nil {
		return fmt.Errorf("write wallet store: %w", err)
	}
	return nil
}

// initMasterKey chooses and provisions the master-key source for a new
// store: explicit override → keyring → scrypt fallback (requires the
// passphrase env).
func (s *WalletStore) initMasterKey(f *walletFile) error {
	switch strings.ToLower(os.Getenv(keySourceEnv)) {
	case "scrypt":
		return s.initScrypt(f)
	case "keyring":
		key, err := generateWalletKey()
		if err != nil {
			return err
		}
		defer zeroBytes(key)
		if err := keyring.Set(keyringService, keyringAccount,
			base64.StdEncoding.EncodeToString(key)); err != nil {
			return fmt.Errorf("store wallet key in keyring: %w", err)
		}
		f.KeySource = "keyring"
		return nil
	}
	// Auto: keyring first, scrypt fallback.
	key, err := generateWalletKey()
	if err != nil {
		return err
	}
	defer zeroBytes(key)
	if err := keyring.Set(keyringService, keyringAccount,
		base64.StdEncoding.EncodeToString(key)); err == nil {
		f.KeySource = "keyring"
		return nil
	}
	return s.initScrypt(f)
}

func (s *WalletStore) initScrypt(f *walletFile) error {
	if os.Getenv(passphraseEnv) == "" {
		return fmt.Errorf(
			"no OS keyring available — set %s to encrypt the wallet with a passphrase",
			passphraseEnv)
	}
	salt := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("read salt: %w", err)
	}
	f.KeySource = "scrypt"
	f.Salt = base64.StdEncoding.EncodeToString(salt)
	return nil
}

// masterKey returns the 32-byte AES key for the store.
func (s *WalletStore) masterKey(f *walletFile) ([]byte, error) {
	switch f.KeySource {
	case "keyring":
		secret, err := keyring.Get(keyringService, keyringAccount)
		if err != nil {
			return nil, fmt.Errorf("keyring get: %w", err)
		}
		key, err := base64.StdEncoding.DecodeString(secret)
		if err != nil || len(key) != walletKeyLen {
			return nil, fmt.Errorf("keyring wallet key is malformed")
		}
		return key, nil
	case "scrypt":
		pass := os.Getenv(passphraseEnv)
		if pass == "" {
			return nil, ErrWalletLocked
		}
		salt, err := base64.StdEncoding.DecodeString(f.Salt)
		if err != nil {
			return nil, fmt.Errorf("decode wallet salt: %w", err)
		}
		return scrypt.Key([]byte(pass), salt, scryptN, scryptR, scryptP, walletKeyLen)
	default:
		return nil, fmt.Errorf("wallet store has unknown key_source %q", f.KeySource)
	}
}

func findKey(f *walletFile, addr string) *walletKey {
	norm := strings.ToLower(strings.TrimPrefix(addr, "0x"))
	for i := range f.Keys {
		if strings.ToLower(strings.TrimPrefix(f.Keys[i].Address, "0x")) == norm {
			return &f.Keys[i]
		}
	}
	return nil
}

func generateWalletKey() ([]byte, error) {
	key := make([]byte, walletKeyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	return key, nil
}

// resealWalletKey upgrades a legacy (empty-marker) walletKey to the
// current write cipher. Best-effort: any decrypt/re-seal failure leaves
// the entry untouched.
func resealWalletKey(k *walletKey, master []byte) {
	if k.Cipher != "" && k.Cipher != cipherAES256GCM {
		return
	}
	ct, err := base64.StdEncoding.DecodeString(k.Ciphertext)
	if err != nil {
		return
	}
	nonce, err := base64.StdEncoding.DecodeString(k.Nonce)
	if err != nil {
		return
	}
	raw, err := walletDecrypt(nonce, ct, master, k.Cipher)
	if err != nil {
		return
	}
	defer zeroBytes(raw)
	newNonce, newCt, err := walletEncrypt(raw, master)
	if err != nil {
		return
	}
	k.Ciphertext = base64.StdEncoding.EncodeToString(newCt)
	k.Nonce = base64.StdEncoding.EncodeToString(newNonce)
	k.Cipher = cipherXChaCha20Poly1305
}

// walletEncrypt seals data with the current write cipher
// (XChaCha20-Poly1305); the nonce is returned separately.
func walletEncrypt(data, key []byte) (nonce, ciphertext []byte, err error) {
	aead, err := newAEAD(key, cipherXChaCha20Poly1305)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("read nonce: %w", err)
	}
	return nonce, aead.Seal(nil, nonce, data, nil), nil
}

// walletDecrypt opens a walletEncrypt blob (nonce passed separately),
// dispatching on the entry's cipher marker ("" = legacy AES-256-GCM).
func walletDecrypt(nonce, ciphertext, key []byte, cipherName string) ([]byte, error) {
	aead, err := newAEAD(key, cipherName)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("bad nonce length %d", len(nonce))
	}
	return aead.Open(nil, nonce, ciphertext, nil)
}

// newAEAD constructs the AEAD named by cipherName; the empty marker selects
// the legacy AES-256-GCM read path.
func newAEAD(key []byte, cipherName string) (cipher.AEAD, error) {
	if len(key) != walletKeyLen {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", walletKeyLen, len(key))
	}
	switch cipherName {
	case "", cipherAES256GCM:
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	case cipherXChaCha20Poly1305:
		return chacha20poly1305.NewX(key)
	default:
		return nil, fmt.Errorf("unknown wallet cipher %q", cipherName)
	}
}

func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// DeriveAddress computes the EIP-55 checksummed address for a private key:
// keccak256(uncompressed pubkey sans 0x04)[12:] → checksum.
func DeriveAddress(priv *secp256k1.PrivateKey) string {
	pub := priv.PubKey().SerializeUncompressed() // 0x04 || X || Y
	h := Keccak256(pub[1:])
	return ChecksumAddress(h[12:])
}

// ChecksumAddress renders 20 bytes as an EIP-55 mixed-case 0x address.
func ChecksumAddress(addr []byte) string {
	lower := hex.EncodeToString(addr)
	digest := Keccak256([]byte(lower))
	var b strings.Builder
	b.WriteString("0x")
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		// Nibble i of the keccak digest: even i = high nibble, odd = low.
		nibble := (digest[i/2] >> (4 * (1 - uint(i%2)))) & 0x0f
		if c >= 'a' && c <= 'f' && nibble >= 8 {
			c -= 32
		}
		b.WriteByte(c)
	}
	return b.String()
}

// NormalizeAddress validates and EIP-55-normalizes an address string.
func NormalizeAddress(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !IsAddress(s) {
		return "", fmt.Errorf("invalid address %q", s)
	}
	raw, err := hex.DecodeString(s[2:])
	if err != nil {
		return "", fmt.Errorf("invalid address %q", s)
	}
	return ChecksumAddress(raw), nil
}

// Keccak256 returns the legacy Keccak-256 digest (Ethereum variant, not
// FIPS SHA-3).
func Keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

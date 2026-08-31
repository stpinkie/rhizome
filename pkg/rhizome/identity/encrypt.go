package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/scrypt"
)

// Default keyring identifiers. These are combined into a single target name
// on Windows, so they must remain stable.
const (
	keyringService = "github.com/stpinkie/rhizome"
	keyringAccount = "node-identity"
	scryptN        = 32768
	scryptR        = 8
	scryptP        = 1
	keyLen         = 32
)

// KeyProvider supplies the 32-byte symmetric key needed to decrypt an
// encrypted node identity.
type KeyProvider interface {
	Key(ni *NodeIdentity) ([]byte, error)
}

// KeyringProvider retrieves the key from the OS credential store.
// On macOS this is Keychain, on Windows Credential Manager, and on Linux the
// Secret Service D-Bus API (GNOME Keyring / kwallet).
type KeyringProvider struct {
	// Service is the application name. If empty, the Rhizome default is used.
	Service string
	// Account identifies the identity entry. If empty, the default is used.
	Account string
}

// Key returns the stored base64-encoded key, or an error if the keyring
// cannot be reached or has no entry for this identity.
func (k *KeyringProvider) Key(ni *NodeIdentity) ([]byte, error) {
	service := k.Service
	if service == "" {
		service = keyringService
	}
	account := k.Account
	if account == "" {
		account = keyringAccount + "-" + ni.PeerID
	}

	secret, err := keyring.Get(service, account)
	if err != nil {
		return nil, fmt.Errorf("keyring get: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return nil, fmt.Errorf("keyring secret is not base64: %w", err)
	}
	if len(key) != keyLen {
		return nil, fmt.Errorf("keyring secret has wrong length: %d", len(key))
	}
	return key, nil
}

// StoreKey persists a new random key in the OS keyring.
func (k *KeyringProvider) StoreKey(ni *NodeIdentity, key []byte) error {
	service := k.Service
	if service == "" {
		service = keyringService
	}
	account := k.Account
	if account == "" {
		account = keyringAccount + "-" + ni.PeerID
	}
	return keyring.Set(service, account, base64.StdEncoding.EncodeToString(key))
}

// DeleteKey removes the key from the OS keyring. Used mainly in tests.
func (k *KeyringProvider) DeleteKey(ni *NodeIdentity) error {
	service := k.Service
	if service == "" {
		service = keyringService
	}
	account := k.Account
	if account == "" {
		account = keyringAccount + "-" + ni.PeerID
	}
	return keyring.Delete(service, account)
}

// ScryptProvider derives the key from a user passphrase and the salt stored in
// the NodeIdentity. It is used as a fallback when the OS keyring is
// unavailable, or when the user explicitly chose passphrase encryption.
type ScryptProvider struct {
	Passphrase string
}

// Key derives the 32-byte AES key using scrypt.
func (s *ScryptProvider) Key(ni *NodeIdentity) ([]byte, error) {
	if ni.Salt == "" {
		return nil, fmt.Errorf("no salt in encrypted identity")
	}
	salt, err := base64.StdEncoding.DecodeString(ni.Salt)
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}
	return scrypt.Key([]byte(s.Passphrase), salt, scryptN, scryptR, scryptP, keyLen)
}

// encrypt seals the private key with AES-256-GCM and returns the nonce and
// ciphertext separately.
func encrypt(privateKey, key []byte) (nonce, ciphertext []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("new gcm: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("read nonce: %w", err)
	}
	ciphertext = gcm.Seal(nonce, nonce, privateKey, nil)
	return nonce, ciphertext, nil
}

// decrypt opens the AES-256-GCM ciphertext.
func decrypt(ciphertext, key []byte) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, fmt.Errorf("ciphertext is empty")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
}

// generateKey returns a 32-byte random key.
func generateKey() ([]byte, error) {
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

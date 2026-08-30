package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ErrIdentityEncrypted is returned by Load when node.json is encrypted and no
// KeyProvider was supplied.
var ErrIdentityEncrypted = errors.New("node identity is encrypted; provide a key provider or set RHIZOME_IDENTITY_PASSPHRASE")

// NodeIdentity is the on-disk representation of a node identity.
// It intentionally does not store the BIP39 mnemonic or root seed.
//
// When Encrypted is false, PrivateKey holds the base64-encoded raw private
// key. When Encrypted is true, PrivateKey is empty and Ciphertext/Nonce hold
// the encrypted key; the actual key is obtained from a KeyProvider.
type NodeIdentity struct {
	NodeIndex  uint32 `json:"node_index"`
	NodeName   string `json:"node_name"`
	PeerID     string `json:"peer_id"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key,omitempty"`
	Encrypted  bool   `json:"encrypted"`
	KeySource  string `json:"key_source,omitempty"` // "keyring" or "scrypt"
	Salt       string `json:"salt,omitempty"`       // base64, set when KeySource is "scrypt"
	Nonce      string `json:"nonce,omitempty"`      // base64, set when Encrypted
	Ciphertext string `json:"ciphertext,omitempty"` // base64, set when Encrypted
}

// Save writes the derived identity to identityDir as node.json without
// encryption. This preserves the legacy behaviour for callers that do not need
// encryption.
func Save(identityDir string, d *Derived, name string) error {
	if err := os.MkdirAll(identityDir, 0700); err != nil {
		return fmt.Errorf("create identity directory: %w", err)
	}

	ni := NodeIdentity{
		NodeIndex:  d.NodeIndex,
		NodeName:   name,
		PeerID:     d.PeerID,
		PublicKey:  base64.StdEncoding.EncodeToString(d.PublicKey),
		PrivateKey: base64.StdEncoding.EncodeToString(d.PrivateKey),
	}

	return writeNodeIdentity(identityDir, ni)
}

// SaveEncrypted writes the derived identity to identityDir as an encrypted
// node.json. The key is obtained from the KeyProvider and, depending on
// keySource, may be persisted to the OS keyring or derived from a passphrase.
func SaveEncrypted(identityDir string, d *Derived, name string, provider KeyProvider, keySource string) error {
	if err := os.MkdirAll(identityDir, 0700); err != nil {
		return fmt.Errorf("create identity directory: %w", err)
	}

	// For scrypt, generate a random salt so the provider can derive the key.
	var salt []byte
	if _, ok := provider.(*ScryptProvider); ok {
		salt = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return fmt.Errorf("read salt: %w", err)
		}
	}

	ni := NodeIdentity{
		NodeIndex: d.NodeIndex,
		NodeName:  name,
		PeerID:    d.PeerID,
		PublicKey: base64.StdEncoding.EncodeToString(d.PublicKey),
		Encrypted: true,
		KeySource: keySource,
	}
	if salt != nil {
		ni.Salt = base64.StdEncoding.EncodeToString(salt)
	}

	key, err := provider.Key(&ni)
	if err != nil {
		return fmt.Errorf("obtain encryption key: %w", err)
	}
	if len(key) != 32 {
		return fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}

	nonce, ciphertext, err := encrypt(d.PrivateKey, key)
	if err != nil {
		return fmt.Errorf("encrypt private key: %w", err)
	}

	ni.Nonce = base64.StdEncoding.EncodeToString(nonce)
	ni.Ciphertext = base64.StdEncoding.EncodeToString(ciphertext)

	return writeNodeIdentity(identityDir, ni)
}

// SaveEncryptedWithKeyring generates a random key, stores it in the OS
// keyring, and writes an encrypted node.json.
func SaveEncryptedWithKeyring(identityDir string, d *Derived, name string) error {
	key, err := generateKey()
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}

	kp := &KeyringProvider{}
	ni := &NodeIdentity{PeerID: d.PeerID}
	if err := kp.StoreKey(ni, key); err != nil {
		return fmt.Errorf("store key in keyring: %w", err)
	}

	return SaveEncrypted(identityDir, d, name, &KeyringProvider{}, "keyring")
}

// SaveEncryptedWithPassphrase derives a key from the given passphrase using
// scrypt, then writes an encrypted node.json.
func SaveEncryptedWithPassphrase(identityDir string, d *Derived, name, passphrase string) error {
	return SaveEncrypted(identityDir, d, name, &ScryptProvider{Passphrase: passphrase}, "scrypt")
}

// Load reads the saved identity and reconstructs a Derived value. If the file
// is encrypted, ErrIdentityEncrypted is returned unless a KeyProvider is
// supplied via LoadWithProvider.
func Load(identityDir string) (*Derived, string, error) {
	return LoadWithProvider(identityDir, nil)
}

// LoadWithProvider reads the saved identity. If it is encrypted, the provider
// is used to obtain the decryption key.
func LoadWithProvider(identityDir string, provider KeyProvider) (*Derived, string, error) {
	path := filepath.Join(identityDir, "node.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read node identity: %w", err)
	}

	var ni NodeIdentity
	if err := json.Unmarshal(data, &ni); err != nil {
		return nil, "", fmt.Errorf("unmarshal node identity: %w", err)
	}

	var privBytes []byte
	if ni.Encrypted {
		if provider == nil {
			return nil, "", ErrIdentityEncrypted
		}
		key, err := provider.Key(&ni)
		if err != nil {
			return nil, "", fmt.Errorf("obtain decryption key: %w", err)
		}
		cipherBytes, err := base64.StdEncoding.DecodeString(ni.Ciphertext)
		if err != nil {
			return nil, "", fmt.Errorf("decode ciphertext: %w", err)
		}
		privBytes, err = decrypt(cipherBytes, key)
		if err != nil {
			return nil, "", fmt.Errorf("decrypt private key: %w", err)
		}
	} else {
		privBytes, err = base64.StdEncoding.DecodeString(ni.PrivateKey)
		if err != nil {
			return nil, "", fmt.Errorf("decode private key: %w", err)
		}
	}

	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, "", fmt.Errorf("invalid ed25519 private key size: %d", len(privBytes))
	}

	priv := ed25519.PrivateKey(privBytes)
	pub := priv.Public().(ed25519.PublicKey)

	libp2pPriv, err := crypto.UnmarshalEd25519PrivateKey(priv)
	if err != nil {
		return nil, "", fmt.Errorf("unmarshal libp2p private key: %w", err)
	}

	libp2pPub := libp2pPriv.GetPublic()
	pid, err := peer.IDFromPublicKey(libp2pPub)
	if err != nil {
		return nil, "", fmt.Errorf("derive peer id: %w", err)
	}

	if pid.String() != ni.PeerID {
		return nil, "", fmt.Errorf("loaded peer id %s does not match stored %s", pid.String(), ni.PeerID)
	}

	return &Derived{
		NodeIndex:     ni.NodeIndex,
		PrivateKey:    priv,
		PublicKey:     pub,
		Libp2pPrivKey: libp2pPriv,
		Libp2pPubKey:  libp2pPub,
		PeerID:        pid.String(),
	}, ni.NodeName, nil
}

func writeNodeIdentity(identityDir string, ni NodeIdentity) error {
	data, err := json.MarshalIndent(ni, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal node identity: %w", err)
	}

	path := filepath.Join(identityDir, "node.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write node identity: %w", err)
	}
	return nil
}

package identity

import (
	"crypto/ed25519"
	"crypto/sha512"
	"fmt"
	"io"

	"github.com/anyproto/go-slip10"
	"github.com/cosmos/go-bip39"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/crypto/hkdf"
)

// Default network constants for BIP44/SLIP-0010 derivation.
const (
	// DefaultCoinType is a project-specific BIP44 coin type constant.
	// It is not registered at SLIP-44 and should only be used for Rhizome
	// agent networks with this mnemonic scheme.
	DefaultCoinType = 123456

	// DefaultKeyPathTemplate is the SLIP-0010 derivation path for a node.
	// All path components are hardened, which is required for Ed25519.
	// Parameters: coin type, node index.
	DefaultKeyPathTemplate = "m/44'/%d'/0'/%d'"

	// NetworkKeyInfo is the HKDF info string used to derive the network
	// symmetric key from the BIP39 seed.
	NetworkKeyInfo = "rhizome-network-key"
)

// Derived holds the cryptographic material for one Rhizome node.
type Derived struct {
	// NodeIndex is the SLIP-0010 child index used for this node.
	NodeIndex uint32

	// Ed25519 raw and libp2p-wrapped keys.
	PrivateKey    ed25519.PrivateKey
	PublicKey     ed25519.PublicKey
	Libp2pPrivKey crypto.PrivKey
	Libp2pPubKey  crypto.PubKey
	PeerID        string
}

// FromMnemonic derives a node's identity from a BIP39 mnemonic and index.
// The returned networkKey is a 32-byte AES-256-GCM key derived from the seed
// and may be used to encrypt shared network secrets.
func FromMnemonic(mnemonic string, nodeIndex uint32) (*Derived, []byte, error) {
	if !bip39.IsMnemonicValid(mnemonic) {
		return nil, nil, fmt.Errorf("invalid BIP39 mnemonic")
	}

	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		return nil, nil, fmt.Errorf("mnemonic to seed: %w", err)
	}

	path := fmt.Sprintf(DefaultKeyPathTemplate, DefaultCoinType, nodeIndex)
	node, err := slip10.DeriveForPath(path, seed)
	if err != nil {
		return nil, nil, fmt.Errorf("derive SLIP-0010 path %s: %w", path, err)
	}

	pub, priv := node.Keypair()

	libp2pPriv, err := crypto.UnmarshalEd25519PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("unmarshal ed25519 private key: %w", err)
	}

	libp2pPub := libp2pPriv.GetPublic()
	pid, err := peer.IDFromPublicKey(libp2pPub)
	if err != nil {
		return nil, nil, fmt.Errorf("derive peer id: %w", err)
	}

	networkKey, err := deriveNetworkKey(seed)
	if err != nil {
		return nil, nil, fmt.Errorf("derive network key: %w", err)
	}

	return &Derived{
		NodeIndex:     nodeIndex,
		PrivateKey:    priv,
		PublicKey:     pub,
		Libp2pPrivKey: libp2pPriv,
		Libp2pPubKey:  libp2pPub,
		PeerID:        pid.String(),
	}, networkKey, nil
}

// deriveNetworkKey derives a 32-byte symmetric key from the BIP39 seed.
func deriveNetworkKey(seed []byte) ([]byte, error) {
	r := hkdf.New(sha512.New, seed, nil, []byte(NetworkKeyInfo))
	key := make([]byte, 32)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

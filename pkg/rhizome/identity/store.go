package identity

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// NodeIdentity is the on-disk representation of a node's identity.
// It intentionally does not store the BIP39 mnemonic or root seed.
type NodeIdentity struct {
	NodeIndex  uint32 `json:"node_index"`
	NodeName   string `json:"node_name"`
	PeerID     string `json:"peer_id"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// Save writes the derived identity to identityDir as node.json.
// The private key is base64-encoded. File permissions are set to 0600.
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

// Load reads the saved identity and reconstructs a Derived value.
func Load(identityDir string) (*Derived, string, error) {
	path := filepath.Join(identityDir, "node.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read node identity: %w", err)
	}

	var ni NodeIdentity
	if err := json.Unmarshal(data, &ni); err != nil {
		return nil, "", fmt.Errorf("unmarshal node identity: %w", err)
	}

	privBytes, err := base64.StdEncoding.DecodeString(ni.PrivateKey)
	if err != nil {
		return nil, "", fmt.Errorf("decode private key: %w", err)
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

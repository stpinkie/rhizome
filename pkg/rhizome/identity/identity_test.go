package identity

import (
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestFromMnemonicDeterministic(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

	d1, k1, err := FromMnemonic(mnemonic, 0)
	if err != nil {
		t.Fatalf("FromMnemonic first call failed: %v", err)
	}

	d2, k2, err := FromMnemonic(mnemonic, 0)
	if err != nil {
		t.Fatalf("FromMnemonic second call failed: %v", err)
	}

	if d1.PeerID != d2.PeerID {
		t.Fatalf("peer id not deterministic: %s vs %s", d1.PeerID, d2.PeerID)
	}

	if string(d1.PublicKey) != string(d2.PublicKey) {
		t.Fatalf("public key not deterministic")
	}

	if string(k1) != string(k2) {
		t.Fatalf("network key not deterministic")
	}

	if _, err := peer.Decode(d1.PeerID); err != nil {
		t.Fatalf("peer id %q is not valid: %v", d1.PeerID, err)
	}
}

func TestFromMnemonicDifferentIndex(t *testing.T) {
	mnemonic := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

	d0, _, err := FromMnemonic(mnemonic, 0)
	if err != nil {
		t.Fatalf("FromMnemonic(0) failed: %v", err)
	}

	d1, _, err := FromMnemonic(mnemonic, 1)
	if err != nil {
		t.Fatalf("FromMnemonic(1) failed: %v", err)
	}

	if d0.PeerID == d1.PeerID {
		t.Fatalf("different node indices must produce different peer ids")
	}

	if string(d0.PublicKey) == string(d1.PublicKey) {
		t.Fatalf("different node indices must produce different public keys")
	}
}

func TestFromMnemonicInvalid(t *testing.T) {
	if _, _, err := FromMnemonic("not a valid mnemonic", 0); err == nil {
		t.Fatalf("expected error for invalid mnemonic")
	}
}

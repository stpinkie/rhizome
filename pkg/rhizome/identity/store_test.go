package identity

import (
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	d, _, err := FromMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 0)
	if err != nil {
		t.Fatalf("FromMnemonic failed: %v", err)
	}

	dir := t.TempDir()
	if err := Save(dir, d, "test-node"); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, name, err := Load(dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if name != "test-node" {
		t.Fatalf("node name mismatch: got %q, want %q", name, "test-node")
	}

	if loaded.PeerID != d.PeerID {
		t.Fatalf("peer id mismatch: got %s, want %s", loaded.PeerID, d.PeerID)
	}

	if string(loaded.PublicKey) != string(d.PublicKey) {
		t.Fatalf("public key mismatch")
	}

	if filepath.Join(dir, "node.json") == "" {
		t.Fatalf("path should not be empty")
	}
}

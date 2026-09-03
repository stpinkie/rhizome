package network

import (
	"testing"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

func TestPeerIDFromMultiaddr(t *testing.T) {
	derived, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		0,
	)
	if err != nil {
		t.Fatalf("derive identity: %v", err)
	}

	addr := "/ip4/127.0.0.1/tcp/4001/p2p/" + derived.PeerID
	pid, err := PeerIDFromMultiaddr(addr)
	if err != nil {
		t.Fatalf("PeerIDFromMultiaddr: %v", err)
	}
	if pid.String() != derived.PeerID {
		t.Fatalf("pid = %s, want %s", pid.String(), derived.PeerID)
	}

	if _, err := PeerIDFromMultiaddr("not-a-multiaddr"); err == nil {
		t.Fatal("expected error for invalid multiaddr")
	}
	if _, err := PeerIDFromMultiaddr("/ip4/127.0.0.1/tcp/4001"); err == nil {
		t.Fatal("expected error for missing /p2p/")
	}
}

func TestAppendUnique(t *testing.T) {
	items := []string{"a", "b"}
	items = AppendUnique(items, "b")
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	items = AppendUnique(items, "c")
	if len(items) != 3 || items[2] != "c" {
		t.Fatalf("items = %v, want [a b c]", items)
	}
}

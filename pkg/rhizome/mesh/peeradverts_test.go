package mesh

import (
	"encoding/json"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/peeradverts"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// SetCapability journals each peer's module adverts to
// <RHIZOME_HOME>/peer-adverts.json — the file seam companion modules read
// for direct-peer discovery. A bare Mesh suffices: the journal write only
// touches caps/trust state and the home directory.
func TestSetCapabilityJournalsModuleAdverts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RHIZOME_HOME", home)
	m := &Mesh{
		caps:  map[peer.ID]Capability{},
		trust: map[peer.ID]bool{},
	}
	pid, err := peer.Decode(testutil.NewIdentity(t).PeerID)
	if err != nil {
		t.Fatal(err)
	}
	adv := map[string]json.RawMessage{
		"rhizome-market": json.RawMessage(`{"v":1,"offers":[{"id":"o1"}]}`),
	}
	m.SetCapability(pid, Capability{ModuleAdverts: adv})

	rows, err := peeradverts.Load(home)
	if err != nil || len(rows) != 1 {
		t.Fatalf("journal: %v rows=%v", err, rows)
	}
	if rows[0].PeerID != pid.String() {
		t.Fatalf("peer %q", rows[0].PeerID)
	}
	if _, ok := rows[0].Adverts["rhizome-market"]; !ok {
		t.Fatal("module advert not journaled")
	}

	// Trusted peers journal trusted: true; the daemon's trust list feeds it.
	m.trust[pid] = true
	m.SetCapability(pid, Capability{ModuleAdverts: adv})
	rows, _ = peeradverts.Load(home)
	if len(rows) != 1 || !rows[0].Trusted {
		t.Fatalf("trusted flag not recorded: %+v", rows)
	}

	// A capability carrying no module adverts clears the row's adverts.
	m.SetCapability(pid, Capability{})
	rows, _ = peeradverts.Load(home)
	if len(rows) != 1 || rows[0].Adverts != nil {
		t.Fatalf("empty adverts should clear the row: %+v", rows)
	}
}

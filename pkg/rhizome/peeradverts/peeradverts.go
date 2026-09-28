// Package peeradverts is the file contract between the mesh (writer) and
// companion modules (readers): a bounded journal of the module capability
// adverts each peer has most recently presented.
//
// The mesh records every verified capability's ModuleAdverts into
// <RHIZOME_HOME>/peer-adverts.json (0600, atomic whole-file writes). A
// module such as rhizome-market reads the journal to discover direct-peer
// providers without reaching into mesh internals — the same file-posture
// the bridge token, api.addr, and web3-pending.json contracts use.
package peeradverts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

const (
	// FileName is the journal's fixed name under the Rhizome home.
	FileName = "peer-adverts.json"
	// maxPeers bounds the journal — beyond it the oldest received_at
	// rows are dropped first.
	maxPeers = 256
	// staleAfter is when a peer's row stops being loadable; the sweep
	// also drops it from the file on the next write.
	staleAfter = 7 * 24 * time.Hour
)

// Row is one peer's journaled adverts: the module id → raw advert map
// the peer's capability manifest carried, plus trust/staleness metadata
// the consumer reports honestly rather than inferring.
type Row struct {
	PeerID     string                     `json:"peer_id"`
	Trusted    bool                       `json:"trusted"`
	ReceivedAt time.Time                  `json:"received_at"`
	Adverts    map[string]json.RawMessage `json:"adverts,omitempty"`
}

// Expired reports whether the row is past the staleness horizon.
func (r Row) Expired(now time.Time) bool { return now.Sub(r.ReceivedAt) > staleAfter }

type file struct {
	Version int    `json:"version"`
	Peers   []*Row `json:"peers"`
}

// Path returns the journal location under a Rhizome home.
func Path(home string) string { return filepath.Join(home, FileName) }

// Record upserts (or, when adverts is empty, removes) the peer's row and
// atomically rewrites the journal. A nil/empty home disables journalling
// — the caller's environment isn't rooted (tests, daemonless paths).
func Record(home, peerID string, trusted bool, adverts map[string]json.RawMessage) error {
	if home == "" || peerID == "" {
		return nil
	}
	f, err := read(Path(home))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	f.Peers = sweep(f.Peers, now)
	var row *Row
	for _, p := range f.Peers {
		if p.PeerID == peerID {
			row = p
			break
		}
	}
	if len(adverts) == 0 {
		if row == nil {
			return nil // nothing to record or remove
		}
		row.Trusted = trusted
		row.ReceivedAt = now
		row.Adverts = nil
	} else {
		if row == nil {
			row = &Row{PeerID: peerID}
			f.Peers = append(f.Peers, row)
		}
		row.Trusted = trusted
		row.ReceivedAt = now
		row.Adverts = adverts
	}
	bound(f)
	return write(Path(home), f)
}

// Load returns the journal's rows — stale rows included (consumers decide
// whether an expired advert is still worth listing with an expired flag).
// A missing or unparseable file yields (nil, nil): no peers have shared
// adverts, which is a normal empty state, not an error.
func Load(home string) ([]Row, error) {
	if home == "" {
		return nil, nil
	}
	f, err := read(Path(home))
	if err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(f.Peers))
	for _, p := range f.Peers {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	return out, nil
}

// sweep drops rows older than the staleness horizon.
func sweep(peers []*Row, now time.Time) []*Row {
	out := peers[:0]
	for _, p := range peers {
		if now.Sub(p.ReceivedAt) <= staleAfter {
			out = append(out, p)
		}
	}
	return out
}

// bound enforces maxPeers by dropping the oldest rows first.
func bound(f *file) {
	if len(f.Peers) <= maxPeers {
		return
	}
	sort.SliceStable(f.Peers, func(i, j int) bool {
		return f.Peers[i].ReceivedAt.Before(f.Peers[j].ReceivedAt)
	})
	f.Peers = f.Peers[len(f.Peers)-maxPeers:]
}

func read(path string) (*file, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is Path(home) — the daemon-owned journal location
	if os.IsNotExist(err) {
		return &file{Version: 1}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read peer adverts: %w", err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		//nolint:nilerr // A corrupt journal is not fatal — the writer
		// starts fresh and readers see an empty market until re-announce.
		return &file{Version: 1}, nil
	}
	if f.Version == 0 {
		f.Version = 1
	}
	return &f, nil
}

func write(path string, f *file) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

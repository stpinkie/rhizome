package mesh

import (
	"fmt"
	"sort"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/config"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
)

// SavedPeer describes a persistent mesh peer from configuration, optionally
// augmented with live state from a running Mesh.
type SavedPeer struct {
	PeerID         string         `json:"peer_id"`
	BootstrapAddrs []string       `json:"bootstrap_addrs,omitempty"`
	Trusted        bool           `json:"trusted"`
	Connected      bool           `json:"connected"`
	Capability     PeerCapability `json:"capability,omitempty"`
}

// SavedPeersResponse is the payload returned by /network/saved-peers.
type SavedPeersResponse struct {
	PeerID     string      `json:"peer_id"`
	SavedPeers []SavedPeer `json:"saved_peers"`
}

// BuildSavedPeersResponse merges persistent config peers with optional live
// mesh state. When m is nil, only config data is returned.
func BuildSavedPeersResponse(m *Mesh, cfg *config.Config, includeStatus bool) SavedPeersResponse {
	resp := SavedPeersResponse{}
	if m != nil {
		resp.PeerID = m.NetworkStatus("").PeerID
	}

	peers := buildSavedPeersFromConfig(cfg)
	if includeStatus && m != nil {
		for i := range peers {
			p := &peers[i]
			pid, err := peer.Decode(p.PeerID)
			if err != nil {
				continue
			}
			p.Connected = m.IsConnected(pid)
			if cap, ok := m.PeerCapabilities(pid); ok {
				p.Capability = PeerCapability{
					Models: cap.Models,
					Skills: cap.Skills,
					Agents: cap.Agents,
				}
			}
		}
	}

	resp.SavedPeers = peers
	return resp
}

// BuildSavedPeerForPID returns the saved peer for a specific peer id, with
// optional live mesh state.
func BuildSavedPeerForPID(m *Mesh, cfg *config.Config, pid peer.ID, includeStatus bool) SavedPeer {
	peers := buildSavedPeersFromConfig(cfg)
	id := pid.String()
	for _, p := range peers {
		if p.PeerID == id {
			if includeStatus && m != nil {
				p.Connected = m.IsConnected(pid)
				if cap, ok := m.PeerCapabilities(pid); ok {
					p.Capability = PeerCapability{
						Models: cap.Models,
						Skills: cap.Skills,
						Agents: cap.Agents,
					}
				}
			}
			return p
		}
	}
	return SavedPeer{PeerID: id}
}

// UntrustSavedPeer loads config at configPath, removes pid from
// mesh.trusted_peers, and writes the file back. mu serializes callers within
// this process and may be nil.
func UntrustSavedPeer(configPath string, mu *sync.Mutex, pid peer.ID) error {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	peerID := pid.String()
	cfg.Mesh.TrustedPeers = filterStrings(cfg.Mesh.TrustedPeers, func(s string) bool { return s != peerID })

	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// RemoveSavedPeer loads config at configPath, removes pid from
// mesh.trusted_peers and any matching mesh.bootstrap_peers, and writes the file
// back. mu serializes callers within this process and may be nil. It returns
// true if the peer was present in either list and removed.
//
// Cross-process race: CLI, launcher, and daemon writes may overwrite each other.
// config.SaveConfig writes atomically, so the file will not be corrupted, but
// one side's edits may be lost (last writer wins).
func RemoveSavedPeer(configPath string, mu *sync.Mutex, pid peer.ID) (bool, error) {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return false, fmt.Errorf("load config: %w", err)
	}

	peerID := pid.String()
	existed := false
	for _, p := range cfg.Mesh.TrustedPeers {
		if p == peerID {
			existed = true
			break
		}
	}
	for _, b := range cfg.Mesh.BootstrapPeers {
		got, err := rnet.PeerIDFromMultiaddr(b)
		if err == nil && got.String() == peerID {
			existed = true
			break
		}
	}

	cfg.Mesh.TrustedPeers = filterStrings(cfg.Mesh.TrustedPeers, func(s string) bool { return s != peerID })
	cfg.Mesh.BootstrapPeers = filterStrings(cfg.Mesh.BootstrapPeers, func(b string) bool {
		got, err := rnet.PeerIDFromMultiaddr(b)
		return err != nil || got.String() != peerID
	})

	if err := config.SaveConfig(configPath, cfg); err != nil {
		return false, fmt.Errorf("save config: %w", err)
	}
	return existed, nil
}

func buildSavedPeersFromConfig(cfg *config.Config) []SavedPeer {
	m := make(map[string]*SavedPeer)

	for _, p := range cfg.Mesh.TrustedPeers {
		if p == "" {
			continue
		}
		if _, ok := m[p]; !ok {
			m[p] = &SavedPeer{PeerID: p}
		}
		m[p].Trusted = true
	}

	for _, b := range cfg.Mesh.BootstrapPeers {
		if b == "" {
			continue
		}
		pid, err := rnet.PeerIDFromMultiaddr(b)
		if err != nil {
			continue
		}
		id := pid.String()
		if _, ok := m[id]; !ok {
			m[id] = &SavedPeer{PeerID: id}
		}
		m[id].BootstrapAddrs = rnet.AppendUnique(m[id].BootstrapAddrs, b)
	}

	out := make([]SavedPeer, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	return out
}

func filterStrings(items []string, keep func(string) bool) []string {
	out := items[:0]
	for _, s := range items {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

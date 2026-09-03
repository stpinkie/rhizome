package network

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	rhizomeidentity "github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
)

type peerStatus struct {
	PeerID     string   `json:"peer_id"`
	Addrs      []string `json:"addrs"`
	Trusted    bool     `json:"trusted"`
	Capability struct {
		Models []string `json:"models,omitempty"`
		Skills []string `json:"skills,omitempty"`
		Agents []string `json:"agents,omitempty"`
	} `json:"capability,omitempty"`
}

// NewStatusCommand returns the network status command.
func NewStatusCommand() *cobra.Command {
	var (
		asJSON     bool
		showPeers  bool
		showDHT    bool
		bootstraps []string
		listen     []string
		timeout    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the Rhizome node identity, peers, and DHT status",
		Long:  "Show the Rhizome node identity. With --peers or --dht, start a temporary P2P node and query live mesh/DHT status.",
		Run: func(cmd *cobra.Command, args []string) {
			home := config.GetHome()
			identityDir := filepath.Join(home, "identity")

			derived, name, err := internal.LoadIdentity(identityDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "No node identity found at %s\n", identityDir)
				fmt.Fprintf(os.Stderr, "Run: rhizome network onboard\n")
				os.Exit(1)
			}

			if !showPeers && !showDHT {
				printIdentity(name, derived, identityDir, asJSON)
				return
			}

			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				cfg = config.DefaultConfig()
			}
			config.SetGlobal(cfg)

			if timeout <= 0 {
				timeout = 15 * time.Second
			}
			if len(listen) == 0 {
				listen = []string{"/ip4/127.0.0.1/tcp/0"}
			}

			dhtEnabled := cfg.Mesh.DHTEnabled || showDHT
			nodeCfg := rnet.Config{
				ListenAddrs:    listen,
				BootstrapPeers: bootstraps,
				DHT: rnet.DHTConfig{
					Enabled:           dhtEnabled,
					Server:            false,
					Rendezvous:        cfg.Mesh.DHTRendezvous,
					BootstrapPeers:    append([]string(nil), cfg.Mesh.DHTBootstrap...),
					ReprovideInterval: cfg.Mesh.DHTReprovideInterval,
				},
				Timeouts: &cfg.Timeouts.Network,
			}
			if showDHT {
				nodeCfg.DHT.Enabled = true
				if nodeCfg.DHT.Rendezvous == "" {
					nodeCfg.DHT.Rendezvous = config.DefaultMeshConfig().DHTRendezvous
				}
				if nodeCfg.DHT.ReprovideInterval <= 0 {
					nodeCfg.DHT.ReprovideInterval = config.DefaultMeshConfig().DHTReprovideInterval
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			node, err := rnet.NewNode(ctx, derived.Libp2pPrivKey, nodeCfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to start node: %v\n", err)
				os.Exit(1)
			}
			defer node.Close()

			var rhizomeMesh *mesh.Mesh
			if cfg.Mesh.Enabled && showPeers {
				meshCfg := cfg.Mesh
				// Status is read-only; do not accept remote execution from a status probe.
				meshCfg.AllowRemoteDelegate = false
				meshCfg.AllowRemoteSpawn = false
				rhizomeMesh = mesh.NewMesh(node, nil, derived, meshCfg, nil)
				if cfg.Mesh.AdvertiseModels {
					rhizomeMesh.SetModelList(cfg.ModelList)
				}
				if cfg.Mesh.AdvertiseSkills {
					rhizomeMesh.SetSkillsLoader(nil)
				}
				for _, p := range cfg.Mesh.TrustedPeers {
					if pid, err := peer.Decode(p); err == nil {
						rhizomeMesh.TrustPeer(pid)
					}
				}
				// Trust explicit bootstrap peers so we can receive their capabilities.
				for _, addr := range bootstraps {
					if pid, err := peerIDFromMultiaddr(addr); err == nil {
						rhizomeMesh.TrustPeer(pid)
					}
				}
				if err := rhizomeMesh.Start(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to start mesh: %v\n", err)
					os.Exit(1)
				}
				defer rhizomeMesh.Stop()
			}

			if !waitForAnyConnectedPeer(ctx, node, timeout) {
				fmt.Fprintln(os.Stderr, "No connected peers found. Check --bootstrap or DHT configuration.")
			}

			out := map[string]any{
				"name":       name,
				"node_index": derived.NodeIndex,
				"peer_id":    derived.PeerID,
				"identity":   identityDir,
			}
			if showPeers {
				out["peers"] = buildPeerStatus(node, rhizomeMesh)
			}
			if showDHT {
				out["dht"] = node.DHTStatus()
			}

			if asJSON {
				data, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error encoding status: %v\n", err)
					os.Exit(1)
				}
				fmt.Println(string(data))
				return
			}

			fmt.Printf("Node:      %s\n", name)
			fmt.Printf("Index:     %d\n", derived.NodeIndex)
			fmt.Printf("Peer ID:   %s\n", derived.PeerID)
			fmt.Printf("Identity:  %s\n", identityDir)
			if showPeers {
				printPeerStatus(out["peers"].([]peerStatus))
			}
			if showDHT {
				printDHTStatus(out["dht"].(rnet.DHTStatus))
			}
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "Print status as JSON")
	cmd.Flags().BoolVar(&showPeers, "peers", false, "Show live connected peers and capabilities")
	cmd.Flags().BoolVar(&showDHT, "dht", false, "Show DHT status")
	cmd.Flags().StringArrayVar(&bootstraps, "bootstrap", nil, "Bootstrap peer multiaddr(s)")
	cmd.Flags().StringArrayVar(&listen, "listen", []string{"/ip4/127.0.0.1/tcp/0"}, "Listen multiaddr(s)")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Second, "Timeout for peer/DHT discovery")
	return cmd
}

func printIdentity(name string, derived *rhizomeidentity.Derived, identityDir string, asJSON bool) {
	if asJSON {
		out := map[string]any{
			"name":       name,
			"node_index": derived.NodeIndex,
			"peer_id":    derived.PeerID,
			"identity":   identityDir,
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding status: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}

	fmt.Printf("Node:      %s\n", name)
	fmt.Printf("Index:     %d\n", derived.NodeIndex)
	fmt.Printf("Peer ID:   %s\n", derived.PeerID)
	fmt.Printf("Identity:  %s\n", identityDir)
}

func waitForAnyConnectedPeer(ctx context.Context, node *rnet.Node, timeout time.Duration) bool {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		if len(node.ConnectedPeers()) > 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func buildPeerStatus(node *rnet.Node, m *mesh.Mesh) []peerStatus {
	peers := node.ConnectedPeers()
	out := make([]peerStatus, 0, len(peers))

	for _, pid := range peers {
		ps := peerStatus{PeerID: pid.String()}
		for _, a := range node.Host().Peerstore().Addrs(pid) {
			ps.Addrs = append(ps.Addrs, a.String())
		}
		if m != nil {
			ps.Trusted = m.IsTrusted(pid)
			if cap, ok := m.PeerCapabilities(pid); ok {
				ps.Capability.Models = cap.Models
				ps.Capability.Skills = cap.Skills
				ps.Capability.Agents = cap.Agents
			}
		}
		out = append(out, ps)
	}
	return out
}

func printPeerStatus(peers []peerStatus) {
	if len(peers) == 0 {
		fmt.Println("No connected peers.")
		return
	}
	fmt.Println("Connected peers:")
	for _, p := range peers {
		fmt.Printf("  - %s", p.PeerID)
		if p.Trusted {
			fmt.Print(" (trusted)")
		}
		fmt.Println()
		if len(p.Addrs) > 0 {
			fmt.Printf("      addrs: %s\n", strings.Join(p.Addrs, ", "))
		}
		if len(p.Capability.Models) > 0 {
			fmt.Printf("      models: %s\n", strings.Join(p.Capability.Models, ", "))
		}
		if len(p.Capability.Skills) > 0 {
			fmt.Printf("      skills: %s\n", strings.Join(p.Capability.Skills, ", "))
		}
		if len(p.Capability.Agents) > 0 {
			fmt.Printf("      agents: %s\n", strings.Join(p.Capability.Agents, ", "))
		}
	}
}

func printDHTStatus(s rnet.DHTStatus) {
	fmt.Println("DHT status:")
	fmt.Printf("  rendezvous:        %s\n", s.Rendezvous)
	fmt.Printf("  rendezvous_cid:    %s\n", s.RendezvousCID)
	fmt.Printf("  mode:              %s\n", s.Mode)
	fmt.Printf("  routing_table:     %d\n", s.RoutingTableSize)
	fmt.Printf("  bootstrap_peers:   %d\n", s.BootstrapPeers)
	fmt.Printf("  discovered_peers:  %d\n", s.DiscoveredPeerCount)
	fmt.Printf("  has_provided:      %v\n", s.HasProvided)
	fmt.Printf("  has_discovered:    %v\n", s.HasDiscovered)
	if !s.LastProvideTime.IsZero() {
		fmt.Printf("  last_provide:      %s\n", s.LastProvideTime.Format(time.RFC3339))
	}
	if !s.LastDiscoverTime.IsZero() {
		fmt.Printf("  last_discover:     %s\n", s.LastDiscoverTime.Format(time.RFC3339))
	}
}

func peerIDFromMultiaddr(addr string) (peer.ID, error) {
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return "", err
	}
	info, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

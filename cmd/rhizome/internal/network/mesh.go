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
	"github.com/spf13/pflag"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

func NewPeersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "peers",
		Short: "List trusted and connected peers",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
				os.Exit(1)
			}
			config.SetGlobal(cfg)

			if len(cfg.Mesh.TrustedPeers) == 0 {
				fmt.Println("No trusted peers configured.")
				return
			}

			fmt.Println("Trusted peers:")
			for _, p := range cfg.Mesh.TrustedPeers {
				fmt.Printf("  - %s\n", p)
			}
		},
	}
}

func NewTrustCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "trust <peer-id>",
		Short: "Add a peer to the trusted list",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			mutateTrustedPeers(args[0], true)
		},
	}
}

func NewUntrustCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "untrust <peer-id>",
		Short: "Remove a peer from the trusted list",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			mutateTrustedPeers(args[0], false)
		},
	}
}

func mutateTrustedPeers(peerID string, add bool) {
	cfg, err := config.LoadConfig(internal.GetConfigPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	config.SetGlobal(cfg)

	var peers []string
	seen := make(map[string]bool)
	if add {
		peers = append(peers, peerID)
		seen[peerID] = true
	}

	for _, p := range cfg.Mesh.TrustedPeers {
		if p == peerID && !add {
			continue
		}
		if !seen[p] {
			peers = append(peers, p)
			seen[p] = true
		}
	}

	cfg.Mesh.TrustedPeers = peers

	if err := config.SaveConfig(internal.GetConfigPath(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	if add {
		fmt.Printf("Trusted peer %s\n", peerID)
	} else {
		fmt.Printf("Untrusted peer %s\n", peerID)
	}
}

func NewDelegateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delegate <peer-multiaddr> <agent-id> <task>",
		Short: "Delegate a task to a trusted peer (synchronous)",
		Args:  cobra.MinimumNArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			maddr := args[0]
			agentID := args[1]
			task := ""
			if len(args) > 2 {
				for i, a := range args[2:] {
					if i > 0 {
						task += " "
					}
					task += a
				}
			}
			runMeshClient(cmd.Flags(), maddr, agentID, task, false)
		},
	}
	cmd.Flags().String("bootstrap", "", "Optional bootstrap multiaddr of a Rhizome daemon")
	return cmd
}

func NewSpawnCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spawn <peer-multiaddr> <agent-id> <task>",
		Short: "Spawn a task on a trusted peer (asynchronous)",
		Args:  cobra.MinimumNArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			maddr := args[0]
			agentID := args[1]
			task := ""
			if len(args) > 2 {
				for i, a := range args[2:] {
					if i > 0 {
						task += " "
					}
					task += a
				}
			}
			runMeshClient(cmd.Flags(), maddr, agentID, task, true)
		},
	}
	cmd.Flags().String("bootstrap", "", "Optional bootstrap multiaddr of a Rhizome daemon")
	cmd.Flags().Bool("no-wait", false, "Submit the task and return its id without waiting for the result")
	return cmd
}

// dialMeshPeer starts a temporary node and mesh, connects to the peer at
// maddrStr, and returns the mesh plus the resolved peer id. The returned
// cleanup shuts everything down.
func dialMeshPeer(
	ctx context.Context,
	flags *pflag.FlagSet,
	maddrStr string,
) (*mesh.Mesh, peer.ID, func()) {
	home := config.GetHome()
	identityDir := filepath.Join(home, "identity")
	derived, _, err := internal.LoadIdentity(identityDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No node identity found. Run: rhizome network onboard\n")
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(internal.GetConfigPath())
	if err != nil {
		cfg = config.DefaultConfig()
	}
	config.SetGlobal(cfg)
	cfg.Mesh.Enabled = true

	maddr, err := multiaddr.NewMultiaddr(maddrStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid multiaddr %q: %v\n", maddrStr, err)
		os.Exit(1)
	}
	addrInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not extract peer info from %q: %v\n", maddrStr, err)
		os.Exit(1)
	}

	var bootstraps []string
	if b, _ := flags.GetString("bootstrap"); b != "" {
		bootstraps = append(bootstraps, b)
	} else {
		bootstraps = append(bootstraps, maddrStr)
	}

	node, err := network.NewNode(ctx, derived.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: bootstraps,
		NATTraversal:   cfg.Mesh.NATTraversal,
		StaticRelays:   cfg.Mesh.StaticRelays,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting node: %v\n", err)
		os.Exit(1)
	}

	m := mesh.NewMesh(node, nil, derived, cfg.Mesh, nil)
	if err = m.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting mesh: %v\n", err)
		_ = node.Close()
		os.Exit(1)
	}

	// Wait briefly for mDNS and bootstrap connections.
	time.Sleep(500 * time.Millisecond)

	if err = node.Connect(ctx, maddrStr); err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to peer %s: %v\n", maddrStr, err)
		_ = m.Stop()
		_ = node.Close()
		os.Exit(1)
	}

	if !cfg.Mesh.IsPeerTrusted(addrInfo.ID.String()) {
		m.TrustPeer(addrInfo.ID)
	}

	cleanup := func() {
		_ = m.Stop()
		_ = node.Close()
	}
	return m, addrInfo.ID, cleanup
}

func runMeshClient(flags *pflag.FlagSet, maddrStr, agentID, task string, spawn bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	m, pid, cleanup := dialMeshPeer(ctx, flags, maddrStr)
	defer cleanup()

	if spawn {
		if noWait, _ := flags.GetBool("no-wait"); noWait {
			usedPeer, taskID, err := m.SubmitRemoteTaskWithPeer(ctx, pid, mesh.RemoteCall{
				TargetAgentID: agentID,
				SystemPrompt:  task,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Remote submit failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Task submitted to %s: %s\n", usedPeer, taskID)
			fmt.Printf("Check progress: rhizome network task status %s %s\n", usedPeer, taskID)
			return
		}
	}

	result, err := m.CallRemote(ctx, pid, mesh.RemoteCall{
		TargetAgentID: agentID,
		SystemPrompt:  task,
		Async:         spawn,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Remote call failed: %v\n", err)
		os.Exit(1)
	}

	if result.ForUser != "" {
		fmt.Println(result.ForUser)
	} else {
		fmt.Println(result.ForLLM)
	}
}

// NewSavedPeersCommand returns the saved-peers command, which lists all
// persistent mesh peers from config (trusted + bootstrap) merged by peer id.
func NewSavedPeersCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "saved-peers",
		Short: "List saved mesh peers from config",
		Long:  "List all peers persisted in mesh.trusted_peers and mesh.bootstrap_peers, merged by peer id.",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
				os.Exit(1)
			}
			config.SetGlobal(cfg)

			resp := mesh.BuildSavedPeersResponse(nil, cfg, false)

			if asJSON {
				data, err := json.MarshalIndent(resp, "", "  ")
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error encoding saved peers: %v\n", err)
					os.Exit(1)
				}
				cmd.Println(string(data))
				return
			}

			if len(resp.SavedPeers) == 0 {
				cmd.Println("No saved peers.")
				return
			}

			cmd.Println("Saved peers:")
			for _, p := range resp.SavedPeers {
				status := ""
				if p.Trusted {
					status = "trusted"
				}
				cmd.Printf("  - %s", p.PeerID)
				if status != "" {
					cmd.Printf(" (%s)", status)
				}
				cmd.Println()
				if len(p.BootstrapAddrs) > 0 {
					cmd.Printf("      addrs: %s\n", strings.Join(p.BootstrapAddrs, ", "))
				}
			}
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "Print saved peers as JSON")
	return cmd
}

// NewRemoveCommand returns the remove command, which removes a peer id from
// mesh.trusted_peers and any mesh.bootstrap_peers whose /p2p/ suffix matches.
func NewRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <peer-id>",
		Short: "Remove a saved peer from the mesh config",
		Long:  "Remove the peer id from mesh.trusted_peers and delete any mesh.bootstrap_peers that contain the same peer id.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			peerID := args[0]
			pid, err := peer.Decode(peerID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid peer id: %v\n", err)
				os.Exit(1)
			}

			configPath := internal.GetConfigPath()
			removed, err := mesh.RemoveSavedPeer(configPath, nil, pid)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error removing saved peer: %v\n", err)
				os.Exit(1)
			}

			if !removed {
				cmd.Printf("Peer %s is not in saved peers\n", pid.String())
				return
			}

			cmd.Printf("Removed peer %s\n", pid.String())
		},
	}
}

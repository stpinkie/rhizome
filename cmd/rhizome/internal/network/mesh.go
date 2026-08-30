package network

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

func newPeersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "peers",
		Short: "List trusted and connected peers",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
				os.Exit(1)
			}

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

func newTrustCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "trust <peer-id>",
		Short: "Add a peer to the trusted list",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			mutateTrustedPeers(args[0], true)
		},
	}
}

func newUntrustCommand() *cobra.Command {
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

func newDelegateCommand() *cobra.Command {
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

func newSpawnCommand() *cobra.Command {
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
	return cmd
}

func runMeshClient(flags *pflag.FlagSet, maddrStr, agentID, task string, spawn bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	home := config.GetHome()
	identityDir := filepath.Join(home, "identity")
	derived, _, err := identity.Load(identityDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No node identity found. Run: rhizome network onboard\n")
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(internal.GetConfigPath())
	if err != nil {
		cfg = config.DefaultConfig()
	}

	cfg.Mesh.Enabled = true
	if spawn {
		cfg.Mesh.AllowRemoteSpawn = true
	} else {
		cfg.Mesh.AllowRemoteDelegate = true
	}

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
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting node: %v\n", err)
		os.Exit(1)
	}
	defer node.Close()

	m := mesh.NewMesh(node, nil, derived, cfg.Mesh, nil)
	if err := m.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting mesh: %v\n", err)
		os.Exit(1)
	}
	defer m.Stop()

	// Wait briefly for mDNS and bootstrap connections.
	time.Sleep(500 * time.Millisecond)

	if err := node.Connect(ctx, maddrStr); err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to peer %s: %v\n", maddrStr, err)
		os.Exit(1)
	}

	if !cfg.Mesh.IsPeerTrusted(addrInfo.ID.String()) {
		m.TrustPeer(addrInfo.ID)
	}

	result, err := m.CallRemote(ctx, addrInfo.ID, agentID, task)
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

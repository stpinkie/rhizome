package network

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
)

func newPingCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ping <multiaddr>",
		Short: "Ping a Rhizome peer by multiaddr",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			home := config.GetHome()
			identityDir := filepath.Join(home, "identity")

			derived, _, err := identity.Load(identityDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "No node identity found at %s\n", identityDir)
				fmt.Fprintf(os.Stderr, "Run: rhizome network onboard\n")
				os.Exit(1)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			node, err := rnet.NewNode(ctx, derived.Libp2pPrivKey, rnet.Config{
				ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
				BootstrapPeers: []string{args[0]},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to start node: %v\n", err)
				os.Exit(1)
			}
			defer node.Close()

			// Give discovery a brief moment.
			time.Sleep(300 * time.Millisecond)

			peers := node.Peers()
			if len(peers) == 0 {
				fmt.Fprintln(os.Stderr, "No peer found. Check the multiaddr.")
				os.Exit(1)
			}

			peerID := peers[0].ID.String()
			rtt, err := node.Ping(ctx, peerID, 5*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Ping failed: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("Ping %s: %v\n", peerID, rtt)
		},
	}
}

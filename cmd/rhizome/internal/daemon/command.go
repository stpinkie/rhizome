package daemon

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

func NewDaemonCommand() *cobra.Command {
	var listenAddrs []string
	var bootstrapPeers []string

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run a long-running Rhizome P2P node",
		Run: func(cmd *cobra.Command, args []string) {
			home := config.GetHome()
			identityDir := filepath.Join(home, "identity")

			derived, name, err := identity.Load(identityDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "No node identity found at %s\n", identityDir)
				fmt.Fprintf(os.Stderr, "Run: rhizome network onboard\n")
				os.Exit(1)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			node, err := network.NewNode(ctx, derived.Libp2pPrivKey, network.Config{
				ListenAddrs:    listenAddrs,
				BootstrapPeers: bootstrapPeers,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to start Rhizome node: %v\n", err)
				os.Exit(1)
			}
			defer node.Close()

			fmt.Printf("Rhizome node online\n")
			fmt.Printf("  Name:    %s\n", name)
			fmt.Printf("  Peer ID: %s\n", node.PeerID())
			fmt.Printf("  Addrs:   %v\n", node.BootstrapAddrs())
			fmt.Println("Press Ctrl+C to stop.")

			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig

			fmt.Println("Shutting down...")
		},
	}

	cmd.Flags().StringArrayVar(&listenAddrs, "listen", []string{"/ip4/0.0.0.0/tcp/0"}, "Multiaddrs to listen on")
	cmd.Flags().StringArrayVar(&bootstrapPeers, "bootstrap", nil, "Bootstrap peer multiaddrs")

	return cmd
}

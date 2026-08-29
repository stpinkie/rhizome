package network

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the Rhizome node identity",
		Run: func(cmd *cobra.Command, args []string) {
			home := config.GetHome()
			identityDir := filepath.Join(home, "identity")

			derived, name, err := identity.Load(identityDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "No node identity found at %s\n", identityDir)
				fmt.Fprintf(os.Stderr, "Run: rhizome network onboard\n")
				os.Exit(1)
			}

			fmt.Printf("Node:      %s\n", name)
			fmt.Printf("Index:     %d\n", derived.NodeIndex)
			fmt.Printf("Peer ID:   %s\n", derived.PeerID)
			fmt.Printf("Identity:  %s\n", identityDir)
		},
	}
}

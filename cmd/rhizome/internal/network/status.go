package network

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
)

func newStatusCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the Rhizome node identity",
		Run: func(cmd *cobra.Command, args []string) {
			home := config.GetHome()
			identityDir := filepath.Join(home, "identity")

			derived, name, err := internal.LoadIdentity(identityDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "No node identity found at %s\n", identityDir)
				fmt.Fprintf(os.Stderr, "Run: rhizome network onboard\n")
				os.Exit(1)
			}

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
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "Print status as JSON")
	return cmd
}

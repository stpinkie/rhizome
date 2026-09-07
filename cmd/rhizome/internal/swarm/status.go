package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
)

// persistedView mirrors the swarm registry file for read-only CLI output.
type persistedView struct {
	Swarms map[string]struct {
		JoinedAt string `json:"joined_at"`
		Members  []struct {
			PeerID   string `json:"peer_id"`
			LastSeen string `json:"last_seen"`
			Source   string `json:"source"`
		} `json:"members"`
	} `json:"swarms"`
}

func loadPersisted() persistedView {
	var view persistedView
	data, err := os.ReadFile(filepath.Join(internal.GetRhizomeHome(), "swarms.json"))
	if err != nil {
		return view
	}
	_ = json.Unmarshal(data, &view)
	return view
}

func newListCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured and known swarms",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
				os.Exit(1)
			}
			view := loadPersisted()

			if asJSON {
				out := map[string]any{
					"enabled":     cfg.Swarm.Enabled,
					"memberships": cfg.Swarm.Memberships,
					"known":       view.Swarms,
				}
				data, _ := json.MarshalIndent(out, "", "  ")
				cmd.Println(string(data))
				return
			}

			if !cfg.Swarm.Enabled {
				cmd.Println("Swarm is disabled (swarm.enabled=false).")
			}
			if len(cfg.Swarm.Memberships) == 0 {
				cmd.Println("No swarm memberships configured.")
			} else {
				cmd.Println("Memberships:")
				for _, m := range cfg.Swarm.Memberships {
					cmd.Printf("  - %s\n", m)
				}
			}
			if len(view.Swarms) > 0 {
				cmd.Println("Known swarms (saved roster):")
				for id, sw := range view.Swarms {
					cmd.Printf("  - %s (%d members)\n", id, len(sw.Members))
				}
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

func newMembersCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "members <swarm-id>",
		Short: "Show the saved member roster for a swarm",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			view := loadPersisted()
			sw, ok := view.Swarms[args[0]]
			if !ok {
				fmt.Fprintf(os.Stderr, "No saved roster for swarm %q — is the daemon running?\n", args[0])
				os.Exit(1)
			}
			if asJSON {
				data, _ := json.MarshalIndent(sw.Members, "", "  ")
				cmd.Println(string(data))
				return
			}
			cmd.Printf("Members of %q:\n", args[0])
			for _, m := range sw.Members {
				cmd.Printf("  - %s (last seen %s, %s)\n", m.PeerID, m.LastSeen, m.Source)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

func newStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show swarm configuration and saved state",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
				os.Exit(1)
			}
			view := loadPersisted()

			out := map[string]any{
				"enabled":     cfg.Swarm.Enabled,
				"transport":   cfg.Swarm.Transport,
				"memberships": cfg.Swarm.Memberships,
				"known":       view.Swarms,
			}
			if asJSON {
				data, _ := json.MarshalIndent(out, "", "  ")
				cmd.Println(string(data))
				return
			}
			cmd.Printf("Swarm enabled:   %v\n", cfg.Swarm.Enabled)
			cmd.Printf("Transport:       %s\n", cfg.Swarm.Transport)
			cmd.Printf("Memberships:     %v\n", cfg.Swarm.Memberships)
			cmd.Printf("Known swarms:    %d\n", len(view.Swarms))
			cmd.Println("Live roster is available while the daemon runs (rhizome daemon).")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

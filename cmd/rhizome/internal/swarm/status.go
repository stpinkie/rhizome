package swarm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
			Role     string `json:"role,omitempty"`
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
			w := cmd.OutOrStdout()

			if asJSON {
				out := map[string]any{
					"enabled":     cfg.Swarm.Enabled,
					"memberships": cfg.Swarm.Memberships,
					"known":       view.Swarms,
				}
				data, _ := json.MarshalIndent(out, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}

			if !cfg.Swarm.Enabled {
				fmt.Fprintln(w, "Swarm is disabled (swarm.enabled=false).")
			}
			if len(cfg.Swarm.Memberships) == 0 {
				fmt.Fprintln(w, "No swarm memberships configured.")
			} else {
				fmt.Fprintln(w, "Memberships:")
				for _, m := range cfg.Swarm.Memberships {
					fmt.Fprintf(w, "  - %s\n", m)
				}
			}
			if len(view.Swarms) > 0 {
				fmt.Fprintln(w, "Known swarms (saved roster):")
				for id, sw := range view.Swarms {
					fmt.Fprintf(w, "  - %s (%d members)\n", id, len(sw.Members))
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
			if err := internal.ValidateSwarmID(args[0]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			view := loadPersisted()
			sw, ok := view.Swarms[args[0]]
			if !ok {
				fmt.Fprintf(os.Stderr, "No saved roster for swarm %q — is the daemon running?\n", args[0])
				os.Exit(1)
			}
			w := cmd.OutOrStdout()
			if asJSON {
				data, _ := json.MarshalIndent(sw.Members, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			fmt.Fprintf(w, "Members of %q:\n", args[0])
			for _, m := range sw.Members {
				role := m.Role
				if role == "" {
					role = "full"
				}
				fmt.Fprintf(w, "  - %s (last seen %s, %s, %s)\n", m.PeerID, m.LastSeen, m.Source, role)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

// liveSwarmInfo mirrors the daemon's swarm.SwarmInfo for status output.
type liveSwarmInfo struct {
	ID             string    `json:"id"`
	Joined         bool      `json:"joined"`
	Coordinator    string    `json:"coordinator"`
	Epoch          int64     `json:"epoch"`
	LastStateWrite time.Time `json:"last_state_write"`
	Members        []struct {
		PeerID string `json:"peer_id"`
	} `json:"members"`
}

// liveStatus queries the daemon for live swarm state; nil when unreachable.
func liveStatus() *struct {
	PeerID string          `json:"peer_id"`
	Swarms []liveSwarmInfo `json:"swarms"`
} {
	data, code, err := daemonRequest(http.MethodGet, "/network/swarms", nil, 3*time.Second)
	if err != nil || code != http.StatusOK {
		return nil
	}
	var st struct {
		PeerID string          `json:"peer_id"`
		Swarms []liveSwarmInfo `json:"swarms"`
	}
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

func newStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show swarm configuration, saved state, and coordination freshness",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.LoadConfig(internal.GetConfigPath())
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
				os.Exit(1)
			}
			view := loadPersisted()
			live := liveStatus()

			out := map[string]any{
				"enabled":     cfg.Swarm.Enabled,
				"transport":   cfg.Swarm.Transport,
				"memberships": cfg.Swarm.Memberships,
				"known":       view.Swarms,
			}
			if live != nil {
				out["live"] = live
			}
			w := cmd.OutOrStdout()
			if asJSON {
				data, _ := json.MarshalIndent(out, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			fmt.Fprintf(w, "Swarm enabled:   %v\n", cfg.Swarm.Enabled)
			fmt.Fprintf(w, "Transport:       %s\n", cfg.Swarm.Transport)
			fmt.Fprintf(w, "Memberships:     %v\n", cfg.Swarm.Memberships)
			fmt.Fprintf(w, "Known swarms:    %d\n", len(view.Swarms))
			if live == nil {
				fmt.Fprintln(w, "Live status:     unavailable (daemon not running)")
				return
			}
			fmt.Fprintf(w, "Live peer:       %s\n", shortPeer(live.PeerID))
			for _, sw := range live.Swarms {
				coord := "(none)"
				if sw.Coordinator != "" {
					coord = shortPeer(sw.Coordinator)
				}
				fresh := "never written"
				if !sw.LastStateWrite.IsZero() {
					fresh = time.Since(sw.LastStateWrite).Round(time.Second).String() + " ago"
				}
				joined := ""
				if !sw.Joined {
					joined = " (not joined)"
				}
				fmt.Fprintf(w, "  %s%s: coordinator %s, epoch %d, state %s, %d members\n",
					sw.ID, joined, coord, sw.Epoch, fresh, len(sw.Members))
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

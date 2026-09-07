package swarm

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	rswarm "github.com/stpinkie/rhizome/pkg/rhizome/swarm"
)

func newJoinCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "join <swarm-id>",
		Short: "Join a swarm (persisted to swarm.memberships)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			mutateMembership(args[0], true)
		},
	}
}

func newLeaveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "leave <swarm-id>",
		Short: "Leave a swarm (persisted to swarm.memberships)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			mutateMembership(args[0], false)
		},
	}
}

// mutateMembership adds or removes a swarm id from swarm.memberships in
// config.json and enables the swarm section. The running daemon picks the
// change up on restart.
func mutateMembership(swarmID string, add bool) {
	if !rswarm.ValidSwarmID(swarmID) {
		fmt.Fprintf(os.Stderr, "Invalid swarm id %q (allowed: [a-zA-Z0-9_.-], 1-64 chars)\n", swarmID)
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(internal.GetConfigPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	config.SetGlobal(cfg)

	var memberships []string
	seen := make(map[string]bool)
	if add {
		memberships = append(memberships, swarmID)
		seen[swarmID] = true
	}
	for _, m := range cfg.Swarm.Memberships {
		if m == swarmID && !add {
			continue
		}
		if !seen[m] {
			memberships = append(memberships, m)
			seen[m] = true
		}
	}

	cfg.Swarm.Memberships = memberships
	if add {
		cfg.Swarm.Enabled = true
	}

	if err := config.SaveConfig(internal.GetConfigPath(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}

	if add {
		fmt.Printf("Joined swarm %q (effective on next daemon start)\n", swarmID)
	} else {
		fmt.Printf("Left swarm %q (effective on next daemon start)\n", swarmID)
	}
}

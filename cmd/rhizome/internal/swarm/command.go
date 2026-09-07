package swarm

import (
	"github.com/spf13/cobra"
)

// NewSwarmCommand returns the rhizome swarm command tree for managing named
// trusted-peer groups.
func NewSwarmCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "swarm",
		Short: "Join and inspect trusted-peer swarms",
		Long:  "Manage swarm memberships: named groups of trusted mesh peers that share presence and distribute work.",
	}

	cmd.AddCommand(
		newJoinCommand(),
		newLeaveCommand(),
		newListCommand(),
		newMembersCommand(),
		newStatusCommand(),
		newOfferCommand(),
		newOffersCommand(),
		newRunCommand(),
	)

	return cmd
}

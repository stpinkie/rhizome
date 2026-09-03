package network

import (
	"github.com/spf13/cobra"
)

func NewNetworkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Manage the Rhizome P2P network identity and peers",
		Long:  "Create a node identity, view network status, and ping other Rhizome nodes.",
	}

	cmd.AddCommand(
		NewOnboardCommand(),
		NewStatusCommand(),
		NewPingCommand(),
		NewPeersCommand(),
		NewTrustCommand(),
		NewUntrustCommand(),
		NewDelegateCommand(),
		NewSpawnCommand(),
	)

	return cmd
}

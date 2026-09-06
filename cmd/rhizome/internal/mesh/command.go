package mesh

import (
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal/network"
)

// NewMeshCommand returns the rhizome mesh command tree. It mirrors the
// network commands, but defaults status and peers to live mesh/DHT output.
func NewMeshCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mesh",
		Short: "Inspect and control the Rhizome P2P mesh",
		Long:  "Inspect and control the Rhizome P2P mesh. Commands are live-mesh shortcuts for the network command tree.",
	}

	statusCmd := network.NewStatusCommand()
	statusCmd.Use = "status"
	statusCmd.Short = "Show live mesh and DHT status"
	_ = statusCmd.Flags().Set("peers", "true")
	_ = statusCmd.Flags().Set("dht", "true")

	peersCmd := network.NewStatusCommand()
	peersCmd.Use = "peers"
	peersCmd.Short = "Show live connected peers and their capabilities"
	_ = peersCmd.Flags().Set("peers", "true")

	cmd.AddCommand(
		statusCmd,
		peersCmd,
		network.NewSavedPeersCommand(),
		network.NewTrustCommand(),
		network.NewUntrustCommand(),
		network.NewRemoveCommand(),
		network.NewDelegateCommand(),
		network.NewSpawnCommand(),
		network.NewTaskCommand(),
		network.NewRouteCommand(),
		network.NewAuditCommand(),
		network.NewPingCommand(),
	)

	return cmd
}

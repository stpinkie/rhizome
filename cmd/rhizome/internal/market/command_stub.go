//go:build !economy

package market

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewMarketCommand returns a hidden stub in the default build — the
// rhizome-market verbs are bucket-B and only exist under the `economy`
// build tag (ALIGNMENT.md §3).
func NewMarketCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "market",
		Short:  "Interact with the rhizome-market module (requires -tags economy)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("rhizome market requires building with -tags economy")
		},
	}
}

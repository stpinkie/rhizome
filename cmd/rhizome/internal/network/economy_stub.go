//go:build !economy

package network

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewEconomyCommand returns a hidden stub in the default build — the
// paired-settlement verbs are bucket-B and only exist under the
// `economy` build tag (ALIGNMENT.md §3).
func NewEconomyCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "economy",
		Short:  "Paired-settlement ledger verbs (requires -tags economy)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("network economy requires building with -tags economy")
		},
	}
}

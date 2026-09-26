package market

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewMarketCommand returns the rhizome market command tree: thin verbs over
// the rhizome-market module's loopback API. The verbs carry their final
// argument shapes now; payload schemas flesh out in the market tracks.
func NewMarketCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "market",
		Short: "Interact with the rhizome-market module (buy/sell agent work)",
		Long: "Thin client for the rhizome-market companion module's loopback " +
			"API. Install the module first: rhizome module install rhizome-market.",
	}
	cmd.AddCommand(
		newFindCommand(),
		newBuyCommand(),
		newSessionCommand(),
		newReceiptCommand(),
	)
	return cmd
}

// runVerb resolves the module client and POSTs body to /v1/<verb>, printing
// the response verbatim. Non-2xx responses print the body and exit non-zero.
func runVerb(cmd *cobra.Command, verb string, body any) {
	client, err := resolveClient()
	if err != nil {
		fatal(err)
	}
	data, code, err := client.call(cmd.Context(), verb, body)
	if err != nil {
		fatal(err)
	}
	w := cmd.OutOrStdout()
	if code < 200 || code >= 300 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", string(data))
		os.Exit(1)
	}
	fmt.Fprintln(w, string(data))
}

func newFindCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "find <service>",
		Short: "Query the market index for a service",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "find", map[string]any{"query": args[0]})
		},
	}
}

func newBuyCommand() *cobra.Command {
	var maxCost string
	cmd := &cobra.Command{
		Use:   "buy <provider> <offer> <task>",
		Short: "Purchase a task from a market provider",
		Args:  cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "buy", map[string]any{
				"provider": args[0],
				"offer":    args[1],
				"task":     args[2],
				"max_cost": maxCost,
			})
		},
	}
	cmd.Flags().StringVar(&maxCost, "max-cost", "", "Maximum spend for this purchase")
	return cmd
}

func newSessionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "session <id>",
		Short: "Show a market session's status",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "session", map[string]any{"id": args[0]})
		},
	}
}

func newReceiptCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "receipt <id>",
		Short: "Verify a stored market receipt",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runVerb(cmd, "receipt", map[string]any{"id": args[0]})
		},
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

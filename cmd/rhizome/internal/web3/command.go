// Package web3cmd implements the rhizome web3 command tree — inspection and
// resolution of the signing-approval queue.
package web3cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// NewWeb3Command returns the rhizome web3 command tree.
func NewWeb3Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "web3",
		Short: "Web3 signing approvals (pending queue)",
		Long: "Signing tools queue durable approval requests; a human resolves " +
			"them here (or via the daemon API / dashboard). Approval signs and " +
			"broadcasts immediately — reject with `rhizome web3 reject`.",
	}
	cmd.AddCommand(
		newPendingCommand(),
		newResolveCommand("approve"),
		newResolveCommand("reject"),
	)
	return cmd
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func printJSON(w io.Writer, v any) {
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(w, string(out))
}

func newPendingCommand() *cobra.Command {
	var asJSON bool
	var statusFilter string
	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List signing requests awaiting approval",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			entries := fetchPending()
			out := make([]*web3.PendingEntry, 0, len(entries))
			for _, e := range entries {
				if statusFilter == "" || string(e.Status) == statusFilter {
					out = append(out, e)
				}
			}
			if asJSON {
				printJSON(cmd.OutOrStdout(), map[string]any{"pending": out})
				return
			}
			if len(out) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No pending signing requests.")
				return
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tKIND\tSTATUS\tSUMMARY\tEXPIRES")
			for _, e := range out {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					e.ID, e.Kind, e.Status, e.Summary,
					e.ExpiresAt.Local().Format("15:04:05"))
			}
			w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	cmd.Flags().StringVar(&statusFilter, "status", "", "filter by status (pending|sent|rejected|…)")
	return cmd
}

func newResolveCommand(action string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   action + " <id>",
		Short: strings.Title(action) + " a pending signing request",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if resolveViaDaemon(action, args[0]) {
				return
			}
			resolveLocal(action, args[0])
		},
	}
	return cmd
}

// fetchPending prefers the daemon's queue view; falls back to the local
// file. Both read the same store — the daemon path adds live sweep state.
func fetchPending() []*web3.PendingEntry {
	if base, _ := internal.DaemonBaseURL(); base != "" {
		data, code, err := internal.DaemonRequest(http.MethodGet, "/web3/pending", nil, 10*time.Second)
		if err == nil && code == http.StatusOK {
			var resp struct {
				Pending []*web3.PendingEntry `json:"pending"`
			}
			if err := json.Unmarshal(data, &resp); err == nil {
				return resp.Pending
			}
		}
	}
	cfg, err := internal.LoadConfig()
	if err != nil {
		fatal(err)
	}
	stack, err := web3.OpenSigningStack(internal.GetRhizomeHome(), &cfg.Tools.Web3.Signing)
	if err != nil {
		fatal(err)
	}
	entries, err := stack.Pending.List()
	if err != nil {
		fatal(err)
	}
	return entries
}

// resolveViaDaemon POSTs the resolution to the running daemon — it owns
// execution (sign + broadcast) when a daemon is live.
func resolveViaDaemon(action, id string) bool {
	base, _ := internal.DaemonBaseURL()
	if base == "" {
		return false
	}
	payload, _ := json.Marshal(map[string]string{"action": action})
	data, code, err := internal.DaemonRequest(
		http.MethodPost, "/web3/approvals/"+id, payload, 60*time.Second)
	if err != nil {
		return false
	}
	if code != http.StatusOK {
		fatal(fmt.Errorf("%s", trimErr(data)))
	}
	var e web3.PendingEntry
	if err := json.Unmarshal(data, &e); err == nil {
		printResolved(action, &e)
	}
	return true
}

// resolveLocal resolves daemonless: reject writes the file; approve signs
// and broadcasts from the CLI process.
func resolveLocal(action, id string) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		fatal(err)
	}
	stack, err := web3.OpenSigningStack(internal.GetRhizomeHome(), &cfg.Tools.Web3.Signing)
	if err != nil {
		fatal(err)
	}
	e, err := stack.Pending.Resolve(id, action == "approve", "cli")
	if err != nil {
		fatal(err)
	}
	if action == "reject" {
		printResolved(action, e)
		return
	}
	provider := web3.NewProvider(cfg)
	done, execErr := stack.Pending.ExecuteApproved(
		context.Background(), id, stack.Wallets, provider, stack.Ledger)
	if done != nil {
		e = done
	}
	if execErr != nil {
		fmt.Fprintf(os.Stderr, "Request %s failed: %v\n", id, execErr)
		printResolved(action, e)
		os.Exit(1)
	}
	printResolved(action, e)
}

func printResolved(action string, e *web3.PendingEntry) {
	switch {
	case e.Status == web3.StatusSent:
		fmt.Printf("Approved %s — broadcast as %s\n", e.ID, e.TxHash)
	case e.Status == web3.StatusDone:
		fmt.Printf("Approved %s — signature: %s\n", e.ID, e.Result)
	case e.Status == web3.StatusRejected:
		fmt.Printf("Rejected %s\n", e.ID)
	case e.Status == web3.StatusFailed:
		fmt.Printf("%s %s — failed: %s\n", strings.Title(action), e.ID, e.Error)
	default:
		fmt.Printf("%s %s — status %s\n", strings.Title(action), e.ID, e.Status)
	}
}

func trimErr(data []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &e); err == nil && e.Error != "" {
		return e.Error
	}
	return string(data)
}

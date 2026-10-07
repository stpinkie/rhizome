package swarm

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/network"
)

// newTraceCommand correlates an offer or run id across the offer queue, run
// records, the activity feed, and the mesh audit trail (daemon required).
func newTraceCommand() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "trace <offer-id|run-id>",
		Short: "Correlate an offer or run id across queue, runs, activity, and audit (daemon required)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			path := "/network/trace?id=" + url.QueryEscape(args[0])
			body, status, err := internal.DaemonRequest(http.MethodGet, path, nil, 15*time.Second)
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(body))
				return
			}
			if asJSON {
				fmt.Fprintln(w, string(body))
				return
			}
			network.PrintTraceReport(w, body)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

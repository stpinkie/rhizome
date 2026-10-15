package evolution

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	rhevo "github.com/stpinkie/rhizome/pkg/evolution"
)

func newRollbackCommand() *cobra.Command {
	var sel workspaceSelection
	var to string
	var dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "rollback <skill>",
		Short: "Restore a skill to a prior snapshot (daemon required)",
		Long: "Restore a skill's SKILL.md to a prior snapshot: the newest " +
			"pre-apply backup, or a named version's snapshot with --to. The " +
			"current content is backed up first so the rollback is itself " +
			"undoable; restored content passes the same validation apply does.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			workspace := ""
			if sel.workspace != "" || sel.agent != "" {
				var werr error
				workspace, werr = sel.singleWorkspace(cfg)
				if werr != nil {
					fmt.Fprintf(w, "error: %v\n", werr)
					return
				}
			}
			body, _ := json.Marshal(map[string]any{
				"workspace": workspace,
				"to":        to,
				"dry_run":   dryRun,
			})
			respBody, status, err := internal.DaemonRequest(
				http.MethodPost,
				"/evolution/skills/"+url.PathEscape(args[0])+"/rollback",
				body,
				30*time.Second,
			)
			if err != nil {
				fmt.Fprintf(w, "error: %v (daemon required for rollback)\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(respBody))
				return
			}
			var resp struct {
				Rollback rhevo.RollbackResult `json:"rollback"`
			}
			if err := json.Unmarshal(respBody, &resp); err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if asJSON {
				data, _ := json.MarshalIndent(resp.Rollback, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			printRollbackResult(w, resp.Rollback)
		},
	}
	sel.register(cmd)
	cmd.Flags().StringVar(&to, "to", "", "restore the named version's snapshot instead of the newest backup")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "resolve and validate the target without writing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func printRollbackResult(w io.Writer, r rhevo.RollbackResult) {
	verb := "Rolled back"
	if r.DryRun {
		verb = "Would roll back"
	}
	fmt.Fprintf(w, "%s %s", verb, r.Skill)
	if r.Removed {
		fmt.Fprint(w, " → removed (pre-create state)")
	} else if r.RestoredVersion != "" {
		fmt.Fprintf(w, " → %s", r.RestoredVersion)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  source: %s\n", r.Source)
	if r.BackedUpTo != "" {
		fmt.Fprintf(w, "  current content preserved at: %s\n", r.BackedUpTo)
	}
	if r.PreviousVersion != "" && r.PreviousVersion != r.RestoredVersion {
		fmt.Fprintf(w, "  previous version: %s\n", r.PreviousVersion)
	}
}

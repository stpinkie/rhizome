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

// newSkillTransitionCommand builds `evolution skills archive|restore` —
// manual lifecycle transitions through the daemon (the running agent owns
// the store while it's up).
func newSkillTransitionCommand(verb string) *cobra.Command {
	var sel workspaceSelection
	var asJSON bool
	var target rhevo.SkillStatus
	var short string
	switch verb {
	case "archive":
		target = rhevo.SkillStatusArchived
		short = "Park an active/cold skill (manual lifecycle archive)"
	case "restore":
		target = rhevo.SkillStatusActive
		short = "Return a cold/archived skill to active"
	}
	cmd := &cobra.Command{
		Use:   verb + " <name>",
		Short: short + " (daemon required)",
		Args:  cobra.ExactArgs(1),
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
			body, _ := json.Marshal(map[string]any{"workspace": workspace})
			respBody, status, err := internal.DaemonRequest(
				http.MethodPost,
				"/evolution/skills/"+url.PathEscape(args[0])+"/"+verb,
				body,
				30*time.Second,
			)
			if err != nil {
				fmt.Fprintf(w, "error: %v (daemon required for skills %s)\n", err, verb)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(respBody))
				return
			}
			var resp struct {
				Skill rhevo.SkillProfile `json:"skill"`
			}
			if err := json.Unmarshal(respBody, &resp); err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if asJSON {
				data, _ := json.MarshalIndent(resp.Skill, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			if resp.Skill.Status != target {
				fmt.Fprintf(w, "%s is already %s — no change.\n", args[0], resp.Skill.Status)
				return
			}
			fmt.Fprintf(w, "%s → %s\n", args[0], resp.Skill.Status)
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

// newPruneCommand builds `evolution prune` — a manual retention sweep.
// --dry-run counts what would be pruned daemonless; the live sweep goes
// through the daemon so it serializes with the running agent's writes.
func newPruneCommand() *cobra.Command {
	var sel workspaceSelection
	var all, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune expired learning records and rejected drafts",
		Long: "Apply the retention windows (evolution.retention_task_records_days, " +
			"retention_drafts_days) now instead of waiting for the next cold-path " +
			"lifecycle sweep. Candidate/quarantined/accepted drafts and accepted " +
			"history are never pruned. --dry-run reports counts daemonless; the " +
			"live sweep requires the daemon.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			var workspaces []string
			switch {
			case all:
				workspaces = rhevo.ConfigWorkspaces(cfg)
			default:
				ws, werr := sel.singleWorkspace(cfg)
				if werr != nil {
					fmt.Fprintf(w, "error: %v\n", werr)
					return
				}
				workspaces = []string{ws}
			}
			if len(workspaces) == 0 {
				fmt.Fprintln(w, "error: no workspaces resolved")
				return
			}

			if dryRun {
				for _, ws := range workspaces {
					summary, err := rhevo.PruneExpired(storeFor(cfg, ws), ws, time.Now(),
						cfg.Evolution.EffectiveRetentionTaskRecordsDays(),
						cfg.Evolution.EffectiveRetentionDraftsDays(),
						true,
					)
					if err != nil {
						fmt.Fprintf(w, "error: %v\n", err)
						return
					}
					printPruneSummary(w, summary, asJSON)
				}
				return
			}

			body, _ := json.Marshal(map[string]any{
				"workspace": sel.workspace,
				"agent":     sel.agent,
				"all":       all,
			})
			respBody, status, err := internal.DaemonRequest(
				http.MethodPost, "/evolution/prune", body, 30*time.Second)
			if err != nil {
				fmt.Fprintf(w, "error: %v (daemon required for prune)\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(respBody))
				return
			}
			var resp struct {
				Results []rhevo.PruneSummary `json:"results"`
			}
			if err := json.Unmarshal(respBody, &resp); err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			for _, summary := range resp.Results {
				printPruneSummary(w, summary, asJSON)
			}
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&all, "all", false, "prune across every known workspace")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be pruned without writing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func printPruneSummary(w io.Writer, s rhevo.PruneSummary, asJSON bool) {
	if asJSON {
		data, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(w, string(data))
		return
	}
	verb := "pruned"
	if s.DryRun {
		verb = "would prune"
	}
	fmt.Fprintf(w, "%s\n  %s: %d task records, %d rejected drafts\n",
		s.Workspace, verb, s.TaskRecordsPruned, s.DraftsPruned)
}

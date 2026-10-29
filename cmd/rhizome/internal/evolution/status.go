package evolution

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	rhevo "github.com/stpinkie/rhizome/pkg/evolution"
)

func newStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show evolution config and per-workspace state",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			ev := cfg.Evolution

			// Best-effort daemon enrichment (last-run info); the counters are
			// computed daemonless regardless.
			var daemonStatus map[string]any
			body, status, derr := internal.DaemonRequest(
				http.MethodGet, "/evolution/status", nil, 10*time.Second)
			if derr == nil && status == http.StatusOK {
				_ = json.Unmarshal(body, &daemonStatus)
			}

			if asJSON {
				out := map[string]any{
					"enabled":                     ev.Enabled,
					"mode":                        ev.EffectiveMode(),
					"cold_path_trigger":           ev.ColdPathTriggerMode(),
					"cold_path_times":             ev.EffectiveColdPathTimes(),
					"state_dir":                   ev.StateDir,
					"apply_require_review":        ev.ApplyRequireReview,
					"apply_max_per_day":           ev.EffectiveApplyMaxPerDay(),
					"protected_skills":            ev.EffectiveProtectedSkills(),
					"retention_task_records_days": ev.EffectiveRetentionTaskRecordsDays(),
					"retention_drafts_days":       ev.EffectiveRetentionDraftsDays(),
					"workspaces":                  workspaceStatusesJSON(cfg, daemonStatus),
				}
				if rs, ok := daemonStatus["last_runs"]; ok {
					out["last_runs"] = rs
				}
				out["audit_log"] = rhevo.DefaultAuditPath()
				if runs := recentRunEntries(); len(runs) > 0 {
					out["recent_runs"] = runs
				}
				data, _ := json.MarshalIndent(out, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}

			if !ev.Enabled {
				fmt.Fprintln(w, "Evolution: disabled (evolution.enabled=false)")
			} else {
				fmt.Fprintf(w, "Evolution: mode=%s trigger=%s min_tasks=%d min_success=%.2f\n",
					ev.EffectiveMode(), ev.ColdPathTriggerMode(),
					ev.EffectiveMinTaskCount(), ev.EffectiveMinSuccessRatio())
				if ev.EffectiveMode() == "apply" {
					protected := ev.EffectiveProtectedSkills()
					if len(protected) == 0 {
						protected = []string{"-"}
					}
					fmt.Fprintf(w, "  apply: require_review=%t max_per_day=%d protected=%s\n",
						ev.ApplyRequireReview, ev.EffectiveApplyMaxPerDay(),
						strings.Join(protected, ","))
				}
				if times := ev.EffectiveColdPathTimes(); len(times) > 0 {
					fmt.Fprintf(w, "  scheduled at: %v\n", times)
				}
				fmt.Fprintf(w, "  retention: task_records=%dd drafts=%dd\n",
					ev.EffectiveRetentionTaskRecordsDays(), ev.EffectiveRetentionDraftsDays())
			}
			for _, ws := range rhevo.ConfigWorkspaces(cfg) {
				st, err := rhevo.SummarizeWorkspace(storeFor(cfg, ws), ws)
				fmt.Fprintf(w, "\nWorkspace %s\n", ws)
				if err != nil {
					fmt.Fprintf(w, "  error: %v\n", err)
					continue
				}
				fmt.Fprintf(w, "  records: %d task (%d unclustered), %d pattern (%d ready)\n",
					st.TaskRecords, st.UnclusteredTasks, st.PatternRecords, st.ReadyPatterns)
				if len(st.Drafts) > 0 {
					fmt.Fprintf(w, "  drafts: %s\n", formatStatusMap(st.Drafts))
				} else {
					fmt.Fprintln(w, "  drafts: none")
				}
				if len(st.Skills) > 0 {
					fmt.Fprintf(w, "  skills: %s\n", formatStatusMap(st.Skills))
				}
				if decided := st.AcceptedDrafts + st.RejectedDrafts; decided > 0 {
					fmt.Fprintf(w, "  acceptance: %d/%d decided drafts accepted (%.0f%%)\n",
						st.AcceptedDrafts, decided, st.AcceptanceRate*100)
				}
			}
			printLastRuns(w, daemonStatus)
			printRecentRuns(w)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func newRunCommand() *cobra.Command {
	var sel workspaceSelection
	var all, dryRun, asJSON bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Trigger a cold-path run (daemon required; --dry-run is offline)",
		Args:  cobra.NoArgs,
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
					summary := rhevo.PreviewColdPathForStore(
						cfg.Evolution.EffectiveMode(), cfg.Evolution.Enabled,
						storeFor(cfg, ws), ws)
					printRunSummary(w, summary, asJSON)
				}
				return
			}

			body, _ := json.Marshal(map[string]any{
				"workspace": sel.workspace,
				"agent":     sel.agent,
				"all":       all,
			})
			// Cold-path runs can take minutes (LLM draft generation).
			respBody, status, err := internal.DaemonRequest(http.MethodPost, "/evolution/run", body, 10*time.Minute)
			if err != nil {
				fmt.Fprintf(w, "error: %v (daemon required for evolution run)\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(respBody))
				return
			}
			var resp struct {
				Results []rhevo.ColdPathRunSummary `json:"results"`
			}
			if err := json.Unmarshal(respBody, &resp); err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			for _, summary := range resp.Results {
				printRunSummary(w, summary, asJSON)
			}
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&all, "all", false, "run across every known workspace")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what a run would process without writing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func printRunSummary(w io.Writer, s rhevo.ColdPathRunSummary, asJSON bool) {
	if asJSON {
		data, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(w, string(data))
		return
	}
	fmt.Fprintf(w, "%s\n", s.Workspace)
	if s.Note != "" {
		fmt.Fprintf(w, "  note: %s\n", s.Note)
	}
	if s.Error != "" {
		fmt.Fprintf(w, "  error: %s\n", s.Error)
		return
	}
	if s.DryRun {
		fmt.Fprintf(w, "  dry-run: %d unclustered task records, %d ready patterns, %d candidate drafts\n",
			s.UnclusteredTasks, s.ReadyPatterns, s.CandidateDrafts)
		return
	}
	fmt.Fprintf(w, "  ran: %v — %d drafts created, %d applied (%d tasks, %d ready patterns)\n",
		s.Ran, s.DraftsCreated, s.DraftsApplied, s.TaskRecords, s.ReadyPatterns)
}

// workspaceStatusesJSON returns the daemon's per-workspace statuses when the
// daemon answered, else computes them daemonless from the state dirs.
func workspaceStatusesJSON(cfg *config.Config, daemonStatus map[string]any) any {
	if ws, ok := daemonStatus["workspaces"]; ok {
		return ws
	}
	out := make([]rhevo.WorkspaceStatus, 0)
	for _, ws := range rhevo.ConfigWorkspaces(cfg) {
		st, err := rhevo.SummarizeWorkspace(storeFor(cfg, ws), ws)
		if err != nil {
			continue
		}
		out = append(out, st)
	}
	return out
}

func formatStatusMap(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += "  "
		}
		out += fmt.Sprintf("%s=%d", k, m[k])
	}
	return out
}

// recentRunEntries returns the last run.finished audit entries — the
// durable cold-path run history backing `evolution status`.
func recentRunEntries() []map[string]any {
	entries, err := rhevo.ReadAuditEntries(
		rhevo.DefaultAuditPath(), 5, rhevo.AuditFilter{Op: "run.finished"})
	if err != nil {
		return nil
	}
	return entries
}

// printRecentRuns prints the audit-derived run history — daemonless, and
// it survives restarts unlike the daemon's in-memory last_runs.
func printRecentRuns(w io.Writer) {
	entries := recentRunEntries()
	if len(entries) == 0 {
		return
	}
	fmt.Fprintln(w, "\nRecent runs:")
	for _, e := range entries {
		get := func(k string) string {
			s, _ := e[k].(string)
			return s
		}
		line := fmt.Sprintf("  %s  %s  %s", get("ts"), get("workspace"), get("status"))
		if v := get("trigger"); v != "" {
			line += fmt.Sprintf(" (%s)", v)
		}
		if ms, ok := e["duration_ms"].(float64); ok {
			line += fmt.Sprintf("  %dms", int64(ms))
		}
		num := func(k string) int64 {
			f, _ := e[k].(float64)
			return int64(f)
		}
		line += fmt.Sprintf("  +%d drafts, %d applied, %d quarantined",
			num("drafts_created"), num("drafts_applied"), num("drafts_quarantined"))
		if v := get("error"); v != "" {
			line += "  error=" + v
		}
		fmt.Fprintln(w, line)
	}
}

func printLastRuns(w io.Writer, daemonStatus map[string]any) {
	runs, ok := daemonStatus["last_runs"].(map[string]any)
	if !ok || len(runs) == 0 {
		return
	}
	fmt.Fprintln(w, "\nLast cold-path activity:")
	keys := make([]string, 0, len(runs))
	for k := range runs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, ws := range keys {
		rec, ok := runs[ws].(map[string]any)
		if !ok {
			continue
		}
		at, _ := rec["at"].(string)
		source, _ := rec["source"].(string)
		kind, _ := rec["kind"].(string)
		errStr, _ := rec["error"].(string)
		line := fmt.Sprintf("  %s  %s (%s %s)", ws, at, source, kind)
		if kind == "run" {
			created, _ := rec["drafts_created"].(float64)
			applied, _ := rec["drafts_applied"].(float64)
			line += fmt.Sprintf("  +%d drafts, %d applied", int(created), int(applied))
		}
		if errStr != "" {
			line += "  error=" + errStr
		}
		fmt.Fprintln(w, line)
	}
}

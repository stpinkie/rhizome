package evolution

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	rhevo "github.com/stpinkie/rhizome/pkg/evolution"
)

// newActivityCommand returns the daemonless audit-trail reader — the
// `mesh audit` precedent applied to evolution-audit.jsonl.
func newActivityCommand() *cobra.Command {
	var tail int
	var kind, workspace, since string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show the tail of the evolution audit log",
		Long: "Print recent entries from the local evolution audit trail " +
			"(evolution-audit.jsonl under RHIZOME_HOME). The daemon's " +
			"evolution runtime appends one JSONL entry per cold-path run, " +
			"draft decision, apply, rollback, lifecycle transition, prune, " +
			"and feedback record.",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			var filter rhevo.AuditFilter
			filter.Op = strings.TrimSpace(kind)
			if workspace != "" {
				filter.Workspace = expandPath(workspace)
			}
			if since != "" {
				at, err := parseActivitySince(since)
				if err != nil {
					fmt.Fprintf(w, "error: %v\n", err)
					return
				}
				filter.Since = at
			}
			path := rhevo.DefaultAuditPath()
			entries, err := rhevo.ReadAuditEntries(path, tail, filter)
			if err != nil {
				fmt.Fprintf(w, "error reading audit log: %v\n", err)
				return
			}

			if asJSON {
				out, err := json.MarshalIndent(entries, "", "  ")
				if err != nil {
					fmt.Fprintf(w, "error encoding audit entries: %v\n", err)
					return
				}
				fmt.Fprintln(w, string(out))
				return
			}

			if len(entries) == 0 {
				fmt.Fprintf(w, "No audit entries in %s\n", path)
				return
			}
			for _, e := range entries {
				fmt.Fprintln(w, formatAuditEntry(e))
			}
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 20, "number of recent entries to show")
	cmd.Flags().StringVar(&kind, "kind", "", "only entries with this op (e.g. run.finished, draft.*)")
	cmd.Flags().StringVar(&workspace, "workspace", "", "only entries for this workspace")
	cmd.Flags().StringVar(&since, "since", "", "only entries newer than this (duration like 30m, or RFC3339)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON entries")
	return cmd
}

// parseActivitySince accepts either a Go duration ("30m", "1h" → now-dur)
// or an RFC3339 timestamp — the same semantics as GET /evolution/activity.
func parseActivitySince(raw string) (time.Time, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf(
		"invalid since %q: want a duration (30m, 1h) or RFC3339 timestamp", raw)
}

// formatAuditEntry renders one audit entry as a single log line.
func formatAuditEntry(e map[string]any) string {
	get := func(k string) string {
		s, _ := e[k].(string)
		return s
	}
	line := fmt.Sprintf("%s  %-16s", get("ts"), get("op"))
	if v := get("status"); v != "" {
		line += fmt.Sprintf("  %s", v)
	}
	if v := get("workspace"); v != "" {
		line += fmt.Sprintf("  ws=%s", v)
	}
	if v := get("skill"); v != "" {
		line += fmt.Sprintf("  skill=%s", v)
	} else if v := get("target_skill"); v != "" {
		line += fmt.Sprintf("  skill=%s", v)
	}
	if v := get("draft_id"); v != "" {
		line += fmt.Sprintf("  draft=%s", v)
	}
	if v := get("duration_ms"); v != "" {
		line += fmt.Sprintf("  %sms", v)
	} else if ms, ok := e["duration_ms"].(float64); ok {
		line += fmt.Sprintf("  %dms", int64(ms))
	}
	if v := get("error"); v != "" {
		line += fmt.Sprintf("  error=%s", v)
	}
	if v := get("detail"); v != "" {
		line += fmt.Sprintf("  detail=%s", v)
	}
	return line
}

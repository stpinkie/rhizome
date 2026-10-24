package evolution

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	rhevo "github.com/stpinkie/rhizome/pkg/evolution"
)

func newRecordsCommand() *cobra.Command {
	var sel workspaceSelection
	var kind string
	var limit int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "records",
		Short: "List learning records (task outcomes and clustered patterns)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			workspaces, err := sel.workspaces(cfg)
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}

			explicit := sel.workspace != "" || sel.agent != ""
			seen := make(map[string]struct{})
			records := make([]rhevo.LearningRecord, 0)
			for _, ws := range workspaces {
				list, lerr := rhevo.LoadRecordsByKind(storeFor(cfg, ws), kind)
				if lerr != nil {
					fmt.Fprintf(w, "error: %v\n", lerr)
					return
				}
				for _, r := range list {
					// Narrow shared-state_dir records to the selected
					// workspace; unowned records stay in their own store.
					if explicit && r.WorkspaceID != "" && r.WorkspaceID != ws {
						continue
					}
					key := r.WorkspaceID + "\x00" + r.ID
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					records = append(records, r)
				}
			}
			if limit > 0 && len(records) > limit {
				records = records[len(records)-limit:]
			}
			if asJSON {
				data, _ := json.MarshalIndent(records, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			if len(records) == 0 {
				fmt.Fprintln(w, "No records.")
				return
			}
			for _, r := range records {
				success := ""
				if r.Success != nil {
					if *r.Success {
						success = "ok"
					} else {
						success = "failed"
					}
				}
				fmt.Fprintf(w, "%s  %-7s  %-9s  %-6s  %s\n",
					r.ID, r.Kind, r.Status, success, truncate(r.Summary, 80))
			}
		},
	}
	sel.register(cmd)
	cmd.Flags().StringVar(&kind, "kind", "", "record kind: task|pattern|feedback (default all)")
	cmd.Flags().IntVar(&limit, "limit", 50, "maximum records to show (0 = all)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func newSkillsCommand() *cobra.Command {
	var sel workspaceSelection
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "List evolved-skill profiles (version, status, usage)",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			workspaces, err := sel.workspaces(cfg)
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			explicit := sel.workspace != "" || sel.agent != ""
			seen := make(map[string]struct{})
			profiles := make([]rhevo.SkillProfile, 0)
			for _, ws := range workspaces {
				list, err := storeFor(cfg, ws).LoadProfiles()
				if err != nil {
					fmt.Fprintf(w, "error: %v\n", err)
					return
				}
				for _, p := range list {
					if explicit && p.WorkspaceID != "" && p.WorkspaceID != ws {
						continue
					}
					key := p.WorkspaceID + "\x00" + p.SkillName
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					profiles = append(profiles, p)
				}
			}
			if asJSON {
				data, _ := json.MarshalIndent(profiles, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			if len(profiles) == 0 {
				fmt.Fprintln(w, "No skill profiles.")
				return
			}
			for _, p := range profiles {
				last := "never"
				if !p.LastUsedAt.IsZero() {
					last = p.LastUsedAt.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(w, "%s  v%s  %-8s  uses=%d retention=%.2f last=%s",
					p.SkillName, p.CurrentVersion, p.Status, p.UseCount, p.RetentionScore, last)
				if p.Origin != "" {
					fmt.Fprintf(w, "  origin=%s", p.Origin)
				}
				fmt.Fprintln(w)
			}
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	cmd.AddCommand(newSkillsHistoryCommand())
	cmd.AddCommand(newSkillTransitionCommand("archive"))
	cmd.AddCommand(newSkillTransitionCommand("restore"))
	return cmd
}

// newSkillsHistoryCommand renders a skill's VersionHistory — version,
// action, draft link, rollback marks — daemonless off the state dir.
func newSkillsHistoryCommand() *cobra.Command {
	var sel workspaceSelection
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "history <name>",
		Short: "Show a skill's version history (drafts, applies, rollbacks)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			workspaces, err := sel.workspaces(cfg)
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			explicit := sel.workspace != "" || sel.agent != ""
			var found *rhevo.SkillProfile
			for _, ws := range workspaces {
				profiles, err := storeFor(cfg, ws).LoadProfiles()
				if err != nil {
					fmt.Fprintf(w, "error: %v\n", err)
					return
				}
				for _, p := range profiles {
					if p.SkillName != args[0] {
						continue
					}
					if explicit && p.WorkspaceID != "" && p.WorkspaceID != ws {
						continue
					}
					if found != nil && found.WorkspaceID != p.WorkspaceID {
						fmt.Fprintf(w, "error: skill %q is ambiguous across workspaces; pass --workspace\n", args[0])
						return
					}
					cp := p
					found = &cp
				}
			}
			if found == nil {
				fmt.Fprintf(w, "No profile for skill %q.\n", args[0])
				return
			}
			if asJSON {
				data, _ := json.MarshalIndent(found.VersionHistory, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			fmt.Fprintf(w, "%s  current=v%s  %s\n", found.SkillName, found.CurrentVersion, found.Status)
			if len(found.VersionHistory) == 0 {
				fmt.Fprintln(w, "  no version history.")
				return
			}
			for _, e := range found.VersionHistory {
				mark := ""
				if e.Rollback {
					mark = " [rollback]"
				}
				fmt.Fprintf(w, "  %s  v%-40s  %-10s%s",
					e.Timestamp.Local().Format("2006-01-02 15:04"), e.Version, e.Action, mark)
				if e.DraftID != "" && e.DraftID != e.Version {
					fmt.Fprintf(w, "  draft=%s", e.DraftID)
				}
				fmt.Fprintln(w)
				if e.RollbackReason != "" {
					fmt.Fprintf(w, "      reason: %s\n", e.RollbackReason)
				}
				if e.Summary != "" {
					fmt.Fprintf(w, "      %s\n", e.Summary)
				}
			}
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

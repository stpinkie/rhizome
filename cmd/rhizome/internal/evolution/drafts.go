package evolution

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	rhevo "github.com/stpinkie/rhizome/pkg/evolution"
)

func newDraftsCommand() *cobra.Command {
	var sel workspaceSelection
	var state string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "drafts",
		Short: "List skill drafts across workspaces",
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

			var states []rhevo.DraftStatus
			if state != "" {
				for _, part := range splitComma(state) {
					states = append(states, rhevo.DraftStatus(part))
				}
			}

			// With an explicit --workspace/--agent the loop has exactly one
			// workspace; filtering by it also narrows drafts inside a shared
			// state_dir. With no selection, pass "" so every draft surfaces.
			explicit := sel.workspace != "" || sel.agent != ""
			seen := make(map[string]struct{})
			drafts := make([]rhevo.SkillDraft, 0)
			for _, ws := range workspaces {
				filter := ""
				if explicit {
					filter = ws
				}
				list, err := rhevo.SortedDrafts(storeFor(cfg, ws), filter, states...)
				if err != nil {
					fmt.Fprintf(w, "error: %v\n", err)
					return
				}
				for _, d := range list {
					key := d.WorkspaceID + "\x00" + d.ID
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					drafts = append(drafts, d)
				}
			}
			if asJSON {
				data, _ := json.MarshalIndent(drafts, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			if len(drafts) == 0 {
				fmt.Fprintln(w, "No drafts.")
				return
			}
			multi := len(workspaces) > 1
			for _, d := range drafts {
				line := fmt.Sprintf("%s  %-10s  %-7s  %s", d.ID, d.Status, d.ChangeKind, d.TargetSkillName)
				if multi {
					line += fmt.Sprintf("  (%s)", d.WorkspaceID)
				}
				fmt.Fprintln(w, line)
				if d.HumanSummary != "" {
					fmt.Fprintf(w, "    %s\n", d.HumanSummary)
				}
			}
		},
	}
	sel.register(cmd)
	cmd.Flags().
		StringVar(&state, "state", "", "comma-separated states to include (candidate,quarantined,accepted,rejected)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func newDraftCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Show, accept, or reject a skill draft",
	}
	cmd.AddCommand(
		newDraftShowCommand(),
		newDraftAcceptCommand(),
		newDraftRejectCommand(),
	)
	return cmd
}

func newDraftShowCommand() *cobra.Command {
	var sel workspaceSelection
	var diff, asJSON bool
	cmd := &cobra.Command{
		Use:   "show <draft-id>",
		Short: "Show a skill draft (optionally the applied diff)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			draft, ws, err := findDraftAcross(cfg, sel, args[0])
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if asJSON {
				data, _ := json.MarshalIndent(draft, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			fmt.Fprintf(w, "Draft %s\n", draft.ID)
			fmt.Fprintf(w, "  workspace: %s\n", draft.WorkspaceID)
			fmt.Fprintf(w, "  status: %s   type: %s   change: %s\n", draft.Status, draft.DraftType, draft.ChangeKind)
			fmt.Fprintf(w, "  target skill: %s   source pattern: %s\n", draft.TargetSkillName, draft.SourceRecordID)
			if len(draft.MatchedSkillRefs) > 0 {
				fmt.Fprintf(w, "  matched skills: %v\n", draft.MatchedSkillRefs)
			}
			if draft.HumanSummary != "" {
				fmt.Fprintf(w, "  summary: %s\n", draft.HumanSummary)
			}
			for _, n := range draft.ReviewNotes {
				fmt.Fprintf(w, "  note: %s\n", n)
			}
			for _, f := range draft.ScanFindings {
				fmt.Fprintf(w, "  finding: %s\n", f)
			}
			if diff {
				out, derr := rhevo.RenderDraftDiff(ws, draft)
				if derr != nil {
					fmt.Fprintf(w, "  diff error: %v\n", derr)
					return
				}
				fmt.Fprintf(w, "\n%s", out)
				if !strings.HasSuffix(out, "\n") {
					fmt.Fprintln(w)
				}
				return
			}
			fmt.Fprintf(w, "\n%s\n", draft.BodyOrPatch)
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&diff, "diff", false, "render the applied before/after instead of the raw draft body")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

func newDraftAcceptCommand() *cobra.Command {
	var sel workspaceSelection
	var force bool
	cmd := &cobra.Command{
		Use:   "accept <draft-id>",
		Short: "Apply a draft to the workspace skills (daemon required)",
		Long: "Apply a candidate draft through the production apply path " +
			"(backup, validate, write, profile sync). Quarantined drafts need --force.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			// Only pin the workspace when the operator selected one — the
			// daemon otherwise searches every workspace it knows.
			workspace := ""
			if sel.workspace != "" || sel.agent != "" {
				var werr error
				workspace, werr = sel.singleWorkspace(cfg)
				if werr != nil {
					fmt.Fprintf(w, "error: %v\n", werr)
					return
				}
			}
			body, _ := json.Marshal(map[string]any{"workspace": workspace, "force": force})
			respBody, status, err := internal.DaemonRequest(
				http.MethodPost, "/evolution/drafts/"+url.PathEscape(args[0])+"/accept", body, 5*time.Minute)
			if err != nil {
				fmt.Fprintf(w, "error: %v (daemon required for draft accept)\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(respBody))
				return
			}
			var resp struct {
				Draft rhevo.SkillDraft `json:"draft"`
			}
			if err := json.Unmarshal(respBody, &resp); err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			fmt.Fprintf(w, "Draft %s → %s (skill %s)\n", resp.Draft.ID, resp.Draft.Status, resp.Draft.TargetSkillName)
		},
	}
	sel.register(cmd)
	cmd.Flags().BoolVar(&force, "force", false, "accept a quarantined draft despite scan findings (logged)")
	return cmd
}

func newDraftRejectCommand() *cobra.Command {
	var sel workspaceSelection
	cmd := &cobra.Command{
		Use:   "reject <draft-id> [reason]",
		Short: "Reject a draft (terminal; blocks re-drafting its pattern)",
		Args:  cobra.RangeArgs(1, 2),
		Run: func(cmd *cobra.Command, args []string) {
			reason := ""
			if len(args) > 1 {
				reason = args[1]
			}
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
			body, _ := json.Marshal(map[string]any{"workspace": workspace, "reason": reason})
			respBody, status, err := internal.DaemonRequest(
				http.MethodPost, "/evolution/drafts/"+url.PathEscape(args[0])+"/reject", body, 30*time.Second)
			if err != nil {
				fmt.Fprintf(w, "error: %v (daemon required for draft reject)\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(respBody))
				return
			}
			fmt.Fprintf(w, "Draft %s rejected.\n", args[0])
		},
	}
	sel.register(cmd)
	return cmd
}

func splitComma(s string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

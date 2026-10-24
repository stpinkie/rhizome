package evolution

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	rhevo "github.com/stpinkie/rhizome/pkg/evolution"
)

// newFeedbackCommand builds `evolution feedback <good|bad>` — an operator
// signal on a task record or session. With no link flags it marks the
// workspace's most recent task record (the last turn). Feedback joins the
// cold path's evidence: a bad link excludes the record from the admitted
// set; a good mark lifts the judge's veto. It never clusters.
func newFeedbackCommand() *cobra.Command {
	var sel workspaceSelection
	var recordID, sessionKey, reason string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "feedback <good|bad>",
		Short: "Record operator feedback on a task (daemon required)",
		Long: "Attach a good/bad signal to a task record (--record, prefix " +
			"match) or every task in a session (--session). With neither, the " +
			"workspace's most recent task record — the last turn — is marked. " +
			"A bad link demotes the task to failure evidence and keeps it out " +
			"of the admitted set; a good mark keeps the judge's veto off it.",
		Args: cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			w := cmd.OutOrStdout()
			cfg, err := loadEvolutionConfig()
			if err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if !rhevo.IsValidFeedbackSignal(args[0]) {
				fmt.Fprintf(w, "error: signal must be good|bad, got %q\n", args[0])
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
				"workspace":   workspace,
				"agent":       sel.agent,
				"signal":      args[0],
				"record_id":   recordID,
				"session_key": sessionKey,
				"reason":      reason,
			})
			respBody, status, err := internal.DaemonRequest(
				http.MethodPost, "/evolution/feedback", body, 30*time.Second)
			if err != nil {
				fmt.Fprintf(w, "error: %v (daemon required for feedback)\n", err)
				return
			}
			if status != http.StatusOK {
				fmt.Fprintf(w, "daemon returned %d: %s\n", status, string(respBody))
				return
			}
			var resp struct {
				Feedback rhevo.LearningRecord `json:"feedback"`
			}
			if err := json.Unmarshal(respBody, &resp); err != nil {
				fmt.Fprintf(w, "error: %v\n", err)
				return
			}
			if asJSON {
				data, _ := json.MarshalIndent(resp.Feedback, "", "  ")
				fmt.Fprintln(w, string(data))
				return
			}
			fmt.Fprintf(w, "Recorded %s feedback: %s\n", args[0], resp.Feedback.ID)
			fmt.Fprintf(w, "  %s\n", resp.Feedback.Summary)
		},
	}
	sel.register(cmd)
	cmd.Flags().StringVar(&recordID, "record", "", "task record id to mark (prefix ok)")
	cmd.Flags().StringVar(&sessionKey, "session", "", "mark every task in this session")
	cmd.Flags().StringVar(&reason, "reason", "", "why — recorded on the feedback entry")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print raw JSON")
	return cmd
}

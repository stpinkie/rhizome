package swarm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
)

// runSummary is the CLI-side view of a persisted swarm run record.
type runSummary struct {
	RunID      string `json:"run_id"`
	SwarmID    string `json:"swarm_id"`
	Goal       string `json:"goal"`
	Status     string `json:"status"`
	Summary    string `json:"summary,omitempty"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	RetryOf    string `json:"retry_of,omitempty"`
	Subtasks   []struct {
		ID     string `json:"id,omitempty"`
		Task   string `json:"task"`
		Status string `json:"status"`
		PeerID string `json:"peer_id,omitempty"`
		Error  string `json:"error,omitempty"`
	} `json:"subtasks,omitempty"`
}

func newRunsCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "runs <swarm-id>",
		Short: "List recorded orchestration runs for a swarm (daemon required)",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			swarmID := args[0]
			if err := internal.ValidateSwarmID(swarmID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			data, code, err := daemonRequest(http.MethodGet,
				"/network/swarms/"+swarmID+"/runs", nil, 15*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — runs require a running daemon\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
				os.Exit(1)
			}
			if asJSON {
				var pretty any
				if json.Unmarshal(data, &pretty) == nil {
					out, _ := json.MarshalIndent(pretty, "", "  ")
					fmt.Println(string(out))
					return
				}
				fmt.Println(string(data))
				return
			}
			var parsed struct {
				Runs []runSummary `json:"runs"`
			}
			if err := json.Unmarshal(data, &parsed); err != nil || len(parsed.Runs) == 0 {
				fmt.Printf("No recorded runs for swarm %q.\n", swarmID)
				return
			}
			fmt.Printf("Runs for %q:\n", swarmID)
			for _, r := range parsed.Runs {
				line := fmt.Sprintf("  - %s status=%s subtasks=%d", r.RunID, r.Status, len(r.Subtasks))
				if r.DurationMS > 0 {
					line += fmt.Sprintf(" dur=%dms", r.DurationMS)
				}
				line += " — " + r.Goal
				fmt.Println(line)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

func newRunStatusCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "run-status <swarm-id> <run-id>",
		Short: "Show one orchestration run's status and subtasks (daemon required)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			swarmID, runID := args[0], args[1]
			if err := internal.ValidateSwarmID(swarmID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			data, code, err := daemonRequest(http.MethodGet,
				"/network/swarms/"+swarmID+"/runs?run="+runID, nil, 15*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — runs require a running daemon\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
				os.Exit(1)
			}
			if asJSON {
				var pretty any
				if json.Unmarshal(data, &pretty) == nil {
					out, _ := json.MarshalIndent(pretty, "", "  ")
					fmt.Println(string(out))
					return
				}
				fmt.Println(string(data))
				return
			}
			var r runSummary
			if err := json.Unmarshal(data, &r); err != nil || r.RunID == "" {
				fmt.Println(string(data))
				return
			}
			fmt.Printf("Run %s — %s (%dms)\nGoal: %s\n", r.RunID, r.Status, r.DurationMS, r.Goal)
			if r.RetryOf != "" {
				fmt.Printf("Retry of: %s\n", r.RetryOf)
			}
			for i, st := range r.Subtasks {
				id := st.ID
				if id == "" {
					id = fmt.Sprintf("%d", i)
				}
				line := fmt.Sprintf("  [%s] %s — %s", id, st.Status, st.Task)
				if st.PeerID != "" {
					line += " (" + st.PeerID + ")"
				}
				if st.Error != "" {
					line += " err: " + st.Error
				}
				fmt.Println(line)
			}
			if r.Summary != "" {
				fmt.Println()
				fmt.Println(r.Summary)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print as JSON")
	return cmd
}

func newRunCancelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run-cancel <swarm-id> <run-id>",
		Short: "Cancel a run's pending or in-flight subtask offers (daemon required)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			swarmID, runID := args[0], args[1]
			if err := internal.ValidateSwarmID(swarmID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			payload, _ := json.Marshal(map[string]any{"run_id": runID})
			data, code, err := daemonRequest(http.MethodPost,
				"/network/swarms/"+swarmID+"/runs/cancel", payload, 15*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — runs require a running daemon\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
				os.Exit(1)
			}
			fmt.Printf("Run %s cancelled.\n", runID)
		},
	}
	return cmd
}

func newRunRetryCommand() *cobra.Command {
	var agentID string
	var asJSON bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "run-retry <swarm-id> <run-id>",
		Short: "Re-offer a run's failed/expired/dead_letter/timeout subtasks (daemon required)",
		Long: "Creates a new run linked to the source via retry_of: subtasks that " +
			"failed, expired, dead-lettered, or timed out are re-offered when all " +
			"their dependencies resolved done. Done subtasks carry over unchanged.",
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			swarmID, runID := args[0], args[1]
			if err := internal.ValidateSwarmID(swarmID); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			validatedAgentID, err := internal.ValidateAgentID(agentID)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			payload, _ := json.Marshal(map[string]any{
				"run_id":   runID,
				"agent_id": validatedAgentID,
			})
			data, code, err := daemonRequest(http.MethodPost,
				"/network/swarms/"+swarmID+"/runs/retry", payload, timeout+30*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — runs require a running daemon\n", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "Error: %s\n", strings.TrimSpace(string(data)))
				os.Exit(1)
			}
			if asJSON {
				var pretty any
				if json.Unmarshal(data, &pretty) == nil {
					out, _ := json.MarshalIndent(pretty, "", "  ")
					fmt.Println(string(out))
					return
				}
				fmt.Println(string(data))
				return
			}
			var res struct {
				RunID    string `json:"run_id"`
				Status   string `json:"status"`
				Subtasks []struct {
					Task   string `json:"task"`
					Status string `json:"status"`
					PeerID string `json:"peer_id,omitempty"`
					Error  string `json:"error,omitempty"`
				} `json:"subtasks"`
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(data, &res); err != nil {
				fmt.Println(string(data))
				return
			}
			fmt.Printf("Retry run %s — %s\n", res.RunID, res.Status)
			for i, st := range res.Subtasks {
				line := fmt.Sprintf("  [%d] %s — %s", i+1, st.Status, st.Task)
				if st.PeerID != "" {
					line += " (" + st.PeerID + ")"
				}
				if st.Error != "" {
					line += " err: " + st.Error
				}
				fmt.Println(line)
			}
			if res.Summary != "" {
				fmt.Println()
				fmt.Println(res.Summary)
			}
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "main", "Default agent for subtasks missing one")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print raw JSON result")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Overall retry timeout")
	return cmd
}

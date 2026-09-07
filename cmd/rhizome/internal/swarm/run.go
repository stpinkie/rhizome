package swarm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newRunCommand() *cobra.Command {
	var agentID string
	var asJSON bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "run <swarm-id> <goal>",
		Short: "Decompose a goal and orchestrate it across the swarm",
		Long: "Runs a goal through the swarm: the daemon's orchestrator decomposes it " +
			"into subtasks, offers them on the swarm work queue, waits for the " +
			"winning claimants' results, and returns a synthesized summary.",
		Args: cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			swarmID, goal := args[0], args[1]
			payload, _ := json.Marshal(map[string]any{
				"goal":     goal,
				"agent_id": agentID,
			})
			data, code, err := daemonRequest(http.MethodPost,
				"/network/swarms/"+swarmID+"/run", payload, timeout+30*time.Second)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v — swarm run requires a running daemon\n", err)
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
			fmt.Println()
			fmt.Println(res.Summary)
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "main", "Default agent for subtasks")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print raw JSON result")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Overall run timeout")
	return cmd
}

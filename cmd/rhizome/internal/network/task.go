package network

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// formatUsageLine renders a remote task's summed usage report for human
// output. Returns "" for nil reports so callers can print conditionally.
func formatUsageLine(u *toolshared.RemoteUsage) string {
	if u == nil {
		return ""
	}
	return fmt.Sprintf(
		"%d llm calls, %d prompt + %d completion = %d tokens, %d ms",
		u.LLMCalls, u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.DurationMS,
	)
}

// NewTaskCommand returns the task command group for submitting to and
// inspecting asynchronous mesh tasks on a trusted peer.
func NewTaskCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Submit and inspect asynchronous mesh tasks on a trusted peer",
		Long: "Submit new async agent tasks or query status, fetch results, " +
			"cancel, or list tasks over /rhizome/agent-task/1.0.0.",
	}

	var submitMaxCost string
	var submitAttaches []string
	submitCmd := &cobra.Command{
		Use:   "submit <peer-multiaddr> <agent-id> <task>",
		Short: "Submit an async task to a trusted peer and return its task id",
		Args:  cobra.MinimumNArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			if err := internal.ValidateMultiaddrWithPeerID(args[0]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			agentID, err := internal.ValidateAgentID(args[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			if err := internal.ValidateMaxCost(submitMaxCost); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			runTaskSubmit(cmd, args[0], agentID, strings.Join(args[2:], " "),
				submitMaxCost, submitAttaches)
		},
	}
	submitCmd.Flags().StringVar(&submitMaxCost, "max-cost", "",
		"Spend cap for this task (decimal); binds the request's econ max_charge — the peer may price_floor:-reject and failover proceeds")
	submitCmd.Flags().
		StringArrayVar(&submitAttaches, "attach", nil, "Attach a local file to the remote task (repeatable)")

	statusCmd := &cobra.Command{
		Use:   "status <peer-multiaddr> <task-id>",
		Short: "Show the status of a remote task",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			mustValidateTaskPeerArgs(args[0], args[1])
			runTaskOp(cmd, args[0], args[1], "status", 0)
		},
	}

	resultCmd := &cobra.Command{
		Use:   "result <peer-multiaddr> <task-id>",
		Short: "Fetch the result of a remote task (long-polls with --wait)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			mustValidateTaskPeerArgs(args[0], args[1])
			wait, _ := cmd.Flags().GetDuration("wait")
			runTaskOp(cmd, args[0], args[1], "result", wait)
		},
	}
	resultCmd.Flags().Duration("wait", 30*time.Second, "Long-poll duration while waiting for completion")

	cancelCmd := &cobra.Command{
		Use:   "cancel <peer-multiaddr> <task-id>",
		Short: "Cancel a running remote task",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			mustValidateTaskPeerArgs(args[0], args[1])
			runTaskOp(cmd, args[0], args[1], "cancel", 0)
		},
	}

	listCmd := &cobra.Command{
		Use:   "list <peer-multiaddr>",
		Short: "List tasks this node has submitted to a peer",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if err := internal.ValidateMultiaddrWithPeerID(args[0]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			runTaskOp(cmd, args[0], "", "list", 0)
		},
	}

	for _, sub := range []*cobra.Command{submitCmd, statusCmd, resultCmd, cancelCmd, listCmd} {
		sub.Flags().String("bootstrap", "", "Optional bootstrap multiaddr of a Rhizome daemon")
		sub.Flags().Bool("json", false, "Print raw JSON output")
		cmd.AddCommand(sub)
	}
	return cmd
}

// runTaskSubmit dials the peer, submits the async task with failover, and
// prints the resulting task id. Equivalent to `spawn --no-wait` but under
// the task verb so submit lives beside status/result/cancel/list.
func runTaskSubmit(
	cmd *cobra.Command,
	maddrStr, agentID, task, maxCost string,
	attaches []string,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	m, pid, cleanup := dialMeshPeer(ctx, cmd.Flags(), maddrStr)
	defer cleanup()

	media := make([]mesh.MediaAttachment, 0, len(attaches))
	for _, p := range attaches {
		if strings.TrimSpace(p) != "" {
			media = append(media, mesh.MediaAttachment{Path: p})
		}
	}

	usedPeer, taskID, err := m.SubmitRemoteTaskWithPeer(ctx, pid, mesh.RemoteCall{
		TargetAgentID: agentID,
		SystemPrompt:  task,
		Media:         media,
		MaxCost:       maxCost,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Remote submit failed: %v\n", err)
		os.Exit(1)
	}

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		data, mErr := json.MarshalIndent(map[string]string{
			"task_id": taskID,
			"peer_id": usedPeer.String(),
		}, "", "  ")
		if mErr != nil {
			fmt.Fprintf(os.Stderr, "Error encoding response: %v\n", mErr)
			os.Exit(1)
		}
		cmd.Println(string(data))
		return
	}
	fmt.Printf("Task:   %s\n", taskID)
	if usedPeer != pid {
		fmt.Printf("Note:   task routed to failover peer %s\n", usedPeer)
	}
	fmt.Printf("Check progress: rhizome network task status %s %s\n", maddrStr, taskID)
}

func mustValidateTaskPeerArgs(maddrStr, taskID string) {
	if err := internal.ValidateMultiaddrWithPeerID(maddrStr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := internal.ValidateTaskID(taskID); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runTaskOp(cmd *cobra.Command, maddrStr, taskID, op string, wait time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), wait+60*time.Second)
	defer cancel()

	m, pid, cleanup := dialMeshPeer(ctx, cmd.Flags(), maddrStr)
	defer cleanup()

	var resp agenttask.Response
	var err error
	var tasks []agenttask.TaskInfo

	switch op {
	case "status":
		resp, err = m.RemoteTaskStatus(ctx, pid, taskID)
	case "result":
		resp, err = m.RemoteTaskResult(ctx, pid, taskID, wait)
	case "cancel":
		resp, err = m.CancelRemoteTask(ctx, pid, taskID)
	case "list":
		tasks, err = m.ListRemoteTasks(ctx, pid)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Task request failed: %v\n", err)
		os.Exit(1)
	}

	asJSON, _ := cmd.Flags().GetBool("json")

	if op == "list" {
		printTaskList(cmd, tasks, asJSON)
		return
	}

	if asJSON {
		data, mErr := json.MarshalIndent(resp, "", "  ")
		if mErr != nil {
			fmt.Fprintf(os.Stderr, "Error encoding response: %v\n", mErr)
			os.Exit(1)
		}
		cmd.Println(string(data))
		return
	}

	fmt.Printf("Task:   %s\n", taskID)
	fmt.Printf("Status: %s\n", resp.Status)
	if resp.Error != "" {
		fmt.Printf("Error:  %s\n", resp.Error)
	}
	if resp.Result != nil {
		content := resp.Result.ForUser
		if content == "" {
			content = resp.Result.ForLLM
		}
		if content != "" {
			fmt.Printf("Result:\n%s\n", content)
		}
	}
	if resp.Usage != nil {
		fmt.Printf("Usage:  %s\n", formatUsageLine(resp.Usage))
	}
}

func printTaskList(cmd *cobra.Command, tasks []agenttask.TaskInfo, asJSON bool) {
	if asJSON {
		data, err := json.MarshalIndent(tasks, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding tasks: %v\n", err)
			os.Exit(1)
		}
		cmd.Println(string(data))
		return
	}
	if len(tasks) == 0 {
		cmd.Println("No tasks.")
		return
	}
	cmd.Println("Tasks:")
	for _, t := range tasks {
		line := fmt.Sprintf("  - %s  %s", t.TaskID, t.Status)
		if t.AgentID != "" {
			line += fmt.Sprintf("  agent=%s", t.AgentID)
		}
		if t.Error != "" {
			line += fmt.Sprintf("  error=%s", t.Error)
		}
		if t.Usage != nil {
			line += fmt.Sprintf("  usage=%dtok/%dcalls", t.Usage.TotalTokens, t.Usage.LLMCalls)
		}
		cmd.Println(line)
	}
}

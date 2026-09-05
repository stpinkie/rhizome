package network

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
)

// NewTaskCommand returns the task command group for inspecting asynchronous
// mesh tasks submitted to a trusted peer.
func NewTaskCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Inspect asynchronous mesh tasks on a trusted peer",
		Long: "Query status, fetch results, cancel, or list async agent tasks " +
			"previously submitted to a peer over /rhizome/agent-task/1.0.0.",
	}

	statusCmd := &cobra.Command{
		Use:   "status <peer-multiaddr> <task-id>",
		Short: "Show the status of a remote task",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			runTaskOp(cmd, args[0], args[1], "status", 0)
		},
	}

	resultCmd := &cobra.Command{
		Use:   "result <peer-multiaddr> <task-id>",
		Short: "Fetch the result of a remote task (long-polls with --wait)",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
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
			runTaskOp(cmd, args[0], args[1], "cancel", 0)
		},
	}

	listCmd := &cobra.Command{
		Use:   "list <peer-multiaddr>",
		Short: "List tasks this node has submitted to a peer",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			runTaskOp(cmd, args[0], "", "list", 0)
		},
	}

	for _, sub := range []*cobra.Command{statusCmd, resultCmd, cancelCmd, listCmd} {
		sub.Flags().String("bootstrap", "", "Optional bootstrap multiaddr of a Rhizome daemon")
		sub.Flags().Bool("json", false, "Print raw JSON output")
		cmd.AddCommand(sub)
	}
	return cmd
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
		cmd.Println(line)
	}
}

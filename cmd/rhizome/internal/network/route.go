package network

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

// NewRouteCommand returns the route command, which picks the best available
// trusted peer for an agent id and dispatches a task to it.
func NewRouteCommand() *cobra.Command {
	var syncCall bool
	var wait time.Duration
	var pickTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "route <agent-id> <task>",
		Short: "Route a task to the best available trusted peer",
		Long: "Pick a connected, trusted peer that advertises the agent id " +
			"(and the requested op) in its capability manifest, preferring " +
			"direct connections and least-loaded peers, then dispatch the task.",
		Args: cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			agentID := args[0]
			task := strings.Join(args[1:], " ")
			runRoute(cmd, agentID, task, syncCall, wait, pickTimeout)
		},
	}
	cmd.Flags().Bool("sync", false, "Delegate synchronously instead of submitting an async task")
	cmd.Flags().DurationVar(&wait, "wait", 0, "After submitting, long-poll for the result up to this duration")
	cmd.Flags().DurationVar(&pickTimeout, "pick-timeout", 15*time.Second, "How long to wait for a suitable peer to appear")
	return cmd
}

func runRoute(cmd *cobra.Command, agentID, task string, syncCall bool, wait, pickTimeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout+wait+2*time.Minute)
	defer cancel()

	home := config.GetHome()
	identityDir := filepath.Join(home, "identity")
	derived, _, err := internal.LoadIdentity(identityDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No node identity found. Run: rhizome network onboard\n")
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(internal.GetConfigPath())
	if err != nil {
		cfg = config.DefaultConfig()
	}
	config.SetGlobal(cfg)
	cfg.Mesh.Enabled = true

	node, err := network.NewNode(ctx, derived.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: cfg.Mesh.BootstrapPeers,
		NATTraversal:   cfg.Mesh.NATTraversal,
		StaticRelays:   cfg.Mesh.StaticRelays,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting node: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = node.Close() }()

	m := mesh.NewMesh(node, nil, derived, cfg.Mesh, nil)
	if err = m.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting mesh: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = m.Stop() }()

	// Trust all configured peers up front so their capability manifests are
	// accepted as soon as the bootstrap connections come up.
	for _, p := range cfg.Mesh.TrustedPeers {
		if pid, err := peer.Decode(p); err == nil {
			m.TrustPeer(pid)
		}
	}

	op := "spawn"
	if syncCall {
		op = "delegate"
	}

	// Capability manifests arrive asynchronously after each connect; poll
	// until a suitable peer shows up or the pick deadline hits.
	pickCtx, pickCancel := context.WithTimeout(ctx, pickTimeout)
	defer pickCancel()

	var pid peer.ID
	var pickedCap mesh.Capability
	for {
		pid, pickedCap, err = m.PickPeer(agentID, op)
		if err == nil {
			break
		}
		select {
		case <-pickCtx.Done():
			fmt.Fprintf(os.Stderr, "No suitable peer: %v\n", err)
			os.Exit(1)
		case <-time.After(250 * time.Millisecond):
		}
	}

	fmt.Printf("Peer:  %s (active tasks: %d)\n", pid.String(), pickedCap.ActiveTasks)

	if syncCall {
		result, err := m.CallRemote(ctx, pid, mesh.RemoteCall{
			TargetAgentID: agentID,
			SystemPrompt:  task,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Remote call failed: %v\n", err)
			os.Exit(1)
		}
		if result.ForUser != "" {
			fmt.Println(result.ForUser)
		} else {
			fmt.Println(result.ForLLM)
		}
		return
	}

	taskID, err := m.SubmitRemoteTask(ctx, pid, mesh.RemoteCall{
		TargetAgentID: agentID,
		SystemPrompt:  task,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Remote submit failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Task:  %s\n", taskID)

	if wait > 0 {
		resp, err := m.RemoteTaskResult(ctx, pid, taskID, wait)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Result fetch failed: %v\n", err)
			os.Exit(1)
		}
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
		return
	}

	fmt.Println("Check progress: rhizome mesh task status <peer-multiaddr> " + taskID)
}

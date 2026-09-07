package network

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
)

// NewScatterCommand returns the scatter command, which fans the same task
// out to several capable trusted peers and aggregates the results.
func NewScatterCommand() *cobra.Command {
	var n, k int
	var strategy, model string
	var wait, pickTimeout time.Duration
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "scatter <agent-id> <task>",
		Short: "Fan a task out to multiple trusted peers and aggregate results",
		Long: "Submit the same task to every connected, trusted peer that " +
			"advertises the agent id for op spawn (or the --n best of them) " +
			"and combine the per-branch results. Strategies: 'first' returns " +
			"as soon as one branch succeeds and cancels the rest, 'quorum' " +
			"waits for --k branches and picks the most common result, 'all' " +
			"waits for every branch.",
		Args: cobra.MinimumNArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			agentID, err := internal.ValidateAgentID(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			if n < 0 {
				fmt.Fprintf(os.Stderr, "Error: --n must be >= 0\n")
				os.Exit(1)
			}
			if k < 0 {
				fmt.Fprintf(os.Stderr, "Error: --k must be >= 0\n")
				os.Exit(1)
			}
			strategy, err = internal.ValidateScatterStrategy(strategy)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			task := strings.Join(args[1:], " ")
			runScatter(cmd, agentID, task, model, strategy, n, k, wait, pickTimeout, asJSON)
		},
	}
	cmd.Flags().IntVar(&n, "n", 0, "Number of peers to fan out to (default: all capable peers)")
	cmd.Flags().StringVar(&strategy, "strategy", "all", "Aggregation strategy: first, quorum, all")
	cmd.Flags().IntVar(&k, "k", 0, "Quorum size for --strategy quorum (default: all submitted branches)")
	cmd.Flags().StringVar(&model, "model", "", "Model override forwarded to the remote agent")
	cmd.Flags().
		DurationVar(&wait, "wait", 60*time.Second, "Per-poll long-poll duration while waiting for branch results")
	cmd.Flags().
		DurationVar(&pickTimeout, "pick-timeout", 15*time.Second, "How long to wait for capable peers to appear")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the fan-out result as JSON")
	return cmd
}

func runScatter(
	cmd *cobra.Command,
	agentID, task, model, strategy string,
	n, k int,
	wait, pickTimeout time.Duration,
	asJSON bool,
) {
	ctx, cancel := context.WithTimeout(context.Background(), pickTimeout+wait+5*time.Minute)
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

	// Capability manifests arrive asynchronously after each connect; poll
	// until at least one capable peer shows up or the pick deadline hits.
	pickCtx, pickCancel := context.WithTimeout(ctx, pickTimeout)
	defer pickCancel()
	for len(m.PickPeerRanked(agentID, "spawn", nil)) == 0 {
		select {
		case <-pickCtx.Done():
			fmt.Fprintf(os.Stderr, "No capable trusted peer for agent %q\n", agentID)
			os.Exit(1)
		case <-time.After(250 * time.Millisecond):
		}
	}

	res, err := m.FanoutTask(ctx, mesh.FanoutRequest{
		AgentID:  agentID,
		Model:    model,
		Task:     task,
		N:        n,
		Strategy: strategy,
		K:        k,
		Wait:     wait,
	})

	if asJSON {
		data, mErr := json.MarshalIndent(res, "", "  ")
		if mErr != nil {
			fmt.Fprintf(os.Stderr, "Error encoding result: %v\n", mErr)
			os.Exit(1)
		}
		cmd.Println(string(data))
	} else {
		fmt.Printf("Fanout: %s  strategy=%s\n", res.FanoutID, res.Strategy)
		for _, b := range res.Branches {
			line := fmt.Sprintf("  - %s  %s", b.PeerID, b.Status)
			if b.TaskID != "" {
				line += fmt.Sprintf("  task=%s", b.TaskID)
			}
			if b.Error != "" {
				line += fmt.Sprintf("  error=%s", b.Error)
			}
			cmd.Println(line)
			if b.Result != nil {
				content := b.Result.ForUser
				if content == "" {
					content = b.Result.ForLLM
				}
				if content != "" {
					cmd.Printf("      result: %s\n", content)
				}
			}
		}
		if res.Winner != "" {
			fmt.Printf("Winner: %s\n", res.Winner)
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Fanout failed: %v\n", err)
		os.Exit(1)
	}
	for _, b := range res.Branches {
		if b.Status == string(agenttask.StatusDone) {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "No fan-out branch succeeded\n")
	os.Exit(1)
}

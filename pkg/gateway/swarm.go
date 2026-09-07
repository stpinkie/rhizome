package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	"github.com/stpinkie/rhizome/pkg/rhizome/swarm"
)

// activeSwarm holds the daemon's swarm layer so HTTP handlers can serve
// live swarm state without changing the RunWithMesh signature.
var activeSwarm atomic.Pointer[swarm.Swarm]

// SetSwarm registers the daemon's swarm layer (nil clears it). Called by the
// daemon when swarm support is enabled, before the HTTP handlers serve.
func SetSwarm(s *swarm.Swarm) {
	activeSwarm.Store(s)
}

// currentSwarm returns the registered swarm layer, or nil.
func currentSwarm() *swarm.Swarm {
	return activeSwarm.Load()
}

// wireSwarm connects the registered swarm layer to the mesh and agent loop:
// task dispatch, offer evaluation, presence load hints, shared-state writes,
// and goal orchestration.
func wireSwarm(sw *swarm.Swarm, m *mesh.Mesh, agentLoop *agent.AgentLoop, cfg *config.Config) {
	sw.SetTaskSubmitter(m.SubmitRemoteTaskWithPeer)
	sw.SetResultFetcher(m.RemoteTaskResult)
	sw.SetCapProbe(func() (string, int) {
		return "", m.ActiveTaskCount()
	})
	sw.SetOfferEvaluator(func(swarmID string, o swarm.Offer) bool {
		_, ok := agentLoop.GetRegistry().GetAgent(o.AgentID)
		return ok
	})
	sw.SetStateWriter(func(swarmID string, state []byte) error {
		dir := filepath.Join(cfg.WorkspacePath(), "swarm", swarmID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "state.json"), state, 0o644)
	})

	dispatch := func(ctx context.Context, prompt string) (string, error) {
		resp, err := agentLoop.ProcessRemoteDispatch(ctx, agent.RemoteDispatchRequest{
			AgentID:    "main",
			Prompt:     prompt,
			SessionKey: "swarm-orchestrator",
			SenderID:   "swarm",
		})
		if err != nil {
			return "", err
		}
		return resp, nil
	}

	sw.SetDecomposer(func(ctx context.Context, goal string) ([]swarm.Subtask, error) {
		agents := agentLoop.GetRegistry().ListAgentIDs()
		prompt := fmt.Sprintf(decomposePrompt, strings.Join(agents, ", "), goal)
		out, err := dispatch(ctx, prompt)
		if err != nil {
			return nil, err
		}
		return parseSubtasks(out), nil
	})
	sw.SetSynthesizer(func(ctx context.Context, goal string, results []swarm.SubtaskResult) (string, error) {
		var b strings.Builder
		fmt.Fprintf(&b, synthesizePrompt, goal)
		for i, r := range results {
			fmt.Fprintf(&b, "\n[subtask %d: %s — status %s]\n%s\n", i+1, r.Task, r.Status, r.Result)
		}
		return dispatch(ctx, b.String())
	})
}

const decomposePrompt = `You are decomposing a goal for a swarm of peer agents.
Available agent ids: %s

Goal: %s

Break the goal into independent subtasks that can run in parallel. Reply with
ONLY a JSON array — either of strings (task descriptions) or objects with
{"agent_id","task"} fields. No prose, no markdown fences.`

const synthesizePrompt = `Goal: %s

The swarm produced the following subtask results. Synthesize a concise final
answer to the goal.`

// parseSubtasks tolerantly parses the decomposer's JSON output. On parse
// failure it falls back to non-empty lines as task strings.
func parseSubtasks(raw string) []swarm.Subtask {
	raw = strings.TrimSpace(raw)
	// Strip markdown fences if the model added them.
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimPrefix(strings.TrimLeft(raw, "json\n"), "\n")
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
		raw = strings.TrimSpace(raw)
	}
	var objs []swarm.Subtask
	if err := json.Unmarshal([]byte(raw), &objs); err == nil && len(objs) > 0 {
		return objs
	}
	var strs []string
	if err := json.Unmarshal([]byte(raw), &strs); err == nil && len(strs) > 0 {
		out := make([]swarm.Subtask, 0, len(strs))
		for _, t := range strs {
			if strings.TrimSpace(t) != "" {
				out = append(out, swarm.Subtask{Task: t})
			}
		}
		return out
	}
	logger.Warnf("swarm decomposer returned unparseable output; using single-task fallback")
	return nil
}

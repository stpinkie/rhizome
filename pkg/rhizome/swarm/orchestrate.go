package swarm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
)

// Subtask is one unit of work produced by decomposing a goal.
type Subtask struct {
	AgentID string `json:"agent_id"`
	Task    string `json:"task"`
}

// SubtaskResult is the outcome of one dispatched subtask.
type SubtaskResult struct {
	Subtask
	OfferID string `json:"offer_id,omitempty"`
	PeerID  string `json:"peer_id,omitempty"`
	TaskID  string `json:"task_id,omitempty"`
	Status  string `json:"status"`
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RunResult is the aggregated outcome of a swarm goal run.
type RunResult struct {
	SwarmID    string          `json:"swarm_id"`
	Goal       string          `json:"goal"`
	Subtasks   []SubtaskResult `json:"subtasks"`
	Summary    string          `json:"summary"`
	DurationMS int64           `json:"duration_ms"`
}

// Decomposer turns a goal into subtasks. The daemon wires it to the local
// agent; nil means "one task = the whole goal".
type Decomposer func(ctx context.Context, goal string) ([]Subtask, error)

// Synthesizer merges subtask results into a summary. Nil means simple
// concatenation.
type Synthesizer func(ctx context.Context, goal string, results []SubtaskResult) (string, error)

// ResultFetcher polls a remote task to completion; usually
// mesh.Mesh.RemoteTaskResult.
type ResultFetcher func(ctx context.Context, pid peer.ID, taskID string, wait time.Duration) (agenttask.Response, error)

// SetDecomposer wires the goal decomposer used by RunGoal.
func (s *Swarm) SetDecomposer(fn Decomposer) {
	s.orch.decomposer = fn
}

// SetSynthesizer wires the result synthesizer used by RunGoal.
func (s *Swarm) SetSynthesizer(fn Synthesizer) {
	s.orch.synthesizer = fn
}

// SetResultFetcher wires the remote-task result poller used by RunGoal.
func (s *Swarm) SetResultFetcher(fn ResultFetcher) {
	s.orch.resultFetcher = fn
}

// orchestrator holds the seams for goal runs.
type orchestrator struct {
	decomposer    Decomposer
	synthesizer   Synthesizer
	resultFetcher ResultFetcher
}

// RunGoal decomposes a goal into subtasks, offers them to the swarm's work
// queue, waits for results, and returns the aggregated outcome. When no
// decomposer is wired the goal is offered as a single task.
func (s *Swarm) RunGoal(ctx context.Context, swarmID, goal, defaultAgent string) (RunResult, error) {
	started := time.Now()
	if !s.isJoined(swarmID) {
		return RunResult{}, fmt.Errorf("not a member of swarm %q", swarmID)
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return RunResult{}, fmt.Errorf("goal is required")
	}
	if defaultAgent == "" {
		defaultAgent = "main"
	}

	res := RunResult{SwarmID: swarmID, Goal: goal}
	s.publishEvent(runtimeevents.KindSwarmRunStart, map[string]any{
		"swarm_id": swarmID,
		"goal":     goal,
	})
	defer func() {
		res.DurationMS = time.Since(started).Milliseconds()
		s.publishEvent(runtimeevents.KindSwarmRunEnd, map[string]any{
			"swarm_id":    swarmID,
			"goal":        goal,
			"subtasks":    len(res.Subtasks),
			"duration_ms": res.DurationMS,
		})
	}()

	var subtasks []Subtask
	if s.orch.decomposer != nil {
		var err error
		subtasks, err = s.orch.decomposer(ctx, goal)
		if err != nil {
			return res, fmt.Errorf("decompose goal: %w", err)
		}
	}
	if len(subtasks) == 0 {
		subtasks = []Subtask{{AgentID: defaultAgent, Task: goal}}
	}
	for i := range subtasks {
		if subtasks[i].AgentID == "" {
			subtasks[i].AgentID = defaultAgent
		}
	}

	// Offer all subtasks, then wait for assignment + results.
	res.Subtasks = make([]SubtaskResult, len(subtasks))
	type pending struct {
		idx     int
		offerID string
	}
	var pend []pending
	for i, st := range subtasks {
		res.Subtasks[i].Subtask = st
		res.Subtasks[i].Status = "offering"
		offerID, err := s.Offer(ctx, swarmID, OfferRequest{
			AgentID: st.AgentID,
			Task:    st.Task,
		})
		if err != nil {
			res.Subtasks[i].Status = "failed"
			res.Subtasks[i].Error = err.Error()
			continue
		}
		res.Subtasks[i].OfferID = offerID
		pend = append(pend, pending{idx: i, offerID: offerID})
	}

	for _, p := range pend {
		s.awaitSubtask(ctx, swarmID, &res.Subtasks[p.idx], p.offerID)
	}

	if s.orch.synthesizer != nil {
		if summary, err := s.orch.synthesizer(ctx, goal, res.Subtasks); err == nil {
			res.Summary = summary
		}
	}
	if res.Summary == "" {
		res.Summary = defaultSummary(res.Subtasks)
	}
	return res, nil
}

// awaitSubtask waits for an offer to be assigned and then polls the remote
// task until it terminates or the context ends.
func (s *Swarm) awaitSubtask(ctx context.Context, swarmID string, out *SubtaskResult, offerID string) {
	// Phase 1: wait for the offer to leave "open". The queue closes its
	// resolved channel when the offer reaches a terminal state; select on it
	// instead of busy-waiting.
	resolved := s.queue.offerResolved(offerID)
	timeout := s.cfg.Queue.OfferTTL + s.cfg.Queue.ClaimWindow
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		out.Status = "cancelled"
		out.Error = ctx.Err().Error()
		return
	case <-timer.C:
		out.Status = "timeout"
		out.Error = "offer did not resolve in time"
		return
	case <-resolved:
	}
	info, ok := s.queue.offerInfo(offerID)
	if !ok {
		out.Status = "timeout"
		out.Error = "offer did not resolve in time"
		return
	}
	if info.Status == OfferOpen {
		out.Status = "timeout"
		out.Error = "offer did not resolve in time"
		return
	}
	if info.Status != OfferAssigned {
		out.Status = string(info.Status)
		out.Error = info.Error
		return
	}
	out.PeerID = info.Assignee
	out.TaskID = info.TaskID

	// Phase 2: poll the remote task to a terminal state.
	if s.orch.resultFetcher == nil {
		out.Status = "dispatched"
		return
	}
	pid, err := peer.Decode(info.Assignee)
	if err != nil {
		out.Status = "failed"
		out.Error = "invalid assignee peer id"
		return
	}
	for {
		resp, err := s.orch.resultFetcher(ctx, pid, info.TaskID, 30*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				out.Status = "cancelled"
				out.Error = ctx.Err().Error()
				return
			}
			out.Status = "error"
			out.Error = err.Error()
			return
		}
		if resp.Status.Terminal() {
			out.Status = string(resp.Status)
			out.Error = resp.Error
			if resp.Result != nil {
				out.Result = resp.Result.ForLLM
			}
			return
		}
	}
}

func defaultSummary(results []SubtaskResult) string {
	var b strings.Builder
	succeeded := 0
	for _, r := range results {
		if r.Status == string(agenttask.StatusDone) {
			succeeded++
			if r.Result != "" {
				fmt.Fprintf(&b, "— %s\n\n", r.Result)
			}
		}
	}
	fmt.Fprintf(&b, "%d/%d subtasks succeeded.", succeeded, len(results))
	return b.String()
}

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
	// ID identifies the subtask within its run; when the decomposer omits
	// it the planner assigns the subtask's array index.
	ID      string `json:"id,omitempty"`
	AgentID string `json:"agent_id"`
	Task    string `json:"task"`
	// DependsOn lists prerequisite subtasks by id or array index. Dependents
	// are scheduled only after their prerequisites complete; a failed
	// dependency marks the dependent "skipped".
	DependsOn []string `json:"depends_on,omitempty"`
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
	RunID      string          `json:"run_id"`
	SwarmID    string          `json:"swarm_id"`
	Goal       string          `json:"goal"`
	Status     string          `json:"status"` // done | partial | failed
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
	runs          *runStore
}

// planWaves topologically sorts subtasks into dependency waves (Kahn's
// algorithm). Each wave contains indices of subtasks whose dependencies are
// satisfied by earlier waves; subtasks within a wave run in parallel.
// Unknown dependencies and cycles are rejected.
func planWaves(subtasks []Subtask) ([][]int, error) {
	indexOf := make(map[string]int, len(subtasks))
	for i := range subtasks {
		indexOf[subtasks[i].ID] = i
	}
	indegree := make([]int, len(subtasks))
	dependents := make([][]int, len(subtasks))
	for i, st := range subtasks {
		for _, dep := range st.DependsOn {
			j, ok := indexOf[dep]
			if !ok {
				return nil, fmt.Errorf("subtask %q depends on unknown subtask %q", st.ID, dep)
			}
			if j == i {
				return nil, fmt.Errorf("subtask %q depends on itself", st.ID)
			}
			indegree[i]++
			dependents[j] = append(dependents[j], i)
		}
	}

	var waves [][]int
	ready := make([]int, 0, len(subtasks))
	for i := range subtasks {
		if indegree[i] == 0 {
			ready = append(ready, i)
		}
	}
	scheduled := 0
	for len(ready) > 0 {
		wave := ready
		ready = nil
		waves = append(waves, wave)
		scheduled += len(wave)
		for _, i := range wave {
			for _, d := range dependents[i] {
				indegree[d]--
				if indegree[d] == 0 {
					ready = append(ready, d)
				}
			}
		}
	}
	if scheduled < len(subtasks) {
		return nil, fmt.Errorf("subtask dependency cycle detected")
	}
	return waves, nil
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

	runID := newNonce()
	res := RunResult{RunID: runID, SwarmID: swarmID, Goal: goal, Status: "running"}
	s.publishEvent(runtimeevents.KindSwarmRunStart, map[string]any{
		"run_id":   runID,
		"swarm_id": swarmID,
		"goal":     goal,
	})
	s.recordRun(res, started)

	defer func() {
		res.DurationMS = time.Since(started).Milliseconds()
		succeeded := 0
		dispatched := 0
		for _, st := range res.Subtasks {
			if st.Status == string(agenttask.StatusDone) {
				succeeded++
			} else if st.Status == "dispatched" {
				dispatched++
			}
		}
		switch {
		case succeeded == len(res.Subtasks):
			res.Status = "done"
		case succeeded == 0 && dispatched == 0:
			res.Status = "failed"
		default:
			// A mix of done, dispatched (outcome unknown), and/or
			// failed subtasks is partial — the run did not fully
			// succeed, but not everything failed either.
			res.Status = "partial"
		}
		s.publishEvent(runtimeevents.KindSwarmRunEnd, map[string]any{
			"run_id":      runID,
			"swarm_id":    swarmID,
			"goal":        goal,
			"status":      res.Status,
			"subtasks":    len(res.Subtasks),
			"duration_ms": res.DurationMS,
		})
		s.recordRun(res, started)
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
		if subtasks[i].ID == "" {
			subtasks[i].ID = fmt.Sprintf("%d", i)
		}
	}

	waves, err := planWaves(subtasks)
	if err != nil {
		return res, fmt.Errorf("plan subtasks: %w", err)
	}

	// Schedule subtasks in dependency waves: each wave waits for its
	// prerequisites to finish; failed prerequisites mark dependents skipped.
	res.Subtasks = make([]SubtaskResult, len(subtasks))
	for i, st := range subtasks {
		res.Subtasks[i].Subtask = st
		res.Subtasks[i].Status = "pending"
	}

	type pending struct {
		idx     int
		offerID string
	}
	for _, wave := range waves {
		var pend []pending
		for _, i := range wave {
			if depErr := s.depsFailed(subtasks[i], res.Subtasks); depErr != "" {
				res.Subtasks[i].Status = "skipped"
				res.Subtasks[i].Error = depErr
				s.emitSubtask(runID, swarmID, res.Subtasks[i])
				continue
			}
			res.Subtasks[i].Status = "offering"
			offerID, err := s.Offer(ctx, swarmID, OfferRequest{
				AgentID: subtasks[i].AgentID,
				Task:    subtasks[i].Task,
			})
			if err != nil {
				res.Subtasks[i].Status = "failed"
				res.Subtasks[i].Error = err.Error()
				s.emitSubtask(runID, swarmID, res.Subtasks[i])
				continue
			}
			res.Subtasks[i].OfferID = offerID
			res.Subtasks[i].Status = "offered"
			s.emitSubtask(runID, swarmID, res.Subtasks[i])
			pend = append(pend, pending{idx: i, offerID: offerID})
		}
		for _, p := range pend {
			s.awaitSubtask(ctx, swarmID, runID, &res.Subtasks[p.idx], p.offerID)
			s.emitSubtask(runID, swarmID, res.Subtasks[p.idx])
		}
		s.recordRun(res, started)
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

// depsFailed reports a reason to skip a subtask whose prerequisites did not
// succeed, or "" when all dependencies are satisfied.
func (s *Swarm) depsFailed(st Subtask, results []SubtaskResult) string {
	indexOf := make(map[string]int, len(results))
	for i := range results {
		indexOf[results[i].ID] = i
	}
	for _, dep := range st.DependsOn {
		j, ok := indexOf[dep]
		if !ok {
			continue // unknown deps were rejected by planWaves
		}
		switch results[j].Status {
		case string(agenttask.StatusDone):
			// prerequisite confirmed successful
		case "dispatched":
			// The prerequisite was submitted but its outcome is unknown
			// (no result fetcher wired). Dependents cannot safely run.
			return fmt.Sprintf("dependency %q dispatched (outcome unknown)", dep)
		default:
			return fmt.Sprintf("dependency %q %s: %s", dep, results[j].Status, results[j].Error)
		}
	}
	return ""
}

// emitSubtask publishes a swarm.run.subtask lifecycle event.
func (s *Swarm) emitSubtask(runID, swarmID string, r SubtaskResult) {
	s.publishEvent(runtimeevents.KindSwarmRunSubtask, map[string]any{
		"run_id":     runID,
		"swarm_id":   swarmID,
		"subtask_id": r.ID,
		"agent_id":   r.AgentID,
		"status":     r.Status,
		"peer_id":    r.PeerID,
		"task_id":    r.TaskID,
		"offer_id":   r.OfferID,
		"error":      r.Error,
	})
}

// recordRun persists the current run snapshot to the run store.
func (s *Swarm) recordRun(res RunResult, started time.Time) {
	if s.orch.runs == nil {
		return
	}
	finished := time.Now().UTC()
	status := res.Status
	if status == "" {
		status = "running"
	}
	rec := RunRecord{
		RunID:      res.RunID,
		SwarmID:    res.SwarmID,
		Goal:       res.Goal,
		Status:     status,
		Subtasks:   res.Subtasks,
		Summary:    res.Summary,
		StartedAt:  started.UTC(),
		DurationMS: res.DurationMS,
	}
	if status != "running" {
		rec.FinishedAt = finished
	}
	s.orch.runs.Record(rec)
}

// Runs returns recorded orchestration runs for a swarm (empty = all).
func (s *Swarm) Runs(swarmID string) []RunRecord {
	if s.orch.runs == nil {
		return nil
	}
	return s.orch.runs.List(swarmID)
}

// RunRecord returns one orchestration run by id.
func (s *Swarm) RunRecord(runID string) (RunRecord, bool) {
	if s.orch.runs == nil {
		return RunRecord{}, false
	}
	return s.orch.runs.Get(runID)
}

// awaitSubtask waits for an offer to be assigned and then polls the remote
// task until it terminates or the context ends.
func (s *Swarm) awaitSubtask(
	ctx context.Context,
	swarmID, runID string,
	out *SubtaskResult,
	offerID string,
) {
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
		// A retry may have re-opened the offer just after it resolved; wait
		// for the true terminal state before reporting a timeout.
		if finished := s.queue.offerFinished(offerID); finished != nil {
			select {
			case <-ctx.Done():
				out.Status = "cancelled"
				out.Error = ctx.Err().Error()
				return
			case <-time.After(timeout):
				out.Status = "timeout"
				out.Error = "offer did not resolve in time"
				return
			case <-finished:
				info, _ = s.queue.offerInfo(offerID)
			}
		} else {
			out.Status = "timeout"
			out.Error = "offer did not resolve in time"
			return
		}
	}
	if info.Status != OfferAssigned {
		out.Status = string(info.Status)
		out.Error = info.Error
		out.Result = info.Result
		return
	}
	out.PeerID = info.Assignee
	out.TaskID = info.TaskID
	if out.Status != string(OfferAssigned) {
		out.Status = string(OfferAssigned)
		s.emitSubtask(runID, swarmID, *out)
	}

	// Phase 2: poll the remote task to a terminal state.
	if s.orch.resultFetcher == nil {
		out.Status = "dispatched"
		return
	}
	// When the queue watcher is active (resultFetcher wired), the queue
	// already watches the task and drives retries — wait for the offer's
	// true terminal state instead of racing a second poller.
	if finished := s.queue.offerFinished(offerID); finished != nil {
		select {
		case <-ctx.Done():
			out.Status = "cancelled"
			out.Error = ctx.Err().Error()
			return
		case <-finished:
		}
		if final, ok := s.queue.offerInfo(offerID); ok {
			out.Status = string(final.Status)
			out.Error = final.Error
			out.Result = final.Result
			if final.Status == OfferAssigned {
				// Watcher ended without a terminal transition — fall back to
				// the direct poll below.
				out.Status = "dispatched"
			}
		}
		if out.Status != "dispatched" {
			return
		}
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

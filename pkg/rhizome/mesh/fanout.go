package mesh

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// Fan-out aggregation strategies.
const (
	// FanoutStrategyFirst returns as soon as one branch completes
	// successfully and best-effort cancels the remaining tasks.
	FanoutStrategyFirst = "first"
	// FanoutStrategyQuorum waits for K terminal branches and picks the
	// result that appears most often.
	FanoutStrategyQuorum = "quorum"
	// FanoutStrategyAll waits for every submitted branch to reach a
	// terminal state.
	FanoutStrategyAll = "all"
)

// Branch-local statuses that do not map to a remote agenttask status.
const (
	fanoutStatusSubmitting   = "submitting"
	fanoutStatusSubmitFailed = "submit_failed"
	fanoutStatusPending      = "pending"
)

// fanoutPollWait is the default per-poll long-poll bound for branch results.
const fanoutPollWait = 30 * time.Second

// FanoutRequest describes a scatter-gather task submitted to several capable
// trusted peers concurrently.
type FanoutRequest struct {
	// AgentID is the remote agent the task targets.
	AgentID string
	// Model and Tools are forwarded to the remote agent unchanged.
	Model string
	Task  string
	Tools []string
	// N caps the number of peers used; <= 0 or > number of capable peers
	// fans out to every capable peer.
	N int
	// Strategy selects how branch results are aggregated (default "all").
	Strategy string
	// K is the quorum size for the quorum strategy; <= 0 or > N defaults
	// to the number of submitted branches.
	K int
	// Wait bounds each long-poll for a branch result (default 30s). The
	// server caps a single poll at taskPollWait, so larger values simply
	// re-poll; the caller's context bounds the whole fan-out.
	Wait time.Duration
}

// FanoutBranch is the outcome of one peer in a fan-out. Status is a terminal
// agenttask status ("done", "cancelled", ...) or a local marker:
// "submitting" while the task is being submitted, "submit_failed" when the
// peer rejected the submission, "error" when polling the result failed, and
// "pending" when the task was accepted but the fan-out returned before the
// branch reached a terminal state.
type FanoutBranch struct {
	PeerID string                 `json:"peer_id"`
	TaskID string                 `json:"task_id,omitempty"`
	Status string                 `json:"status"`
	Result *toolshared.ToolResult `json:"result,omitempty"`
	Error  string                 `json:"error,omitempty"`
}

// FanoutResult aggregates the per-branch outcomes of a FanoutTask call.
// Winner is the peer id of the first successful branch, or, for the quorum
// strategy, the peer whose result content appeared most often.
type FanoutResult struct {
	FanoutID string         `json:"fanout_id"`
	Strategy string         `json:"strategy"`
	Branches []FanoutBranch `json:"branches"`
	Winner   string         `json:"winner,omitempty"`
}

// FanoutTask submits the same agent task to up to N capable trusted peers
// over the async task protocol and aggregates the results according to the
// requested strategy. Per-branch ACL, trust, and rate-limit enforcement is
// done by SubmitRemoteTaskWithPeer; failures are reported per branch.
func (m *Mesh) FanoutTask(ctx context.Context, req FanoutRequest) (FanoutResult, error) {
	strategy := req.Strategy
	if strategy == "" {
		strategy = FanoutStrategyAll
	}
	switch strategy {
	case FanoutStrategyFirst, FanoutStrategyQuorum, FanoutStrategyAll:
	default:
		return FanoutResult{}, fmt.Errorf("unknown fanout strategy %q", req.Strategy)
	}

	candidates := m.PickPeerRanked(req.AgentID, "spawn", nil)
	if len(candidates) == 0 {
		return FanoutResult{}, fmt.Errorf("no capable trusted peer for agent %q", req.AgentID)
	}
	n := req.N
	if n <= 0 || n > len(candidates) {
		n = len(candidates)
	}
	candidates = candidates[:n]

	k := req.K
	if k <= 0 || k > n {
		k = n
	}

	wait := req.Wait
	if wait <= 0 {
		wait = fanoutPollWait
	}

	res := FanoutResult{
		FanoutID: newCorrelationID(),
		Strategy: strategy,
		Branches: make([]FanoutBranch, n),
	}
	m.publishMeshEvent(runtimeevents.KindMeshFanoutStart, map[string]any{
		"fanout_id": res.FanoutID,
		"agent_id":  req.AgentID,
		"n":         n,
		"strategy":  strategy,
	})

	call := RemoteCall{
		TargetAgentID: req.AgentID,
		Model:         req.Model,
		SystemPrompt:  req.Task,
		Tools:         req.Tools,
		Async:         true,
	}

	// Submit phase: one goroutine per candidate. The returned task ids are
	// grouped under res.FanoutID for observability; usedPeers records the
	// peer that actually accepted each branch (failover may reroute).
	usedPeers := make([]peer.ID, n)
	var submitWg sync.WaitGroup
	for i, c := range candidates {
		res.Branches[i].PeerID = c.PID.String()
		res.Branches[i].Status = fanoutStatusSubmitting
		submitWg.Add(1)
		go func(i int, pid peer.ID) {
			defer submitWg.Done()
			used, taskID, err := m.SubmitRemoteTaskWithPeer(ctx, pid, call)
			if err != nil {
				res.Branches[i].Status = fanoutStatusSubmitFailed
				res.Branches[i].Error = err.Error()
				return
			}
			usedPeers[i] = used
			res.Branches[i].PeerID = used.String()
			res.Branches[i].TaskID = taskID
			res.Branches[i].Status = fanoutStatusPending
		}(i, c.PID)
	}
	submitWg.Wait()

	accepted := 0
	for i := range res.Branches {
		if res.Branches[i].TaskID != "" {
			accepted++
		}
	}
	if accepted == 0 {
		m.publishFanoutEnd(&res, req.AgentID, n)
		return res, fmt.Errorf("fanout %s: no peer accepted the task", res.FanoutID)
	}

	// Poll phase: one goroutine per accepted branch. pollCtx stops the
	// remaining pollers once the strategy is satisfied.
	pollCtx, stopPolls := context.WithCancel(ctx)
	defer stopPolls()

	type outcome struct {
		idx  int
		resp agenttask.Response
		err  error
	}
	outCh := make(chan outcome, n)
	for i := range res.Branches {
		if res.Branches[i].TaskID == "" {
			continue
		}
		go func(idx int) {
			resp, err := m.pollFanoutTask(pollCtx, usedPeers[idx], res.Branches[idx].TaskID, wait)
			outCh <- outcome{idx: idx, resp: resp, err: err}
		}(i)
	}

	apply := func(oc outcome) {
		b := &res.Branches[oc.idx]
		if oc.err != nil {
			b.Status = "error"
			b.Error = oc.err.Error()
			return
		}
		b.Status = string(oc.resp.Status)
		b.Result = oc.resp.Result
		b.Error = oc.resp.Error
		if b.Status == string(agenttask.StatusDone) && res.Winner == "" {
			res.Winner = b.PeerID
		}
	}

	need := accepted
	if strategy == FanoutStrategyQuorum && k < need {
		need = k
	}

	var retErr error
	collected := 0
collect:
	for collected < accepted {
		select {
		case oc := <-outCh:
			collected++
			apply(oc)
			switch strategy {
			case FanoutStrategyFirst:
				if res.Winner != "" {
					break collect
				}
			case FanoutStrategyQuorum:
				if collected >= need {
					break collect
				}
			}
		case <-ctx.Done():
			retErr = ctx.Err()
			break collect
		}
	}

	// Stop the remaining pollers so they return promptly (either with their
	// real terminal result if already done, or with ctx.Err()). Then drain
	// every outstanding outcome so late-finishing branches report their real
	// terminal state instead of being marked pending/cancelled. Each poll
	// goroutine sends exactly one outcome, so we drain accepted-collected.
	// Outcomes caused by pollCtx cancellation (not a real remote result) are
	// skipped so the branch keeps its "pending" status — the cancel loop
	// below handles "first" strategy, and quorum leaves stragglers pending.
	stopPolls()
	remaining := accepted - collected
	for remaining > 0 {
		select {
		case oc := <-outCh:
			if oc.err != nil && errors.Is(oc.err, context.Canceled) {
				// pollCtx was cancelled by stopPolls(); leave branch as
				// "pending" for the cancel loop / quorum semantics.
				remaining--
				continue
			}
			apply(oc)
			remaining--
		case <-time.After(5 * time.Second):
			// Safety net: a poller stuck in transport should not block
			// forever; treat undelivered outcomes as cancelled below.
			remaining = 0
		}
	}

	// First-success strategy: best-effort cancel every branch still in
	// flight on its remote peer.
	if strategy == FanoutStrategyFirst && res.Winner != "" {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var wg sync.WaitGroup
		for i := range res.Branches {
			if res.Branches[i].Status != fanoutStatusPending {
				continue
			}
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				b := &res.Branches[idx]
				resp, err := m.CancelRemoteTask(cancelCtx, usedPeers[idx], b.TaskID)
				if err == nil && resp.Status != "" {
					b.Status = string(resp.Status)
					b.Result = resp.Result
					b.Error = resp.Error
					return
				}
				b.Status = string(agenttask.StatusCancelled)
				if err != nil {
					b.Error = fmt.Sprintf("cancel: %v", err)
				}
			}(i)
		}
		wg.Wait()
		cancel()
	}

	// Branches that submitted but never delivered an outcome (e.g. the drain
	// safety timeout fired) keep their fanoutStatusPending marker from the
	// submit phase; no rewrite needed here.

	if strategy == FanoutStrategyQuorum {
		res.Winner = fanoutQuorumWinner(res.Branches)
	}

	m.publishFanoutEnd(&res, req.AgentID, n)
	return res, retErr
}

// pollFanoutTask long-polls a remote task until it reaches a terminal state,
// the context is done, or too many consecutive transport errors occur. Each
// poll waits up to wait; the server caps a single wait at taskPollWait and
// returns the current (possibly still running) status, so the loop re-polls.
func (m *Mesh) pollFanoutTask(
	ctx context.Context,
	pid peer.ID,
	taskID string,
	wait time.Duration,
) (agenttask.Response, error) {
	if wait <= 0 {
		wait = fanoutPollWait
	}
	const maxConsecutiveFailures = 5
	failures := 0
	for {
		resp, err := m.RemoteTaskResult(ctx, pid, taskID, wait)
		if err != nil {
			if ctx.Err() != nil {
				return resp, ctx.Err()
			}
			failures++
			if failures >= maxConsecutiveFailures {
				return resp, fmt.Errorf("poll task %s: %w", taskID, err)
			}
			m.node.ForceReconnect(ctx, pid)
			select {
			case <-ctx.Done():
				return resp, ctx.Err()
			case <-time.After(time.Duration(failures) * 500 * time.Millisecond):
			}
			continue
		}
		failures = 0
		if resp.Status.Terminal() {
			return resp, nil
		}
	}
}

// fanoutQuorumWinner returns the peer whose result content (ForLLM) appears
// most often among the done branches. Ties resolve to the earliest branch in
// submission order; with no done branches it returns "".
func fanoutQuorumWinner(branches []FanoutBranch) string {
	groups := make(map[string][]int) // result content -> branch indices
	for i, b := range branches {
		if b.Status != string(agenttask.StatusDone) {
			continue
		}
		content := ""
		if b.Result != nil {
			content = b.Result.ForLLM
		}
		groups[content] = append(groups[content], i)
	}
	bestIdx, bestCount := -1, 0
	for _, idxs := range groups {
		if len(idxs) > bestCount || (len(idxs) == bestCount && (bestIdx < 0 || idxs[0] < bestIdx)) {
			bestIdx, bestCount = idxs[0], len(idxs)
		}
	}
	if bestIdx < 0 {
		return ""
	}
	return branches[bestIdx].PeerID
}

// publishFanoutEnd emits the fanout end event with per-branch tallies.
func (m *Mesh) publishFanoutEnd(res *FanoutResult, agentID string, n int) {
	succeeded, failed := 0, 0
	for _, b := range res.Branches {
		switch b.Status {
		case string(agenttask.StatusDone):
			succeeded++
		case fanoutStatusPending, string(agenttask.StatusCancelled):
		default:
			failed++
		}
	}
	m.publishMeshEvent(runtimeevents.KindMeshFanoutEnd, map[string]any{
		"fanout_id": res.FanoutID,
		"agent_id":  agentID,
		"n":         n,
		"strategy":  res.Strategy,
		"succeeded": succeeded,
		"failed":    failed,
		"winner":    res.Winner,
	})
}

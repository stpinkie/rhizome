package swarm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// Queue message kinds carried inside MsgBroadcast payloads.
const (
	queueKindOffer  = "offer"
	queueKindClaim  = "claim"
	queueKindAssign = "assign"
	queueKindCancel = "cancel"
)

// queueMsg wraps offer/claim/assign payloads in a broadcast envelope.
type queueMsg struct {
	Kind   string  `json:"kind"`
	Offer  *Offer  `json:"offer,omitempty"`
	Claim  *Claim  `json:"claim,omitempty"`
	Assign *Assign `json:"assign,omitempty"`
	Cancel *Cancel `json:"cancel,omitempty"`
}

// OfferRequirements constrain which members may claim an offer. Empty lists
// mean "any member".
type OfferRequirements struct {
	// Agents limits claiming to members that host one of these agent ids.
	Agents []string `json:"agents,omitempty"`
	// Models limits claiming to members advertising one of these models.
	Models []string `json:"models,omitempty"`
	// Skills limits claiming to members advertising one of these skills.
	Skills []string `json:"skills,omitempty"`
}

// Empty reports whether the requirements constrain claiming at all.
func (r OfferRequirements) Empty() bool {
	return len(r.Agents) == 0 && len(r.Models) == 0 && len(r.Skills) == 0
}

// Offer is a task published to a swarm for members to claim.
type Offer struct {
	OfferID   string    `json:"offer_id"`
	SwarmID   string    `json:"swarm_id"`
	AgentID   string    `json:"agent_id"`
	Model     string    `json:"model,omitempty"`
	Task      string    `json:"task"`
	Tools     []string  `json:"tools,omitempty"`
	Offerer   string    `json:"offerer"`
	CreatedAt time.Time `json:"created_at"`
	// Media carries file attachments for the task. Path entries are
	// offerer-local; they are pushed to the chosen claimant over the blob
	// protocol at assignment time.
	Media []mesh.MediaAttachment `json:"media,omitempty"`
	// Requires lists capabilities a claimant must satisfy before bidding.
	Requires *OfferRequirements `json:"requires,omitempty"`
	// Attempt is the broadcast attempt number — incremented when a failed
	// or stalled offer is re-offered. Members treat a higher Attempt as a
	// fresh offer (re-claim) rather than a duplicate.
	Attempt int `json:"attempt,omitempty"`
	// TTLSeconds bounds how long the offer stays open for claims.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

// Claim is a member's bid for an offer, sent point-to-point to the offerer.
type Claim struct {
	OfferID  string `json:"offer_id"`
	Claimant string `json:"claimant"`
	// Attempt is the offer attempt this claim answers; stale-attempt claims
	// are dropped by the offerer.
	Attempt int `json:"attempt,omitempty"`
	// ActiveTasks is the claimant's current remote-task load.
	ActiveTasks int `json:"active_tasks,omitempty"`
	// CapDigest is the claimant's advertised capability digest, so the
	// offerer can weight claims by capability as well as load.
	CapDigest string `json:"cap_digest,omitempty"`
}

// Assign announces which claimant won an offer.
type Assign struct {
	OfferID string `json:"offer_id"`
	PeerID  string `json:"peer_id"`
	TaskID  string `json:"task_id,omitempty"`
}

// Cancel withdraws an open or assigned offer. Only the original offerer may
// cancel; receivers verify Cancel.Offerer against the recorded offer.
type Cancel struct {
	OfferID string `json:"offer_id"`
	Offerer string `json:"offerer"`
}

// OfferRequest describes a task to offer to a swarm.
type OfferRequest struct {
	AgentID string
	Model   string
	Task    string
	Tools   []string
	// Media attaches files; pushed to the claimant at assignment time.
	Media []mesh.MediaAttachment
	// Requires constrains which members may claim (capability-aware
	// claiming). Zero value means any member.
	Requires OfferRequirements
}

// OfferStatus is the lifecycle state of a tracked offer.
type OfferStatus string

const (
	// OfferOpen collects claims.
	OfferOpen OfferStatus = "open"
	// OfferAssigned means a claimant was picked and the task submitted.
	OfferAssigned OfferStatus = "assigned"
	// OfferExpired means the claim window closed with no claims.
	OfferExpired OfferStatus = "expired"
	// OfferFailed means assignment or submit failed.
	OfferFailed OfferStatus = "failed"
	// OfferCancelled means the offerer withdrew the offer.
	OfferCancelled OfferStatus = "cancelled"
	// OfferDone means the assigned task completed successfully.
	OfferDone OfferStatus = "done"
	// OfferDeadLetter means the offer exhausted retries after
	// failed/stalled attempts.
	OfferDeadLetter OfferStatus = "dead_letter"
	// OfferObserved marks an offer seen from another peer.
	OfferObserved OfferStatus = "observed"
)

// Terminal reports whether the offer has reached a final lifecycle state.
func (os OfferStatus) Terminal() bool {
	switch os {
	case OfferExpired, OfferFailed, OfferCancelled, OfferDone, OfferDeadLetter:
		return true
	}
	return false
}

// OfferInfo is the JSON-friendly view of a tracked offer.
type OfferInfo struct {
	Offer
	Status   OfferStatus `json:"status"`
	TaskID   string      `json:"task_id,omitempty"`
	Assignee string      `json:"assignee,omitempty"`
	// Result holds the completed task output once status is done.
	Result  string `json:"result,omitempty"`
	Error   string `json:"error,omitempty"`
	Retries int    `json:"retries,omitempty"`
}

// TaskSubmitter submits a task to a peer; usually
// mesh.Mesh.SubmitRemoteTaskWithPeer.
type TaskSubmitter func(ctx context.Context, preferred peer.ID, call mesh.RemoteCall) (peer.ID, string, error)

// TaskCanceller asks a peer to cancel a submitted task; usually
// mesh.Mesh.CancelRemoteTask.
type TaskCanceller func(ctx context.Context, pid peer.ID, taskID string) error

// OfferEvaluator decides whether the local node should claim an incoming
// offer. A nil evaluator claims nothing.
type OfferEvaluator func(swarmID string, o Offer) bool

// CapMatcher reports whether the local node satisfies an offer's declared
// requirements (agents/models/skills). A nil matcher is permissive.
type CapMatcher func(swarmID string, req OfferRequirements) bool

// trackedOffer is the runtime record for one offer we published.
type trackedOffer struct {
	info     OfferInfo
	claims   []Claim
	claimsCh chan Claim
	// resolved is closed when the offer first leaves "open" (assigned or a
	// terminal state) so `swarm offer --wait`-style awaiters stop polling.
	resolved chan struct{}
	// finished is closed when the offer reaches a true terminal state —
	// done, expired, failed, cancelled, or dead_letter — including any
	// retries of a failed assignment.
	finished chan struct{}
	// cancelCh is closed by CancelOffer; retry loops and the task watcher
	// select on it.
	cancelCh  chan struct{}
	closeOnce sync.Once
	retries   int

	// resolvedOnce / finishedOnce make channel closure idempotent under
	// concurrent completion/cancellation paths.
	resolvedOnce sync.Once
	finishedOnce sync.Once

	// finishedAt records when finished was first closed; used to reap
	// terminal offers after a caller-visible retention window.
	finishedAt time.Time
}

// cancelled reports whether CancelOffer was invoked.
func (t *trackedOffer) cancelled() bool {
	select {
	case <-t.cancelCh:
		return true
	default:
		return false
	}
}

// workQueue implements the distributed offer/claim work queue. Offers are
// broadcast to a swarm; members reply with point-to-point claims during the
// claim window; the offerer picks the least-loaded claimer and submits the
// task through the mesh task protocol. Failed or stalled assignments are
// re-offered up to swarm.queue.max_retries before landing in dead_letter.
type workQueue struct {
	s *Swarm

	mu       sync.Mutex
	offers   map[string]*trackedOffer // offers we published, by offer id
	incoming map[string]OfferInfo     // offers observed from other peers
	subs     map[string]func()        // swarm id -> unsubscribe

	submitter TaskSubmitter
	canceller TaskCanceller
	evaluator OfferEvaluator
	matcher   CapMatcher

	wg sync.WaitGroup
}

func newWorkQueue(s *Swarm) *workQueue {
	return &workQueue{
		s:        s,
		offers:   make(map[string]*trackedOffer),
		incoming: make(map[string]OfferInfo),
		subs:     make(map[string]func()),
	}
}

// activeOffers returns the number of non-terminal offers we are currently
// tracking. It must be called with q.mu held.
func (q *workQueue) activeOffers() int {
	n := 0
	for _, to := range q.offers {
		switch to.info.Status {
		case OfferOpen, OfferAssigned:
			n++
		}
	}
	return n
}

// retention returns how long a terminal offer is retained in memory after it
// finishes so callers can still poll its status.
func (q *workQueue) retention() time.Duration {
	return q.s.cfg.Queue.AssignTimeout + 2*time.Minute
}

// maxIncoming is the cap for observed offers from other peers.
func (q *workQueue) maxIncoming() int {
	maxOffers := q.s.cfg.Queue.MaxOffers * 2
	if maxOffers < 64 {
		maxOffers = 64
	}
	return maxOffers
}

// reap removes terminal offers and stale/capped observed offers. It is safe to
// call outside q.mu (it acquires the lock itself).
func (q *workQueue) reap() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reapLocked()
}

// reapLocked performs the same cleanup as reap but assumes the caller already
// holds q.mu.
func (q *workQueue) reapLocked() {
	retention := q.retention()
	now := time.Now()

	// Reap terminal published offers.
	for id, to := range q.offers {
		if !to.info.Status.Terminal() {
			continue
		}
		if !to.finishedAt.IsZero() && now.Sub(to.finishedAt) > retention {
			delete(q.offers, id)
		}
	}

	// Reap stale observed offers.
	for id, info := range q.incoming {
		if info.Status == OfferCancelled {
			delete(q.incoming, id)
			continue
		}
		ttl := time.Duration(info.TTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = q.s.cfg.Queue.OfferTTL
		}
		if now.Sub(info.CreatedAt) > ttl+retention {
			delete(q.incoming, id)
		}
	}

	// Cap observed offers by evicting the oldest, preferring already-terminal
	// observed entries first.
	for len(q.incoming) > q.maxIncoming() {
		var oldestID string
		var oldest time.Time
		var hasObserved bool
		var oldestObservedID string
		for id, info := range q.incoming {
			if !oldest.IsZero() && !info.CreatedAt.Before(oldest) {
				continue
			}
			oldest = info.CreatedAt
			oldestID = id
			if info.Status == OfferObserved {
				hasObserved = true
				oldestObservedID = id
			}
		}
		if hasObserved {
			delete(q.incoming, oldestObservedID)
		} else if oldestID != "" {
			delete(q.incoming, oldestID)
		} else {
			break
		}
	}
}

// reapLoop runs periodic queue cleanup for the lifetime of the swarm.
func (q *workQueue) reapLoop() {
	defer q.s.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-q.s.ctx.Done():
			return
		case <-ticker.C:
			q.reap()
		}
	}
}

// SetTaskSubmitter wires the mesh submit function used to dispatch claimed
// offers. Without it, offers are published but never assigned.
func (s *Swarm) SetTaskSubmitter(fn TaskSubmitter) {
	s.queue.mu.Lock()
	s.queue.submitter = fn
	s.queue.mu.Unlock()
}

// SetOfferEvaluator wires the local capability check for incoming offers.
func (s *Swarm) SetOfferEvaluator(fn OfferEvaluator) {
	s.queue.mu.Lock()
	s.queue.evaluator = fn
	s.queue.mu.Unlock()
}

// SetTaskCanceller wires the remote-task cancel used when an assigned offer
// is cancelled or stalls past swarm.queue.assign_timeout.
func (s *Swarm) SetTaskCanceller(fn TaskCanceller) {
	s.queue.mu.Lock()
	s.queue.canceller = fn
	s.queue.mu.Unlock()
}

// SetCapMatcher wires the requirement check for capability-aware claiming;
// the local node only bids on offers whose Requires it satisfies.
func (s *Swarm) SetCapMatcher(fn CapMatcher) {
	s.queue.mu.Lock()
	s.queue.matcher = fn
	s.queue.mu.Unlock()
}

// start subscribes the queue to every currently-joined swarm and starts the
// periodic offer reaper.
func (q *workQueue) start() {
	for _, id := range q.s.joinedIDs() {
		q.watch(id)
	}
	q.s.wg.Add(1)
	go q.reapLoop()
}

// watch subscribes the queue to a swarm's broadcast channel (idempotent).
func (q *workQueue) watch(swarmID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.subs[swarmID]; ok {
		return
	}
	ch, cancel := q.s.bc.Subscribe(swarmID)
	q.subs[swarmID] = cancel
	q.wg.Add(1)
	go q.consume(swarmID, ch)
}

// unwatch detaches the queue from a swarm.
func (q *workQueue) unwatch(swarmID string) {
	q.mu.Lock()
	cancel, ok := q.subs[swarmID]
	delete(q.subs, swarmID)
	q.mu.Unlock()
	if ok {
		cancel()
	}
}

// consume reads broadcast envelopes for one swarm until the subscription is
// cancelled or the swarm shuts down.
func (q *workQueue) consume(swarmID string, ch <-chan Envelope) {
	defer q.wg.Done()
	for {
		select {
		case <-q.s.ctx.Done():
			return
		case env, ok := <-ch:
			if !ok {
				return
			}
			q.onMessage(swarmID, env)
		}
	}
}

// onMessage decodes and dispatches a queue broadcast.
func (q *workQueue) onMessage(swarmID string, env Envelope) {
	var msg queueMsg
	if err := decodePayload(env, &msg); err != nil {
		return
	}
	switch msg.Kind {
	case queueKindOffer:
		if msg.Offer != nil {
			q.onOffer(swarmID, env, *msg.Offer)
		}
	case queueKindClaim:
		if msg.Claim != nil {
			q.onClaim(env.SwarmID, *msg.Claim)
		}
	case queueKindAssign:
		if msg.Assign != nil {
			q.onAssign(*msg.Assign)
		}
	case queueKindCancel:
		if msg.Cancel != nil {
			q.onCancel(*msg.Cancel)
		}
	}
}

// onOffer records an inbound offer and claims it when the local evaluator
// accepts and the offer is still fresh.
func (q *workQueue) onOffer(swarmID string, env Envelope, o Offer) {
	started := time.Now()
	if o.Offerer == q.s.host.ID().String() {
		return // our own offer echoed back locally
	}

	// The offer must target the swarm it was broadcast on.
	if o.SwarmID != "" && o.SwarmID != swarmID {
		return
	}

	// Expiry: silently drop stale offers.
	if o.TTLSeconds > 0 && time.Since(o.CreatedAt) > time.Duration(o.TTLSeconds)*time.Second {
		return
	}

	// ACL: the offerer must be allowed to offer this agent to us.
	if offerer, err := peer.Decode(o.Offerer); err == nil {
		if err := q.s.checkSwarmOp(offerer, swarmID, "offer", o.AgentID); err != nil {
			q.s.auditSwarm(offerer, "offer", swarmID, o.OfferID, "rejected", started, err.Error())
			return
		}
	}

	q.mu.Lock()
	q.reapLocked()
	if q.incoming == nil {
		q.incoming = make(map[string]OfferInfo)
	}
	if seen, ok := q.incoming[o.OfferID]; ok && o.Attempt <= seen.Attempt {
		// Duplicate of a seen attempt; a re-offer uses a higher Attempt.
		q.mu.Unlock()
		return
	}
	q.incoming[o.OfferID] = OfferInfo{Offer: o, Status: OfferObserved}
	evaluator := q.evaluator
	matcher := q.matcher
	q.mu.Unlock()

	// Capability-aware claiming: satisfy declared requirements first.
	if o.Requires != nil && !o.Requires.Empty() && matcher != nil &&
		!matcher(swarmID, *o.Requires) {
		return
	}

	if evaluator == nil || !evaluator(swarmID, o) {
		return
	}

	claim := Claim{OfferID: o.OfferID, Claimant: q.s.host.ID().String(), Attempt: o.Attempt}
	if q.s.presence.capProbe != nil {
		claim.CapDigest, claim.ActiveTasks = q.s.presence.capProbe()
	}
	offerer, err := peer.Decode(o.Offerer)
	if err != nil {
		return
	}
	payload, err := encodePayload(queueMsg{Kind: queueKindClaim, Claim: &claim})
	if err != nil {
		return
	}
	// Claims are sent point-to-point to the offerer but use MsgBroadcast so
	// the offerer's HandlePush delivers them to the queue's local subscriber
	// (the same path as broadcast offers/assigns).
	_ = q.s.Send(q.s.ctx, offerer, Envelope{SwarmID: swarmID, Type: MsgBroadcast, Payload: payload})
}

// onClaim records a claim for one of our open offers. The claimant must be
// allowed to claim, and (when the rule restricts agents) the offer's target
// agent must be permitted.
func (q *workQueue) onClaim(swarmID string, c Claim) {
	started := time.Now()
	q.mu.Lock()
	to, ok := q.offers[c.OfferID]
	open := ok && to.info.Status == OfferOpen
	// Drop claims answering a superseded re-offer attempt.
	currentAttempt := ok && c.Attempt == to.info.Attempt
	q.mu.Unlock()
	if !open || !currentAttempt {
		return
	}
	if claimant, err := peer.Decode(c.Claimant); err == nil {
		if err := q.s.checkSwarmOp(claimant, swarmID, "claim", to.info.AgentID); err != nil {
			q.s.auditSwarm(claimant, "claim", swarmID, c.OfferID, "rejected", started, err.Error())
			return
		}
	}
	select {
	case to.claimsCh <- c:
	default:
	}
}

// onAssign records the winner of an observed offer.
func (q *workQueue) onAssign(a Assign) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if info, ok := q.incoming[a.OfferID]; ok {
		info.Status = OfferAssigned
		info.Assignee = a.PeerID
		info.TaskID = a.TaskID
		q.incoming[a.OfferID] = info
	}
}

// onCancel marks an observed offer cancelled. Only the recorded offerer may
// cancel — forged cancels from other members are ignored.
func (q *workQueue) onCancel(c Cancel) {
	q.mu.Lock()
	defer q.mu.Unlock()
	info, ok := q.incoming[c.OfferID]
	if !ok {
		return
	}
	if c.Offerer != "" && info.Offerer != "" && c.Offerer != info.Offerer {
		return
	}
	if info.Status == OfferObserved || info.Status == OfferAssigned {
		info.Status = OfferCancelled
		info.Error = "cancelled by offerer"
		q.incoming[c.OfferID] = info
	}
}

// Offer publishes a task offer to a swarm and returns the offer id. Claims
// are collected in the background for claim_window; the assignment runs
// asynchronously — poll Offers() for the outcome.
func (s *Swarm) Offer(ctx context.Context, swarmID string, req OfferRequest) (string, error) {
	if !s.isJoined(swarmID) {
		return "", fmt.Errorf("not a member of swarm %q", swarmID)
	}
	if req.AgentID == "" || req.Task == "" {
		return "", fmt.Errorf("agent id and task are required")
	}

	q := s.queue
	q.mu.Lock()
	q.reapLocked()
	if q.activeOffers() >= s.cfg.Queue.MaxOffers {
		q.mu.Unlock()
		return "", fmt.Errorf("too many open offers (max %d)", s.cfg.Queue.MaxOffers)
	}
	o := Offer{
		OfferID:    newNonce(),
		SwarmID:    swarmID,
		AgentID:    req.AgentID,
		Model:      req.Model,
		Task:       req.Task,
		Tools:      req.Tools,
		Offerer:    s.host.ID().String(),
		CreatedAt:  time.Now(),
		Media:      req.Media,
		TTLSeconds: int64(s.cfg.Queue.OfferTTL.Seconds()),
	}
	if !req.Requires.Empty() {
		reqCopy := req.Requires
		o.Requires = &reqCopy
	}
	to := &trackedOffer{
		info:     OfferInfo{Offer: o, Status: OfferOpen},
		claimsCh: make(chan Claim, s.cfg.Queue.MaxOffers),
		resolved: make(chan struct{}),
		finished: make(chan struct{}),
		cancelCh: make(chan struct{}),
	}
	q.offers[o.OfferID] = to
	q.mu.Unlock()

	if err := s.PublishBroadcast(ctx, swarmID, queueMsg{Kind: queueKindOffer, Offer: &o}); err != nil {
		q.mu.Lock()
		to.info.Status = OfferFailed
		to.info.Error = err.Error()
		q.mu.Unlock()
		s.finishTracked(to)
		return "", err
	}

	s.publishEvent(runtimeevents.KindSwarmOfferPublished, map[string]any{
		"swarm_id": swarmID,
		"offer_id": o.OfferID,
		"agent_id": o.AgentID,
	})

	// Resolution runs under the swarm lifetime context — the caller's ctx
	// (e.g. a CLI timeout) must not kill claim collection.
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		s.resolveOffer(s.ctx, swarmID, to)
	}()
	return o.OfferID, nil
}

// CancelOffer withdraws a published offer: pending claims are dropped, an
// assigned remote task is cancelled, and a cancel is broadcast so members
// stop tracking the offer. Cancelling an already-terminal offer is a no-op.
func (s *Swarm) CancelOffer(ctx context.Context, swarmID, offerID string) error {
	started := time.Now()
	q := s.queue
	q.mu.Lock()
	to, ok := q.offers[offerID]
	canceller := q.canceller
	q.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown offer %q", offerID)
	}
	if to.cancelled() {
		return nil // idempotent
	}

	q.mu.Lock()
	assignee := to.info.Assignee
	taskID := to.info.TaskID
	status := to.info.Status
	switch status {
	case OfferOpen, OfferAssigned:
		to.info.Status = OfferCancelled
		to.info.Error = "cancelled by offerer"
	default:
		// Already terminal (done/expired/failed/dead_letter).
		q.mu.Unlock()
		return nil
	}
	q.mu.Unlock()
	to.closeOnce.Do(func() { close(to.cancelCh) })

	// Cancel the in-flight remote task, if any.
	if status == OfferAssigned && canceller != nil && assignee != "" && taskID != "" {
		if pid, err := peer.Decode(assignee); err == nil {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = canceller(cancelCtx, pid, taskID)
			cancel()
		}
	}

	cancelMsg := Cancel{OfferID: offerID, Offerer: s.host.ID().String()}
	_ = s.PublishBroadcast(ctx, swarmID, queueMsg{Kind: queueKindCancel, Cancel: &cancelMsg})

	s.finishOffer(to)
	s.finishTracked(to)
	s.publishEvent(runtimeevents.KindSwarmOfferCancelled, map[string]any{
		"swarm_id": swarmID,
		"offer_id": offerID,
	})
	s.auditSwarm(s.host.ID(), "offer_cancel", swarmID, offerID, "ok", started, "")
	return nil
}

// resolveOffer runs the offer/claim/assign loop with retry: claims are
// collected for the claim window, the least-loaded claimer is assigned, the
// remote task is watched, and a failed/stalled attempt re-opens the offer up
// to swarm.queue.max_retries times before dead-lettering.
func (s *Swarm) resolveOffer(ctx context.Context, swarmID string, to *trackedOffer) {
	for {
		window := time.NewTimer(s.cfg.Queue.ClaimWindow)

	collect:
		for {
			select {
			case <-ctx.Done():
				window.Stop()
				s.failOffer(to, "cancelled")
				return
			case <-to.cancelCh:
				window.Stop()
				// CancelOffer already transitioned the state.
				return
			case <-window.C:
				break collect
			case c := <-to.claimsCh:
				if c.Attempt == s.offerAttempt(to) {
					to.claims = append(to.claims, c)
				}
			}
		}
		window.Stop()

		if len(to.claims) == 0 {
			// No claims is a normal terminal outcome — retries are for
			// failed/stalled assignments, not an empty window.
			q := s.queue
			q.mu.Lock()
			to.info.Status = OfferExpired
			q.mu.Unlock()
			s.finishOffer(to)
			s.finishTracked(to)
			s.publishEvent(runtimeevents.KindSwarmOfferExpired, map[string]any{
				"swarm_id": swarmID,
				"offer_id": to.info.OfferID,
			})
			return
		}

		// Pick the least-loaded claimer; ties resolve to the earliest claim.
		best := to.claims[0]
		for _, c := range to.claims[1:] {
			if c.ActiveTasks < best.ActiveTasks {
				best = c
			}
		}

		q := s.queue
		q.mu.Lock()
		submitter := q.submitter
		canceller := q.canceller
		fetcher := s.orch.resultFetcher
		q.mu.Unlock()

		if submitter == nil {
			s.failOffer(to, "no task submitter configured")
			return
		}
		claimer, err := peer.Decode(best.Claimant)
		if err != nil {
			s.failOffer(to, fmt.Sprintf("invalid claimant %q", best.Claimant))
			return
		}

		usedPeer, taskID, err := submitter(ctx, claimer, mesh.RemoteCall{
			TargetAgentID: to.info.AgentID,
			Model:         to.info.Model,
			SystemPrompt:  to.info.Task,
			Tools:         to.info.Tools,
			Async:         true,
			Media:         to.info.Media,
		})
		if err != nil {
			if s.retryOffer(ctx, swarmID, to, "submit: "+err.Error()) {
				continue
			}
			s.deadLetterOffer(to, swarmID, "submit: "+err.Error())
			return
		}

		q.mu.Lock()
		to.info.Status = OfferAssigned
		to.info.Assignee = usedPeer.String()
		to.info.TaskID = taskID
		q.mu.Unlock()
		s.finishOffer(to)

		assign := Assign{OfferID: to.info.OfferID, PeerID: usedPeer.String(), TaskID: taskID}
		_ = s.PublishBroadcast(ctx, swarmID, queueMsg{Kind: queueKindAssign, Assign: &assign})
		s.publishEvent(runtimeevents.KindSwarmOfferAssigned, map[string]any{
			"swarm_id": swarmID,
			"offer_id": to.info.OfferID,
			"peer_id":  usedPeer.String(),
			"task_id":  taskID,
		})

		// Without a result fetcher the queue cannot watch the task — the
		// offer stays "assigned" and the caller polls the task itself.
		if fetcher == nil {
			s.finishTracked(to)
			return
		}

		outcome, resultText, failReason := s.watchAssignedOffer(ctx, to, usedPeer, taskID, canceller)
		switch outcome {
		case offerOutcomeDone:
			q.mu.Lock()
			to.info.Status = OfferDone
			to.info.Result = resultText
			q.mu.Unlock()
			s.finishTracked(to)
			s.publishEvent(runtimeevents.KindSwarmOfferDone, map[string]any{
				"swarm_id": swarmID,
				"offer_id": to.info.OfferID,
				"peer_id":  usedPeer.String(),
				"task_id":  taskID,
			})
			return
		case offerOutcomeCancelled:
			return // CancelOffer handled the transition
		default: // failed or stalled
			if s.retryOffer(ctx, swarmID, to, failReason) {
				continue
			}
			s.deadLetterOffer(to, swarmID, failReason)
			return
		}
	}
}

// offerOutcome is the terminal result of watching one assignment attempt.
type offerOutcome int

const (
	offerOutcomeDone offerOutcome = iota
	offerOutcomeCancelled
	offerOutcomeRetryable
)

// watchAssignedOffer polls the assigned remote task until it completes,
// fails, is cancelled, or stalls past swarm.queue.assign_timeout. A stalled
// task is cancelled via the TaskCanceller seam before re-offering.
func (s *Swarm) watchAssignedOffer(
	ctx context.Context,
	to *trackedOffer,
	pid peer.ID,
	taskID string,
	canceller TaskCanceller,
) (offerOutcome, string, string) {
	fetcher := s.orch.resultFetcher
	deadline := time.NewTimer(s.cfg.Queue.AssignTimeout)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return offerOutcomeCancelled, "", "swarm stopped"
		case <-to.cancelCh:
			return offerOutcomeCancelled, "", "cancelled"
		case <-deadline.C:
			// Stall: cancel the remote task, then re-offer.
			if canceller != nil {
				cancelCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				_ = canceller(cancelCtx, pid, taskID)
				cancel()
			}
			return offerOutcomeRetryable, "",
				fmt.Sprintf("assigned task %s stalled past %s", taskID, s.cfg.Queue.AssignTimeout)
		default:
		}

		resp, err := fetcher(ctx, pid, taskID, 10*time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return offerOutcomeCancelled, "", ctx.Err().Error()
			}
			if to.cancelled() {
				return offerOutcomeCancelled, "", "cancelled"
			}
			return offerOutcomeRetryable, "", fmt.Sprintf("poll task %s: %v", taskID, err)
		}
		if !resp.Status.Terminal() {
			// The remote did not honor the 10s long-poll or returned
			// quickly with a non-terminal status. Sleep briefly to avoid
			// a tight CPU spin until the next poll.
			select {
			case <-ctx.Done():
				return offerOutcomeCancelled, "", "swarm stopped"
			case <-to.cancelCh:
				return offerOutcomeCancelled, "", "cancelled"
			case <-deadline.C:
				if canceller != nil {
					cancelCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					_ = canceller(cancelCtx, pid, taskID)
					cancel()
				}
				return offerOutcomeRetryable, "",
					fmt.Sprintf("assigned task %s stalled past %s", taskID, s.cfg.Queue.AssignTimeout)
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if resp.Status == agenttask.StatusDone {
			result := ""
			if resp.Result != nil {
				result = resp.Result.ForLLM
			}
			return offerOutcomeDone, result, ""
		}
		reason := resp.Error
		if reason == "" {
			reason = "remote task " + string(resp.Status)
		}
		return offerOutcomeRetryable, "", reason
	}
}

// offerAttempt reads the offer's current attempt under the queue lock.
func (s *Swarm) offerAttempt(to *trackedOffer) int {
	q := s.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	return to.info.Attempt
}

// retryOffer re-opens a failed offer for another attempt when retries
// remain. Returns true when the offer was re-broadcast.
func (s *Swarm) retryOffer(ctx context.Context, swarmID string, to *trackedOffer, reason string) bool {
	if to.cancelled() {
		return false
	}
	q := s.queue
	q.mu.Lock()
	if to.retries >= s.cfg.Queue.MaxRetries {
		q.mu.Unlock()
		return false
	}
	to.retries++
	to.info.Retries = to.retries
	to.info.Attempt++
	to.info.Status = OfferOpen
	to.info.Error = ""
	to.info.TaskID = ""
	to.info.Assignee = ""
	to.claims = nil
	offer := to.info.Offer // includes bumped Attempt
	q.mu.Unlock()

	// Drop any stale claims buffered for the previous attempt.
drain:
	for {
		select {
		case <-to.claimsCh:
		default:
			break drain
		}
	}

	if err := s.PublishBroadcast(ctx, swarmID, queueMsg{Kind: queueKindOffer, Offer: &offer}); err != nil {
		return false
	}
	s.publishEvent(runtimeevents.KindSwarmOfferRetry, map[string]any{
		"swarm_id": swarmID,
		"offer_id": to.info.OfferID,
		"attempt":  to.info.Attempt,
		"reason":   reason,
	})
	return true
}

// deadLetterOffer marks an offer dead-lettered after retries ran out.
func (s *Swarm) deadLetterOffer(to *trackedOffer, swarmID, reason string) {
	q := s.queue
	q.mu.Lock()
	to.info.Status = OfferDeadLetter
	to.info.Error = reason
	retries := to.retries
	q.mu.Unlock()
	s.finishOffer(to)
	s.finishTracked(to)
	s.publishEvent(runtimeevents.KindSwarmOfferDeadLetter, map[string]any{
		"swarm_id": swarmID,
		"offer_id": to.info.OfferID,
		"retries":  retries,
		"error":    reason,
	})
}

func (s *Swarm) failOffer(to *trackedOffer, msg string) {
	q := s.queue
	q.mu.Lock()
	to.info.Status = OfferFailed
	to.info.Error = msg
	q.mu.Unlock()
	s.finishOffer(to)
	s.finishTracked(to)
	s.publishEvent(runtimeevents.KindSwarmError, map[string]any{
		"stage":    "offer",
		"offer_id": to.info.OfferID,
		"error":    msg,
	})
}

// finishOffer closes the resolved channel so awaiters stop polling. It is
// idempotent and safe to call from concurrent completion/cancellation paths.
func (s *Swarm) finishOffer(to *trackedOffer) {
	if to.resolved == nil {
		return
	}
	to.resolvedOnce.Do(func() { close(to.resolved) })
}

// finishTracked closes the finished channel — the offer reached a true
// terminal state. It is idempotent and records the first close time for
// terminal-offer eviction.
func (s *Swarm) finishTracked(to *trackedOffer) {
	if to.finished == nil {
		return
	}
	to.finishedOnce.Do(func() {
		close(to.finished)
		to.finishedAt = time.Now()
	})
}

// offerInfo returns a copy of a tracked published offer.
func (q *workQueue) offerInfo(offerID string) (OfferInfo, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	to, ok := q.offers[offerID]
	if !ok {
		return OfferInfo{}, false
	}
	return to.info, true
}

// offerResolved returns a channel that is closed when the offer first leaves
// "open" (assigned or a terminal state), or nil if the offer is unknown.
func (q *workQueue) offerResolved(offerID string) <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	to, ok := q.offers[offerID]
	if !ok {
		return nil
	}
	return to.resolved
}

// offerFinished returns a channel that is closed when the offer reaches a
// true terminal state (done/expired/failed/cancelled/dead_letter), or nil if
// the offer is unknown.
func (q *workQueue) offerFinished(offerID string) <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	to, ok := q.offers[offerID]
	if !ok {
		return nil
	}
	return to.finished
}

// OffersFor returns tracked offers for one swarm.
func (s *Swarm) OffersFor(swarmID string) []OfferInfo {
	var out []OfferInfo
	for _, info := range s.Offers() {
		if info.SwarmID == swarmID {
			out = append(out, info)
		}
	}
	return out
}

// Offers returns the tracked offers — both published and observed.
func (s *Swarm) Offers() []OfferInfo {
	q := s.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]OfferInfo, 0, len(q.offers)+len(q.incoming))
	for _, to := range q.offers {
		out = append(out, to.info)
	}
	for _, info := range q.incoming {
		out = append(out, info)
	}
	return out
}

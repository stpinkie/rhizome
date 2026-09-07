package swarm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// Queue message kinds carried inside MsgBroadcast payloads.
const (
	queueKindOffer  = "offer"
	queueKindClaim  = "claim"
	queueKindAssign = "assign"
)

// queueMsg wraps offer/claim/assign payloads in a broadcast envelope.
type queueMsg struct {
	Kind   string  `json:"kind"`
	Offer  *Offer  `json:"offer,omitempty"`
	Claim  *Claim  `json:"claim,omitempty"`
	Assign *Assign `json:"assign,omitempty"`
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
	// TTLSeconds bounds how long the offer stays open for claims.
	TTLSeconds int64 `json:"ttl_seconds,omitempty"`
}

// Claim is a member's bid for an offer, sent point-to-point to the offerer.
type Claim struct {
	OfferID     string `json:"offer_id"`
	Claimant    string `json:"claimant"`
	ActiveTasks int    `json:"active_tasks,omitempty"`
}

// Assign announces which claimant won an offer.
type Assign struct {
	OfferID string `json:"offer_id"`
	PeerID  string `json:"peer_id"`
	TaskID  string `json:"task_id,omitempty"`
}

// OfferRequest describes a task to offer to a swarm.
type OfferRequest struct {
	AgentID string
	Model   string
	Task    string
	Tools   []string
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
	// OfferObserved marks an offer seen from another peer.
	OfferObserved OfferStatus = "observed"
)

// OfferInfo is the JSON-friendly view of a tracked offer.
type OfferInfo struct {
	Offer
	Status   OfferStatus `json:"status"`
	TaskID   string      `json:"task_id,omitempty"`
	Assignee string      `json:"assignee,omitempty"`
	Error    string      `json:"error,omitempty"`
}

// TaskSubmitter submits a task to a peer; usually
// mesh.Mesh.SubmitRemoteTaskWithPeer.
type TaskSubmitter func(ctx context.Context, preferred peer.ID, call mesh.RemoteCall) (peer.ID, string, error)

// OfferEvaluator decides whether the local node should claim an incoming
// offer. A nil evaluator claims nothing.
type OfferEvaluator func(swarmID string, o Offer) bool

// trackedOffer is the runtime record for one offer we published.
type trackedOffer struct {
	info     OfferInfo
	claims   []Claim
	claimsCh chan Claim
	// resolved is closed when the offer reaches a terminal state (assigned,
	// expired, or failed), so awaiters can stop polling.
	resolved chan struct{}
}

// workQueue implements the distributed offer/claim work queue. Offers are
// broadcast to a swarm; members reply with point-to-point claims during the
// claim window; the offerer picks the least-loaded claimer and submits the
// task through the mesh task protocol.
type workQueue struct {
	s *Swarm

	mu       sync.Mutex
	offers   map[string]*trackedOffer // offers we published, by offer id
	incoming map[string]OfferInfo     // offers observed from other peers
	subs     map[string]func()        // swarm id -> unsubscribe

	submitter TaskSubmitter
	evaluator OfferEvaluator

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

// start subscribes the queue to every currently-joined swarm.
func (q *workQueue) start() {
	for _, id := range q.s.joinedIDs() {
		q.watch(id)
	}
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
	if q.incoming == nil {
		q.incoming = make(map[string]OfferInfo)
	}
	if _, seen := q.incoming[o.OfferID]; seen {
		q.mu.Unlock()
		return
	}
	q.incoming[o.OfferID] = OfferInfo{Offer: o, Status: OfferObserved}
	evaluator := q.evaluator
	q.mu.Unlock()

	if evaluator == nil || !evaluator(swarmID, o) {
		return
	}

	claim := Claim{OfferID: o.OfferID, Claimant: q.s.host.ID().String()}
	if q.s.presence.capProbe != nil {
		_, claim.ActiveTasks = q.s.presence.capProbe()
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
	q.mu.Unlock()
	if !ok || to.info.Status != OfferOpen {
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
	if len(q.offers) >= s.cfg.Queue.MaxOffers {
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
		TTLSeconds: int64(s.cfg.Queue.OfferTTL.Seconds()),
	}
	to := &trackedOffer{
		info:     OfferInfo{Offer: o, Status: OfferOpen},
		claimsCh: make(chan Claim, s.cfg.Queue.MaxOffers),
		resolved: make(chan struct{}),
	}
	q.offers[o.OfferID] = to
	q.mu.Unlock()

	if err := s.PublishBroadcast(ctx, swarmID, queueMsg{Kind: queueKindOffer, Offer: &o}); err != nil {
		q.mu.Lock()
		to.info.Status = OfferFailed
		to.info.Error = err.Error()
		q.mu.Unlock()
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

// resolveOffer collects claims for the claim window, picks the least-loaded
// claimer, submits the task, and publishes the assignment.
func (s *Swarm) resolveOffer(ctx context.Context, swarmID string, to *trackedOffer) {
	window := time.NewTimer(s.cfg.Queue.ClaimWindow)
	defer window.Stop()

collect:
	for {
		select {
		case <-ctx.Done():
			s.failOffer(to, "cancelled")
			return
		case <-window.C:
			break collect
		case c := <-to.claimsCh:
			to.claims = append(to.claims, c)
		}
	}

	if len(to.claims) == 0 {
		q := s.queue
		q.mu.Lock()
		to.info.Status = OfferExpired
		q.mu.Unlock()
		s.finishOffer(to)
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
	})
	if err != nil {
		s.failOffer(to, err.Error())
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
}

func (s *Swarm) failOffer(to *trackedOffer, msg string) {
	q := s.queue
	q.mu.Lock()
	to.info.Status = OfferFailed
	to.info.Error = msg
	q.mu.Unlock()
	s.finishOffer(to)
	s.publishEvent(runtimeevents.KindSwarmError, map[string]any{
		"stage":    "offer",
		"offer_id": to.info.OfferID,
		"error":    msg,
	})
}

// finishOffer closes the resolved channel so awaiters stop polling. Safe to
// call once per tracked offer; subsequent calls are no-ops (close panics on a
// closed channel, so we guard with a select).
func (s *Swarm) finishOffer(to *trackedOffer) {
	if to.resolved == nil {
		return
	}
	select {
	case <-to.resolved:
		// Already closed.
	default:
		close(to.resolved)
	}
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

// offerResolved returns a channel that is closed when the offer reaches a
// terminal state, or nil if the offer is unknown. Callers can select on it
// to avoid busy-waiting.
func (q *workQueue) offerResolved(offerID string) <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	to, ok := q.offers[offerID]
	if !ok {
		return nil
	}
	return to.resolved
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

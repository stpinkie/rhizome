package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/time/rate"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
)

// Member is one known swarm member in the local roster.
type Member struct {
	PeerID   string    `json:"peer_id"`
	LastSeen time.Time `json:"last_seen"`
	// Source records how the member was learned: "direct" (the peer sent us
	// a JOIN/PING) or "gossip" (another member's roster).
	Source string `json:"source"`
	// CapDigest is the member's last advertised capability digest.
	CapDigest string `json:"cap_digest,omitempty"`
	// ActiveTasks is the member's last advertised non-terminal task count.
	ActiveTasks int `json:"active_tasks,omitempty"`
}

// swarmState is the per-swarm runtime registry entry.
type swarmState struct {
	ID       string
	Joined   bool
	JoinedAt time.Time
	Members  map[string]Member
	// Epoch is the coordinator's shared-state sequence number.
	Epoch int64
}

// MemberInfo is the JSON-friendly view of one member.
type MemberInfo struct {
	PeerID      string    `json:"peer_id"`
	LastSeen    time.Time `json:"last_seen,omitempty"`
	Source      string    `json:"source,omitempty"`
	CapDigest   string    `json:"cap_digest,omitempty"`
	ActiveTasks int       `json:"active_tasks,omitempty"`
}

// Compile-time guard: if Member or MemberInfo fields diverge, this fails.
var _ MemberInfo = MemberInfo(Member{})

// SwarmInfo is the JSON-friendly view of one swarm.
type SwarmInfo struct {
	ID          string       `json:"id"`
	Joined      bool         `json:"joined"`
	JoinedAt    time.Time    `json:"joined_at,omitempty"`
	Coordinator string       `json:"coordinator,omitempty"`
	Epoch       int64        `json:"epoch,omitempty"`
	Members     []MemberInfo `json:"members,omitempty"`
}

// Status is the combined swarm snapshot for status output and APIs.
type Status struct {
	PeerID string      `json:"peer_id"`
	Swarms []SwarmInfo `json:"swarms,omitempty"`
}

// Swarm is the swarm coordination layer: named groups of trusted mesh peers.
// It consumes mesh trust through the trusted func so it stays decoupled from
// the mesh implementation.
type Swarm struct {
	node    *rnet.Node
	host    host.Host
	id      *identity.Derived
	cfg     config.SwarmConfig
	trusted func(peer.ID) bool
	bus     runtimeevents.Bus

	transport *Transport
	localBus  *localBus
	bc        broadcaster
	guard     *guard
	presence  presence
	queue     *workQueue
	coord     coordination
	orch      orchestrator
	// coordinators maps swarm id -> elected coordinator peer id.
	coordinators map[string]string

	rateMu     sync.Mutex
	peerLims   map[peer.ID]*rate.Limiter
	peerLimOrd []peer.ID // LRU order for peerLims eviction
	globalLim  *rate.Limiter
	auditLog   *auditLogger

	mu     sync.RWMutex
	swarms map[string]*swarmState
	path   string

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a swarm layer over an existing node. trusted gates every
// inbound envelope and outbound announce; it is usually Mesh.IsTrusted.
// homeDir enables roster persistence at <homeDir>/swarms.json.
func New(
	node *rnet.Node,
	id *identity.Derived,
	cfg config.SwarmConfig,
	trusted func(peer.ID) bool,
	bus runtimeevents.Bus,
	homeDir string,
) *Swarm {
	cfg.Normalize()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Swarm{
		node:         node,
		host:         node.Host(),
		id:           id,
		cfg:          cfg,
		trusted:      trusted,
		bus:          bus,
		swarms:       make(map[string]*swarmState),
		coordinators: make(map[string]string),
		guard:        newGuard(cfg.RequestMaxSkew),
		ctx:          ctx,
		cancel:       cancel,
	}
	if homeDir != "" {
		s.path = filepath.Join(homeDir, "swarms.json")
		s.orch.runs = newRunStore(filepath.Join(homeDir, "swarm-runs.jsonl"))
		_ = s.orch.runs.Load()
	}
	if cfg.AuditLog {
		s.auditLog = newAuditLogger(defaultSwarmAuditPath(homeDir))
	}
	s.transport = NewTransport(s.host, s, cfg.MaxMessageBytes)
	s.localBus = newLocalBus()
	s.bc = newDirectBroadcaster(s, s.localBus)
	s.queue = newWorkQueue(s)
	return s
}

// Broadcaster returns the swarm broadcast channel used by higher-level
// features (presence, work queue, coordination).
func (s *Swarm) Broadcaster() Broadcaster {
	return s.bc
}

// PeerID returns the local node's peer id.
func (s *Swarm) PeerID() string {
	return s.host.ID().String()
}

// IsJoined reports whether the local node is a member of the swarm.
func (s *Swarm) IsJoined(swarmID string) bool {
	return s.isJoined(swarmID)
}

// SetEventBus sets the runtime event bus used to publish swarm events.
func (s *Swarm) SetEventBus(bus runtimeevents.Bus) {
	s.bus = bus
}

func (s *Swarm) publishEvent(kind runtimeevents.Kind, attrs map[string]any) {
	if s.bus == nil {
		return
	}
	severity := runtimeevents.SeverityInfo
	if kind == runtimeevents.KindSwarmError {
		severity = runtimeevents.SeverityError
	}
	s.bus.PublishNonBlocking(runtimeevents.Event{
		Kind:     kind,
		Severity: severity,
		Source:   runtimeevents.Source{Component: "swarm"},
		Attrs:    attrs,
	})
}

// Start loads persisted state, registers the protocol handler, joins the
// configured memberships, and announces to connected trusted peers.
func (s *Swarm) Start(ctx context.Context) error {
	// Replace the placeholder context created in New so Stop always works.
	if s.cancel != nil {
		s.cancel()
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.load()

	// Swap in the GossipSub broadcast backend when configured. The direct
	// backend needs no setup; the pub/sub backend is created now so that
	// topic subscriptions exist before membership joins below.
	if s.cfg.Transport == "gossipsub" {
		gb, err := newGossipsubBroadcaster(s.ctx, s)
		if err != nil {
			return fmt.Errorf("start swarm gossipsub backend: %w", err)
		}
		s.bc = gb
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		_ = s.transport.Start(s.ctx)
	}()

	for _, id := range s.cfg.Memberships {
		if !ValidSwarmID(id) {
			s.publishEvent(runtimeevents.KindSwarmError, map[string]any{
				"stage": "membership", "swarm_id": id, "error": "invalid swarm id",
			})
			continue
		}
		s.joinLocal(id)
	}

	// Re-announce membership whenever a trusted peer connects.
	s.node.OnConnected(func(ev rnet.PeerEvent) {
		if !s.isTrusted(ev.PeerID) {
			return
		}
		for _, id := range s.joinedIDs() {
			go s.announceTo(s.ctx, ev.PeerID, id)
		}
	})

	// Announce to peers that connected before Start ran.
	for _, pid := range s.connectedTrustedPeers() {
		for _, id := range s.joinedIDs() {
			go s.announceTo(s.ctx, pid, id)
		}
	}

	s.startPresence()
	s.queue.start()
	if s.cfg.CoordinationEnabled() {
		s.wg.Add(1)
		go s.sharedStateLoop(s.ctx)
	}
	return nil
}

// Stop deregisters the handler, persists state, and waits for goroutines.
func (s *Swarm) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.transport.Stop()
	if gb, ok := s.bc.(*gossipsubBroadcaster); ok {
		gb.close()
	}
	s.wg.Wait()
	s.save()
	return nil
}

// Join marks the local node as a member of the swarm and announces the join
// to connected trusted peers.
func (s *Swarm) Join(ctx context.Context, id string) error {
	if !ValidSwarmID(id) {
		return fmt.Errorf("invalid swarm id %q", id)
	}
	s.joinLocal(id)
	s.queue.watch(id)
	s.publishEvent(runtimeevents.KindSwarmJoined, map[string]any{"swarm_id": id})
	for _, pid := range s.connectedTrustedPeers() {
		go s.announceTo(ctx, pid, id)
	}
	return nil
}

// Leave removes the local node from the swarm and notifies known members.
func (s *Swarm) Leave(ctx context.Context, id string) error {
	s.mu.Lock()
	swarm, ok := s.swarms[id]
	if !ok || !swarm.Joined {
		s.mu.Unlock()
		return fmt.Errorf("not a member of swarm %q", id)
	}
	swarm.Joined = false
	members := make([]peer.ID, 0, len(swarm.Members))
	for pidStr := range swarm.Members {
		if pid, err := peer.Decode(pidStr); err == nil {
			members = append(members, pid)
		}
	}
	delete(s.swarms, id)
	s.mu.Unlock()
	s.queue.unwatch(id)
	s.afterMembershipChange(id)
	s.save()

	env := Envelope{SwarmID: id, Type: MsgLeave}
	if err := s.sign(&env); err == nil {
		for _, pid := range members {
			_ = s.transport.Push(ctx, pid, env)
		}
	}
	s.publishEvent(runtimeevents.KindSwarmLeft, map[string]any{"swarm_id": id})
	return nil
}

// joinLocal records local membership (idempotent).
func (s *Swarm) joinLocal(id string) {
	s.mu.Lock()
	swarm, ok := s.swarms[id]
	if !ok {
		swarm = &swarmState{ID: id, Members: make(map[string]Member)}
		s.swarms[id] = swarm
	}
	if !swarm.Joined {
		swarm.Joined = true
		swarm.JoinedAt = time.Now()
	}
	s.mu.Unlock()
	s.bc.ensureTopic(id)
	s.afterMembershipChange(id)
	s.save()
}

// joinedIDs returns the ids of swarms the local node has joined.
func (s *Swarm) joinedIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.swarms))
	for id, swarm := range s.swarms {
		if swarm.Joined {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// isTrusted reports whether the peer is in the mesh trust set.
func (s *Swarm) isTrusted(pid peer.ID) bool {
	if s.trusted == nil {
		return false
	}
	return s.trusted(pid)
}

// connectedTrustedPeers returns currently connected peers that are trusted.
func (s *Swarm) connectedTrustedPeers() []peer.ID {
	var out []peer.ID
	for _, pid := range s.host.Network().Peers() {
		if s.isTrusted(pid) && rnet.IsConnectednessUp(s.node.Connectedness(pid)) {
			out = append(out, pid)
		}
	}
	return out
}

// announceTo sends a JOIN for the swarm to one peer and folds the response
// (membership + gossiped roster) into the local registry.
func (s *Swarm) announceTo(ctx context.Context, pid peer.ID, swarmID string) {
	env := Envelope{SwarmID: swarmID, Type: MsgJoin}
	if err := s.sign(&env); err != nil {
		return
	}
	resp, err := s.transport.Call(ctx, pid, env)
	if err != nil {
		return
	}
	if err := s.verify(pid, resp); err != nil {
		return
	}
	if resp.Type != MsgJoinAck {
		return
	}
	var ack joinAckPayload
	if err := decodePayload(resp, &ack); err != nil || !ack.Member {
		return
	}
	s.addMember(swarmID, pid, "direct")
	for _, memberID := range ack.Members {
		mpid, err := peer.Decode(memberID)
		if err != nil || mpid == s.host.ID() {
			continue
		}
		s.addMember(swarmID, mpid, "gossip")
	}
}

// addMember records a peer in a swarm roster, bounded by max_members.
func (s *Swarm) addMember(swarmID string, pid peer.ID, source string) {
	pidStr := pid.String()
	s.mu.Lock()
	swarm, ok := s.swarms[swarmID]
	if !ok {
		swarm = &swarmState{ID: swarmID, Members: make(map[string]Member)}
		s.swarms[swarmID] = swarm
	}
	existing, seen := swarm.Members[pidStr]
	if !seen && len(swarm.Members) >= s.cfg.MaxMembers {
		s.mu.Unlock()
		return
	}
	isNew := !seen
	if seen && existing.Source == "direct" {
		source = "direct"
	}
	swarm.Members[pidStr] = Member{PeerID: pidStr, LastSeen: time.Now(), Source: source}
	s.mu.Unlock()
	s.save()
	if isNew {
		s.publishEvent(runtimeevents.KindSwarmMemberJoined, map[string]any{
			"swarm_id": swarmID,
			"peer_id":  pidStr,
			"source":   source,
		})
		s.afterMembershipChange(swarmID)
	}
}

// removeMember drops a peer from a swarm roster.
func (s *Swarm) removeMember(swarmID string, pid peer.ID) {
	pidStr := pid.String()
	s.mu.Lock()
	swarm, ok := s.swarms[swarmID]
	if !ok {
		s.mu.Unlock()
		return
	}
	_, seen := swarm.Members[pidStr]
	delete(swarm.Members, pidStr)
	s.mu.Unlock()
	if seen {
		s.save()
		s.publishEvent(runtimeevents.KindSwarmMemberLeft, map[string]any{
			"swarm_id": swarmID,
			"peer_id":  pidStr,
		})
		s.afterMembershipChange(swarmID)
	}
}

// memberPeers returns the roster's peer ids for a swarm.
func (s *Swarm) memberPeers(swarmID string) []peer.ID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	swarm, ok := s.swarms[swarmID]
	if !ok {
		return nil
	}
	out := make([]peer.ID, 0, len(swarm.Members))
	for pidStr := range swarm.Members {
		if pid, err := peer.Decode(pidStr); err == nil {
			out = append(out, pid)
		}
	}
	return out
}

// Members returns the known member roster for a swarm.
func (s *Swarm) Members(swarmID string) []MemberInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	swarm, ok := s.swarms[swarmID]
	if !ok {
		return nil
	}
	return membersSnapshotLocked(swarm)
}

// Swarms returns info for every swarm the node knows about. The whole
// snapshot is built under a single RLock hold so a concurrent Leave cannot
// nil out a swarmState mid-iteration (which previously panicked) and the
// member list stays consistent with the Joined/Epoch fields.
func (s *Swarm) Swarms() []SwarmInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.swarms))
	for id := range s.swarms {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]SwarmInfo, 0, len(ids))
	for _, id := range ids {
		swarm, ok := s.swarms[id]
		if !ok {
			// Should not happen under the lock, but guard defensively.
			continue
		}
		info := SwarmInfo{
			ID:          id,
			Joined:      swarm.Joined,
			JoinedAt:    swarm.JoinedAt,
			Coordinator: s.coordinators[id],
			Epoch:       swarm.Epoch,
			Members:     membersSnapshotLocked(swarm),
		}
		out = append(out, info)
	}
	return out
}

// membersSnapshotLocked builds a sorted MemberInfo slice from a swarmState.
// Caller must hold s.mu (at least RLock).
func membersSnapshotLocked(swarm *swarmState) []MemberInfo {
	if swarm == nil {
		return nil
	}
	out := make([]MemberInfo, 0, len(swarm.Members))
	for _, m := range swarm.Members {
		out = append(out, MemberInfo(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	return out
}

// Status returns the combined swarm snapshot used by the daemon API.
func (s *Swarm) Status() Status {
	return Status{PeerID: s.host.ID().String(), Swarms: s.Swarms()}
}

// Events subscribes to swarm.* runtime events, optionally filtered to one
// swarm id. The returned cleanup closes the subscription.
func (s *Swarm) Events(ctx context.Context, swarmID string) (<-chan runtimeevents.Event, func(), error) {
	if s.bus == nil {
		return nil, nil, fmt.Errorf("event bus not configured")
	}

	filter := s.bus.Channel().OfKind(
		runtimeevents.KindSwarmJoined,
		runtimeevents.KindSwarmLeft,
		runtimeevents.KindSwarmMemberJoined,
		runtimeevents.KindSwarmMemberLeft,
		runtimeevents.KindSwarmMemberExpired,
		runtimeevents.KindSwarmOfferPublished,
		runtimeevents.KindSwarmOfferAssigned,
		runtimeevents.KindSwarmOfferExpired,
		runtimeevents.KindSwarmCoordinatorElected,
		runtimeevents.KindSwarmStateWritten,
		runtimeevents.KindSwarmRunStart,
		runtimeevents.KindSwarmRunEnd,
		runtimeevents.KindSwarmError,
	)
	if swarmID != "" {
		filter = filter.Filter(func(evt runtimeevents.Event) bool {
			sid, _ := evt.Attrs["swarm_id"].(string)
			return sid == swarmID
		})
	}

	sub, ch, err := filter.SubscribeChan(ctx, runtimeevents.SubscribeOptions{
		Name:         "swarm-events",
		Buffer:       64,
		Backpressure: runtimeevents.DropOldest,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe to swarm events: %w", err)
	}
	cleanup := func() { _ = sub.Close() }
	return ch, cleanup, nil
}

// sign stamps From, Timestamp, Nonce and the Ed25519 signature over the
// canonical encoding.
func (s *Swarm) sign(env *Envelope) error {
	env.Signature = nil
	env.From = s.host.ID().String()
	env.Timestamp = time.Now().Unix()
	if env.Nonce == "" {
		env.Nonce = newNonce()
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}
	env.Signature = identity.Sign(s.id.PrivateKey, payload)
	return nil
}

// verify authenticates an envelope: the claimed From must match the
// authenticated stream peer and the signature must verify against that
// peer's public key.
func (s *Swarm) verify(from peer.ID, env Envelope) error {
	if env.From != "" && env.From != from.String() {
		return fmt.Errorf("envelope from %q does not match stream peer %s", env.From, from)
	}
	if len(env.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}
	sig := env.Signature
	env.Signature = nil
	payload, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}
	pub := s.host.Peerstore().PubKey(from)
	if pub == nil {
		return fmt.Errorf("no public key for peer %s", from)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify envelope signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid envelope signature from peer %s", from)
	}
	return nil
}

// SetAuditPath overrides the audit trail location. An empty path disables
// file-based auditing.
func (s *Swarm) SetAuditPath(path string) {
	if path == "" {
		s.auditLog = nil
		return
	}
	s.auditLog = newAuditLogger(path)
}

// validateInbound runs the shared inbound checks: trust, signature, replay,
// and inbound rate limits.
func (s *Swarm) validateInbound(from peer.ID, env Envelope) error {
	if !s.isTrusted(from) {
		return fmt.Errorf("peer %s is not trusted", from)
	}
	if err := s.verify(from, env); err != nil {
		return err
	}
	if !s.allowSwarmRate(from) {
		return fmt.Errorf("rate_limited: peer %s exceeded the swarm message limit", from)
	}
	return s.guard.check(from, env.SwarmID, env.Nonce, env.Timestamp)
}

// HandleRequest implements Transport.Handler for request envelopes.
func (s *Swarm) HandleRequest(from peer.ID, env Envelope) Envelope {
	started := time.Now()
	if err := s.validateInbound(from, env); err != nil {
		s.publishEvent(runtimeevents.KindSwarmError, map[string]any{
			"stage":   "inbound",
			"peer_id": from.String(),
			"error":   err.Error(),
		})
		s.auditSwarm(from, string(env.Type), env.SwarmID, env.Nonce, "rejected", started, err.Error())
		return Envelope{SwarmID: env.SwarmID, Type: env.Type + "_rejected"}
	}

	switch env.Type {
	case MsgJoin:
		var ack joinAckPayload
		if s.isJoined(env.SwarmID) {
			s.addMember(env.SwarmID, from, "direct")
			ack.Member = true
			ack.Members = s.rosterIDs(env.SwarmID, from)
		}
		payload, _ := encodePayload(ack)
		resp := Envelope{SwarmID: env.SwarmID, Type: MsgJoinAck, Payload: payload}
		_ = s.sign(&resp)
		return resp
	case MsgQuery:
		resp := Envelope{SwarmID: env.SwarmID, Type: MsgQueryResp}
		qr := queryRespPayload{Swarms: s.joinedIDs(), Members: make(map[string][]string)}
		for _, id := range qr.Swarms {
			for _, m := range s.Members(id) {
				qr.Members[id] = append(qr.Members[id], m.PeerID)
			}
		}
		resp.Payload, _ = encodePayload(qr)
		_ = s.sign(&resp)
		return resp
	default:
		resp := Envelope{SwarmID: env.SwarmID, Type: env.Type + "_rejected"}
		_ = s.sign(&resp)
		return resp
	}
}

// HandlePush implements Transport.Handler for one-way envelopes.
func (s *Swarm) HandlePush(from peer.ID, env Envelope) {
	started := time.Now()
	if err := s.validateInbound(from, env); err != nil {
		s.publishEvent(runtimeevents.KindSwarmError, map[string]any{
			"stage":   "inbound",
			"peer_id": from.String(),
			"error":   err.Error(),
		})
		s.auditSwarm(from, string(env.Type), env.SwarmID, env.Nonce, "rejected", started, err.Error())
		return
	}

	switch env.Type {
	case MsgLeave:
		s.removeMember(env.SwarmID, from)
	case MsgPing:
		if s.isJoined(env.SwarmID) {
			s.notePing(from, env)
		}
	case MsgBroadcast:
		if s.isJoined(env.SwarmID) {
			s.localBus.deliver(env)
		}
	case MsgJoin:
		// A peer may announce via push; treat like a request join minus ack.
		if s.isJoined(env.SwarmID) {
			s.addMember(env.SwarmID, from, "direct")
		}
	}
}

// isJoined reports whether the local node is a member of the swarm.
func (s *Swarm) isJoined(swarmID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	swarm, ok := s.swarms[swarmID]
	return ok && swarm.Joined
}

// rosterIDs returns the known member peer ids for a swarm, excluding one peer.
func (s *Swarm) rosterIDs(swarmID string, exclude peer.ID) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	swarm, ok := s.swarms[swarmID]
	if !ok {
		return nil
	}
	self := s.host.ID().String()
	out := make([]string, 0, len(swarm.Members)+1)
	out = append(out, self)
	for pidStr := range swarm.Members {
		if pidStr == exclude.String() || pidStr == self {
			continue
		}
		out = append(out, pidStr)
	}
	sort.Strings(out)
	return out
}

// Send is a convenience wrapper: sign and push an envelope to one peer.
func (s *Swarm) Send(ctx context.Context, pid peer.ID, env Envelope) error {
	if err := s.sign(&env); err != nil {
		return err
	}
	return s.transport.Push(ctx, pid, env)
}

// Request is a convenience wrapper: sign and call one peer.
func (s *Swarm) Request(ctx context.Context, pid peer.ID, env Envelope) (Envelope, error) {
	if err := s.sign(&env); err != nil {
		return Envelope{}, err
	}
	return s.transport.Call(ctx, pid, env)
}

// PublishBroadcast signs and broadcasts an application-level message to all
// swarm members, and delivers it to local subscribers too.
func (s *Swarm) PublishBroadcast(ctx context.Context, swarmID string, payload any) error {
	data, err := encodePayload(payload)
	if err != nil {
		return err
	}
	env := Envelope{SwarmID: swarmID, Type: MsgBroadcast, Payload: data}
	if err := s.sign(&env); err != nil {
		return err
	}
	s.localBus.deliver(env)
	return s.bc.Publish(ctx, env)
}

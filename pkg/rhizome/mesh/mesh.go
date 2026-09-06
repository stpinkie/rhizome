package mesh

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libnet "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"golang.org/x/time/rate"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
	rsync "github.com/stpinkie/rhizome/pkg/rhizome/sync"
	"github.com/stpinkie/rhizome/pkg/skills"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

const (
	// CapsProtocolID is the libp2p protocol used for capability exchange.
	CapsProtocolID = protocol.ID("/rhizome/caps/1.0.0")

	capFrameAnnounce = byte(1)
	capFrameQuery    = byte(2)
	capFrameResponse = byte(3)
)

// Capability describes what a peer is willing/able to run.
type Capability struct {
	PeerID    string          `json:"peer_id"`
	Models    []string        `json:"models,omitempty"`
	Skills    []string        `json:"skills,omitempty"`
	Agents    []string        `json:"agents,omitempty"`
	Timestamp int64           `json:"timestamp"`
	Allows    map[string]bool `json:"allows,omitempty"`
	// ActiveTasks is the number of non-terminal remote tasks the peer is
	// currently executing, as of the manifest timestamp. Callers use it as a
	// least-loaded hint when picking a peer; it goes stale within the
	// manifest freshness window.
	ActiveTasks int `json:"active_tasks,omitempty"`
	// Signature covers the canonical encoding of all fields above, proving
	// the manifest was issued by PeerID. Unsigned manifests are rejected
	// unless mesh.require_signed_caps is disabled; a mesh.cap.unsigned
	// event is emitted either way.
	Signature []byte `json:"signature,omitempty"`
}

// PeerCapability is a minimal view of a peer's capability for status output.
type PeerCapability struct {
	Models      []string `json:"models,omitempty"`
	Skills      []string `json:"skills,omitempty"`
	Agents      []string `json:"agents,omitempty"`
	ActiveTasks int      `json:"active_tasks,omitempty"`
}

// PeerStatus is the JSON-friendly status for one connected peer.
type PeerStatus struct {
	PeerID     string         `json:"peer_id"`
	Addrs      []string       `json:"addrs"`
	Trusted    bool           `json:"trusted"`
	Capability PeerCapability `json:"capability,omitempty"`
}

// NetworkStatus is the combined mesh/DHT snapshot returned by Mesh.NetworkStatus.
type NetworkStatus struct {
	Name         string          `json:"name"`
	NodeIndex    uint32          `json:"node_index"`
	PeerID       string          `json:"peer_id"`
	Identity     string          `json:"identity"`
	Reachability string          `json:"reachability,omitempty"`
	Addrs        []string        `json:"addrs,omitempty"`
	RelayedAddrs []string        `json:"relayed_addrs,omitempty"`
	Peers        []PeerStatus    `json:"peers,omitempty"`
	DHT          *rnet.DHTStatus `json:"dht,omitempty"`
}

// RankedPeer is a capable peer with its capability and routing score.
type RankedPeer struct {
	PID   peer.ID
	Cap   Capability
	Score int64
}

// Mesh is the decentralized agent runtime layer over a Rhizome node.
type Mesh struct {
	node   *rnet.Node
	syncer *rsync.Syncer
	id     *identity.Derived
	cfg    config.MeshConfig
	host   host.Host

	caps   map[peer.ID]Capability
	capsMu sync.RWMutex

	trust   map[peer.ID]bool
	trustMu sync.RWMutex

	rpc        *agentrpc.Transport
	cap        *CapsTransport
	taskRPC    *agenttask.Transport
	tasks      *TaskStore
	scoreStore *PeerScoreStore
	runFunc    func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, error)

	models   []string
	modelsMu sync.RWMutex

	agentLister   func() []string
	agentListerMu sync.RWMutex

	skillsLoader   *skills.SkillsLoader
	skillsLoaderMu sync.RWMutex

	stop     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	eventBus runtimeevents.Bus
	name     string

	replay    *replayGuard
	rateMu    sync.Mutex
	peerLims  map[peer.ID]*rate.Limiter
	globalLim *rate.Limiter
	auditLog  *auditLogger
}

// NewMesh creates a mesh layer over an existing node and syncer.
func NewMesh(
	node *rnet.Node,
	syncer *rsync.Syncer,
	id *identity.Derived,
	cfg config.MeshConfig,
	runFunc func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, error),
) *Mesh {
	m := &Mesh{
		node:    node,
		syncer:  syncer,
		id:      id,
		cfg:     cfg,
		host:    node.Host(),
		caps:    make(map[peer.ID]Capability),
		trust:   make(map[peer.ID]bool),
		runFunc: runFunc,
		stop:    make(chan struct{}),
		replay:  newReplayGuard(cfg.RequestMaxSkew),
	}
	if cfg.AuditLog {
		m.auditLog = newAuditLogger(defaultAuditPath())
	}
	m.rpc = agentrpc.NewTransport(m.host, m)
	m.tasks = NewTaskStore()
	m.scoreStore = NewPeerScoreStore()
	m.taskRPC = agenttask.NewTransport(m.host, m)
	m.cap = NewCapsTransportWithPolicy(
		m.host,
		func(pid peer.ID, c Capability) {
			if err := m.verifyCapability(pid, &c); err != nil {
				m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
					"stage":   "capability.announce",
					"error":   err.Error(),
					"peer_id": pid.String(),
				})
				return
			}
			m.SetCapability(pid, c)
		},
		m.isTrusted,
		m.localCapability,
		func(pid peer.ID, allowed bool) {
			m.publishMeshEvent(runtimeevents.KindMeshCapabilityQueried, map[string]any{
				"peer_id":  pid.String(),
				"outgoing": false,
				"allowed":  allowed,
			})
		},
	)
	return m
}

// SetEventBus sets the runtime event bus used to publish mesh events.
func (m *Mesh) SetEventBus(bus runtimeevents.Bus) {
	m.eventBus = bus
}

// SetAuditPath overrides the audit trail location. An empty path disables
// file-based auditing (the mesh.remote.audit event is still emitted).
func (m *Mesh) SetAuditPath(path string) {
	if path == "" {
		m.auditLog = nil
		return
	}
	m.auditLog = newAuditLogger(path)
}

// SetName sets the human-readable node name included in NetworkStatus.
func (m *Mesh) SetName(name string) {
	m.name = name
}

// Name returns the human-readable node name, if any.
func (m *Mesh) Name() string {
	if m == nil {
		return ""
	}
	return m.name
}

// Connect dials a peer by its multiaddr using the mesh's underlying node.
func (m *Mesh) Connect(ctx context.Context, addr string) error {
	if m == nil || m.node == nil {
		return fmt.Errorf("mesh not ready")
	}
	return m.node.Connect(ctx, addr)
}

// publishMeshEvent publishes a non-blocking mesh runtime event if a bus is configured.
func (m *Mesh) publishMeshEvent(kind runtimeevents.Kind, attrs map[string]any) {
	if m.eventBus == nil {
		return
	}
	severity := runtimeevents.SeverityInfo
	if kind == runtimeevents.KindMeshError {
		severity = runtimeevents.SeverityError
	}
	m.eventBus.PublishNonBlocking(runtimeevents.Event{
		Kind:     kind,
		Severity: severity,
		Source: runtimeevents.Source{
			Component: "mesh",
		},
		Attrs: attrs,
	})
}

// recordPeerCall records the outcome and latency of a call to a peer.
// It is used by CallRemote and taskCall to build a quality score for PickPeer.
func (m *Mesh) recordPeerCall(pid peer.ID, success bool, latency time.Duration, err error) {
	if m.scoreStore == nil {
		return
	}
	m.scoreStore.Record(pid, success, latency, err)
}

// Start registers the agent and capability protocol handlers.
func (m *Mesh) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	// Publish a terminal event when a running task is evicted to make room,
	// so the owner sees a coherent error state instead of a silent drop.
	m.tasks.SetOnEvict(func(t *MeshTask) {
		m.publishMeshEvent(runtimeevents.KindMeshTaskUpdate, map[string]any{
			"peer_id":  t.Owner.String(),
			"task_id":  t.ID,
			"agent_id": t.AgentID,
			"status":   string(agenttask.StatusError),
			"error":    t.Err,
		})
	})

	// Recover any tasks and peer scores persisted from a previous run. Running
	// tasks cannot be resumed (their goroutine context is gone), so they are
	// marked as errors.
	restarted, err := m.tasks.Load()
	if err != nil {
		logger.Warnf("failed to load persisted tasks: %v", err)
	} else {
		for _, t := range restarted {
			m.publishMeshEvent(runtimeevents.KindMeshTaskUpdate, map[string]any{
				"peer_id":  t.Owner.String(),
				"task_id":  t.ID,
				"agent_id": t.AgentID,
				"status":   string(agenttask.StatusError),
				"error":    t.Err,
			})
		}
	}

	if m.scoreStore != nil {
		if err := m.scoreStore.Load(); err != nil {
			logger.Warnf("failed to load peer scores: %v", err)
		}
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		_ = m.rpc.Start(m.ctx)
	}()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		_ = m.cap.Start(m.ctx)
	}()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		_ = m.taskRPC.Start(m.ctx)
	}()

	for _, p := range m.cfg.TrustedPeers {
		pid, err := peer.Decode(p)
		if err != nil {
			continue
		}
		m.trust[pid] = true
	}

	// Eagerly advertise capabilities to newly connected trusted peers.
	m.node.OnConnected(func(ev rnet.PeerEvent) {
		if !m.isTrusted(ev.PeerID) {
			return
		}
		m.advertiseTo(ev.PeerID)
	})

	// Start periodic capability advertisement.
	m.wg.Add(1)
	go m.announceLoop(m.ctx)

	return nil
}

// SetRunFunc sets the function used to execute remote agent requests.
// It is typically called by the gateway after the AgentLoop is created.
func (m *Mesh) SetRunFunc(fn func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, error)) {
	m.runFunc = fn
}

// SetTaskStorePath enables persistence of the task store at the given path.
// It must be called before Mesh.Start.
func (m *Mesh) SetTaskStorePath(path string) {
	if m.tasks == nil {
		m.tasks = NewTaskStore()
	}
	m.tasks.SetPath(path)
}

// SetScoreStorePath enables persistence of the peer score store at the given
// path. It must be called before Mesh.Start.
func (m *Mesh) SetScoreStorePath(path string) {
	if m.scoreStore == nil {
		m.scoreStore = NewPeerScoreStore()
	}
	m.scoreStore.SetPath(path)
}

// TaskEvents returns a channel of mesh task events (submit/update) and a
// cleanup function. If peerID is non-empty, the stream only includes events
// whose attrs contain that peer id.
func (m *Mesh) TaskEvents(ctx context.Context, peerID string) (<-chan runtimeevents.Event, func(), error) {
	if m.eventBus == nil {
		return nil, nil, fmt.Errorf("event bus not configured")
	}

	filter := m.eventBus.Channel().OfKind(
		runtimeevents.KindMeshTaskSubmit,
		runtimeevents.KindMeshTaskUpdate,
	)

	if peerID != "" {
		pid, err := peer.Decode(peerID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid peer id: %w", err)
		}
		want := pid.String()
		filter = filter.Filter(func(evt runtimeevents.Event) bool {
			s, _ := evt.Attrs["peer_id"].(string)
			return s == want
		})
	}

	sub, ch, err := filter.SubscribeChan(ctx, runtimeevents.SubscribeOptions{
		Name:         "task-events",
		Buffer:       64,
		Backpressure: runtimeevents.DropOldest,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe to task events: %w", err)
	}

	cleanup := func() {
		_ = sub.Close()
	}
	return ch, cleanup, nil
}

// Stop cleanly shuts down the mesh.
func (m *Mesh) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}
	close(m.stop)
	if m.tasks != nil {
		m.tasks.Close()
	}
	if m.scoreStore != nil {
		m.scoreStore.Close()
	}
	m.wg.Wait()
	return nil
}

func (m *Mesh) makeErrorResponse(req agentrpc.Request, message string) (agentrpc.Response, error) {
	resp := agentrpc.Response{
		CorrelationID: req.CorrelationID,
		Nonce:         req.Nonce,
		Status:        "error",
		Error:         message,
	}
	if err := m.signResponse(&resp); err != nil {
		return agentrpc.Response{}, fmt.Errorf("sign response: %w", err)
	}
	return resp, nil
}

// HandleRequest implements agentrpc.Handler for incoming remote agent tasks.
func (m *Mesh) HandleRequest(from peer.ID, req agentrpc.Request) (agentrpc.Response, error) {
	started := time.Now()
	op := "delegate"
	if req.Async {
		op = "spawn"
	}
	reject := func(msg string) (agentrpc.Response, error) {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":          "remote.request",
			"error":          msg,
			"peer_id":        from.String(),
			"agent_id":       req.TargetAgentID,
			"correlation_id": req.CorrelationID,
			"op":             op,
		})
		m.auditMesh(from, op, req.TargetAgentID, req.CorrelationID, "rejected", started, msg)
		return m.makeErrorResponse(req, msg)
	}

	if !m.isTrusted(from) {
		return reject(fmt.Sprintf("peer %s is not trusted", from))
	}
	if err := m.verifyRequest(from, req); err != nil {
		return reject(fmt.Sprintf("verify request: %v", err))
	}

	// Replay protection. Requests must carry a nonce and timestamp, both
	// covered by the signature; requests without them are rejected.
	if req.Nonce == "" || req.Timestamp == 0 {
		return reject("request missing nonce/timestamp")
	}
	if err := m.replay.check(from, req.Nonce, req.Timestamp); err != nil {
		return reject(fmt.Sprintf("replay check failed: %v", err))
	}

	if err := m.checkRemoteAllowed(from, op, req.TargetAgentID); err != nil {
		return reject(fmt.Sprintf("forbidden: %v", err))
	}
	if !m.allowRate(from) {
		return reject("rate_limited: too many requests")
	}

	startKind, endKind := m.remoteAgentEventKinds(req.Async)
	m.publishMeshEvent(startKind, map[string]any{
		"peer_id":        from.String(),
		"agent_id":       req.TargetAgentID,
		"correlation_id": req.CorrelationID,
		"async":          req.Async,
	})

	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = m.cfg.RemoteTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	result, err := m.runFunc(ctx, req)

	status := "ok"
	if err != nil {
		status = "error"
	}
	m.publishMeshEvent(endKind, map[string]any{
		"peer_id":        from.String(),
		"agent_id":       req.TargetAgentID,
		"correlation_id": req.CorrelationID,
		"async":          req.Async,
		"status":         status,
	})
	m.auditMesh(from, op, req.TargetAgentID, req.CorrelationID, status, started, errString(err))

	if err != nil {
		resp := agentrpc.Response{
			CorrelationID: req.CorrelationID,
			Nonce:         req.Nonce,
			Status:        "error",
			Error:         err.Error(),
		}
		if signErr := m.signResponse(&resp); signErr != nil {
			return agentrpc.Response{}, fmt.Errorf("sign response: %w", signErr)
		}
		return resp, nil
	}

	resp := agentrpc.Response{
		CorrelationID: req.CorrelationID,
		Nonce:         req.Nonce,
		Status:        "ok",
		Result:        result,
	}
	if err := m.signResponse(&resp); err != nil {
		return agentrpc.Response{}, fmt.Errorf("sign response: %w", err)
	}
	return resp, nil
}

// CallRemote sends an agent task to a trusted peer and waits for the result.
// If preferred is empty, the best connected, capable peer is selected
// automatically. If mesh.task_failover is enabled and the first peer fails,
// the call is retried on the next-best capable peer. Async calls go through
// the task protocol (/rhizome/agent-task/1.0.0) and poll for completion;
// peers without task protocol support fall back to the synchronous agent
// protocol.
func (m *Mesh) CallRemote(
	ctx context.Context,
	preferred peer.ID,
	call RemoteCall,
) (*toolshared.ToolResult, error) {
	if preferred != "" && !m.isTrusted(preferred) {
		return nil, fmt.Errorf("peer %s is not trusted", preferred)
	}

	if call.Async {
		if result, err, ok := m.callRemoteTask(ctx, preferred, call); ok {
			return result, err
		}
		// Peer does not support the task protocol; fall through to the
		// synchronous request/response protocol for compatibility.
	}

	req := agentrpc.Request{
		CorrelationID: newCorrelationID(),
		TargetAgentID: call.TargetAgentID,
		Model:         call.Model,
		SystemPrompt:  call.SystemPrompt,
		Timeout:       m.cfg.RemoteTimeout,
		Tools:         toolNamesToRefs(call.Tools),
		Async:         call.Async,
		Nonce:         newTaskNonce(),
		Timestamp:     time.Now().Unix(),
	}

	// Sign the request payload with the local node key.
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req.Signature = identity.Sign(m.id.PrivateKey, payload)

	candidates := m.syncCandidates(preferred, call.TargetAgentID)
	if len(candidates) == 0 {
		return nil, fmt.Errorf(
			"no trusted, connected peer advertises agent %q for op delegate",
			call.TargetAgentID)
	}

	maxAttempts := m.cfg.TaskRetries
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	startKind, endKind := m.remoteAgentEventKinds(call.Async)

	var lastErr error
	for i, c := range candidates {
		pid := c.PID
		if i > 0 && !m.cfg.TaskFailover {
			break
		}

		m.publishMeshEvent(startKind, map[string]any{
			"peer_id":        pid.String(),
			"agent_id":       call.TargetAgentID,
			"correlation_id": req.CorrelationID,
			"async":          call.Async,
		})

		for attempt := 0; attempt < maxAttempts; attempt++ {
			if attempt > 0 {
				m.node.ForceReconnect(ctx, pid)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(250 * time.Millisecond):
				}
			}

			start := time.Now()
			resp, err := m.rpc.Call(ctx, pid, req)
			latency := time.Since(start)
			if err != nil {
				m.recordPeerCall(pid, false, latency, err)
				lastErr = err
				if !m.isSyncRetryable(err) {
					break
				}
				continue
			}
			if err := m.verifyResponse(pid, &resp); err != nil {
				m.recordPeerCall(pid, false, latency, err)
				lastErr = err
				if !m.isSyncRetryable(err) {
					break
				}
				continue
			}
			// The signed response must echo the request nonce so it is bound
			// to this exact request and cannot be replayed for another.
			if req.Nonce != "" && resp.Nonce != req.Nonce {
				err := fmt.Errorf("response nonce does not match request")
				m.recordPeerCall(pid, false, latency, err)
				lastErr = err
				if !m.isSyncRetryable(err) {
					break
				}
				continue
			}
			if resp.Status != "ok" {
				err := fmt.Errorf("remote agent failed: %s", resp.Error)
				m.recordPeerCall(pid, false, latency, err)
				// A remote agent failure is a task execution error; do not
				// failover to another peer and risk duplicate execution.
				m.publishMeshEvent(endKind, map[string]any{
					"peer_id":        pid.String(),
					"agent_id":       call.TargetAgentID,
					"correlation_id": req.CorrelationID,
					"async":          call.Async,
					"error":          err.Error(),
				})
				return nil, err
			}
			m.recordPeerCall(pid, true, latency, nil)
			m.publishMeshEvent(endKind, map[string]any{
				"peer_id":        pid.String(),
				"agent_id":       call.TargetAgentID,
				"correlation_id": req.CorrelationID,
				"async":          call.Async,
			})
			return resp.Result, nil
		}

		if lastErr != nil {
			m.publishMeshEvent(endKind, map[string]any{
				"peer_id":        pid.String(),
				"agent_id":       call.TargetAgentID,
				"correlation_id": req.CorrelationID,
				"async":          call.Async,
				"error":          lastErr.Error(),
			})
		}
	}
	return nil, fmt.Errorf("call remote failed: %w", lastErr)
}

// syncCandidates returns connected, trusted peers able to delegate the given
// agent. The preferred peer, if given, is tried first.
func (m *Mesh) syncCandidates(preferred peer.ID, agentID string) []RankedPeer {
	exclude := make(map[peer.ID]bool)
	if preferred != "" {
		exclude[preferred] = true
	}
	candidates := m.PickPeerRanked(agentID, "delegate", exclude)
	if preferred != "" {
		candidates = append([]RankedPeer{{PID: preferred}}, candidates...)
	}
	return candidates
}

// isSyncRetryable reports whether a synchronous call failure is worth retrying
// on the same or another peer. Task execution failures are not retried to
// avoid duplicate execution; local signing errors and context cancellation are
// also not retried.
func (m *Mesh) isSyncRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	msg := err.Error()
	nonRetryable := []string{
		"encode request",
		"remote agent failed",
		"is not trusted",
	}
	for _, s := range nonRetryable {
		if strings.Contains(msg, s) {
			return false
		}
	}
	return true
}

// Advertise sends the local capability manifest to all connected peers.
func (m *Mesh) Advertise(ctx context.Context) {
	capability := m.localCapability()
	for _, pid := range m.node.ConnectedPeers() {
		if pid == m.node.ID() {
			continue
		}
		m.advertiseToWithCap(ctx, pid, capability)
	}
}

func (m *Mesh) advertiseTo(pid peer.ID) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	m.advertiseToWithCap(ctx, pid, m.localCapability())
}

func (m *Mesh) advertiseToWithCap(ctx context.Context, pid peer.ID, capability Capability) {
	go func(p peer.ID) {
		_ = m.cap.Send(ctx, p, capability)
	}(pid)
}

// announceLoop periodically re-advertises capabilities. The interval defaults
// to the configured DHT reprovide interval to keep capability info fresh.
func (m *Mesh) announceLoop(ctx context.Context) {
	defer m.wg.Done()

	m.Advertise(ctx)

	interval := m.cfg.DHTReprovideInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Advertise(ctx)
		}
	}
}

// SetModelList sets the list of model names to advertise. It filters out
// disabled and virtual models and strips all other metadata to avoid leaking
// sensitive configuration.
func (m *Mesh) SetModelList(models config.SecureModelList) {
	m.modelsMu.Lock()
	defer m.modelsMu.Unlock()

	m.models = m.models[:0]
	for _, model := range models {
		if model == nil || !model.Enabled || model.IsVirtual() || model.ModelName == "" {
			continue
		}
		m.models = append(m.models, model.ModelName)
	}
}

// SetSkillsLoader sets the skills loader used to discover skill names for
// capability advertisement.
func (m *Mesh) SetSkillsLoader(loader *skills.SkillsLoader) {
	m.skillsLoaderMu.Lock()
	defer m.skillsLoaderMu.Unlock()
	m.skillsLoader = loader
}

func (m *Mesh) remoteAgentEventKinds(async bool) (start, end runtimeevents.Kind) {
	if async {
		return runtimeevents.KindMeshRemoteSpawnStart, runtimeevents.KindMeshRemoteSpawnEnd
	}
	return runtimeevents.KindMeshRemoteDelegateStart, runtimeevents.KindMeshRemoteDelegateEnd
}

// localCapability builds a capability manifest from local configuration.
func (m *Mesh) localCapability() Capability {
	c := Capability{
		PeerID:    m.node.PeerID(),
		Timestamp: time.Now().Unix(),
		Allows:    make(map[string]bool),
	}
	c.Allows["delegate"] = m.cfg.AllowRemoteDelegate
	c.Allows["spawn"] = m.cfg.AllowRemoteSpawn
	c.Allows["sync"] = true
	if m.tasks != nil {
		c.ActiveTasks = m.tasks.ActiveCount()
	}

	if m.cfg.AdvertiseModels {
		m.modelsMu.RLock()
		c.Models = append([]string(nil), m.models...)
		m.modelsMu.RUnlock()
	}
	if m.cfg.AdvertiseSkills {
		m.skillsLoaderMu.RLock()
		loader := m.skillsLoader
		m.skillsLoaderMu.RUnlock()
		if loader != nil {
			seen := make(map[string]bool)
			for _, info := range loader.ListSkills() {
				if info.Name == "" || seen[info.Name] {
					continue
				}
				seen[info.Name] = true
				c.Skills = append(c.Skills, info.Name)
			}
		}
	}

	// Advertise the configured agent ids so remote spawn/delegate can target
	// them. The gateway wires the registry's live agent list via SetAgentLister.
	m.agentListerMu.RLock()
	lister := m.agentLister
	m.agentListerMu.RUnlock()
	if lister != nil {
		c.Agents = lister()
	}
	if len(c.Agents) == 0 {
		c.Agents = append(c.Agents, "main")
	}
	m.signCapability(&c)
	return c
}

// signCapability signs the canonical capability payload with the node key.
func (m *Mesh) signCapability(c *Capability) {
	c.Signature = nil
	payload, err := json.Marshal(c)
	if err != nil {
		return
	}
	c.Signature = identity.Sign(m.id.PrivateKey, payload)
}

// verifyCapability authenticates a capability received from a peer. It
// enforces that the claimed peer id matches the sender and, when the
// manifest is signed, that the signature verifies against the sender's
// public key and the timestamp is fresh. Unsigned manifests are flagged
// via the mesh.cap.unsigned event and rejected unless
// mesh.require_signed_caps is disabled.
//
// Note on trust boundaries: this function runs for every incoming capability
// announce, including from untrusted peers. A passing result means the
// manifest is authentic — it does NOT mean the peer is authorized to run
// remote tasks. Capability *storage* (m.caps) is separate from remote
// *execution* authorization (trust + ACL + rate limits in HandleRequest).
// An untrusted peer's valid capability may be stored for informational
// purposes but cannot trigger any agent work on this node.
func (m *Mesh) verifyCapability(from peer.ID, c *Capability) error {
	if c.PeerID == "" {
		c.PeerID = from.String()
	}
	if c.PeerID != from.String() {
		return fmt.Errorf("capability claims peer %s but was sent by %s", c.PeerID, from)
	}

	if len(c.Signature) == 0 {
		m.publishMeshEvent(runtimeevents.KindMeshCapabilityUnsigned, map[string]any{
			"peer_id": from.String(),
		})
		if m.cfg.RequireSignedCaps {
			return fmt.Errorf("unsigned capability manifest from peer %s", from)
		}
		return nil
	}

	sig := c.Signature
	c.Signature = nil
	defer func() { c.Signature = sig }()

	payload, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode capability: %w", err)
	}
	pub := m.host.Peerstore().PubKey(from)
	if pub == nil {
		return fmt.Errorf("no public key for peer %s", from)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify capability signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid capability signature from peer %s", from)
	}

	if age := time.Since(time.Unix(c.Timestamp, 0)); c.Timestamp == 0 || age < 0 || age > capabilityMaxAge {
		return fmt.Errorf("capability timestamp outside allowed window")
	}
	return nil
}

// SetAgentLister sets the function used to enumerate local agent ids for
// capability advertisement. It is called each time a capability is built so
// reloads are reflected without further wiring.
func (m *Mesh) SetAgentLister(fn func() []string) {
	m.agentListerMu.Lock()
	defer m.agentListerMu.Unlock()
	m.agentLister = fn
}

// QueryCapability fetches the current capability from a trusted peer.
func (m *Mesh) QueryCapability(ctx context.Context, pid peer.ID) (Capability, error) {
	if !m.isTrusted(pid) {
		return Capability{}, fmt.Errorf("peer %s is not trusted", pid)
	}

	m.publishMeshEvent(runtimeevents.KindMeshCapabilityQueried, map[string]any{
		"peer_id":  pid.String(),
		"outgoing": true,
	})

	capability, err := m.cap.Query(ctx, pid)
	if err != nil {
		return Capability{}, err
	}
	if err := m.verifyCapability(pid, &capability); err != nil {
		return Capability{}, err
	}
	return capability, nil
}

// TrustAndDiscover trusts a peer, eagerly fetches its capability, stores it,
// and advertises the local capability back. It returns the fetched capability.
func (m *Mesh) TrustAndDiscover(ctx context.Context, pid peer.ID) (Capability, error) {
	m.TrustPeer(pid)

	capability, err := m.QueryCapability(ctx, pid)
	if err != nil {
		return Capability{}, err
	}

	m.SetCapability(pid, capability)
	m.advertiseTo(pid)
	return capability, nil
}

// SetCapability stores a capability received from a peer.
func (m *Mesh) SetCapability(pid peer.ID, c Capability) {
	m.capsMu.Lock()
	m.caps[pid] = c
	m.capsMu.Unlock()

	m.publishMeshEvent(runtimeevents.KindMeshCapabilityReceived, map[string]any{
		"peer_id":      pid.String(),
		"models_count": len(c.Models),
		"skills_count": len(c.Skills),
		"agents_count": len(c.Agents),
		"trusted":      m.isTrusted(pid),
	})
}

// PeerCapabilities returns the last known capability for a peer.
func (m *Mesh) PeerCapabilities(pid peer.ID) (Capability, bool) {
	m.capsMu.RLock()
	defer m.capsMu.RUnlock()
	c, ok := m.caps[pid]
	return c, ok
}

// TrustPeer adds a peer to the trust set.
func (m *Mesh) TrustPeer(pid peer.ID) {
	m.trustMu.Lock()
	defer m.trustMu.Unlock()
	m.trust[pid] = true
}

// UntrustPeer removes a peer from the trust set.
func (m *Mesh) UntrustPeer(pid peer.ID) {
	m.trustMu.Lock()
	defer m.trustMu.Unlock()
	delete(m.trust, pid)
}

// isTrusted reports whether a peer is in the local trust set.
func (m *Mesh) isTrusted(pid peer.ID) bool {
	return m.IsTrusted(pid)
}

// IsTrusted reports whether a peer is in the local trust set.
func (m *Mesh) IsTrusted(pid peer.ID) bool {
	m.trustMu.RLock()
	defer m.trustMu.RUnlock()
	return m.trust[pid]
}

// IsConnected reports whether the peer currently has an open connection.
func (m *Mesh) IsConnected(pid peer.ID) bool {
	if m == nil || m.node == nil {
		return false
	}
	return m.node.Host().Network().Connectedness(pid) != libnet.NotConnected
}

// Disconnect closes any open connection to the peer.
func (m *Mesh) Disconnect(pid peer.ID) error {
	if m == nil || m.node == nil {
		return nil
	}
	return m.node.Disconnect(pid)
}

// NetworkStatus returns a combined snapshot of the local mesh/DHT state.
// If m is nil, it returns an empty status (callers should decide how to render).
func (m *Mesh) NetworkStatus(identityPath string) NetworkStatus {
	if m == nil {
		return NetworkStatus{}
	}

	out := NetworkStatus{
		Name:      m.name,
		NodeIndex: m.id.NodeIndex,
		PeerID:    m.id.PeerID,
		Identity:  identityPath,
	}
	if m.node != nil {
		out.PeerID = m.node.PeerID()
		out.Reachability = m.node.ReachabilityString()
		out.Addrs = m.node.BootstrapAddrs()
		if relayed := m.node.RelayedAddrs(); len(relayed) > 0 {
			out.RelayedAddrs = relayed
		}
		peers := m.node.ConnectedPeers()
		out.Peers = make([]PeerStatus, 0, len(peers))
		for _, pid := range peers {
			ps := PeerStatus{PeerID: pid.String()}
			for _, a := range m.node.Host().Peerstore().Addrs(pid) {
				ps.Addrs = append(ps.Addrs, a.String())
			}
			ps.Trusted = m.IsTrusted(pid)
			if capability, ok := m.PeerCapabilities(pid); ok {
				pc := PeerCapability{}
				if len(capability.Models) > 0 {
					pc.Models = capability.Models
				}
				if len(capability.Skills) > 0 {
					pc.Skills = capability.Skills
				}
				if len(capability.Agents) > 0 {
					pc.Agents = capability.Agents
				}
				pc.ActiveTasks = capability.ActiveTasks
				ps.Capability = pc
			}
			out.Peers = append(out.Peers, ps)
		}
		dht := m.node.DHTStatus()
		if dht.Rendezvous != "" {
			out.DHT = &dht
		}
	}
	return out
}

// TrustedPeers returns the list of trusted peer IDs.
func (m *Mesh) TrustedPeers() []peer.ID {
	m.trustMu.RLock()
	defer m.trustMu.RUnlock()
	out := make([]peer.ID, 0, len(m.trust))
	for pid := range m.trust {
		out = append(out, pid)
	}
	return out
}

// ConnectedTrustedPeers returns trusted peers that are currently connected.
func (m *Mesh) ConnectedTrustedPeers() []peer.ID {
	trusted := make(map[peer.ID]bool)
	for _, pid := range m.TrustedPeers() {
		trusted[pid] = true
	}
	out := make([]peer.ID, 0, len(m.node.ConnectedPeers()))
	for _, pid := range m.node.ConnectedPeers() {
		if trusted[pid] {
			out = append(out, pid)
		}
	}
	return out
}

// PickPeer selects a connected, trusted peer able to run agentID for the
// given op ("delegate" or "spawn"). Candidates must have advertised a
// capability manifest listing the agent (or "*") and allowing the op. Peers
// are ranked by connection quality — direct connections beat relayed ones —
// then by fewest advertised active tasks, then by persisted call quality
// scores, with the peer id as a deterministic tiebreak. The returned
// capability is the manifest the decision was based on.
func (m *Mesh) PickPeer(agentID, op string) (peer.ID, Capability, error) {
	candidates := m.rankedCandidates(agentID, op, nil)
	if len(candidates) == 0 {
		return "", Capability{}, fmt.Errorf(
			"no trusted, connected peer advertises agent %q for op %q", agentID, op)
	}
	return candidates[0].PID, candidates[0].Cap, nil
}

// PickPeerRanked returns all connected, trusted, capable peers for the given
// agent and op, sorted from best to worst by the same scoring logic used in
// PickPeer. Peers in exclude are omitted.
func (m *Mesh) PickPeerRanked(agentID, op string, exclude map[peer.ID]bool) []RankedPeer {
	return m.rankedCandidates(agentID, op, exclude)
}

// rankedCandidates builds the list of connected, trusted peers that can serve
// the given agent/op. It filters by advertised capability and sorts by the
// composite routing score. If bootstrapFallback is true and no connected peer
// matches, it attempts to connect to saved bootstrap peers that can serve the
// request and re-evaluates.
func (m *Mesh) rankedCandidates(agentID, op string, exclude map[peer.ID]bool) []RankedPeer {
	var out []RankedPeer
	for _, pid := range m.ConnectedTrustedPeers() {
		if exclude[pid] {
			continue
		}
		c, ok := m.PeerCapabilities(pid)
		if !ok || !capabilityServes(c, agentID, op) {
			continue
		}
		var score int64
		if m.node.Connectedness(pid) == libnet.Connected {
			score += 1 << 20
		}
		score -= int64(c.ActiveTasks)
		if m.scoreStore != nil {
			if sc, ok := m.scoreStore.Get(pid); ok {
				// Score() is a positive quality metric. Scale it down so
				// it does not outweigh the connection/active-tasks signals.
				score += int64(sc.Score() / 10)
			}
		}
		out = append(out, RankedPeer{PID: pid, Cap: c, Score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].PID.String() < out[j].PID.String()
	})
	return out
}

// capabilityServes reports whether a manifest allows the op and lists the
// requested agent. An empty agentID matches any manifest.
func capabilityServes(c Capability, agentID, op string) bool {
	if op != "" {
		if allowed, ok := c.Allows[op]; !ok || !allowed {
			return false
		}
	}
	if agentID == "" {
		return true
	}
	for _, a := range c.Agents {
		if a == agentID || a == "*" {
			return true
		}
	}
	return false
}

func (m *Mesh) verifyRequest(from peer.ID, req agentrpc.Request) error {
	if len(req.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}
	sig := req.Signature
	req.Signature = nil
	defer func() { req.Signature = sig }()

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	pub := m.host.Peerstore().PubKey(from)
	if pub == nil {
		return fmt.Errorf("no public key for peer %s", from)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify request signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid request signature from peer %s", from)
	}
	return nil
}

func newCorrelationID() string {
	now := time.Now()
	return fmt.Sprintf("%d-%d", now.UnixNano(), now.Nanosecond())
}

// signResponse signs the response payload with the local node key.
func (m *Mesh) signResponse(resp *agentrpc.Response) error {
	// Zero out any existing signature so it is not part of the payload.
	resp.Signature = nil
	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	resp.Signature = identity.Sign(m.id.PrivateKey, payload)
	return nil
}

// verifyResponse verifies the response signature with the peer's public key.
func (m *Mesh) verifyResponse(pid peer.ID, resp *agentrpc.Response) error {
	if len(resp.Signature) == 0 {
		return fmt.Errorf("missing response signature")
	}
	sig := resp.Signature
	resp.Signature = nil
	defer func() { resp.Signature = sig }()

	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}

	pub := m.host.Peerstore().PubKey(pid)
	if pub == nil {
		return fmt.Errorf("no public key for peer %s", pid)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify response signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid response signature from peer %s", pid)
	}
	return nil
}

// CapsTransport handles the capability exchange protocol.
type CapsTransport struct {
	host          host.Host
	handler       func(peer.ID, Capability)
	isTrusted     func(peer.ID) bool
	getCapability func() Capability
	onQueried     func(peer.ID, bool)
}

// NewCapsTransport creates a new capability transport.
func NewCapsTransport(h host.Host, handler func(peer.ID, Capability)) *CapsTransport {
	return NewCapsTransportWithPolicy(h, handler, nil, nil, nil)
}

// NewCapsTransportWithPolicy creates a capability transport with an optional
// trust policy for responding to capability queries. If isTrusted or
// getCapability are nil, the transport will not respond to queries.
func NewCapsTransportWithPolicy(
	h host.Host,
	handler func(peer.ID, Capability),
	isTrusted func(peer.ID) bool,
	getCapability func() Capability,
	onQueried func(peer.ID, bool),
) *CapsTransport {
	return &CapsTransport{
		host:          h,
		handler:       handler,
		isTrusted:     isTrusted,
		getCapability: getCapability,
		onQueried:     onQueried,
	}
}

// Start registers the capability protocol and blocks until the context is done.
func (c *CapsTransport) Start(ctx context.Context) error {
	c.host.SetStreamHandler(CapsProtocolID, c.handleStream)
	<-ctx.Done()
	c.host.RemoveStreamHandler(CapsProtocolID)
	return ctx.Err()
}

// waitForPeerProtocol polls until the given peer advertises support for the
// capability protocol. It returns false if the context is canceled or the
// timeout expires.
func (c *CapsTransport) waitForPeerProtocol(ctx context.Context, pid peer.ID, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, p := range c.host.Network().Peers() {
			if p == pid {
				protos, err := c.host.Peerstore().SupportsProtocols(pid, CapsProtocolID)
				if err == nil && len(protos) > 0 {
					return true
				}
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return false
}

// Send pushes a capability manifest to a peer.
func (c *CapsTransport) Send(ctx context.Context, pid peer.ID, capability Capability) error {
	if !c.waitForPeerProtocol(ctx, pid, 5*time.Second) {
		return fmt.Errorf("peer %s does not support %s", pid, CapsProtocolID)
	}

	s, err := c.host.NewStream(ctx, pid, CapsProtocolID)
	if err != nil {
		return fmt.Errorf("open caps stream: %w", err)
	}
	defer s.Close()

	w := bufio.NewWriter(s)
	data, err := json.Marshal(capability)
	if err != nil {
		return fmt.Errorf("encode capability: %w", err)
	}
	if err := stream.WriteFrame(w, capFrameAnnounce, data); err != nil {
		return fmt.Errorf("write capability: %w", err)
	}
	return w.Flush()
}

func (c *CapsTransport) handleStream(s libnet.Stream) {
	defer s.Close()

	r := bufio.NewReader(s)
	typ, payload, err := stream.ReadFrame(r)
	if err != nil {
		return
	}

	switch typ {
	case capFrameAnnounce:
		var capability Capability
		if err := json.Unmarshal(payload, &capability); err != nil {
			return
		}
		c.handler(s.Conn().RemotePeer(), capability)

	case capFrameQuery:
		c.handleQuery(s, r)

	case capFrameResponse:
		// Response frames are read by the client in Query; the server ignores them.
	}
}

func (c *CapsTransport) handleQuery(s libnet.Stream, r *bufio.Reader) {
	remote := s.Conn().RemotePeer()

	if c.isTrusted != nil && !c.isTrusted(remote) {
		if c.onQueried != nil {
			c.onQueried(remote, false)
		}
		// Untrusted peers get no response. The stream will be closed.
		return
	}

	if c.onQueried != nil {
		c.onQueried(remote, true)
	}

	var cap Capability
	if c.getCapability != nil {
		cap = c.getCapability()
	}

	data, err := json.Marshal(cap)
	if err != nil {
		return
	}

	w := bufio.NewWriter(s)
	if err := stream.WriteFrame(w, capFrameResponse, data); err != nil {
		return
	}
	_ = w.Flush()
}

// Query requests a capability from a peer. It blocks until the peer responds
// or the context is canceled. The trust check is the caller's responsibility.
func (c *CapsTransport) Query(ctx context.Context, pid peer.ID) (Capability, error) {
	if !c.waitForPeerProtocol(ctx, pid, 5*time.Second) {
		return Capability{}, fmt.Errorf("peer %s does not support %s", pid, CapsProtocolID)
	}

	s, err := c.host.NewStream(ctx, pid, CapsProtocolID)
	if err != nil {
		return Capability{}, fmt.Errorf("open caps stream: %w", err)
	}
	defer s.Close()

	w := bufio.NewWriter(s)
	if err := stream.WriteFrame(w, capFrameQuery, nil); err != nil {
		return Capability{}, fmt.Errorf("write query: %w", err)
	}
	if err := w.Flush(); err != nil {
		return Capability{}, fmt.Errorf("flush query: %w", err)
	}

	r := bufio.NewReader(s)
	typ, payload, err := stream.ReadFrame(r)
	if err != nil {
		return Capability{}, fmt.Errorf("read response: %w", err)
	}
	if typ != capFrameResponse {
		return Capability{}, fmt.Errorf("unexpected caps frame type: %d", typ)
	}

	var capability Capability
	if err := json.Unmarshal(payload, &capability); err != nil {
		return Capability{}, fmt.Errorf("decode capability: %w", err)
	}
	return capability, nil
}

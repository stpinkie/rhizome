package mesh

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	libnet "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
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
}

// PeerCapability is a minimal view of a peer's capability for status output.
type PeerCapability struct {
	Models []string `json:"models,omitempty"`
	Skills []string `json:"skills,omitempty"`
	Agents []string `json:"agents,omitempty"`
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
	Name      string          `json:"name"`
	NodeIndex uint32          `json:"node_index"`
	PeerID    string          `json:"peer_id"`
	Identity  string          `json:"identity"`
	Peers     []PeerStatus    `json:"peers,omitempty"`
	DHT       *rnet.DHTStatus `json:"dht,omitempty"`
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

	rpc     *agentrpc.Transport
	cap     *CapsTransport
	runFunc func(ctx context.Context, req agentrpc.Request) (*toolshared.ToolResult, error)

	models   []string
	modelsMu sync.RWMutex

	skillsLoader   *skills.SkillsLoader
	skillsLoaderMu sync.RWMutex

	stop     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	eventBus runtimeevents.Bus
	name     string
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
	}
	m.rpc = agentrpc.NewTransport(m.host, m)
	m.cap = NewCapsTransportWithPolicy(
		m.host,
		func(pid peer.ID, c Capability) { m.SetCapability(pid, c) },
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

// Start registers the agent and capability protocol handlers.
func (m *Mesh) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

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

// Stop cleanly shuts down the mesh.
func (m *Mesh) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}
	close(m.stop)
	m.wg.Wait()
	return nil
}

func (m *Mesh) makeErrorResponse(correlationID, message string) (agentrpc.Response, error) {
	resp := agentrpc.Response{
		CorrelationID: correlationID,
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
	if !m.isTrusted(from) {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":          "remote.request",
			"error":          fmt.Sprintf("peer %s is not trusted", from),
			"peer_id":        from.String(),
			"agent_id":       req.TargetAgentID,
			"correlation_id": req.CorrelationID,
		})
		return m.makeErrorResponse(req.CorrelationID, fmt.Sprintf("peer %s is not trusted", from))
	}
	if err := m.verifyRequest(from, req); err != nil {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":          "remote.request",
			"error":          fmt.Sprintf("verify request: %v", err),
			"peer_id":        from.String(),
			"agent_id":       req.TargetAgentID,
			"correlation_id": req.CorrelationID,
		})
		return m.makeErrorResponse(req.CorrelationID, fmt.Sprintf("verify request: %v", err))
	}

	if !m.cfg.AllowRemoteDelegate && !m.cfg.AllowRemoteSpawn {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":          "remote.request",
			"error":          "remote agent execution is disabled",
			"peer_id":        from.String(),
			"agent_id":       req.TargetAgentID,
			"correlation_id": req.CorrelationID,
		})
		return m.makeErrorResponse(req.CorrelationID, "remote agent execution is disabled")
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

	if err != nil {
		resp := agentrpc.Response{
			CorrelationID: req.CorrelationID,
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
		Status:        "ok",
		Result:        result,
	}
	if err := m.signResponse(&resp); err != nil {
		return agentrpc.Response{}, fmt.Errorf("sign response: %w", err)
	}
	return resp, nil
}

// CallRemote sends an agent task to a trusted peer and waits for the result.
// It retries on transient failures and attempts to reconnect to the peer
// between attempts. The async flag is used for event reporting (spawn vs. delegate).
func (m *Mesh) CallRemote(
	ctx context.Context,
	pid peer.ID,
	targetAgentID, prompt string,
	async bool,
) (*toolshared.ToolResult, error) {
	if !m.isTrusted(pid) {
		return nil, fmt.Errorf("peer %s is not trusted", pid)
	}

	req := agentrpc.Request{
		CorrelationID: newCorrelationID(),
		TargetAgentID: targetAgentID,
		SystemPrompt:  prompt,
		Timeout:       m.cfg.RemoteTimeout,
		Async:         async,
	}

	startKind, endKind := m.remoteAgentEventKinds(async)
	m.publishMeshEvent(startKind, map[string]any{
		"peer_id":        pid.String(),
		"agent_id":       targetAgentID,
		"correlation_id": req.CorrelationID,
		"async":          async,
	})
	defer func() {
		m.publishMeshEvent(endKind, map[string]any{
			"peer_id":        pid.String(),
			"agent_id":       targetAgentID,
			"correlation_id": req.CorrelationID,
			"async":          async,
		})
	}()

	// Sign the request payload with the local node key.
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req.Signature = identity.Sign(m.id.PrivateKey, payload)

	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			m.node.ForceReconnect(ctx, pid)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}

		resp, err := m.rpc.Call(ctx, pid, req)
		if err == nil {
			if verifyErr := m.verifyResponse(pid, &resp); verifyErr != nil {
				return nil, fmt.Errorf("verify response: %w", verifyErr)
			}
			if resp.Status != "ok" {
				return nil, fmt.Errorf("remote agent failed: %s", resp.Error)
			}
			return resp.Result, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("call remote after %d attempts: %w", maxAttempts, lastErr)
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

	// Always advertise the default agent id 'main'.
	c.Agents = append(c.Agents, "main")
	return c
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

	return m.cap.Query(ctx, pid)
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
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), time.Now().Nanosecond())
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

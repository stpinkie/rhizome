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

	stop   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
	m.cap = NewCapsTransport(m.host, func(pid peer.ID, c Capability) { m.SetCapability(pid, c) })
	return m
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
		return m.makeErrorResponse(req.CorrelationID, fmt.Sprintf("peer %s is not trusted", from))
	}
	if err := m.verifyRequest(from, req); err != nil {
		return m.makeErrorResponse(req.CorrelationID, fmt.Sprintf("verify request: %v", err))
	}

	if !m.cfg.AllowRemoteDelegate && !m.cfg.AllowRemoteSpawn {
		return m.makeErrorResponse(req.CorrelationID, "remote agent execution is disabled")
	}

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
// between attempts.
func (m *Mesh) CallRemote(
	ctx context.Context,
	pid peer.ID,
	targetAgentID, prompt string,
) (*toolshared.ToolResult, error) {
	if !m.isTrusted(pid) {
		return nil, fmt.Errorf("peer %s is not trusted", pid)
	}

	req := agentrpc.Request{
		CorrelationID: newCorrelationID(),
		TargetAgentID: targetAgentID,
		SystemPrompt:  prompt,
		Timeout:       m.cfg.RemoteTimeout,
	}

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

// SetCapability stores a capability received from a peer.
func (m *Mesh) SetCapability(pid peer.ID, c Capability) {
	m.capsMu.Lock()
	defer m.capsMu.Unlock()
	m.caps[pid] = c
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
	m.trustMu.RLock()
	defer m.trustMu.RUnlock()
	return m.trust[pid]
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
	host    host.Host
	handler func(peer.ID, Capability)
}

// NewCapsTransport creates a new capability transport.
func NewCapsTransport(h host.Host, handler func(peer.ID, Capability)) *CapsTransport {
	return &CapsTransport{host: h, handler: handler}
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
	if typ != capFrameAnnounce {
		return
	}

	var capability Capability
	if err := json.Unmarshal(payload, &capability); err != nil {
		return
	}
	c.handler(s.Conn().RemotePeer(), capability)
}

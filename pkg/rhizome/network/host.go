package network

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	libnet "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	quic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"

	rhizomeconfig "github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/logger"
)

var defaultNetworkTimeouts = rhizomeconfig.DefaultTimeouts().Network

// Node wraps a libp2p host for Rhizome.
type Node struct {
	host            host.Host
	ping            *ping.PingService
	mdns            mdns.Service
	dht             *Discovery
	peers           map[peer.ID]peer.AddrInfo
	mu              sync.RWMutex
	notifiee        *PeerNotifiee
	timeouts        *rhizomeconfig.NetworkTimeouts
	reconnectTick   *time.Ticker
	reconnectWg     sync.WaitGroup
	reconnectCtx    context.Context
	reconnectCancel context.CancelFunc
	eventBus        runtimeevents.Bus

	reachability atomic.Int32 // stores libnet.Reachability
	relayAddrs   []string
	relayAddrsMu sync.RWMutex
	eventSubs    []event.Subscription
	relaySource  *relayPeerSource
}

// Config is the static configuration for a Rhizome network node.
type Config struct {
	// ListenAddrs are multiaddrs to listen on. "0" ports mean auto-assign.
	ListenAddrs []string

	// BootstrapPeers are multiaddrs (with /p2p/<peer-id>) of known peers.
	BootstrapPeers []string

	// DHT controls public DHT discovery.
	DHT DHTConfig

	// Timeouts override the built-in defaults. A nil value means use defaults.
	Timeouts *rhizomeconfig.NetworkTimeouts

	// NATTraversal enables the circuit-relay client transport, DCUtR hole
	// punching, AutoNATv2 reachability detection, and AutoRelay reservations.
	NATTraversal bool

	// RelayService runs a circuit relay v2 service when the node detects it
	// is publicly reachable, letting it relay traffic for NAT'd peers.
	RelayService bool

	// NATService provides the AutoNAT dial-back service to other peers when
	// the node is publicly reachable.
	NATService bool

	// StaticRelays are extra relay multiaddrs (with /p2p/<peer-id>) used as
	// AutoRelay candidates in addition to discovered mesh peers.
	StaticRelays []string

	// ForceReachability overrides reachability detection: "public" or "private".
	ForceReachability string

	// PublicAddrs are extra multiaddrs advertised to peers (e.g. a static
	// public endpoint behind port-forwarding).
	PublicAddrs []string
}

// SetEventBus sets the runtime event bus used to publish mesh events.
func (n *Node) SetEventBus(bus runtimeevents.Bus) {
	n.eventBus = bus
	if n.dht != nil {
		n.dht.SetEventBus(bus)
	}
}

// publishMeshEvent publishes a non-blocking mesh runtime event if a bus is configured.
func (n *Node) publishMeshEvent(kind runtimeevents.Kind, attrs map[string]any) {
	if n.eventBus == nil {
		return
	}
	severity := runtimeevents.SeverityInfo
	if kind == runtimeevents.KindMeshError {
		severity = runtimeevents.SeverityError
	}
	n.eventBus.PublishNonBlocking(runtimeevents.Event{
		Kind:     kind,
		Severity: severity,
		Source: runtimeevents.Source{
			Component: "network",
		},
		Attrs: attrs,
	})
}

// NewNode creates a libp2p host and starts mDNS discovery.
func NewNode(ctx context.Context, priv crypto.PrivKey, cfg Config) (*Node, error) {
	addrs := cfg.ListenAddrs
	if len(addrs) == 0 {
		addrs = []string{"/ip4/0.0.0.0/tcp/0"}
	}

	// Transports are configured explicitly rather than via
	// libp2p.DefaultTransports so that WebTransport and WebRTC (which need
	// UDP features and network interface enumeration unavailable on Android
	// and pre-3.9 kernels) are never required. TCP uses DisableReuseport
	// because SO_REUSEPORT only exists on Linux 3.9+.
	baseOpts := []libp2p.Option{
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(addrs...),
		libp2p.Transport(tcp.NewTCPTransport, tcp.DisableReuseport()),
		libp2p.NATPortMap(),
	}

	// relaySource is late-bound: AutoRelay calls it after the host (and Node)
	// exist, so it can feed candidates from the live peer table.
	relaySource := &relayPeerSource{}

	if cfg.NATTraversal {
		// EnableRelay wires the /p2p-circuit transport (accept inbound via a
		// relay, dial out through one). EnableHolePunching upgrades relayed
		// connections to direct ones where the NAT allows it. EnableAutoNATv2
		// measures our own reachability.
		baseOpts = append(baseOpts,
			libp2p.EnableRelay(),
			libp2p.EnableHolePunching(),
			libp2p.EnableAutoNATv2(),
		)

		var staticRelays []peer.AddrInfo
		if len(cfg.StaticRelays) > 0 {
			relayMaddrs, err := MultiaddrStrings(cfg.StaticRelays)
			if err != nil {
				return nil, fmt.Errorf("parse static_relays: %w", err)
			}
			staticRelays, err = peer.AddrInfosFromP2pAddrs(relayMaddrs...)
			if err != nil {
				return nil, fmt.Errorf("parse static_relays: %w", err)
			}
		}
		relaySource.static = staticRelays
		// libp2p's autorelay defaults (4 min candidates, 3m boot delay, 30s
		// query interval) are tuned for the public IPFS fleet. Rhizome meshes
		// are small, so reserve a relay slot as soon as one candidate is found.
		baseOpts = append(baseOpts, libp2p.EnableAutoRelayWithPeerSource(
			relaySource.peers,
			autorelay.WithMinCandidates(1),
			autorelay.WithBootDelay(5*time.Second),
			autorelay.WithMinInterval(15*time.Second),
		))
	}

	// Both services only run when the node detects it is publicly reachable,
	// so they are safe to leave enabled on NAT'd nodes.
	if cfg.RelayService {
		baseOpts = append(baseOpts, libp2p.EnableRelayService())
	}
	if cfg.NATService {
		baseOpts = append(baseOpts, libp2p.EnableNATService())
	}

	switch strings.ToLower(strings.TrimSpace(cfg.ForceReachability)) {
	case "public":
		baseOpts = append(baseOpts, libp2p.ForceReachabilityPublic())
	case "private":
		baseOpts = append(baseOpts, libp2p.ForceReachabilityPrivate())
	}

	if len(cfg.PublicAddrs) > 0 {
		extra, err := MultiaddrStrings(cfg.PublicAddrs)
		if err != nil {
			return nil, fmt.Errorf("parse public_addrs: %w", err)
		}
		baseOpts = append(baseOpts, libp2p.AddrsFactory(func(addrs []multiaddr.Multiaddr) []multiaddr.Multiaddr {
			return append(addrs, extra...)
		}))
	}

	h, err := libp2p.New(append(baseOpts, libp2p.Transport(quic.NewTransport))...)
	if err != nil {
		// QUIC needs UDP socket options (IP_PKTINFO, ECN) that old kernels and
		// restricted Android sandboxes may reject. Fall back to TCP-only so the
		// mesh still works instead of failing startup outright.
		logger.WarnCF(
			"network",
			"QUIC transport unavailable; falling back to TCP-only",
			map[string]any{"error": err.Error()},
		)
		h, err = libp2p.New(baseOpts...)
		if err != nil {
			return nil, fmt.Errorf("create libp2p host: %w", err)
		}
	}

	// Wait briefly for at least one listener to be ready.
	timeouts := cfg.Timeouts
	if timeouts == nil {
		timeouts = &defaultNetworkTimeouts
	}
	listenerReady := timeouts.ListenerReady.Duration()
	if listenerReady <= 0 {
		listenerReady = 2 * time.Second
	}
	ready := time.Now()
	for time.Since(ready) < listenerReady && len(h.Addrs()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	n := &Node{
		host:          h,
		ping:          ping.NewPingService(h),
		peers:         make(map[peer.ID]peer.AddrInfo),
		notifiee:      NewPeerNotifiee(),
		timeouts:      timeouts,
		reconnectTick: time.NewTicker(10 * time.Second),
	}
	n.reconnectCtx, n.reconnectCancel = context.WithCancel(ctx)
	h.Network().Notify(n.notifiee)
	n.reconnectWg.Add(1)
	go n.reconnectLoop(n.reconnectCtx)

	n.notifiee.OnConnected(func(ev PeerEvent) {
		n.publishMeshEvent(runtimeevents.KindMeshPeerConnected, map[string]any{
			"peer_id": ev.PeerID.String(),
			"addr":    ev.Addr.String(),
		})
	})
	n.notifiee.OnDisconnected(func(ev PeerEvent) {
		n.publishMeshEvent(runtimeevents.KindMeshPeerDisconnected, map[string]any{
			"peer_id": ev.PeerID.String(),
			"addr":    ev.Addr.String(),
		})
	})

	relaySource.node.Store(n)
	n.relaySource = relaySource
	n.reachability.Store(int32(libnet.ReachabilityUnknown))
	n.watchHostEvents(n.reconnectCtx)

	// mDNS LAN discovery.
	n.mdns = mdns.NewMdnsService(h, "_rhizome._p2p", n)
	if err := n.mdns.Start(); err != nil {
		logger.WarnCF(
			"network",
			"mDNS discovery failed; continuing without local multicast discovery",
			map[string]any{"error": err.Error()},
		)
		_ = n.mdns.Close()
		n.mdns = nil
	}

	// Connect to bootstrap peers with a small amount of retry/backoff.
	// A bootstrap peer may still be starting when we dial it.
	bootstrapAttempts := timeouts.BootstrapAttempts
	if bootstrapAttempts <= 0 {
		bootstrapAttempts = 3
	}
	bootstrapBackoff := timeouts.BootstrapBackoff.Duration()
	if bootstrapBackoff <= 0 {
		bootstrapBackoff = 250 * time.Millisecond
	}
	for _, a := range cfg.BootstrapPeers {
		if err := n.connectAddrWithRetry(ctx, a, bootstrapAttempts, bootstrapBackoff); err != nil {
			// Log but do not fail startup because a bootstrap may be offline.
		}
	}

	// Public DHT discovery.
	if cfg.DHT.Enabled {
		if err := cfg.DHT.Validate(); err != nil {
			return nil, err
		}
		dhtCfg := cfg.DHT
		if dhtCfg.ReprovideInterval <= 0 {
			dhtCfg.ReprovideInterval = timeouts.DHTReprovideInterval.Duration()
			if dhtCfg.ReprovideInterval <= 0 {
				dhtCfg.ReprovideInterval = 10 * time.Minute
			}
		}
		if dhtCfg.DialTimeout <= 0 {
			dhtCfg.DialTimeout = timeouts.DHTDial.Duration()
			if dhtCfg.DialTimeout <= 0 {
				dhtCfg.DialTimeout = 15 * time.Second
			}
		}
		if dhtCfg.QueryTimeout <= 0 {
			dhtCfg.QueryTimeout = timeouts.DHTQuery.Duration()
			if dhtCfg.QueryTimeout <= 0 {
				dhtCfg.QueryTimeout = 60 * time.Second
			}
		}
		if dhtCfg.RetryInterval <= 0 {
			dhtCfg.RetryInterval = timeouts.DHTRetry.Duration()
			if dhtCfg.RetryInterval <= 0 {
				dhtCfg.RetryInterval = 5 * time.Second
			}
		}
		d, err := NewDiscovery(h, dhtCfg)
		if err != nil {
			_ = h.Close()
			return nil, fmt.Errorf("create dht discovery: %w", err)
		}
		d.OnFound = n.addPeer
		n.dht = d
		if err := d.Start(ctx); err != nil {
			_ = h.Close()
			return nil, fmt.Errorf("start dht: %w", err)
		}
	}

	return n, nil
}

func (n *Node) connectAddrWithRetry(ctx context.Context, addr string, maxAttempts int, backoff time.Duration) error {
	var lastErr error
	for i := 0; i < maxAttempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
		if err := n.connectAddr(ctx, addr); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("bootstrap %s after %d attempts: %w", addr, maxAttempts, lastErr)
}

// connectAddr parses and dials a multiaddr that may include a /p2p/ peer id.
func (n *Node) connectAddr(ctx context.Context, addr string) error {
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return fmt.Errorf("parse multiaddr %q: %w", addr, err)
	}

	addrInfo, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("extract peer info from %q: %w", addr, err)
	}

	if err := n.host.Connect(ctx, *addrInfo); err != nil {
		return fmt.Errorf("connect to %q: %w", addr, err)
	}
	n.addPeer(*addrInfo)
	return nil
}

// HandlePeerFound implements mdns.Notifee and is called when a peer is
// discovered on the local network.
func (n *Node) HandlePeerFound(pi peer.AddrInfo) {
	if err := n.host.Connect(context.Background(), pi); err != nil {
		return
	}
	n.addPeer(pi)
}

func (n *Node) addPeer(pi peer.AddrInfo) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.peers[pi.ID] = pi
}

// relayPeerSource feeds AutoRelay candidates: configured static relays plus
// the node's known peers (trusted/bootstrap/DHT/mDNS discovered). AutoRelay
// filters out candidates that do not run the circuit relay v2 hop protocol.
type relayPeerSource struct {
	static []peer.AddrInfo
	node   atomic.Pointer[Node]
}

func (r *relayPeerSource) peers(ctx context.Context, _ int) <-chan peer.AddrInfo {
	out := make(chan peer.AddrInfo)
	go func() {
		defer close(out)
		seen := make(map[peer.ID]struct{})
		send := func(ai peer.AddrInfo) bool {
			if ai.ID == "" {
				return true
			}
			if _, dup := seen[ai.ID]; dup {
				return true
			}
			seen[ai.ID] = struct{}{}
			select {
			case out <- ai:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var selfID peer.ID
		if n := r.node.Load(); n != nil {
			selfID = n.ID()
		}
		for _, ai := range r.static {
			if ai.ID == selfID {
				continue
			}
			if !send(ai) {
				return
			}
		}
		if n := r.node.Load(); n != nil {
			for _, ai := range n.Peers() {
				if ai.ID == selfID {
					continue
				}
				if !send(ai) {
					return
				}
			}
		}
	}()
	return out
}

// watchHostEvents subscribes to the host event bus for reachability and
// advertised-address changes, keeping Node state and mesh events current.
func (n *Node) watchHostEvents(ctx context.Context) {
	reachSub, err := n.host.EventBus().Subscribe(new(event.EvtLocalReachabilityChanged))
	if err != nil {
		logger.WarnCF("network", "failed to subscribe to reachability events", map[string]any{"error": err.Error()})
	} else {
		n.eventSubs = append(n.eventSubs, reachSub)
	}
	addrSub, err := n.host.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
	if err != nil {
		logger.WarnCF("network", "failed to subscribe to address events", map[string]any{"error": err.Error()})
	} else {
		n.eventSubs = append(n.eventSubs, addrSub)
	}
	if reachSub == nil && addrSub == nil {
		return
	}

	n.reconnectWg.Add(1)
	go func() {
		defer n.reconnectWg.Done()
		var reachCh <-chan any
		if reachSub != nil {
			reachCh = reachSub.Out()
		}
		var addrCh <-chan any
		if addrSub != nil {
			addrCh = addrSub.Out()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-reachCh:
				if !ok {
					reachCh = nil
					continue
				}
				if e, ok := ev.(event.EvtLocalReachabilityChanged); ok {
					n.setReachability(e.Reachability)
				}
			case _, ok := <-addrCh:
				if !ok {
					addrCh = nil
					continue
				}
				n.refreshRelayedAddrs()
			}
		}
	}()
}

func (n *Node) setReachability(r libnet.Reachability) {
	prev := libnet.Reachability(n.reachability.Swap(int32(r)))
	if prev == r {
		return
	}
	n.publishMeshEvent(runtimeevents.KindMeshReachabilityChanged, map[string]any{
		"reachability": n.ReachabilityString(),
		"previous":     prev.String(),
	})
}

// refreshRelayedAddrs recomputes the /p2p-circuit addrs this node advertises
// and emits an event when the set changes (relay reservation gained/lost).
func (n *Node) refreshRelayedAddrs() {
	var current []string
	for _, a := range n.host.Addrs() {
		if strings.Contains(a.String(), "/p2p-circuit") {
			current = append(current, fmt.Sprintf("%s/p2p/%s", a.String(), n.host.ID().String()))
		}
	}
	n.relayAddrsMu.Lock()
	changed := !equalStringsUnordered(current, n.relayAddrs)
	if changed {
		n.relayAddrs = current
	}
	n.relayAddrsMu.Unlock()
	if changed {
		n.publishMeshEvent(runtimeevents.KindMeshRelayReservation, map[string]any{
			"relay_addrs": current,
		})
	}
}

func equalStringsUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

// Reachability returns the node's last detected NAT reachability.
func (n *Node) Reachability() libnet.Reachability {
	return libnet.Reachability(n.reachability.Load())
}

// ReachabilityString returns the node's reachability as a display string.
func (n *Node) ReachabilityString() string {
	return n.Reachability().String()
}

// RelayedAddrs returns the /p2p-circuit multiaddrs (with /p2p/<peer-id>)
// currently advertised by this node, one per relay reservation.
func (n *Node) RelayedAddrs() []string {
	n.relayAddrsMu.RLock()
	defer n.relayAddrsMu.RUnlock()
	return append([]string(nil), n.relayAddrs...)
}

// OnConnected registers a callback invoked when a peer connects.
func (n *Node) OnConnected(fn func(PeerEvent)) {
	n.notifiee.OnConnected(fn)
}

// OnDisconnected registers a callback invoked when a peer disconnects.
func (n *Node) OnDisconnected(fn func(PeerEvent)) {
	n.notifiee.OnDisconnected(fn)
}

// Connectedness returns the current connectedness state of a peer.
func (n *Node) Connectedness(pid peer.ID) libnet.Connectedness {
	return n.host.Network().Connectedness(pid)
}

// IsConnectednessUp reports whether the given connectedness means we have a
// usable connection: Connected for direct links or Limited for transient
// (e.g. relayed) links that can still carry streams.
func IsConnectednessUp(c libnet.Connectedness) bool {
	return c == libnet.Connected || c == libnet.Limited
}

func (n *Node) timeoutsOrDefault() *rhizomeconfig.NetworkTimeouts {
	if n.timeouts != nil {
		return n.timeouts
	}
	return &defaultNetworkTimeouts
}

// PeerID returns this node's libp2p peer id.
func (n *Node) PeerID() string {
	return n.host.ID().String()
}

// ID returns this node's libp2p peer id as a peer.ID.
func (n *Node) ID() peer.ID {
	return n.host.ID()
}

// Host returns the underlying libp2p host.
func (n *Node) Host() host.Host {
	return n.host
}

// DHTStatus returns the DHT status snapshot, or an empty status if DHT is disabled.
func (n *Node) DHTStatus() DHTStatus {
	if n.dht == nil {
		return DHTStatus{}
	}
	return n.dht.Status()
}

// ConnectedPeers returns the peer IDs of currently connected peers.
func (n *Node) ConnectedPeers() []peer.ID {
	return n.host.Network().Peers()
}

// Disconnect closes any open connection to the peer.
func (n *Node) Disconnect(pid peer.ID) error {
	if n == nil || n.host == nil {
		return nil
	}
	return n.host.Network().ClosePeer(pid)
}

// Addrs returns the listen addresses of this node as strings.
func (n *Node) Addrs() []string {
	out := make([]string, 0, len(n.host.Addrs()))
	for _, a := range n.host.Addrs() {
		out = append(out, a.String())
	}
	return out
}

// BootstrapAddrs returns multiaddrs that include the /p2p/<peer-id> suffix
// and can be used by another node to bootstrap to this one.
func (n *Node) BootstrapAddrs() []string {
	out := make([]string, 0, len(n.host.Addrs()))
	for _, a := range n.host.Addrs() {
		out = append(out, fmt.Sprintf("%s/p2p/%s", a.String(), n.host.ID().String()))
	}
	return out
}

// Peers returns the currently known peer AddrInfo values.
func (n *Node) Peers() []peer.AddrInfo {
	n.mu.RLock()
	defer n.mu.RUnlock()

	out := make([]peer.AddrInfo, 0, len(n.peers))
	for _, pi := range n.peers {
		out = append(out, pi)
	}
	return out
}

// Ping sends libp2p ping messages to the given peer and returns the first
// round-trip time. The peer must be connected or discoverable.
func (n *Node) Ping(ctx context.Context, peerID string, timeout time.Duration) (time.Duration, error) {
	pid, err := peer.Decode(peerID)
	if err != nil {
		return 0, fmt.Errorf("decode peer id: %w", err)
	}

	if err := n.host.Connect(ctx, n.host.Network().Peerstore().PeerInfo(pid)); err != nil {
		return 0, fmt.Errorf("connect to peer: %w", err)
	}

	if timeout <= 0 {
		timeout = n.timeoutsOrDefault().Ping.Duration()
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch := ping.Ping(ctx, n.host, pid)
	select {
	case res := <-ch:
		if res.Error != nil {
			return 0, res.Error
		}
		return res.RTT, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// Connect dials a peer by its multiaddr (with /p2p/<peer-id> suffix).
func (n *Node) Connect(ctx context.Context, addr string) error {
	return n.connectAddr(ctx, addr)
}

// Close shuts down the libp2p host.
func (n *Node) Close() error {
	if n.reconnectCancel != nil {
		n.reconnectCancel()
	}
	if n.reconnectTick != nil {
		n.reconnectTick.Stop()
	}
	for _, sub := range n.eventSubs {
		_ = sub.Close()
	}
	n.reconnectWg.Wait()

	if n.dht != nil {
		_ = n.dht.Stop()
	}
	if n.mdns != nil {
		_ = n.mdns.Close()
	}
	if n.notifiee != nil {
		n.host.Network().StopNotify(n.notifiee)
	}
	return n.host.Close()
}

// MultiaddrStrings parses a slice of multiaddr strings and validates them.
func MultiaddrStrings(addrs []string) ([]multiaddr.Multiaddr, error) {
	var out []multiaddr.Multiaddr
	for _, a := range addrs {
		m, err := multiaddr.NewMultiaddr(strings.TrimSpace(a))
		if err != nil {
			return nil, fmt.Errorf("invalid multiaddr %q: %w", a, err)
		}
		out = append(out, m)
	}
	return out, nil
}

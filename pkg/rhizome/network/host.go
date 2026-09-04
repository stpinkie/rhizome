package network

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	libnet "github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
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

	h, err := libp2p.New(append(baseOpts, libp2p.Transport(quic.NewTransport))...)
	if err != nil {
		// QUIC needs UDP socket options (IP_PKTINFO, ECN) that old kernels and
		// restricted Android sandboxes may reject. Fall back to TCP-only so the
		// mesh still works instead of failing startup outright.
		logger.WarnCF("network", "QUIC transport unavailable; falling back to TCP-only", map[string]any{"error": err.Error()})
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

	// mDNS LAN discovery.
	n.mdns = mdns.NewMdnsService(h, "_rhizome._p2p", n)
	if err := n.mdns.Start(); err != nil {
		logger.WarnCF("network", "mDNS discovery failed; continuing without local multicast discovery", map[string]any{"error": err.Error()})
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

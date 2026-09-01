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
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/protocol/ping"
	quic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// Node wraps a libp2p host for Rhizome.
type Node struct {
	host  host.Host
	ping  *ping.PingService
	mdns  mdns.Service
	dht   *Discovery
	peers map[peer.ID]peer.AddrInfo
	mu    sync.RWMutex
}

// Config is the static configuration for a Rhizome network node.
type Config struct {
	// ListenAddrs are multiaddrs to listen on. "0" ports mean auto-assign.
	ListenAddrs []string

	// BootstrapPeers are multiaddrs (with /p2p/<peer-id>) of known peers.
	BootstrapPeers []string

	// DHT controls public DHT discovery.
	DHT DHTConfig
}

// NewNode creates a libp2p host and starts mDNS discovery.
func NewNode(ctx context.Context, priv crypto.PrivKey, cfg Config) (*Node, error) {
	addrs := cfg.ListenAddrs
	if len(addrs) == 0 {
		addrs = []string{"/ip4/0.0.0.0/tcp/0"}
	}

	h, err := libp2p.New(
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(addrs...),
		libp2p.Transport(tcp.NewTCPTransport, tcp.DisableReuseport()),
		libp2p.Transport(quic.NewTransport),
		libp2p.NATPortMap(),
	)
	if err != nil {
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}

	// Wait briefly for at least one listener to be ready.
	ready := time.Now()
	for time.Since(ready) < 2*time.Second && len(h.Addrs()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	n := &Node{
		host:  h,
		ping:  ping.NewPingService(h),
		peers: make(map[peer.ID]peer.AddrInfo),
	}

	// mDNS LAN discovery.
	n.mdns = mdns.NewMdnsService(h, "_rhizome._p2p", n)
	if err := n.mdns.Start(); err != nil {
		logger.WarnCF("network", "mDNS discovery failed; continuing without local multicast discovery", map[string]any{"error": err.Error()})
		n.mdns = nil
	}

	// Connect to bootstrap peers with a small amount of retry/backoff.
	// A bootstrap peer may still be starting when we dial it.
	for _, a := range cfg.BootstrapPeers {
		if err := n.connectAddrWithRetry(ctx, a, 3, 250*time.Millisecond); err != nil {
			// Log but do not fail startup because a bootstrap may be offline.
		}
	}

	// Public DHT discovery.
	if cfg.DHT.Enabled {
		d, err := NewDiscovery(h, cfg.DHT)
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

// ConnectedPeers returns the peer IDs of currently connected peers.
func (n *Node) ConnectedPeers() []peer.ID {
	return n.host.Network().Peers()
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
	if n.dht != nil {
		_ = n.dht.Stop()
	}
	if n.mdns != nil {
		_ = n.mdns.Close()
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

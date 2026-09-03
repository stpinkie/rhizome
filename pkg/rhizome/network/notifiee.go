package network

import (
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/stpinkie/rhizome/pkg/logger"
)

// PeerEvent describes a connect/disconnect event for a peer.
type PeerEvent struct {
	PeerID    peer.ID
	Connected bool
	Addr      multiaddr.Multiaddr
}

// PeerNotifiee implements network.Notifiee and forwards Connected/Disconnected
// events to a Node.
type PeerNotifiee struct {
	mu             sync.RWMutex
	onConnected    []func(PeerEvent)
	onDisconnected []func(PeerEvent)
}

// NewPeerNotifiee creates a notifiee that can be registered with a libp2p host.
func NewPeerNotifiee() *PeerNotifiee {
	return &PeerNotifiee{}
}

func (n *PeerNotifiee) Listen(network.Network, multiaddr.Multiaddr)      {}
func (n *PeerNotifiee) ListenClose(network.Network, multiaddr.Multiaddr) {}

func (n *PeerNotifiee) Connected(_ network.Network, c network.Conn) {
	n.mu.RLock()
	callbacks := n.onConnected
	n.mu.RUnlock()

	ev := PeerEvent{PeerID: c.RemotePeer(), Connected: true, Addr: c.RemoteMultiaddr()}
	for _, fn := range callbacks {
		fn(ev)
	}
}

func (n *PeerNotifiee) Disconnected(_ network.Network, c network.Conn) {
	n.mu.RLock()
	callbacks := n.onDisconnected
	n.mu.RUnlock()

	ev := PeerEvent{PeerID: c.RemotePeer(), Connected: false, Addr: c.RemoteMultiaddr()}
	for _, fn := range callbacks {
		fn(ev)
	}
}

// OnConnected registers a callback for peer connect events.
func (n *PeerNotifiee) OnConnected(fn func(PeerEvent)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onConnected = append(n.onConnected, fn)
}

// OnDisconnected registers a callback for peer disconnect events.
func (n *PeerNotifiee) OnDisconnected(fn func(PeerEvent)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.onDisconnected = append(n.onDisconnected, fn)
}

// reconnectLoop watches the address book and attempts to re-dial peers that
// are known but not currently connected. It should be started in its own
// goroutine.
func (n *Node) reconnectLoop(ctx context.Context) {
	defer n.reconnectWg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-n.reconnectTick.C:
		}

		n.reconnectOnce(ctx)
	}
}

func (n *Node) reconnectOnce(ctx context.Context) {
	timeouts := n.timeoutsOrDefault()

	n.mu.RLock()
	peers := make([]peer.AddrInfo, 0, len(n.peers))
	for _, pi := range n.peers {
		peers = append(peers, pi)
	}
	n.mu.RUnlock()

	for _, pi := range peers {
		if n.Connectedness(pi.ID) == network.Connected {
			continue
		}

		backoff := timeouts.BootstrapBackoff.Duration()
		for i := 0; i < timeouts.BootstrapAttempts; i++ {
			if i > 0 {
				time.Sleep(backoff)
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}

			if err := n.host.Connect(ctx, pi); err == nil {
				break
			} else if ctx.Err() != nil {
				return
			}
		}
	}
}

// ForceReconnect triggers an immediate reconnection attempt for a peer.
func (n *Node) ForceReconnect(ctx context.Context, pid peer.ID) {
	n.mu.RLock()
	pi, ok := n.peers[pid]
	n.mu.RUnlock()
	if !ok {
		pi = n.host.Network().Peerstore().PeerInfo(pid)
	}

	timeouts := n.timeoutsOrDefault()
	for i := 0; i < timeouts.BootstrapAttempts; i++ {
		if err := n.host.Connect(ctx, pi); err == nil {
			return
		}
		if i < timeouts.BootstrapAttempts-1 {
			time.Sleep(timeouts.BootstrapBackoff.Duration())
		}
		if ctx.Err() != nil {
			return
		}
	}

	logger.WarnCF("network", "force reconnect failed", map[string]any{"peer": pid.String()})
}

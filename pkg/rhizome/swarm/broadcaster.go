package swarm

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/libp2p/go-libp2p/core/peer"
)

// atomic is used for sent/failed counters in Publish.

// Broadcaster fans a signed envelope out to a swarm's members and delivers
// inbound broadcast envelopes to local subscribers. It is the seam that lets
// the GossipSub pub/sub backend replace the direct per-member stream fan-out
// without changing swarm consumers.
type Broadcaster interface {
	// Publish sends env to every known member of env.SwarmID. It is
	// best-effort: it returns nil when the envelope reached at least one
	// member or the swarm has no other members.
	Publish(ctx context.Context, env Envelope) error
	// Subscribe registers a local consumer for broadcast envelopes in the
	// given swarm. The returned cancel func detaches the channel.
	Subscribe(swarmID string) (<-chan Envelope, func())
}

// broadcaster is the internal view: a Broadcaster plus the join hook used by
// backends that need per-swarm setup (GossipSub topic subscription).
type broadcaster interface {
	Broadcaster
	ensureTopic(swarmID string)
}

// localBus fans envelopes out to local subscribers. Both transport backends
// share it so subscriptions survive a backend swap.
type localBus struct {
	mu   sync.Mutex
	subs map[string]map[chan Envelope]struct{}
}

func newLocalBus() *localBus {
	return &localBus{subs: make(map[string]map[chan Envelope]struct{})}
}

// Subscribe registers a buffered channel that receives broadcast envelopes
// for the swarm.
func (b *localBus) Subscribe(swarmID string) (<-chan Envelope, func()) {
	ch := make(chan Envelope, 16)
	b.mu.Lock()
	if b.subs[swarmID] == nil {
		b.subs[swarmID] = make(map[chan Envelope]struct{})
	}
	b.subs[swarmID][ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if set, ok := b.subs[swarmID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(b.subs, swarmID)
			}
		}
		close(ch)
	}
	return ch, cancel
}

// deliver fans an envelope out to local subscribers. Delivery is
// non-blocking: a slow subscriber drops envelopes rather than stalling the
// protocol handler.
func (b *localBus) deliver(env Envelope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[env.SwarmID] {
		select {
		case ch <- env:
		default:
		}
	}
}

// directBroadcaster fans envelopes out over per-member streams.
type directBroadcaster struct {
	sw *Swarm
	*localBus
}

func newDirectBroadcaster(sw *Swarm, bus *localBus) *directBroadcaster {
	return &directBroadcaster{sw: sw, localBus: bus}
}

// ensureTopic is a no-op for the direct backend — fan-out needs no
// per-swarm setup.
func (b *directBroadcaster) ensureTopic(string) {}

// Publish sends env to every known member of env.SwarmID except the local
// peer, concurrently. Members that are offline or fail the send are skipped;
// Publish only returns an error when the swarm has members and every send
// failed. Concurrent fan-out keeps latency bounded by the slowest single
// peer rather than the sum of all peers.
func (b *directBroadcaster) Publish(ctx context.Context, env Envelope) error {
	members := b.sw.memberPeers(env.SwarmID)
	self := b.sw.host.ID()
	var sent, failed int64
	var errMu sync.Mutex
	var lastErr error
	var wg sync.WaitGroup
	for _, pid := range members {
		if pid == self {
			continue
		}
		wg.Add(1)
		go func(pid peer.ID) {
			defer wg.Done()
			if err := b.sw.transport.Push(ctx, pid, env); err != nil {
				atomic.AddInt64(&failed, 1)
				errMu.Lock()
				lastErr = err
				errMu.Unlock()
			} else {
				atomic.AddInt64(&sent, 1)
			}
		}(pid)
	}
	wg.Wait()
	if atomic.LoadInt64(&sent) == 0 && atomic.LoadInt64(&failed) > 0 {
		errMu.Lock()
		err := lastErr
		errMu.Unlock()
		return err
	}
	return nil
}

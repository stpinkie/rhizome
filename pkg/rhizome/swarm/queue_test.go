package swarm

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stpinkie/rhizome/pkg/config"
)

// newTestQueue returns an isolated workQueue with a minimal Swarm shell so
// queue internals can be tested without libp2p setup.
func newTestQueue(t *testing.T) (*workQueue, *Swarm) {
	t.Helper()
	cfg := config.DefaultSwarmConfig()
	// Make retention effectively zero for terminal-offer reaping while keeping
	// incoming entries alive (OfferTTL is large, so cap logic is exercised).
	cfg.Queue.AssignTimeout = -2 * time.Minute
	cfg.Queue.OfferTTL = 1 * time.Hour
	cfg.Queue.MaxOffers = 100

	s := &Swarm{cfg: cfg}
	return newWorkQueue(s), s
}

func TestQueueReapsTerminalOffers(t *testing.T) {
	q, s := newTestQueue(t)

	to := &trackedOffer{
		info:     OfferInfo{Offer: Offer{OfferID: "offer-1"}, Status: OfferDone},
		resolved: make(chan struct{}),
		finished: make(chan struct{}),
	}
	s.finishOffer(to)
	s.finishTracked(to)

	q.mu.Lock()
	q.offers["offer-1"] = to
	q.mu.Unlock()

	// Small sleep so finishedAt is in the past relative to the reaper.
	time.Sleep(10 * time.Millisecond)
	q.reap()

	q.mu.Lock()
	defer q.mu.Unlock()
	assert.NotContains(t, q.offers, "offer-1", "terminal offer should be reaped")
}

func TestQueueReapDoesNotDeleteOpenOffers(t *testing.T) {
	q, _ := newTestQueue(t)

	to := &trackedOffer{
		info:     OfferInfo{Offer: Offer{OfferID: "open-1"}, Status: OfferOpen},
		resolved: make(chan struct{}),
		finished: make(chan struct{}),
	}

	q.mu.Lock()
	q.offers["open-1"] = to
	q.mu.Unlock()

	q.reap()

	q.mu.Lock()
	defer q.mu.Unlock()
	assert.Contains(t, q.offers, "open-1", "open offer should survive reaping")
}

func TestQueueDoubleFinishIsIdempotent(t *testing.T) {
	_, s := newTestQueue(t)

	to := &trackedOffer{
		resolved: make(chan struct{}),
		finished: make(chan struct{}),
	}

	// Concurrent double completion must not panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.finishOffer(to)
		s.finishTracked(to)
	}()
	s.finishOffer(to)
	s.finishTracked(to)
	<-done

	select {
	case <-to.resolved:
	case <-time.After(time.Second):
		t.Fatal("resolved channel was not closed")
	}
	select {
	case <-to.finished:
	case <-time.After(time.Second):
		t.Fatal("finished channel was not closed")
	}
}

func TestQueueCapsIncomingOffers(t *testing.T) {
	q, _ := newTestQueue(t)

	q.mu.Lock()
	// Seed more observed offers than the incoming cap (MaxOffers * 2).
	for i := 0; i < 250; i++ {
		id := fmt.Sprintf("incoming-%d", i)
		q.incoming[id] = OfferInfo{
			Offer:  Offer{OfferID: id, CreatedAt: time.Now()},
			Status: OfferObserved,
		}
	}
	q.mu.Unlock()

	q.reap()

	q.mu.Lock()
	defer q.mu.Unlock()
	assert.LessOrEqual(t, len(q.incoming), q.maxIncoming(), "incoming offers should be capped")
}

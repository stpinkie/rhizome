package mesh

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
)

// activityCapacity bounds the in-memory ring buffer. The feed is observability
// only; mesh-audit.jsonl remains the durable trail.
const activityCapacity = 200

// ActivityEntry is one mesh/swarm event snapshot for the activity feed.
type ActivityEntry struct {
	Time   time.Time      `json:"time"`
	Kind   string         `json:"kind"`
	Source string         `json:"source,omitempty"`
	Attrs  map[string]any `json:"attrs,omitempty"`
}

// activityFeed is a bounded in-memory ring of mesh.*/swarm.* runtime events.
type activityFeed struct {
	mu      sync.RWMutex
	entries []ActivityEntry
	head    int // next write position; entries are logically ordered oldest→newest
	full    bool
}

func newActivityFeed() *activityFeed {
	return &activityFeed{entries: make([]ActivityEntry, 0, activityCapacity)}
}

func (f *activityFeed) push(evt runtimeevents.Event) {
	e := ActivityEntry{
		Time:  evt.Time,
		Kind:  evt.Kind.String(),
		Attrs: evt.Attrs,
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if evt.Source.Component != "" {
		e.Source = evt.Source.Component
		if evt.Source.Name != "" {
			e.Source += "/" + evt.Source.Name
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.entries) < activityCapacity && !f.full {
		f.entries = append(f.entries, e)
		if len(f.entries) == activityCapacity {
			f.full = true
		}
		return
	}
	f.entries[f.head] = e
	f.head = (f.head + 1) % activityCapacity
}

// tail returns up to n most-recent entries, oldest first. n<=0 means all.
func (f *activityFeed) tail(n int) []ActivityEntry {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var ordered []ActivityEntry
	if f.full {
		ordered = make([]ActivityEntry, 0, activityCapacity)
		for i := 0; i < activityCapacity; i++ {
			ordered = append(ordered, f.entries[(f.head+i)%activityCapacity])
		}
	} else {
		ordered = append([]ActivityEntry(nil), f.entries...)
	}
	if n > 0 && n < len(ordered) {
		ordered = ordered[len(ordered)-n:]
	}
	return ordered
}

// matchMeshSwarmKind matches mesh.*/swarm.*/web3.* runtime events — the feed
// also surfaces signing-approval lifecycle so pending requests are visible
// to operators watching activity.
func matchMeshSwarmKind(evt runtimeevents.Event) bool {
	k := evt.Kind.String()
	return strings.HasPrefix(k, "mesh.") || strings.HasPrefix(k, "swarm.") ||
		strings.HasPrefix(k, "web3.")
}

// startActivityFeed subscribes the feed to the runtime event bus. Called by
// SetEventBus, which can run more than once on the same mesh (daemon and
// gateway both wire the bus) — the Once keeps the subscription single.
func (m *Mesh) startActivityFeed() {
	if m.eventBus == nil {
		return
	}
	m.activityOnce.Do(func() {
		m.activity = newActivityFeed()
		m.runActivityFeed()
	})
}

func (m *Mesh) runActivityFeed() {
	// The subscription channel is drained by a goroutine that pushes into the
	// ring buffer; the feed never blocks publishers.
	sub, ch, err := m.eventBus.Channel().Filter(matchMeshSwarmKind).SubscribeChan(
		context.Background(),
		runtimeevents.SubscribeOptions{
			Name:         "mesh-activity-feed",
			Buffer:       activityCapacity,
			Backpressure: runtimeevents.DropOldest,
		},
	)
	if err != nil {
		return
	}
	_ = sub // lives for the mesh lifetime; feed subscription never unsubscribes
	go func() {
		for evt := range ch {
			m.activity.push(evt)
		}
	}()
}

// Activity returns the tail of the mesh activity feed (oldest first).
// n<=0 returns everything buffered.
func (m *Mesh) Activity(n int) []ActivityEntry {
	if m == nil || m.activity == nil {
		return nil
	}
	return m.activity.tail(n)
}

// Events streams live mesh.*/swarm.* events for the SSE endpoint, mirroring
// TaskEvents' subscribe-then-commit pattern.
func (m *Mesh) Events(
	ctx context.Context,
) (<-chan runtimeevents.Event, func(), error) {
	if m.eventBus == nil {
		return nil, nil, fmt.Errorf("event bus not configured")
	}
	sub, ch, err := m.eventBus.Channel().Filter(matchMeshSwarmKind).SubscribeChan(
		ctx,
		runtimeevents.SubscribeOptions{
			Name:         "network-events-sse",
			Buffer:       64,
			Backpressure: runtimeevents.DropOldest,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	return ch, func() { _ = sub.Close() }, nil
}

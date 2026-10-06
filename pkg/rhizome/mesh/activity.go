package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
)

// activityCapacity bounds the in-memory ring buffer. The durable trail lives
// in mesh-activity.jsonl; the ring warm-loads its tail at feed start.
const activityCapacity = 200

// ActivityEntry is one mesh/swarm event snapshot for the activity feed.
type ActivityEntry struct {
	Time   time.Time      `json:"time"`
	Kind   string         `json:"kind"`
	Source string         `json:"source,omitempty"`
	Attrs  map[string]any `json:"attrs,omitempty"`
}

// ActivityFilter narrows the activity feed. Empty fields match everything.
// Kind is an exact match, or a prefix match when it ends in ".*" (e.g.
// "mesh.*"). Peer matches entries whose attributes mention the peer id
// (peer_id, claimant, offerer, …). Swarm matches attrs["swarm_id"] exactly.
// Since keeps entries at or after the instant. Contains keeps entries whose
// attributes encode the substring — the correlation primitive behind trace.
type ActivityFilter struct {
	Kind     string
	Peer     string
	Swarm    string
	Since    time.Time
	Contains string
}

// matches reports whether e passes every non-empty filter field.
func (e ActivityEntry) matches(f ActivityFilter) bool {
	if f.Kind != "" {
		if strings.HasSuffix(f.Kind, ".*") {
			if !strings.HasPrefix(e.Kind, strings.TrimSuffix(f.Kind, "*")) {
				return false
			}
		} else if e.Kind != f.Kind {
			return false
		}
	}
	if !f.Since.IsZero() && e.Time.Before(f.Since) {
		return false
	}
	if f.Swarm != "" && e.Attrs["swarm_id"] != f.Swarm {
		return false
	}
	needle := f.Peer
	if needle == "" {
		needle = f.Contains
	}
	if needle != "" {
		raw, err := json.Marshal(e.Attrs)
		if err != nil || !strings.Contains(string(raw), needle) {
			return false
		}
	}
	return true
}

// activityFeed is a bounded in-memory ring of mesh.*/swarm.* runtime events,
// optionally mirrored to a rotating JSONL log.
type activityFeed struct {
	mu      sync.RWMutex
	entries []ActivityEntry
	head    int // next write position; entries are logically ordered oldest→newest
	full    bool
	log     *auditLogger
}

func newActivityFeed(log *auditLogger) *activityFeed {
	return &activityFeed{entries: make([]ActivityEntry, 0, activityCapacity), log: log}
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

	f.pushEntry(e)
	if f.log != nil {
		f.log.Log(map[string]any{
			"time":   e.Time.UTC().Format(time.RFC3339Nano),
			"kind":   e.Kind,
			"source": e.Source,
			"attrs":  e.Attrs,
		})
	}
}

// pushEntry inserts one entry into the ring without touching the durable log.
func (f *activityFeed) pushEntry(e ActivityEntry) {
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

// warm loads the tail of the persisted activity log into the ring so
// --since queries span restarts. Entries arrive oldest-first.
func (f *activityFeed) warm(path string, n int) {
	raws, err := ReadAuditTail(path, n)
	if err != nil {
		return
	}
	for _, raw := range raws {
		var e ActivityEntry
		if json.Unmarshal(raw, &e) == nil && e.Kind != "" {
			f.pushEntry(e)
		}
	}
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

// filtered returns entries matching f, oldest first, then applies the tail
// bound (n<=0 means all matches).
func (f *activityFeed) filtered(n int, filter ActivityFilter) []ActivityEntry {
	ordered := f.tail(0)
	if filter != (ActivityFilter{}) {
		kept := ordered[:0]
		for _, e := range ordered {
			if e.matches(filter) {
				kept = append(kept, e)
			}
		}
		ordered = kept
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
		m.activity = newActivityFeed(m.activityLog)
		if m.activityLog != nil {
			m.activity.warm(m.activityLog.path, activityCapacity)
		}
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
	return m.ActivityFiltered(n, ActivityFilter{})
}

// ActivityFiltered returns the tail of the feed matching filter (oldest
// first). n<=0 returns every matching entry buffered.
func (m *Mesh) ActivityFiltered(n int, filter ActivityFilter) []ActivityEntry {
	if m == nil || m.activity == nil {
		return nil
	}
	return m.activity.filtered(n, filter)
}

// ActivityLogPath returns the durable activity trail location, or "" when
// activity logging is disabled.
func (m *Mesh) ActivityLogPath() string {
	if m == nil || m.activityLog == nil {
		return ""
	}
	return m.activityLog.path
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

// defaultActivityPath returns the activity trail location under the Rhizome
// home.
func defaultActivityPath() string {
	home := config.GetHome()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "mesh-activity.jsonl")
}

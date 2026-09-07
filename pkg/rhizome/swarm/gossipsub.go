package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	pubsub "github.com/libp2p/go-libp2p-pubsub"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
)

// gossipsubTopicPrefix namespaces swarm topics on the shared pub/sub bus.
const gossipsubTopicPrefix = "rhizome/swarm/"

// gossipsubBroadcaster is the optional GossipSub transport backend. Join and
// leave envelopes still travel over direct streams; only MsgBroadcast fan-out
// uses pub/sub topics, one topic per swarm.
//
// Authentication does not change: every envelope is signed by its author and
// validated against the author's peer key (pubsub's own message signing pins
// the author field, so the forwarder cannot spoof it).
type gossipsubBroadcaster struct {
	s  *Swarm
	ps *pubsub.PubSub
	*localBus

	mu     sync.Mutex
	topics map[string]*pubsub.Topic
	subs   map[string]*pubsub.Subscription
}

// newGossipsubBroadcaster creates the pub/sub backend for the swarm.
func newGossipsubBroadcaster(ctx context.Context, s *Swarm) (*gossipsubBroadcaster, error) {
	ps, err := pubsub.NewGossipSub(ctx, s.host)
	if err != nil {
		return nil, fmt.Errorf("create gossipsub: %w", err)
	}
	return &gossipsubBroadcaster{
		s:        s,
		ps:       ps,
		localBus: s.localBus,
		topics:   make(map[string]*pubsub.Topic),
		subs:     make(map[string]*pubsub.Subscription),
	}, nil
}

// ensure joins the swarm's topic and starts its reader loop (idempotent).
func (b *gossipsubBroadcaster) ensure(swarmID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.topics[swarmID]; ok {
		return nil
	}
	t, err := b.ps.Join(gossipsubTopicPrefix + swarmID)
	if err != nil {
		return fmt.Errorf("join gossipsub topic for %q: %w", swarmID, err)
	}
	sub, err := t.Subscribe()
	if err != nil {
		_ = t.Close()
		return fmt.Errorf("subscribe gossipsub topic for %q: %w", swarmID, err)
	}
	b.topics[swarmID] = t
	b.subs[swarmID] = sub
	b.s.wg.Add(1)
	go b.readLoop(b.s.ctx, swarmID, sub)
	return nil
}

// ensureTopic is the internal-broadcaster hook called on swarm joins.
func (b *gossipsubBroadcaster) ensureTopic(swarmID string) {
	if err := b.ensure(swarmID); err != nil {
		b.s.publishEvent(runtimeevents.KindSwarmError, map[string]any{
			"stage":    "gossipsub",
			"swarm_id": swarmID,
			"error":    err.Error(),
		})
	}
}

// readLoop consumes the swarm's pub/sub topic until the swarm shuts down.
func (b *gossipsubBroadcaster) readLoop(ctx context.Context, swarmID string, sub *pubsub.Subscription) {
	defer b.s.wg.Done()
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			return
		}
		// Skip our own publications — local subscribers already received them
		// through the local bus.
		if msg.GetFrom() == b.s.host.ID() {
			continue
		}
		if len(msg.Data) > b.s.cfg.MaxMessageBytes {
			continue
		}
		var env Envelope
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			continue
		}
		if env.SwarmID != swarmID {
			continue
		}
		// The author (msg.GetFrom) is pinned by pubsub's message signature;
		// HandlePush re-validates the envelope signature, trust, and replay.
		b.s.HandlePush(msg.GetFrom(), env)
	}
}

// Publish broadcasts a signed envelope to every topic subscriber.
func (b *gossipsubBroadcaster) Publish(ctx context.Context, env Envelope) error {
	if err := b.ensure(env.SwarmID); err != nil {
		return err
	}
	b.mu.Lock()
	t := b.topics[env.SwarmID]
	b.mu.Unlock()
	data, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode envelope: %w", err)
	}
	return t.Publish(ctx, data)
}

// close deregisters all topics.
func (b *gossipsubBroadcaster) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		sub.Cancel()
	}
	for _, t := range b.topics {
		_ = t.Close()
	}
}

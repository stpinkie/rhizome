package tools

import (
	"context"
	"sync/atomic"
	"time"
)

// SwarmHooks connects the swarm_context tool to the running swarm layer. The
// gateway wires it when swarm support is enabled; a nil hook set means swarm
// is unavailable and the tool falls back to local file access only.
type SwarmHooks struct {
	// PeerID returns the local node's peer id (shard author name).
	PeerID func() string
	// IsMember reports whether the local node joined the swarm.
	IsMember func(swarmID string) bool
	// PostNote appends a note to the local shard and broadcasts it to members.
	PostNote func(ctx context.Context, swarmID, kind, key, content string, ttl time.Duration) error
	// SetContext replaces the curated context document (coordinator only).
	SetContext func(swarmID, content string) error
}

var swarmHooks atomic.Pointer[SwarmHooks]

// SetSwarmHooks registers the swarm backend for the swarm_context tool.
// Passing nil clears it.
func SetSwarmHooks(h *SwarmHooks) {
	swarmHooks.Store(h)
}

// currentSwarmHooks returns the registered hooks, or nil.
func currentSwarmHooks() *SwarmHooks {
	return swarmHooks.Load()
}

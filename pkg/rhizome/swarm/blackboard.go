package swarm

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/blackboard"
)

// notePayload is carried by MsgNote envelopes.
type notePayload struct {
	Kind      string    `json:"kind,omitempty"`
	Key       string    `json:"key,omitempty"`
	Content   string    `json:"content"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// ContextDirFunc resolves a swarm's blackboard directory (the workspace path
// under swarm/<id>/); the gateway wires it so blackboards live in the synced
// workspace.
type ContextDirFunc func(swarmID string) string

// SetContextDirFunc registers the blackboard directory resolver. Nil disables
// the shared-context feature.
func (s *Swarm) SetContextDirFunc(fn ContextDirFunc) {
	s.contextDir = fn
}

// ContextEnabled reports whether the shared blackboard is usable: configured
// (default on) and a directory resolver is wired.
func (s *Swarm) ContextEnabled() bool {
	return s.cfg.ContextEnabled() && s.contextDir != nil
}

// Blackboard returns the file-backed shared context store for a swarm, or
// nil when the feature is disabled or the id is invalid.
func (s *Swarm) Blackboard(swarmID string) *blackboard.Blackboard {
	if !s.ContextEnabled() || !ValidSwarmID(swarmID) {
		return nil
	}
	dir := s.contextDir(swarmID)
	if dir == "" {
		return nil
	}
	return blackboard.New(dir, blackboard.Options{
		MaxNoteBytes:     s.cfg.Context.MaxNoteBytes,
		MaxNotesPerShard: s.cfg.Context.MaxNotes,
		DigestBytes:      s.cfg.Context.DigestBytes,
		NoteTTL:          s.cfg.Context.NoteTTL,
	})
}

// PostNote appends a note to the local member's shard and pushes it to
// connected members so it lands in their copy of this member's shard without
// waiting for workspace sync.
func (s *Swarm) PostNote(ctx context.Context, swarmID, kind, key, content string, ttl time.Duration) error {
	if !s.isJoined(swarmID) {
		return fmt.Errorf("not a member of swarm %q", swarmID)
	}
	bb := s.Blackboard(swarmID)
	if bb == nil {
		return fmt.Errorf("swarm context is not enabled")
	}
	note := blackboard.Note{Kind: kind, Key: key, Content: content}
	if ttl > 0 {
		note.ExpiresAt = time.Now().UTC().Add(ttl)
	}
	self := s.host.ID().String()
	if err := bb.Append(self, note); err != nil {
		return err
	}
	s.publishEvent(runtimeevents.KindSwarmContextNote, map[string]any{
		"swarm_id": swarmID,
		"author":   self,
		"kind":     kind,
		"key":      key,
	})
	s.auditSwarm(s.host.ID(), "context.note", swarmID, "", "ok", time.Now(), "")

	payload, err := encodePayload(notePayload{
		Kind:      kind,
		Key:       key,
		Content:   note.Content,
		ExpiresAt: note.ExpiresAt,
	})
	if err != nil {
		return nil //nolint:nilerr // note is durably local; broadcast is best-effort
	}
	env := Envelope{SwarmID: swarmID, Type: MsgNote, Payload: payload}
	if err := s.sign(&env); err != nil {
		return nil //nolint:nilerr // note is durably local; broadcast is best-effort
	}
	for _, pid := range s.memberPeers(swarmID) {
		go func(pid peer.ID) { _ = s.transport.Push(ctx, pid, env) }(pid)
	}
	return nil
}

// handleInboundNote appends a member-pushed note to the sender's shard. The
// envelope was already signature-verified against the stream peer, so the
// shard attribution is authentic.
func (s *Swarm) handleInboundNote(from peer.ID, env Envelope) {
	if !s.isJoined(env.SwarmID) {
		return
	}
	// Only known members may write context.
	s.mu.RLock()
	swarm, ok := s.swarms[env.SwarmID]
	_, isMember := swarm.Members[from.String()]
	s.mu.RUnlock()
	if !ok || !isMember {
		return
	}
	var p notePayload
	if err := decodePayload(env, &p); err != nil {
		return
	}
	bb := s.Blackboard(env.SwarmID)
	if bb == nil {
		return
	}
	if err := bb.Append(from.String(), blackboard.Note{
		Kind:      p.Kind,
		Key:       p.Key,
		Content:   p.Content,
		ExpiresAt: p.ExpiresAt,
	}); err != nil {
		return
	}
	s.publishEvent(runtimeevents.KindSwarmContextNote, map[string]any{
		"swarm_id": env.SwarmID,
		"author":   from.String(),
		"kind":     p.Kind,
		"key":      p.Key,
		"remote":   true,
	})
}

// ContextNotes returns the blackboard's live notes for a swarm.
func (s *Swarm) ContextNotes(swarmID string, since time.Time) []blackboard.Note {
	bb := s.Blackboard(swarmID)
	if bb == nil {
		return nil
	}
	return bb.Notes(since)
}

// ReadContext returns the curated context document for a swarm.
func (s *Swarm) ReadContext(swarmID string) string {
	bb := s.Blackboard(swarmID)
	if bb == nil {
		return ""
	}
	return bb.ReadContext()
}

// ContextDigest renders the blackboard digest used for prompt injection.
func (s *Swarm) ContextDigest(swarmID string) string {
	bb := s.Blackboard(swarmID)
	if bb == nil {
		return ""
	}
	return bb.Digest()
}

// SetContext replaces the curated context document. Only the elected
// coordinator may curate shared context — this keeps a single writer for
// context.md so workspace sync never has to merge concurrent edits.
func (s *Swarm) SetContext(swarmID, content string) error {
	if !s.isJoined(swarmID) {
		return fmt.Errorf("not a member of swarm %q", swarmID)
	}
	if !s.IsCoordinator(swarmID) {
		return fmt.Errorf("forbidden: only the swarm coordinator may write context.md")
	}
	bb := s.Blackboard(swarmID)
	if bb == nil {
		return fmt.Errorf("swarm context is not enabled")
	}
	if err := bb.WriteContext(content); err != nil {
		return err
	}
	s.publishEvent(runtimeevents.KindSwarmContextWritten, map[string]any{
		"swarm_id":    swarmID,
		"coordinator": s.host.ID().String(),
	})
	return nil
}

package mesh

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/tools"
)

// RemoteSpawner wraps a local SubTurnSpawner and routes to remote peers
// when the target agent is advertised by a trusted node.
type RemoteSpawner struct {
	mesh     *Mesh
	local    tools.SubTurnSpawner
	hasLocal func(agentID string) bool
}

// NewRemoteSpawner creates a spawner that falls back to local for local
// targets and uses the mesh for remote targets.
func NewRemoteSpawner(m *Mesh, local tools.SubTurnSpawner) *RemoteSpawner {
	return &RemoteSpawner{mesh: m, local: local}
}

// SetLocalAgentChecker sets a predicate reporting whether an agent id is
// registered locally. When set, remote dispatch is only attempted for agents
// that are not local; a local execution failure is returned to the caller
// instead of silently re-running the task on a remote peer.
func (s *RemoteSpawner) SetLocalAgentChecker(fn func(agentID string) bool) {
	s.hasLocal = fn
}

// SpawnSubTurn implements tools.SubTurnSpawner.
func (s *RemoteSpawner) SpawnSubTurn(ctx context.Context, cfg tools.SubTurnConfig) (*tools.ToolResult, error) {
	if cfg.TargetAgentID == "" {
		if s.local == nil {
			return nil, fmt.Errorf("no local sub-turn spawner configured")
		}
		return s.local.SpawnSubTurn(ctx, cfg)
	}

	// Prefer a locally registered agent. With a checker configured, a local
	// failure is returned directly rather than re-executing remotely; without
	// one, preserve the legacy try-local-first behaviour.
	if s.local != nil {
		if s.hasLocal == nil {
			if res, err := s.local.SpawnSubTurn(ctx, cfg); err == nil {
				return res, nil
			}
		} else if s.hasLocal(cfg.TargetAgentID) {
			return s.local.SpawnSubTurn(ctx, cfg)
		}
	}

	// Find a trusted peer that advertises the target agent.
	var remotePID peer.ID
	for _, pid := range s.mesh.ConnectedTrustedPeers() {
		capability, ok := s.mesh.PeerCapabilities(pid)
		if !ok {
			continue
		}
		for _, a := range capability.Agents {
			if a == cfg.TargetAgentID {
				remotePID = pid
				break
			}
		}
		if remotePID != "" {
			break
		}
	}

	if remotePID == "" {
		return nil, fmt.Errorf("no trusted peer advertises agent %q", cfg.TargetAgentID)
	}

	var toolNames []string
	for _, t := range cfg.Tools {
		if t != nil {
			toolNames = append(toolNames, t.Name())
		}
	}

	return s.mesh.CallRemote(ctx, remotePID, RemoteCall{
		TargetAgentID: cfg.TargetAgentID,
		Model:         cfg.Model,
		SystemPrompt:  cfg.SystemPrompt,
		Tools:         toolNames,
		Async:         cfg.Async,
	})
}

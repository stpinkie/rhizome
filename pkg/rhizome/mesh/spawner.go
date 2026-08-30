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
	mesh  *Mesh
	local tools.SubTurnSpawner
}

// NewRemoteSpawner creates a spawner that falls back to local for local
// targets and uses the mesh for remote targets.
func NewRemoteSpawner(m *Mesh, local tools.SubTurnSpawner) *RemoteSpawner {
	return &RemoteSpawner{mesh: m, local: local}
}

// SpawnSubTurn implements tools.SubTurnSpawner.
func (s *RemoteSpawner) SpawnSubTurn(ctx context.Context, cfg tools.SubTurnConfig) (*tools.ToolResult, error) {
	if cfg.TargetAgentID == "" {
		return s.local.SpawnSubTurn(ctx, cfg)
	}

	// First, try the local registry.
	if s.local != nil {
		res, err := s.local.SpawnSubTurn(ctx, cfg)
		if err == nil {
			return res, nil
		}
	}

	// Find a trusted peer that advertises the target agent.
	var remotePID peer.ID
	for _, pid := range s.mesh.ConnectedTrustedPeers() {
		cap, ok := s.mesh.PeerCapabilities(pid)
		if !ok {
			continue
		}
		for _, a := range cap.Agents {
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

	return s.mesh.CallRemote(ctx, remotePID, cfg.TargetAgentID, cfg.SystemPrompt)
}

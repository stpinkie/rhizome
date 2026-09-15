package acp

import (
	"context"
	"strings"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/tools"
)

// Spawner routes SubTurns targeting ACP-bound agent ids to the external
// process via ClientManager and passes everything else to the wrapped
// (local) spawner. It sits between mesh.RemoteSpawner and the local
// AgentLoopSpawner: remote→acp→local.
type Spawner struct {
	registry func() *agent.AgentRegistry // lazy: registry swaps on reload
	local    tools.SubTurnSpawner
	client   *ClientManager
}

// NewSpawner wraps local with ACP dispatch. Returns local unchanged when
// client is nil so callers can wire unconditionally.
func NewSpawner(
	client *ClientManager,
	getRegistry func() *agent.AgentRegistry,
	local tools.SubTurnSpawner,
) tools.SubTurnSpawner {
	if client == nil {
		return local
	}
	return &Spawner{registry: getRegistry, local: local, client: client}
}

// SpawnSubTurn implements tools.SubTurnSpawner.
func (s *Spawner) SpawnSubTurn(
	ctx context.Context,
	cfg tools.SubTurnConfig,
) (*tools.ToolResult, error) {
	if cfg.TargetAgentID == "" || s.client == nil || s.registry == nil {
		return s.local.SpawnSubTurn(ctx, cfg)
	}
	reg := s.registry()
	if reg == nil {
		return s.local.SpawnSubTurn(ctx, cfg)
	}
	inst, ok := reg.GetAgent(cfg.TargetAgentID)
	if !ok || inst == nil || inst.ACP == nil {
		return s.local.SpawnSubTurn(ctx, cfg)
	}

	prompt := subTurnPrompt(cfg)
	out, err := s.client.RunAgent(ctx, inst.ID, prompt)
	if err != nil {
		return nil, err
	}
	return tools.NewToolResult(out), nil
}

// subTurnPrompt renders a SubTurnConfig into prompt text for the external
// agent: the system prompt (which is where delegate/subagent put the task)
// plus any user-role initial messages.
func subTurnPrompt(cfg tools.SubTurnConfig) string {
	var b strings.Builder
	if p := strings.TrimSpace(cfg.SystemPrompt); p != "" {
		b.WriteString(p)
	}
	for _, m := range cfg.InitialMessages {
		if m.Role != "user" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(m.Content)
	}
	return b.String()
}

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/providers"
)

// RemoteDispatchRequest describes a task submitted by a trusted mesh peer.
// The callee resolves the target agent, model, and tool set from its own
// configuration; nothing here is executed until it has been validated.
type RemoteDispatchRequest struct {
	// AgentID selects the agent to run. Empty means the default agent.
	AgentID string
	// Model overrides the agent's model. It must resolve to an entry in this
	// node's model_list; unconfigured models are rejected.
	Model string
	// Tools, when non-empty, restricts the agent to the named tools. Names
	// that the agent does not have cause the request to be rejected.
	Tools []string
	// Prompt is the user message for the remote turn.
	Prompt string
	// SessionKey scopes the (ephemeral) session; defaults to a generated key.
	SessionKey string
	// SenderID identifies the requesting peer for events and audit logging.
	SenderID string
}

// ProcessRemoteDispatch executes a mesh-submitted task on a specific local
// agent. Unlike ProcessDirect it does not go through channel routing: the
// caller (a trusted mesh peer) names the agent explicitly. The turn runs on a
// shallow copy of the agent with an ephemeral session so remote tasks never
// write into local session history.
func (al *AgentLoop) ProcessRemoteDispatch(
	ctx context.Context,
	req RemoteDispatchRequest,
) (string, error) {
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", err
	}

	registry := al.GetRegistry()
	var base *AgentInstance
	if id := strings.TrimSpace(req.AgentID); id != "" {
		var ok bool
		base, ok = registry.GetAgent(id)
		if !ok {
			return "", fmt.Errorf("agent %q not found on this node", id)
		}
	} else {
		base = registry.GetDefaultAgent()
	}
	if base == nil {
		return "", fmt.Errorf("no agent available for remote dispatch")
	}

	// Shallow copy like subturn execution: remote tasks get an ephemeral
	// session and never mutate the live agent's session store or tool registry.
	agentCopy := *base
	agentCopy.Sessions = newEphemeralSession(nil)

	if err := al.applyRemoteModelOverride(base, &agentCopy, req.Model); err != nil {
		return "", err
	}
	if err := applyRemoteToolOverride(base, &agentCopy, req.Tools); err != nil {
		return "", err
	}

	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		sessionKey = fmt.Sprintf("mesh-%d", time.Now().UnixNano())
	}
	senderID := strings.TrimSpace(req.SenderID)
	if senderID == "" {
		senderID = "mesh"
	}

	return al.runAgentLoop(ctx, &agentCopy, processOptions{
		Dispatch: DispatchRequest{
			SessionKey:  sessionKey,
			UserMessage: req.Prompt,
			InboundContext: &bus.InboundContext{
				Channel:  "mesh",
				ChatID:   sessionKey,
				ChatType: "direct",
				SenderID: senderID,
			},
		},
		SenderID:             senderID,
		DefaultResponse:      defaultResponse,
		EnableSummary:        false,
		SendResponse:         false,
		SuppressToolFeedback: true,
		NoHistory:            true,
	})
}

// applyRemoteModelOverride validates and applies a remote-requested model.
// The model must resolve to a model_list entry on this node; the copy's
// candidates and providers are rebuilt for it, and model routing is disabled
// so an explicit remote choice is never overridden by the light-model router.
func (al *AgentLoop) applyRemoteModelOverride(
	base, agentCopy *AgentInstance,
	model string,
) error {
	model = strings.TrimSpace(model)
	if model == "" || model == base.Model {
		return nil
	}

	cfg := al.GetConfig()
	if cfg == nil {
		return fmt.Errorf("model override unavailable: no config")
	}
	mc := lookupModelConfigByRef(cfg, model, cfg.Agents.Defaults.Provider)
	if mc == nil {
		return fmt.Errorf("model %q is not configured on this node", model)
	}
	candidates := resolveModelCandidates(cfg, cfg.Agents.Defaults.Provider, mc.ModelName, nil)
	if len(candidates) == 0 {
		return fmt.Errorf("model %q could not be resolved on this node", model)
	}

	agentCopy.Model = mc.ModelName
	agentCopy.Fallbacks = nil
	agentCopy.Candidates = candidates
	agentCopy.Router = nil
	agentCopy.LightCandidates = nil
	agentCopy.LightProvider = nil
	agentCopy.Provider = resolvePrimaryProviderForCandidate(
		cfg, agentCopy.Workspace, agentCopy.ID, candidates[0], base.Provider,
	)
	candidateProviders := make(map[string]providers.LLMProvider, len(candidates))
	populateCandidateProvidersFromCandidates(cfg, agentCopy.Workspace, candidates, candidateProviders)
	agentCopy.CandidateProviders = candidateProviders
	return nil
}

// applyRemoteToolOverride restricts the agent copy to the requested tools.
// Missing tools are reported so remote callers get a clear rejection instead
// of a silently degraded tool set.
func applyRemoteToolOverride(base, agentCopy *AgentInstance, tools []string) error {
	if len(tools) == 0 {
		return nil
	}
	if base.Tools == nil {
		return fmt.Errorf("agent %q has no tools available", base.ID)
	}
	filtered, missing := base.Tools.CloneFiltered(tools)
	if len(missing) > 0 {
		return fmt.Errorf(
			"tools not available on agent %q: %s",
			base.ID, strings.Join(missing, ", "),
		)
	}
	agentCopy.Tools = filtered
	return nil
}

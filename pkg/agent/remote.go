package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/providers"
	"github.com/stpinkie/rhizome/pkg/utils"
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
	// Media carries local media:// refs for task attachments, already
	// resolved by the mesh layer from the wire's blob:// refs.
	Media []string
	// SessionKey scopes the (ephemeral) session; defaults to a generated key.
	SessionKey string
	// SenderID identifies the requesting peer for events and audit logging.
	SenderID string
}

// ProcessRemoteDispatch executes a mesh-submitted task on a specific local
// agent. Unlike ProcessDirect it does not go through channel routing: the
// caller (a trusted mesh peer) names the agent explicitly. The turn runs on a
// shallow copy of the agent with an ephemeral session so remote tasks never
// write into local session history. It returns the final text plus any
// media:// refs produced during the turn (send_file outputs, tool
// attachments) so the caller can ship them back over the wire.
func (al *AgentLoop) ProcessRemoteDispatch(
	ctx context.Context,
	req RemoteDispatchRequest,
) (string, []string, error) {
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", nil, err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", nil, err
	}

	registry := al.GetRegistry()
	var base *AgentInstance
	if id := strings.TrimSpace(req.AgentID); id != "" {
		var ok bool
		base, ok = registry.GetAgent(id)
		if !ok {
			return "", nil, fmt.Errorf("agent %q not found on this node", id)
		}
	} else {
		base = registry.GetDefaultAgent()
	}
	if base == nil {
		return "", nil, fmt.Errorf("no agent available for remote dispatch")
	}

	// Shallow copy like subturn execution: remote tasks get an ephemeral
	// session and never mutate the live agent's session store or tool registry.
	agentCopy := *base
	agentCopy.Sessions = newEphemeralSession(nil)

	if err := al.applyRemoteModelOverride(base, &agentCopy, req.Model); err != nil {
		return "", nil, err
	}
	if err := applyRemoteToolOverride(base, &agentCopy, req.Tools); err != nil {
		return "", nil, err
	}

	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		sessionKey = fmt.Sprintf("mesh-%d", time.Now().UnixNano())
	}
	senderID := strings.TrimSpace(req.SenderID)
	if senderID == "" {
		senderID = "mesh"
	}

	// Transcribe audio attachments so speech reaches the model as text even
	// when the provider cannot consume audio media directly.
	prompt := req.Prompt
	if al.transcriber != nil && al.mediaStore != nil {
		prompt = al.appendRemoteTranscriptions(ctx, prompt, req.Media)
	}

	var producedMedia []string
	text, err := al.runAgentLoop(ctx, &agentCopy, processOptions{
		Dispatch: DispatchRequest{
			SessionKey:  sessionKey,
			UserMessage: prompt,
			Media:       append([]string(nil), req.Media...),
			MediaSink:   &producedMedia,
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
	return text, producedMedia, err
}

// appendRemoteTranscriptions transcribes audio media refs attached to a
// remote task and appends [voice: ...] annotations to the prompt. Media refs
// are kept so the agent can still operate on the raw files.
func (al *AgentLoop) appendRemoteTranscriptions(ctx context.Context, prompt string, refs []string) string {
	for _, ref := range refs {
		path, meta, err := al.mediaStore.ResolveWithMeta(ref)
		if err != nil {
			continue
		}
		if !utils.IsAudioFile(meta.Filename, meta.ContentType) {
			continue
		}
		resp, err := al.transcriber.Transcribe(ctx, path)
		if err != nil {
			logger.WarnCF("voice", "Remote attachment transcription failed", map[string]any{
				"ref":   ref,
				"error": err.Error(),
			})
			continue
		}
		if text := strings.TrimSpace(resp.Text); text != "" {
			prompt += "\n[voice: " + text + "]"
		}
	}
	return prompt
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

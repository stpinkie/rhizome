package acp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/providers"
)

// Shared construction for ACP serving without an attached channel manager —
// used by `rhizome acp` (stdio) and the daemon's acp.server.remote path so
// both wire the same provider/loop/bus/session machinery.

// HeadlessStack owns the agent runtime an ACP server (or RemoteMux) drives:
// provider, message bus, agent loop, media store, and the persisted session
// index. Close tears everything down.
type HeadlessStack struct {
	Cfg      *config.Config
	Bus      *bus.MessageBus
	Loop     *agent.AgentLoop
	Media    media.MediaStore
	Sessions *SessionStore
	Models   []string
}

// NewHeadlessStack builds the headless agent runtime: provider from cfg,
// streaming forced on every model entry (ACP's contract is streamed
// updates), the process-local "acp" channel injected, and the persisted
// session index opened at sessionsPath (session/load is disabled when it
// cannot be opened).
func NewHeadlessStack(cfg *config.Config, sessionsPath string) (*HeadlessStack, error) {
	provider, modelID, err := providers.CreateProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("error creating provider: %w", err)
	}
	if modelID != "" {
		cfg.Agents.Defaults.ModelName = modelID
	}
	EnableModelStreaming(cfg)
	InjectChannelConfig(cfg)

	msgBus := bus.NewMessageBus()
	stack := &HeadlessStack{
		Cfg:    cfg,
		Bus:    msgBus,
		Models: SelectableModels(cfg),
	}
	loop := agent.NewAgentLoop(cfg, msgBus, provider)
	stack.Loop = loop
	stack.Media = media.NewFileMediaStore()
	loop.SetMediaStore(stack.Media)

	if store, err := OpenSessionStore(sessionsPath); err == nil {
		stack.Sessions = store
	}
	return stack, nil
}

// Close tears down the stack in reverse construction order.
func (s *HeadlessStack) Close() {
	if s == nil {
		return
	}
	if s.Loop != nil {
		s.Loop.Close()
	}
	if s.Bus != nil {
		s.Bus.Close()
	}
}

// EnableModelStreaming flips Streaming.Enabled on every cfg.ModelList entry
// in-memory. Providers that cannot stream fall back to Chat transparently,
// and unsent final responses are flushed as one chunk.
func EnableModelStreaming(cfg *config.Config) {
	for _, m := range cfg.ModelList {
		if m != nil {
			m.Streaming.Enabled = true
		}
	}
}

// SelectableModels lists the enabled model_list entries offered in the
// session's category:model config option, deduplicated by model_name.
// Virtual entries produced by multi-key expansion are skipped.
func SelectableModels(cfg *config.Config) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range cfg.ModelList {
		if m == nil || m.IsVirtual() || !m.Enabled {
			continue
		}
		name := strings.TrimSpace(m.ModelName)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// acpChannelSettings is the decoded channel-settings shape the streaming
// pipeline looks for: a Streaming field of type config.StreamingConfig.
type acpChannelSettings struct {
	Streaming config.StreamingConfig `json:"streaming,omitempty"`
}

// InjectChannelConfig installs a process-local "acp" channel entry so the
// streaming pipeline's channel gate passes for channel "acp". The enabled
// streaming flag lives in Settings — Channel.Decode unmarshals Settings
// into the cached extend struct, so presetting the target would be
// overwritten.
func InjectChannelConfig(cfg *config.Config) {
	if cfg.Channels == nil {
		cfg.Channels = config.ChannelsConfig{}
	}
	settings, err := json.Marshal(acpChannelSettings{
		Streaming: config.StreamingConfig{Enabled: true},
	})
	if err != nil {
		return
	}
	ch := &config.Channel{
		Enabled:  true,
		Type:     ChannelName,
		Settings: config.RawNode(settings),
	}
	if err := ch.Decode(&acpChannelSettings{}); err != nil {
		return
	}
	cfg.Channels[ChannelName] = ch
}

// Package acp implements `rhizome acp`: an Agent Client Protocol server
// speaking newline-delimited JSON-RPC over stdio, bridging ACP clients
// (Zed, JetBrains IDEs, ...) onto the normal Rhizome agent pipeline.
package acp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	acpbridge "github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/providers"
)

// acpChannelSettings is the decoded channel-settings shape the streaming
// pipeline looks for: a Streaming field of type config.StreamingConfig.
// The "acp" channel exists only inside this process — it is never
// registered as a channel type, so user configs are unaffected.
type acpChannelSettings struct {
	Streaming config.StreamingConfig `json:"streaming,omitempty"`
}

// NewACPCommand returns the rhizome acp command.
func NewACPCommand() *cobra.Command {
	var agentID string
	cmd := &cobra.Command{
		Use:   "acp",
		Short: "Serve the Agent Client Protocol (ACP) over stdio",
		Long: "rhizome acp speaks ACP — the editor↔agent JSON-RPC protocol used by " +
			"Zed and other clients — over stdin/stdout. Configure your editor to " +
			"spawn this command as an agent server; prompts run through the " +
			"normal Rhizome agent pipeline with streamed updates, tool-call " +
			"progress, and permission prompts.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return run(agentID)
		},
	}
	cmd.Flags().StringVar(&agentID, "agent", "",
		"pin ACP sessions to this agent id (default: routing default agent)")
	return cmd
}

func run(agentID string) error {
	// stdout carries the ACP protocol stream — no log line may reach it.
	// This must happen before LoadConfig, which logs through the
	// stdout-bound console writer. ConfigureFromEnv honours
	// RHIZOME_LOG_FILE (file logging keeps working); DisableConsole then
	// guarantees the console writer can never pollute the protocol.
	// Bridge diagnostics use slog → stderr.
	logger.ConfigureFromEnv()
	logger.DisableConsole()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	cfg, err := internal.LoadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}

	policy := acpbridge.PermissionPolicy(cfg.ACP.Server.PermissionPolicy)
	switch policy {
	case "", acpbridge.PermissionPrompt:
		policy = acpbridge.PermissionPrompt
	case acpbridge.PermissionAllow, acpbridge.PermissionDeny:
	default:
		return fmt.Errorf(
			"invalid acp.server.permission_policy %q (prompt|allow|deny)",
			cfg.ACP.Server.PermissionPolicy,
		)
	}

	provider, modelID, err := providers.CreateProvider(cfg)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}
	if modelID != "" {
		cfg.Agents.Defaults.ModelName = modelID
	}

	// ACP's contract is streamed updates; enable streaming on every model
	// entry in-memory. Providers that cannot stream fall back to Chat
	// transparently, and unsent final responses are flushed as one chunk.
	for _, m := range cfg.ModelList {
		if m != nil {
			m.Streaming.Enabled = true
		}
	}
	injectACPChannel(cfg)

	// Pin sessions to --agent via a leading dispatch rule so normal routing
	// machinery (and any user rules) still applies underneath.
	if agentID != "" {
		if cfg.Agents.Dispatch == nil {
			cfg.Agents.Dispatch = &config.DispatchConfig{}
		}
		cfg.Agents.Dispatch.Rules = append([]config.DispatchRule{{
			Name:  "acp-pinned-agent",
			Agent: agentID,
			When:  config.DispatchSelector{Channel: acpbridge.ChannelName},
		}}, cfg.Agents.Dispatch.Rules...)
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	agentLoop := agent.NewAgentLoop(cfg, msgBus, provider)
	defer agentLoop.Close()

	if agentID != "" {
		if _, ok := agentLoop.GetRegistry().GetAgent(agentID); !ok {
			return fmt.Errorf("unknown agent id %q", agentID)
		}
	}

	mediaStore := media.NewFileMediaStore()
	agentLoop.SetMediaStore(mediaStore)

	// The ACP session index lives under RHIZOME_HOME so session/load can
	// resurrect sessions across restarts.
	var sessionStore *acpbridge.SessionStore
	store, storeErr := acpbridge.OpenSessionStore(
		filepath.Join(config.GetHome(), "acp-sessions.json"))
	if storeErr != nil {
		slog.Warn("acp: session store unavailable — session/load disabled",
			"error", storeErr)
	} else {
		sessionStore = store
	}

	srv := acpbridge.NewServer(agentLoop, acpbridge.Options{
		AgentID:  agentID,
		Policy:   policy,
		Media:    mediaStore,
		Version:  config.FormatVersion(),
		Sessions: sessionStore,
	})
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Close()
	msgBus.SetStreamDelegate(srv)

	conn := acpsdk.NewAgentSideConnection(srv, os.Stdout, os.Stdin)
	srv.Bind(conn)

	logger.InfoCF("acp", "ACP server ready on stdio", map[string]any{
		"agent":  agentID,
		"policy": string(policy),
	})

	<-conn.Done()
	return nil
}

// injectACPChannel installs a process-local channel entry so the streaming
// pipeline's channel gate passes for channel "acp". The enabled streaming
// flag lives in Settings — Channel.Decode unmarshals Settings into the
// cached extend struct, so presetting the target would be overwritten.
func injectACPChannel(cfg *config.Config) {
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
		Type:     acpbridge.ChannelName,
		Settings: config.RawNode(settings),
	}
	if err := ch.Decode(&acpChannelSettings{}); err != nil {
		return
	}
	cfg.Channels[acpbridge.ChannelName] = ch
}

package acp

import (
	"context"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// authenticate picks a satisfiable advertised auth method and performs the
// ACP authenticate handshake. It runs once per external-agent connection,
// after initialize and before any session/new — the process is considered
// unauthenticated until it returns nil.
func (m *ClientManager) authenticate(
	ctx context.Context,
	agentID string,
	inst *agent.AgentInstance,
	conn *acpsdk.ClientSideConnection,
	methods []acpsdk.AuthMethod,
) error {
	var binding *config.ACPAgentConfig
	if inst != nil {
		binding = inst.ACP
	}
	return authenticateBound(
		ctx, agentID, binding, conn, methods,
		m.termPolicyFor(inst) == TerminalPolicyAllow)
}

// authenticateBound is the binding-level auth handshake shared by the
// daemon path and bound (per-session) spawns: terminal-backed auth methods
// are only satisfiable when the caller's posture allows terminals — bound
// market sessions always deny, so env_var methods are their only path.
func authenticateBound(
	ctx context.Context,
	agentID string,
	binding *config.ACPAgentConfig,
	conn *acpsdk.ClientSideConnection,
	methods []acpsdk.AuthMethod,
	terminalAllowed bool,
) error {
	if len(methods) == 0 {
		return nil
	}
	id, err := pickAuthMethodBound(agentID, binding, methods, terminalAllowed)
	if err != nil {
		return err
	}
	if _, err := conn.Authenticate(ctx, acpsdk.AuthenticateRequest{MethodId: id}); err != nil {
		return fmt.Errorf("acp authenticate (%s) failed for agent %q: %w", id, agentID, err)
	}
	logger.InfoCF("acp", "external ACP agent authenticated",
		map[string]any{"agent_id": agentID, "method": id})
	return nil
}

// pickAuthMethodBound selects an advertised auth method. The binding's
// acp.auth_method pin wins when set; otherwise the first satisfiable
// method is used.
func pickAuthMethodBound(
	agentID string,
	binding *config.ACPAgentConfig,
	methods []acpsdk.AuthMethod,
	terminalAllowed bool,
) (string, error) {
	want := ""
	if binding != nil {
		want = strings.TrimSpace(binding.AuthMethod)
	}
	if want != "" {
		for _, am := range methods {
			if authMethodID(am) != want {
				continue
			}
			if err := authSatisfiable(binding, am, terminalAllowed); err != nil {
				return "", fmt.Errorf(
					"acp agent %q auth method %q is not satisfiable: %w",
					agentID, want, err)
			}
			return want, nil
		}
		return "", fmt.Errorf(
			"acp agent %q auth method %q is not advertised by the agent (advertised: %s)",
			agentID, want, strings.Join(authMethodIDs(methods), ", "))
	}

	var reasons []string
	for _, am := range methods {
		id := authMethodID(am)
		if err := authSatisfiable(binding, am, terminalAllowed); err != nil {
			reasons = append(reasons, fmt.Sprintf("%s (%v)", id, err))
			continue
		}
		return id, nil
	}
	return "", fmt.Errorf(
		"acp agent %q requires authentication but no advertised method is satisfiable: %s",
		agentID, strings.Join(reasons, "; "))
}

// authSatisfiable reports whether a method can be completed locally:
// env_var needs every required var present (and non-empty) in the binding's
// env; terminal and agent-driven auth need the terminal capability, which
// is gated by the caller's terminal allowance (daemon: terminal_policy;
// bound sessions: always denied).
func authSatisfiable(
	binding *config.ACPAgentConfig,
	am acpsdk.AuthMethod,
	terminalAllowed bool,
) error {
	switch {
	case am.EnvVar != nil:
		var missing []string
		for _, v := range am.EnvVar.Vars {
			if v.Optional {
				continue
			}
			if binding == nil || binding.Env[v.Name] == "" {
				missing = append(missing, v.Name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf(
				"missing required env vars %s (set them via agents.list[].acp.env)",
				strings.Join(missing, ", "))
		}
		return nil
	case am.Terminal != nil:
		if !terminalAllowed {
			return fmt.Errorf("terminal auth requires acp.client.terminal_policy=allow")
		}
		return nil
	case am.Agent != nil:
		// The agent drives its own flow, which may need terminal methods.
		if !terminalAllowed {
			return fmt.Errorf("agent-driven auth requires acp.client.terminal_policy=allow")
		}
		return nil
	default:
		return fmt.Errorf("unsupported auth method")
	}
}

// authMethodID returns the advertised id of an auth-method variant.
func authMethodID(am acpsdk.AuthMethod) string {
	switch {
	case am.EnvVar != nil:
		return am.EnvVar.Id
	case am.Terminal != nil:
		return am.Terminal.Id
	case am.Agent != nil:
		return am.Agent.Id
	}
	return ""
}

func authMethodIDs(methods []acpsdk.AuthMethod) []string {
	ids := make([]string, 0, len(methods))
	for _, am := range methods {
		ids = append(ids, authMethodID(am))
	}
	return ids
}

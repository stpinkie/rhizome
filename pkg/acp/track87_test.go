package acp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
)

// --- external-agent authMethods (session/authenticate) ---

func envVarMethod(id string, vars ...string) acpsdk.AuthMethod {
	vv := make([]acpsdk.AuthEnvVar, 0, len(vars))
	for _, v := range vars {
		vv = append(vv, acpsdk.AuthEnvVar{Name: v})
	}
	return acpsdk.AuthMethod{EnvVar: &acpsdk.AuthMethodEnvVarInline{
		Id: id, Name: id, Type: "env_var", Vars: vv,
	}}
}

func terminalMethod(id string) acpsdk.AuthMethod {
	return acpsdk.AuthMethod{Terminal: &acpsdk.AuthMethodTerminalInline{
		Id: id, Name: id, Type: "terminal",
	}}
}

func agentMethod(id string) acpsdk.AuthMethod {
	return acpsdk.AuthMethod{Agent: &acpsdk.AuthMethodAgent{Id: id, Name: id}}
}

func acpBoundRegistryWithEnv(
	t *testing.T,
	workspace string,
	env map[string]string,
	authMethod string,
) *agent.AgentRegistry {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "ext",
				Workspace: workspace,
				ACP:       &config.ACPAgentConfig{Command: "mock", Env: env, AuthMethod: authMethod},
			}},
		},
	}
	return agent.NewAgentRegistry(cfg, nil)
}

func TestAuthEnvVarSatisfiable(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithEnv(t, ws, map[string]string{"MOCK_KEY": "secret"}, "")
	mock := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{envVarMethod("env", "MOCK_KEY")}}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(mock.authCalls) != 1 || mock.authCalls[0] != "env" {
		t.Fatalf("auth calls = %v, want [env]", mock.authCalls)
	}
}

func TestAuthEnvVarMissingVars(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithEnv(t, ws, nil, "")
	mock := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{
		envVarMethod("env", "MOCK_KEY", "OTHER_KEY"),
	}}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	_, err := m.RunAgent(context.Background(), "ext", "hi")
	if err == nil {
		t.Fatal("expected auth error for missing env vars")
	}
	if !strings.Contains(err.Error(), "MOCK_KEY") || !strings.Contains(err.Error(), "OTHER_KEY") {
		t.Fatalf("error should name missing vars: %v", err)
	}
	if len(mock.authCalls) != 0 {
		t.Fatalf("authenticate should not be called: %v", mock.authCalls)
	}
}

func TestAuthPinnedMethodWins(t *testing.T) {
	ws := t.TempDir()
	// env has neither var — only the pinned agent method is satisfiable.
	reg := acpBoundRegistryWithEnv(t, ws, nil, "self")
	mock := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{
		envVarMethod("env", "MOCK_KEY"),
		agentMethod("self"),
	}}
	cfg := &config.Config{}
	cfg.ACP.Client.TerminalPolicy = "allow"
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(mock.authCalls) != 1 || mock.authCalls[0] != "self" {
		t.Fatalf("auth calls = %v, want [self]", mock.authCalls)
	}
}

func TestAuthPinnedNotAdvertised(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithEnv(t, ws, nil, "oauth")
	mock := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{envVarMethod("env", "K")}}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	_, err := m.RunAgent(context.Background(), "ext", "hi")
	if err == nil || !strings.Contains(err.Error(), "not advertised") {
		t.Fatalf("expected not-advertised error, got %v", err)
	}
}

func TestAuthTerminalGatedByTerminalPolicy(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithEnv(t, ws, nil, "")

	// Deny (default): refused with a clear reason.
	mock := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{terminalMethod("tui")}}
	m := pipedManager(t, &config.Config{}, reg, mock)
	_, err := m.RunAgent(context.Background(), "ext", "hi")
	m.Close()
	if err == nil || !strings.Contains(err.Error(), "terminal_policy=allow") {
		t.Fatalf("expected terminal_policy refusal, got %v", err)
	}
	if len(mock.authCalls) != 0 {
		t.Fatal("authenticate should not be attempted under deny")
	}

	// Allow: attempted and completes.
	mock2 := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{terminalMethod("tui")}}
	cfg := &config.Config{}
	cfg.ACP.Client.TerminalPolicy = "allow"
	m2 := pipedManager(t, cfg, reg, mock2)
	defer m2.Close()
	if _, err := m2.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(mock2.authCalls) != 1 || mock2.authCalls[0] != "tui" {
		t.Fatalf("auth calls = %v", mock2.authCalls)
	}
}

func TestAuthAgentTypeGatedByTerminalPolicy(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithEnv(t, ws, nil, "")

	mock := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{agentMethod("self")}}
	m := pipedManager(t, &config.Config{}, reg, mock)
	_, err := m.RunAgent(context.Background(), "ext", "hi")
	m.Close()
	if err == nil || !strings.Contains(err.Error(), "terminal_policy=allow") {
		t.Fatalf("expected terminal_policy refusal for agent auth, got %v", err)
	}

	mock2 := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{agentMethod("self")}}
	cfg := &config.Config{}
	cfg.ACP.Client.TerminalPolicy = "allow"
	m2 := pipedManager(t, cfg, reg, mock2)
	defer m2.Close()
	if _, err := m2.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(mock2.authCalls) != 1 || mock2.authCalls[0] != "self" {
		t.Fatalf("auth calls = %v", mock2.authCalls)
	}
}

func TestAuthFirstSatisfiable(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithEnv(t, ws, map[string]string{"K2": "v"}, "")
	mock := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{
		envVarMethod("env-a", "K1"),       // unsatisfiable: K1 missing
		envVarMethod("env-b", "K2", "K3"), // unsatisfiable: K3 missing
		envVarMethod("env-c", "K2"),       // satisfiable
	}}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(mock.authCalls) != 1 || mock.authCalls[0] != "env-c" {
		t.Fatalf("auth calls = %v, want [env-c]", mock.authCalls)
	}
}

func TestAuthNoSatisfiableMethodListsReasons(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithEnv(t, ws, nil, "")
	mock := &mockExternalAgent{authMethods: []acpsdk.AuthMethod{
		envVarMethod("env", "K1"),
		terminalMethod("tui"),
	}}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	_, err := m.RunAgent(context.Background(), "ext", "hi")
	if err == nil || !strings.Contains(err.Error(), "no advertised method is satisfiable") {
		t.Fatalf("expected unsatisfiable error, got %v", err)
	}
	if !strings.Contains(err.Error(), "K1") || !strings.Contains(err.Error(), "terminal_policy") {
		t.Fatalf("error should carry per-method reasons: %v", err)
	}
}

func TestAuthRPCFailureIsDescriptive(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithEnv(t, ws, map[string]string{"K": "v"}, "")
	mock := &mockExternalAgent{
		authMethods: []acpsdk.AuthMethod{envVarMethod("env", "K")},
		authErr:     fmt.Errorf("bad credentials"),
	}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	_, err := m.RunAgent(context.Background(), "ext", "hi")
	if err == nil || !strings.Contains(err.Error(), "authenticate") ||
		!strings.Contains(err.Error(), "bad credentials") {
		t.Fatalf("expected descriptive auth failure, got %v", err)
	}
}

// TestAuthRealProcess runs auth over a real subprocess: the helper agent
// refuses session/new until authenticated, so a successful RunAgent proves
// the handshake happened.
func TestAuthRealProcess(t *testing.T) {
	if os.Getenv("CI_SKIP_REAL_PROC") == "1" {
		t.Skip("skipped")
	}
	ws := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "ext",
				Workspace: ws,
				ACP: &config.ACPAgentConfig{
					Command: os.Args[0],
					Args:    []string{"-test.run=TestHelperProcess"},
					Env: map[string]string{
						"RHIZOME_ACP_MOCK":      "1",
						"RHIZOME_ACP_MOCK_AUTH": "env_var",
						"MOCK_API_KEY":          "secret",
					},
				},
			}},
		},
	}
	reg := agent.NewAgentRegistry(cfg, nil)
	m := NewClientManager(cfg, func() *agent.AgentRegistry { return reg })
	if m == nil {
		t.Fatal("manager should exist")
	}
	defer m.Close()

	out, err := m.RunAgent(context.Background(), "ext", "hello")
	if err != nil {
		t.Fatalf("RunAgent over real process: %v", err)
	}
	if out != "Hello world" {
		t.Fatalf("got %q", out)
	}
}

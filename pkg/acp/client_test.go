package acp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/tools"
)

// --- mock external ACP agent (server side of the pipe) ---

type mockExternalAgent struct {
	conn        *acpsdk.AgentSideConnection
	onPrompt    func(ctx context.Context, conn *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error)
	sessions    []acpsdk.SessionId
	mu          sync.Mutex
	sessionN    int
	initCalls   int
	authMethods []acpsdk.AuthMethod
	authCalls   []string
	authErr     error
	authed      bool
}

func (m *mockExternalAgent) Initialize(
	_ context.Context,
	_ acpsdk.InitializeRequest,
) (acpsdk.InitializeResponse, error) {
	m.mu.Lock()
	m.initCalls++
	methods := append([]acpsdk.AuthMethod(nil), m.authMethods...)
	m.mu.Unlock()
	return acpsdk.InitializeResponse{
		ProtocolVersion:   acpsdk.ProtocolVersionNumber,
		AgentCapabilities: acpsdk.AgentCapabilities{},
		AuthMethods:       methods,
	}, nil
}

func (m *mockExternalAgent) NewSession(
	_ context.Context,
	_ acpsdk.NewSessionRequest,
) (acpsdk.NewSessionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.authMethods) > 0 && !m.authed {
		return acpsdk.NewSessionResponse{}, fmt.Errorf("session/new before authenticate")
	}
	m.sessionN++
	sid := acpsdk.SessionId(fmt.Sprintf("ext-sess-%d", m.sessionN))
	m.sessions = append(m.sessions, sid)
	return acpsdk.NewSessionResponse{SessionId: sid}, nil
}

func (m *mockExternalAgent) Prompt(
	ctx context.Context,
	params acpsdk.PromptRequest,
) (acpsdk.PromptResponse, error) {
	if m.onPrompt != nil {
		return m.onPrompt(ctx, m.conn, params)
	}
	// Default: stream two chunks then end_turn.
	_ = m.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: params.SessionId,
		Update:    acpsdk.UpdateAgentMessageText("Hello"),
	})
	_ = m.conn.SessionUpdate(ctx, acpsdk.SessionNotification{
		SessionId: params.SessionId,
		Update:    acpsdk.UpdateAgentMessageText(" world"),
	})
	return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
}

func (m *mockExternalAgent) Cancel(_ context.Context, _ acpsdk.CancelNotification) error {
	return nil
}

func (m *mockExternalAgent) CloseSession(
	_ context.Context,
	_ acpsdk.CloseSessionRequest,
) (acpsdk.CloseSessionResponse, error) {
	return acpsdk.CloseSessionResponse{}, nil
}

func (m *mockExternalAgent) ListSessions(
	_ context.Context,
	_ acpsdk.ListSessionsRequest,
) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, nil
}

func (m *mockExternalAgent) ResumeSession(
	_ context.Context,
	_ acpsdk.ResumeSessionRequest,
) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, nil
}

func (m *mockExternalAgent) Authenticate(
	_ context.Context,
	req acpsdk.AuthenticateRequest,
) (acpsdk.AuthenticateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authCalls = append(m.authCalls, req.MethodId)
	if m.authErr != nil {
		return acpsdk.AuthenticateResponse{}, m.authErr
	}
	m.authed = true
	return acpsdk.AuthenticateResponse{}, nil
}

func (m *mockExternalAgent) Logout(
	_ context.Context,
	_ acpsdk.LogoutRequest,
) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, nil
}

func (m *mockExternalAgent) SetSessionMode(
	_ context.Context,
	_ acpsdk.SetSessionModeRequest,
) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, nil
}

func (m *mockExternalAgent) SetSessionConfigOption(
	_ context.Context,
	_ acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, nil
}

// --- harness ---

// pipedManager returns a ClientManager whose dial creates an in-process
// agent connected by io.Pipe pairs through the real SDK transport.
func pipedManager(
	t *testing.T,
	cfg *config.Config,
	registry *agent.AgentRegistry,
	mock *mockExternalAgent,
) *ClientManager {
	t.Helper()
	m := &ClientManager{
		cfg:        cfg,
		registry:   func() *agent.AgentRegistry { return registry },
		policy:     normalizeClientPolicy(cfg.ACP.Client.PermissionPolicy),
		termPolicy: normalizeTerminalPolicy(cfg.ACP.Client.TerminalPolicy),
		procs:      make(map[string]*agentProcess),
	}
	m.dial = func(_ context.Context, agentID string, inst *agent.AgentInstance) (*agentProcess, error) {
		c2aR, c2aW := io.Pipe()
		a2cR, a2cW := io.Pipe()

		agentConn := acpsdk.NewAgentSideConnection(mock, a2cW, c2aR)
		mock.conn = agentConn

		handler := &clientHandler{
			agentID:   agentID,
			policy:    m.policy,
			workspace: m.sessionCwd(inst),
			restrict:  cfg.Agents.Defaults.RestrictToWorkspace,
			sessions:  make(map[acpsdk.SessionId]*sessionBuffer),
		}
		clientConn := acpsdk.NewClientSideConnection(handler, c2aW, a2cR)

		// Perform the same initialize + auth handshake spawn() does.
		initResp, err := clientConn.Initialize(context.Background(), acpsdk.InitializeRequest{
			ProtocolVersion: acpsdk.ProtocolVersionNumber,
			ClientCapabilities: acpsdk.ClientCapabilities{
				Fs: acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("init: %w", err)
		}
		if err := m.authenticate(context.Background(), agentID, inst, clientConn, initResp.AuthMethods); err != nil {
			_ = c2aW.Close()
			_ = a2cW.Close()
			return nil, err
		}

		return &agentProcess{
			conn:    clientConn,
			handler: handler,
			kill:    func() { _ = c2aW.Close(); _ = a2cW.Close() },
		}, nil
	}
	return m
}

func acpBoundRegistry(t *testing.T, workspace string) *agent.AgentRegistry {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "ext", Workspace: workspace, ACP: &config.ACPAgentConfig{Command: "mock"}},
				{ID: "local1", Workspace: workspace},
			},
		},
	}
	return agent.NewAgentRegistry(cfg, nil)
}

// --- tests ---

func TestClientManagerNilWithoutACPAgents(t *testing.T) {
	reg := agent.NewAgentRegistry(&config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{ID: "plain"}},
		},
	}, nil)
	if m := NewClientManager(&config.Config{}, func() *agent.AgentRegistry { return reg }); m != nil {
		t.Fatal("expected nil manager when no acp-bound agents exist")
	}
}

func TestRunAgentStreamsText(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistry(t, ws)
	mock := &mockExternalAgent{}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	out, err := m.RunAgent(context.Background(), "ext", "say hi")
	if err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if out != "Hello world" {
		t.Fatalf("got %q, want %q", out, "Hello world")
	}
	if len(mock.sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(mock.sessions))
	}
}

func TestRunAgentRejectsNonACPAgent(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistry(t, ws)
	m := pipedManager(t, &config.Config{}, reg, &mockExternalAgent{})
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "local1", "hi"); err == nil {
		t.Fatal("expected error for non-ACP agent")
	}
	if _, err := m.RunAgent(context.Background(), "nope", "hi"); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestRunAgentStopReasons(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistry(t, ws)

	cases := []struct {
		reason   acpsdk.StopReason
		wantErr  string
		wantText string
	}{
		{acpsdk.StopReasonRefusal, "refused", ""},
		{acpsdk.StopReasonMaxTokens, "stopped early", "partial"},
	}
	for _, tc := range cases {
		mock := &mockExternalAgent{
			onPrompt: func(ctx context.Context, conn *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
				if tc.wantText != "" {
					_ = conn.SessionUpdate(ctx, acpsdk.SessionNotification{
						SessionId: p.SessionId, Update: acpsdk.UpdateAgentMessageText(tc.wantText),
					})
				}
				return acpsdk.PromptResponse{StopReason: tc.reason}, nil
			},
		}
		m := pipedManager(t, &config.Config{}, reg, mock)
		out, err := m.RunAgent(context.Background(), "ext", "hi")
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("reason %s: got err %v, want %q", tc.reason, err, tc.wantErr)
		}
		if tc.wantText != "" && !strings.Contains(out, tc.wantText) {
			t.Fatalf("reason %s: got text %q, want prefix %q", tc.reason, out, tc.wantText)
		}
		m.Close()
	}
}

func TestPermissionPolicyDeny(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistry(t, ws)

	var outcome acpsdk.RequestPermissionOutcome
	mock := &mockExternalAgent{
		onPrompt: func(ctx context.Context, conn *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			resp, err := conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
				SessionId: p.SessionId,
				ToolCall:  acpsdk.ToolCallUpdate{ToolCallId: "tc1", Title: acpsdk.Ptr("rm -rf")},
				Options: []acpsdk.PermissionOption{
					{Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow", OptionId: "a1"},
					{Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "r1"},
				},
			})
			if err != nil {
				return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, err
			}
			outcome = resp.Outcome
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	cfg := &config.Config{} // empty policy → deny
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "do it"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if outcome.Selected == nil || outcome.Selected.OptionId != "r1" {
		t.Fatalf("deny policy: expected reject option selected, got %+v", outcome)
	}
}

func TestPermissionPolicyAllowReadOnly(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistry(t, ws)

	var kinds []acpsdk.ToolKind
	var outcomes []acpsdk.RequestPermissionOutcome
	mock := &mockExternalAgent{
		onPrompt: func(ctx context.Context, conn *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			for _, k := range []acpsdk.ToolKind{acpsdk.ToolKindRead, acpsdk.ToolKindExecute} {
				resp, err := conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
					SessionId: p.SessionId,
					ToolCall: acpsdk.ToolCallUpdate{
						ToolCallId: acpsdk.ToolCallId("tc-" + string(k)),
						Title:      acpsdk.Ptr(string(k)),
						Kind:       &k,
					},
					Options: []acpsdk.PermissionOption{
						{Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "Allow", OptionId: "a1"},
						{Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "r1"},
					},
				})
				if err != nil {
					return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, err
				}
				kinds = append(kinds, k)
				outcomes = append(outcomes, resp.Outcome)
			}
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	cfg := &config.Config{}
	cfg.ACP.Client.PermissionPolicy = "allow-read-only"
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "do it"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 permission outcomes, got %d", len(outcomes))
	}
	if outcomes[0].Selected == nil || outcomes[0].Selected.OptionId != "a1" {
		t.Fatalf("read kind should be allowed, got %+v", outcomes[0])
	}
	if outcomes[1].Selected == nil || outcomes[1].Selected.OptionId != "r1" {
		t.Fatalf("execute kind should be rejected, got %+v", outcomes[1])
	}
}

func TestReadTextFileSandboxed(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "ok.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := acpBoundRegistry(t, ws)
	var got, outsideErr string
	mock := &mockExternalAgent{
		onPrompt: func(ctx context.Context, conn *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
			resp, err := conn.ReadTextFile(ctx, acpsdk.ReadTextFileRequest{
				SessionId: p.SessionId,
				Path:      filepath.Join(ws, "ok.txt"),
			})
			if err != nil {
				return acpsdk.PromptResponse{}, err
			}
			got = resp.Content
			_, err = conn.ReadTextFile(ctx, acpsdk.ReadTextFileRequest{
				SessionId: p.SessionId,
				Path:      outside,
			})
			if err != nil {
				outsideErr = err.Error()
			}
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
		},
	}
	cfg := &config.Config{}
	cfg.Agents.Defaults.RestrictToWorkspace = true
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "read"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if got != "inside" {
		t.Fatalf("got %q, want %q", got, "inside")
	}
	if outsideErr == "" {
		t.Fatal("expected sandbox rejection for outside path")
	}
}

func TestSpawnerRoutesACPAndLocal(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistry(t, ws)
	mock := &mockExternalAgent{}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	local := &recordingSpawner{}
	sp := NewSpawner(m, func() *agent.AgentRegistry { return reg }, local)

	res, err := sp.SpawnSubTurn(context.Background(), tools.SubTurnConfig{
		TargetAgentID: "ext",
		SystemPrompt:  "task one",
	})
	if err != nil {
		t.Fatalf("SpawnSubTurn acp: %v", err)
	}
	if res == nil || res.ForLLM != "Hello world" {
		t.Fatalf("got %+v, want Hello world", res)
	}

	_, err = sp.SpawnSubTurn(context.Background(), tools.SubTurnConfig{
		TargetAgentID: "local1",
		SystemPrompt:  "task two",
	})
	if err != nil {
		t.Fatalf("SpawnSubTurn local: %v", err)
	}
	if local.calls != 1 {
		t.Fatalf("expected local spawner to be called once, got %d", local.calls)
	}
}

func TestSpawnerNilClientPassthrough(t *testing.T) {
	local := &recordingSpawner{}
	sp := NewSpawner(nil, nil, local)
	if sp != tools.SubTurnSpawner(local) {
		t.Fatal("nil client should return local spawner unchanged")
	}
}

func TestRunRemoteDelegatesToACP(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistry(t, ws)
	mock := &mockExternalAgent{}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	inst, _ := reg.GetAgent("ext")
	out, err := m.RunRemote(context.Background(), inst, agent.RemoteDispatchRequest{
		AgentID: "ext",
		Prompt:  "remote task",
	})
	if err != nil {
		t.Fatalf("RunRemote: %v", err)
	}
	if out != "Hello world" {
		t.Fatalf("got %q", out)
	}
}

func TestRunAgentProcessReuse(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistry(t, ws)
	mock := &mockExternalAgent{}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	for i := 0; i < 2; i++ {
		if _, err := m.RunAgent(context.Background(), "ext", fmt.Sprintf("t%d", i)); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if mock.initCalls != 1 {
		t.Fatalf("expected process reuse (1 init), got %d", mock.initCalls)
	}
	if len(mock.sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(mock.sessions))
	}
}

// TestHelperProcess is the mock ACP agent subprocess. The parent re-execs
// the test binary with RHIZOME_ACP_MOCK=1; this "test" serves ACP over
// stdio and blocks until the client disconnects.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("RHIZOME_ACP_MOCK") != "1" {
		return
	}
	mock := &mockExternalAgent{}
	// RHIZOME_ACP_MOCK_AUTH=env_var makes the helper advertise an env_var
	// method requiring MOCK_API_KEY and refuse session/new until the
	// client authenticates.
	if os.Getenv("RHIZOME_ACP_MOCK_AUTH") == "env_var" {
		mock.authMethods = []acpsdk.AuthMethod{{EnvVar: &acpsdk.AuthMethodEnvVarInline{
			Id: "env", Name: "env", Type: "env_var",
			Vars: []acpsdk.AuthEnvVar{{Name: "MOCK_API_KEY"}},
		}}}
	}
	conn := acpsdk.NewAgentSideConnection(mock, os.Stdout, os.Stdin)
	mock.conn = conn
	<-conn.Done()
	os.Exit(0)
}

func TestRunAgentRealProcess(t *testing.T) {
	ws := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "ext",
				Workspace: ws,
				ACP: &config.ACPAgentConfig{
					Command: os.Args[0],
					Args:    []string{"-test.run=TestHelperProcess"},
					Env:     map[string]string{"RHIZOME_ACP_MOCK": "1"},
				},
			}},
		},
	}
	reg := agent.NewAgentRegistry(cfg, nil)
	m := NewClientManager(cfg, func() *agent.AgentRegistry { return reg })
	if m == nil {
		t.Fatal("manager should exist for acp-bound agent")
	}
	defer m.Close()

	out, err := m.RunAgent(context.Background(), "ext", "hello")
	if err != nil {
		t.Fatalf("RunAgent over real process: %v", err)
	}
	if out != "Hello world" {
		t.Fatalf("got %q, want %q", out, "Hello world")
	}
}

type recordingSpawner struct{ calls int }

func (r *recordingSpawner) SpawnSubTurn(
	_ context.Context,
	_ tools.SubTurnConfig,
) (*tools.ToolResult, error) {
	r.calls++
	return tools.NewToolResult("local result"), nil
}

var _ = time.Second // keep time import for future timeout tests

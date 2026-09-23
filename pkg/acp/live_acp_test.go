// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package acp

// Live acceptance drive for the external-ACP spine (v0.13.0 Tracks 85–88)
// against a real `rhizome acp` subprocess — a genuine external agent over
// NDJSON stdio, exercising spawn → initialize → session/new → set_mode →
// set_config_option → prompt → session/load end-to-end. The driven
// instance's LLM is a local mock OpenAI endpoint, so the turn is real wire
// work without external credentials.
//
// Gated on RHIZOME_LIVE_ACP=1 — it builds the rhizome binary and binds a
// loopback port, so it is not for CI:
//
//	RHIZOME_LIVE_ACP=1 go test -tags goolm,stdjson -run TestLiveRhizome ./pkg/acp -v
//
// (An earlier revision targeted `gemini --acp`; the CLI's OAuth re-auth
// blocks on a browser prompt in non-interactive contexts, making it
// unusable as a CI-style drive.)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
)

const liveACPEnvVar = "RHIZOME_LIVE_ACP"

// ── Built rhizome binary (once per test run) ─────────────────────────────

var (
	liveBinOnce sync.Once
	liveBinPath string
	errLiveBin  error
)

func liveRhizomeBinary(t *testing.T) string {
	t.Helper()
	if os.Getenv(liveACPEnvVar) == "" {
		t.Skipf("live ACP drive skipped (set %s=1 to run)", liveACPEnvVar)
	}
	liveBinOnce.Do(func() {
		_, src, _, _ := runtime.Caller(0)
		repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(src)))
		out := filepath.Join(os.TempDir(), "rhizome-live-acp", "rhizome.exe")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			errLiveBin = err
			return
		}
		cmd := exec.Command(
			"go", "build", "-tags", "goolm,stdjson", "-o", out, "./cmd/rhizome")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			errLiveBin = fmt.Errorf("go build: %w\n%s", err, b)
			return
		}
		liveBinPath = out
	})
	require.NoError(t, errLiveBin)
	return liveBinPath
}

// ── Mock OpenAI-compatible backend ───────────────────────────────────────

// liveMockLLM serves POST /v1/chat/completions (stream + non-stream) and
// records the requested wire model so tests can prove which model_list
// entry served each session.
type liveMockLLM struct {
	srv       *httptest.Server
	lastModel atomic.Value // string
}

func newLiveMockLLM(t *testing.T) *liveMockLLM {
	t.Helper()
	m := &liveMockLLM{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m.lastModel.Store(req.Model)
		content := "acp-live-drive-ok via " + req.Model
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\","+
				"\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", content)
			fmt.Fprintf(w, "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\","+
				"\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],"+
				"\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n")
			fmt.Fprintf(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"x","object":"chat.completion","choices":[{"index":0,`+
			`"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, content)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *liveMockLLM) apiBase() string { return m.srv.URL + "/v1" }
func (m *liveMockLLM) model() string {
	if v := m.lastModel.Load(); v != nil {
		return v.(string)
	}
	return ""
}

// liveDrivenHome writes a minimal config.json for the driven `rhizome acp`
// instance: two mock model_list entries (mock-a default, mock-b for the
// per-session switch) and an isolated workspace.
func liveDrivenHome(t *testing.T, apiBase string) (home, workspace string) {
	t.Helper()
	home = t.TempDir()
	workspace = t.TempDir()
	entry := func(name, model string) map[string]any {
		return map[string]any{
			"model_name": name,
			"provider":   "openai",
			"model":      model,
			"api_base":   apiBase,
			"api_keys":   []string{"test-key"},
			"enabled":    true,
		}
	}
	cfg := map[string]any{
		"version": config.CurrentVersion,
		"model_list": []map[string]any{
			entry("mock-a", "mock/model-a"),
			entry("mock-b", "mock/model-b"),
		},
		"agents": map[string]any{
			"defaults": map[string]any{
				"model_name": "mock-a",
				"workspace":  workspace,
			},
		},
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t,
		os.WriteFile(filepath.Join(home, "config.json"), data, 0o600))
	return home, workspace
}

func liveClientManager(t *testing.T, home string, sessionMode string) *ClientManager {
	t.Helper()
	bin := liveRhizomeBinary(t)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID: "peer",
				ACP: &config.ACPAgentConfig{
					Command:     bin,
					Args:        []string{"acp"},
					Env:         map[string]string{config.EnvHome: home},
					Cwd:         t.TempDir(),
					SessionMode: sessionMode,
				},
			}},
		},
	}
	reg := agent.NewAgentRegistry(cfg, nil)
	m := NewClientManager(cfg, func() *agent.AgentRegistry { return reg })
	require.NotNil(t, m)
	t.Cleanup(m.Close)
	return m
}

// TestLiveRhizomeACPPrompt drives a real delegation through ClientManager:
// spawn → initialize → session/new → session/prompt → mocked LLM → text.
func TestLiveRhizomeACPPrompt(t *testing.T) {
	mock := newLiveMockLLM(t)
	home, _ := liveDrivenHome(t, mock.apiBase())
	m := liveClientManager(t, home, sessionModeOneshot)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	out, err := m.RunAgent(ctx, "peer", "ping")
	require.NoError(t, err)
	assert.Contains(t, out, "acp-live-drive-ok")
	assert.Equal(t, "mock/model-a", mock.model(),
		"default model should serve the turn")
	t.Logf("prompt reply: %q", out)
}

// TestLiveRhizomeACPPersistentSession verifies session_mode=persistent
// reuses one ACP session across delegations and remembers it in
// m.sessions for session/load.
func TestLiveRhizomeACPPersistentSession(t *testing.T) {
	mock := newLiveMockLLM(t)
	home, _ := liveDrivenHome(t, mock.apiBase())
	m := liveClientManager(t, home, sessionModePersistent)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, err := m.RunAgent(ctx, "peer", "first")
	require.NoError(t, err)
	proc := m.procs["peer"]
	require.NotNil(t, proc)
	sid1 := proc.sessionID
	require.NotEmpty(t, sid1)

	_, err = m.RunAgent(ctx, "peer", "second")
	require.NoError(t, err)
	assert.Equal(t, sid1, proc.sessionID,
		"persistent mode must reuse the live session")
	assert.Equal(t, sid1, m.sessions["peer"],
		"session id must be remembered for session/load")
	t.Logf("persistent session reused: %s", sid1)
}

// TestLiveRhizomeACPSessionMachinery drives the protocol surface directly
// over the wire: advertised modes, the model_list config option, mid-session
// mode switch, per-session model override, and session/load — the Track 93
// acceptance items that need a real agent rather than a pipe double.
func TestLiveRhizomeACPSessionMachinery(t *testing.T) {
	mock := newLiveMockLLM(t)
	home, workspace := liveDrivenHome(t, mock.apiBase())
	bin := liveRhizomeBinary(t)

	proc := exec.Command(bin, "acp")
	proc.Dir = workspace
	proc.Env = append(os.Environ(), config.EnvHome+"="+home)
	stdin, err := proc.StdinPipe()
	require.NoError(t, err)
	stdout, err := proc.StdoutPipe()
	require.NoError(t, err)
	stderr, err := proc.StderrPipe()
	require.NoError(t, err)
	require.NoError(t, proc.Start())
	defer func() { _ = proc.Process.Kill(); _, _ = proc.Process.Wait() }()
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				t.Logf("driven stderr: %s", strings.TrimSpace(string(buf[:n])))
			}
			if err != nil {
				return
			}
		}
	}()

	handler := &clientHandler{
		agentID:  "peer",
		policy:   ClientPolicyAllow, // serve fs/permission requests if any arise
		sessions: make(map[acpsdk.SessionId]*sessionBuffer),
	}
	conn := acpsdk.NewClientSideConnection(handler, stdin, stdout)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// initialize — real handshake against a real process.
	initResp, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo:      &acpsdk.Implementation{Name: "rhizome-live", Version: "test"},
		ClientCapabilities: acpsdk.ClientCapabilities{
			Fs:       acpsdk.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
			Terminal: false,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, acpsdk.ProtocolVersionNumber, int(initResp.ProtocolVersion))
	assert.True(t, initResp.AgentCapabilities.LoadSession,
		"rhizome acp advertises loadSession when the session store is writable")
	assert.Empty(t, initResp.AuthMethods)
	t.Logf("initialize: protocol=%d caps=%+v auth=%v",
		initResp.ProtocolVersion, initResp.AgentCapabilities, initResp.AuthMethods)

	// session/new — modes + model_list config option advertised.
	sess, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        workspace,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)
	require.NotNil(t, sess.Modes)
	modeIDs := make([]string, 0, len(sess.Modes.AvailableModes))
	for _, md := range sess.Modes.AvailableModes {
		modeIDs = append(modeIDs, string(md.Id))
	}
	assert.Contains(t, modeIDs, string(modeAsk))
	assert.Contains(t, modeIDs, string(modeAuto))
	assert.Contains(t, modeIDs, string(modeReadOnly))
	t.Logf("session/new: id=%s modes=%v current=%s",
		sess.SessionId, modeIDs, sess.Modes.CurrentModeId)

	var modelOpt *acpsdk.SessionConfigOptionSelect
	for i := range sess.ConfigOptions {
		if sess.ConfigOptions[i].Select != nil && sess.ConfigOptions[i].Select.Id == modelConfigID {
			modelOpt = sess.ConfigOptions[i].Select
		}
	}
	require.NotNil(t, modelOpt, "model config option advertised")
	values := []string{}
	if modelOpt.Options.Ungrouped != nil {
		for _, v := range *modelOpt.Options.Ungrouped {
			values = append(values, string(v.Value))
		}
	}
	assert.Contains(t, values, "mock-a")
	assert.Contains(t, values, "mock-b")
	t.Logf("model option: current=%q values=%v", modelOpt.CurrentValue, values)

	// Mid-session mode switch — changes the session's effective posture.
	_, err = conn.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{
		SessionId: sess.SessionId,
		ModeId:    modeReadOnly,
	})
	require.NoError(t, err, "mid-session switch to read-only")
	_, err = conn.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{
		SessionId: sess.SessionId,
		ModeId:    acpsdk.SessionModeId("bogus-mode"),
	})
	assert.Error(t, err, "unadvertised mode must be refused")
	_, err = conn.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{
		SessionId: sess.SessionId,
		ModeId:    modeAuto,
	})
	require.NoError(t, err, "switch back to auto")

	// Per-session model override: mock-b serves this session only.
	_, err = conn.SetSessionConfigOption(ctx, acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sess.SessionId,
			ConfigId:  modelConfigID,
			Value:     "mock-b",
		},
	})
	require.NoError(t, err, "set model to mock-b for this session")

	resp, err := conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sess.SessionId,
		Prompt:    []acpsdk.ContentBlock{{Text: &acpsdk.ContentBlockText{Text: "ping"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, acpsdk.StopReasonEndTurn, resp.StopReason)
	assert.Equal(t, "mock/model-b", mock.model(),
		"per-session override must route this session to mock-b")

	// A second session keeps the agent default — overrides don't leak.
	sess2, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        workspace,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err)
	_, err = conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sess2.SessionId,
		Prompt:    []acpsdk.ContentBlock{{Text: &acpsdk.ContentBlockText{Text: "ping"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "mock/model-a", mock.model(),
		"second session must keep the agent default model")

	// session/load resurrects the first session across the live conn.
	loadResp, err := conn.LoadSession(ctx, acpsdk.LoadSessionRequest{
		SessionId:  sess.SessionId,
		Cwd:        workspace,
		McpServers: []acpsdk.McpServer{},
	})
	require.NoError(t, err, "session/load on a real session store")
	require.NotNil(t, loadResp.Modes)
	t.Logf("session/load ok: id=%s modes-advertised=%d",
		sess.SessionId, len(loadResp.Modes.AvailableModes))
}

package acp

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/media"
)

// acpBoundRegistryWithACP returns a registry whose "ext" agent carries the
// given ACP binding fields.
func acpBoundRegistryWithACP(t *testing.T, workspace string, acp *config.ACPAgentConfig) *agent.AgentRegistry {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "ext", Workspace: workspace, ACP: acp},
			},
		},
	}
	return agent.NewAgentRegistry(cfg, nil)
}

func TestRunAgentPersistentSessionReuses(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command:     "mock",
		SessionMode: "persistent",
	})
	mock := &mockExternalAgent{}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "first"); err != nil {
		t.Fatalf("RunAgent 1: %v", err)
	}
	if _, err := m.RunAgent(context.Background(), "ext", "second"); err != nil {
		t.Fatalf("RunAgent 2: %v", err)
	}

	if mock.sessionN != 1 {
		t.Fatalf("expected 1 session/new for persistent mode, got %d", mock.sessionN)
	}
	if len(mock.promptReqs) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(mock.promptReqs))
	}
	if mock.promptReqs[0].SessionId != mock.promptReqs[1].SessionId {
		t.Fatal("persistent prompts should share the session id")
	}
}

func TestRunAgentOneshotNewSessionEach(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{Command: "mock"})
	mock := &mockExternalAgent{}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "one"); err != nil {
		t.Fatalf("RunAgent 1: %v", err)
	}
	if _, err := m.RunAgent(context.Background(), "ext", "two"); err != nil {
		t.Fatalf("RunAgent 2: %v", err)
	}
	if mock.sessionN != 2 {
		t.Fatalf("expected 2 session/new for oneshot mode, got %d", mock.sessionN)
	}
	if mock.closeCalls != 2 {
		t.Fatalf("expected 2 session/close for oneshot mode, got %d", mock.closeCalls)
	}
}

func TestPersistentSessionOutputPerPrompt(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command:     "mock",
		SessionMode: "persistent",
	})
	mock := &mockExternalAgent{}
	call := 0
	mock.onPrompt = func(ctx context.Context, conn *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		call++
		_ = conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: p.SessionId,
			Update:    acpsdk.UpdateAgentMessageText(fmt.Sprintf("reply-%d", call)),
		})
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	out1, err := m.RunAgent(context.Background(), "ext", "q1")
	if err != nil {
		t.Fatalf("RunAgent 1: %v", err)
	}
	out2, err := m.RunAgent(context.Background(), "ext", "q2")
	if err != nil {
		t.Fatalf("RunAgent 2: %v", err)
	}
	if out1 != "reply-1" || out2 != "reply-2" {
		t.Fatalf("per-prompt outputs wrong: %q then %q", out1, out2)
	}
}

func TestPersistentSessionLoadOnRespawn(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command:     "mock",
		SessionMode: "persistent",
	})
	mock := &mockExternalAgent{
		initCaps: acpsdk.AgentCapabilities{LoadSession: true},
	}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "first"); err != nil {
		t.Fatalf("RunAgent 1: %v", err)
	}
	firstSess := mock.sessions[0]

	// Simulate process death: drop the live process so the next run respawns.
	m.mu.Lock()
	proc := m.procs["ext"]
	m.mu.Unlock()
	if proc == nil {
		t.Fatal("expected live process")
	}
	m.dropProcess("ext", proc)

	if _, err := m.RunAgent(context.Background(), "ext", "second"); err != nil {
		t.Fatalf("RunAgent 2: %v", err)
	}
	if len(mock.loadSessionReqs) != 1 {
		t.Fatalf("expected 1 session/load, got %d", len(mock.loadSessionReqs))
	}
	if mock.loadSessionReqs[0].SessionId != firstSess {
		t.Fatalf("session/load used %q, want remembered %q",
			mock.loadSessionReqs[0].SessionId, firstSess)
	}
	if mock.sessionN != 1 {
		t.Fatalf("load should avoid a second session/new, got %d", mock.sessionN)
	}
}

func TestPersistentSessionLoadFallbackToNew(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command:     "mock",
		SessionMode: "persistent",
	})
	mock := &mockExternalAgent{
		initCaps:       acpsdk.AgentCapabilities{LoadSession: true},
		loadSessionErr: fmt.Errorf("session gone"),
	}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "first"); err != nil {
		t.Fatalf("RunAgent 1: %v", err)
	}
	m.mu.Lock()
	proc := m.procs["ext"]
	m.mu.Unlock()
	m.dropProcess("ext", proc)

	out, err := m.RunAgent(context.Background(), "ext", "second")
	if err != nil {
		t.Fatalf("RunAgent 2 should fall back to session/new: %v", err)
	}
	if out != "Hello world" {
		t.Fatalf("got %q", out)
	}
	if len(mock.loadSessionReqs) != 1 {
		t.Fatalf("expected 1 session/load attempt, got %d", len(mock.loadSessionReqs))
	}
	if mock.sessionN != 2 {
		t.Fatalf("expected fallback session/new, total %d", mock.sessionN)
	}
}

func TestPersistentSessionNoLoadCapFallsBack(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command:     "mock",
		SessionMode: "persistent",
	})
	mock := &mockExternalAgent{} // LoadSession not advertised
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "first"); err != nil {
		t.Fatalf("RunAgent 1: %v", err)
	}
	m.mu.Lock()
	proc := m.procs["ext"]
	m.mu.Unlock()
	m.dropProcess("ext", proc)

	if _, err := m.RunAgent(context.Background(), "ext", "second"); err != nil {
		t.Fatalf("RunAgent 2: %v", err)
	}
	if len(mock.loadSessionReqs) != 0 {
		t.Fatalf("session/load must not run without the advertised capability")
	}
	if mock.sessionN != 2 {
		t.Fatalf("expected fresh session/new without loadSession cap, got %d", mock.sessionN)
	}
}

func TestPersistentSessionCloseOnManagerClose(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command:     "mock",
		SessionMode: "persistent",
	})
	mock := &mockExternalAgent{}
	m := pipedManager(t, &config.Config{}, reg, mock)

	if _, err := m.RunAgent(context.Background(), "ext", "work"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	m.Close()
	if mock.closeCalls != 1 {
		t.Fatalf("expected session/close on manager Close, got %d", mock.closeCalls)
	}
}

func TestSessionModeForwarding(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command: "mock",
		Mode:    "plan",
	})
	mock := &mockExternalAgent{
		modes: &acpsdk.SessionModeState{
			AvailableModes: []acpsdk.SessionMode{
				{Id: "plan"}, {Id: "default"},
			},
			CurrentModeId: "default",
		},
	}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(mock.setModeCalls) != 1 {
		t.Fatalf("expected 1 session/set_mode, got %d", len(mock.setModeCalls))
	}
	call := mock.setModeCalls[0]
	if string(call.ModeId) != "plan" || call.SessionId != mock.sessions[0] {
		t.Fatalf("set_mode request wrong: %+v", call)
	}
}

func TestSessionModeForwardingUnadvertised(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command: "mock",
		Mode:    "bogus",
	})
	mock := &mockExternalAgent{
		modes: &acpsdk.SessionModeState{
			AvailableModes: []acpsdk.SessionMode{{Id: "plan"}},
			CurrentModeId:  "plan",
		},
	}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	out, err := m.RunAgent(context.Background(), "ext", "hi")
	if err != nil {
		t.Fatalf("unadvertised mode should warn and continue: %v", err)
	}
	if out != "Hello world" {
		t.Fatalf("got %q", out)
	}
	if len(mock.setModeCalls) != 0 {
		t.Fatalf("no set_mode expected for unadvertised id, got %d", len(mock.setModeCalls))
	}
}

func TestSessionModeForwardingNoModesAdvertised(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command: "mock",
		Mode:    "plan",
	})
	mock := &mockExternalAgent{} // Modes nil in session/new response
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("agent without modes should still serve: %v", err)
	}
	if len(mock.setModeCalls) != 0 {
		t.Fatal("no set_mode expected when agent advertises no modes")
	}
}

func TestPerBindingPermissionPolicyOverride(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command:          "mock",
		PermissionPolicy: "allow",
	})
	// Global denies; binding overrides to allow.
	cfg := &config.Config{}
	cfg.ACP.Client.PermissionPolicy = "deny"

	var outcome acpsdk.RequestPermissionResponse
	mock := &mockExternalAgent{}
	mock.onPrompt = func(ctx context.Context, conn *acpsdk.AgentSideConnection, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		resp, err := conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
			SessionId: p.SessionId,
			ToolCall:  acpsdk.ToolCallUpdate{},
			Options: []acpsdk.PermissionOption{
				{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce, Name: "allow"},
				{OptionId: "deny", Kind: acpsdk.PermissionOptionKindRejectOnce, Name: "deny"},
			},
		})
		if err != nil {
			return acpsdk.PromptResponse{}, err
		}
		outcome = resp
		_ = conn.SessionUpdate(ctx, acpsdk.SessionNotification{
			SessionId: p.SessionId,
			Update:    acpsdk.UpdateAgentMessageText("done"),
		})
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "go"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if outcome.Outcome.Selected == nil || outcome.Outcome.Selected.OptionId != "allow" {
		t.Fatalf("binding allow policy should pick allow option, got %+v", outcome)
	}
}

func TestPerBindingTerminalPolicyOverride(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{
		Command:        "mock",
		TerminalPolicy: "allow",
	})
	cfg := &config.Config{} // global terminal_policy defaults to deny

	mock := &mockExternalAgent{}
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if len(mock.initReqs) != 1 {
		t.Fatalf("expected 1 initialize, got %d", len(mock.initReqs))
	}
	if !mock.initReqs[0].ClientCapabilities.Terminal {
		t.Fatal("per-binding terminal_policy=allow should advertise terminal capability")
	}
}

func TestGlobalTerminalDenyCapsBinding(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{Command: "mock"})
	cfg := &config.Config{} // deny

	mock := &mockExternalAgent{}
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	if mock.initReqs[0].ClientCapabilities.Terminal {
		t.Fatal("terminal capability must stay off under deny")
	}
}

func TestMediaPassthroughImageBlock(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{Command: "mock"})
	mock := &mockExternalAgent{
		initCaps: acpsdk.AgentCapabilities{
			PromptCapabilities: acpsdk.PromptCapabilities{Image: true},
		},
	}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	store := media.NewFileMediaStore()
	imgPath := filepath.Join(ws, "pic.png")
	payload := []byte{0x89, 0x50, 0x4E, 0x47, 0x01, 0x02}
	if err := os.WriteFile(imgPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := store.Store(imgPath, media.MediaMeta{
		Filename:    "pic.png",
		ContentType: "image/png",
	}, "test")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	m.SetMediaStore(store)

	inst, _ := reg.GetAgent("ext")
	if _, err := m.RunRemote(context.Background(), inst, agent.RemoteDispatchRequest{
		Prompt: "describe",
		Media:  []string{ref},
	}); err != nil {
		t.Fatalf("RunRemote: %v", err)
	}
	if len(mock.promptReqs) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(mock.promptReqs))
	}
	blocks := mock.promptReqs[0].Prompt
	if len(blocks) != 2 {
		t.Fatalf("expected text+image blocks, got %d", len(blocks))
	}
	img := blocks[1].Image
	if img == nil {
		t.Fatalf("second block should be an image, got %+v", blocks[1])
	}
	if img.MimeType != "image/png" || img.Data != base64.StdEncoding.EncodeToString(payload) {
		t.Fatal("image block payload mismatch")
	}
}

func TestMediaPassthroughDegradesWithoutImageCap(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{Command: "mock"})
	mock := &mockExternalAgent{} // no image cap
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	store := media.NewFileMediaStore()
	imgPath := filepath.Join(ws, "pic.png")
	if err := os.WriteFile(imgPath, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := store.Store(imgPath, media.MediaMeta{
		Filename:    "pic.png",
		ContentType: "image/png",
	}, "test")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	m.SetMediaStore(store)

	inst, _ := reg.GetAgent("ext")
	if _, err := m.RunRemote(context.Background(), inst, agent.RemoteDispatchRequest{
		Prompt: "describe",
		Media:  []string{ref},
	}); err != nil {
		t.Fatalf("RunRemote: %v", err)
	}
	blocks := mock.promptReqs[0].Prompt
	if len(blocks) != 1 || blocks[0].Text == nil {
		t.Fatalf("expected single degraded text block, got %+v", blocks)
	}
	if !strings.Contains(blocks[0].Text.Text, "pic.png") || !strings.Contains(blocks[0].Text.Text, ref) {
		t.Fatalf("degraded text should reference the attachment, got %q", blocks[0].Text.Text)
	}
}

func TestMediaPassthroughDegradesNonImage(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{Command: "mock"})
	mock := &mockExternalAgent{
		initCaps: acpsdk.AgentCapabilities{
			PromptCapabilities: acpsdk.PromptCapabilities{Image: true},
		},
	}
	m := pipedManager(t, &config.Config{}, reg, mock)
	defer m.Close()

	store := media.NewFileMediaStore()
	docPath := filepath.Join(ws, "doc.pdf")
	if err := os.WriteFile(docPath, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref, err := store.Store(docPath, media.MediaMeta{
		Filename:    "doc.pdf",
		ContentType: "application/pdf",
	}, "test")
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	m.SetMediaStore(store)

	inst, _ := reg.GetAgent("ext")
	if _, err := m.RunRemote(context.Background(), inst, agent.RemoteDispatchRequest{
		Prompt: "read",
		Media:  []string{ref},
	}); err != nil {
		t.Fatalf("RunRemote: %v", err)
	}
	blocks := mock.promptReqs[0].Prompt
	if len(blocks) != 1 || blocks[0].Text == nil {
		t.Fatalf("non-image media should degrade to text, got %+v", blocks)
	}
	if !strings.Contains(blocks[0].Text.Text, "doc.pdf") {
		t.Fatalf("degraded text should name the file, got %q", blocks[0].Text.Text)
	}
}

func TestMCPForwardingHonorsCaps(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{Command: "mock"})
	mock := &mockExternalAgent{
		initCaps: acpsdk.AgentCapabilities{
			McpCapabilities: acpsdk.McpCapabilities{Http: true}, // sse NOT supported
		},
	}
	cfg := &config.Config{}
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"local-tool": {Enabled: true, Command: "tool-bin", Args: []string{"--x"}},
		"sse-srv":    {Enabled: true, Type: "sse", URL: "http://h/sse"},
		"http-srv":   {Enabled: true, Type: "http", URL: "http://h/mcp"},
		"off-srv":    {Enabled: false, Command: "off"},
	}
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	reqs := mock.newSessionReqs
	if len(reqs) != 1 {
		t.Fatalf("expected 1 session/new, got %d", len(reqs))
	}
	got := map[string]string{}
	for _, srv := range reqs[0].McpServers {
		switch {
		case srv.Stdio != nil:
			got[srv.Stdio.Name] = "stdio"
		case srv.Http != nil:
			got[srv.Http.Name] = "http"
		case srv.Sse != nil:
			got[srv.Sse.Name] = "sse"
		}
	}
	if got["local-tool"] != "stdio" {
		t.Fatalf("stdio server missing or wrong variant: %v", got)
	}
	if got["http-srv"] != "http" {
		t.Fatalf("http server should forward under advertised cap: %v", got)
	}
	if _, ok := got["sse-srv"]; ok {
		t.Fatalf("sse server must be refused without the cap: %v", got)
	}
	if _, ok := got["off-srv"]; ok {
		t.Fatal("disabled server must not forward")
	}
}

func TestMCPForwardingStdioEnv(t *testing.T) {
	ws := t.TempDir()
	reg := acpBoundRegistryWithACP(t, ws, &config.ACPAgentConfig{Command: "mock"})
	mock := &mockExternalAgent{}

	envFile := filepath.Join(ws, "srv.env")
	if err := os.WriteFile(envFile, []byte("FROM_FILE=file-val\nTOKEN=tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"srv": {
			Enabled: true,
			Command: "bin",
			Env:     map[string]string{"INLINE": "iv", "TOKEN": "wins"},
			EnvFile: envFile,
		},
	}
	m := pipedManager(t, cfg, reg, mock)
	defer m.Close()

	if _, err := m.RunAgent(context.Background(), "ext", "hi"); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}
	servers := mock.newSessionReqs[0].McpServers
	if len(servers) != 1 || servers[0].Stdio == nil {
		t.Fatalf("expected 1 stdio server, got %+v", servers)
	}
	env := map[string]string{}
	for _, v := range servers[0].Stdio.Env {
		env[v.Name] = v.Value
	}
	if env["INLINE"] != "iv" || env["FROM_FILE"] != "file-val" || env["TOKEN"] != "wins" {
		t.Fatalf("env merge wrong (inline should win): %v", env)
	}
}

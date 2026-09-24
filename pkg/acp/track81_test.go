package acp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
	mcpgo "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/mcp"
	"github.com/stpinkie/rhizome/pkg/providers"
	"github.com/stpinkie/rhizome/pkg/tools"
)

// --- session store ---

func TestSessionStoreRoundTrip(t *testing.T) {
	path := t.TempDir() + "/acp-sessions.json"
	store, err := OpenSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSessionStore: %v", err)
	}
	rec := SessionRecord{
		SessionID:  "s1",
		AgentID:    "main",
		SessionKey: "agent:main:acp:s1",
		Cwd:        "/tmp/ws",
	}
	if err := store.PutSession(rec); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	if err := store.UpdateDecisions("s1", []string{"write_file"}, []string{"exec"}); err != nil {
		t.Fatalf("UpdateDecisions: %v", err)
	}

	reopened, err := OpenSessionStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.GetSession("s1")
	if !ok {
		t.Fatal("record not found after reopen")
	}
	if got.SessionKey != rec.SessionKey || got.Cwd != "/tmp/ws" {
		t.Fatalf("record = %+v", got)
	}
	if len(got.AllowAlways) != 1 || got.AllowAlways[0] != "write_file" {
		t.Fatalf("allow_always = %v", got.AllowAlways)
	}
	if len(got.DenyAlways) != 1 || got.DenyAlways[0] != "exec" {
		t.Fatalf("deny_always = %v", got.DenyAlways)
	}
	if err := reopened.RemoveSession("s1"); err != nil {
		t.Fatalf("RemoveSession: %v", err)
	}
	if _, ok := reopened.GetSession("s1"); ok {
		t.Fatal("record should be removed")
	}
}

func TestSessionStoreEvictsOldest(t *testing.T) {
	path := t.TempDir() + "/acp-sessions.json"
	store, err := OpenSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSessionStore: %v", err)
	}
	for i := 0; i < maxSessionRecords+5; i++ {
		id := fmt.Sprintf("s%04d", i)
		if err := store.PutSession(SessionRecord{
			SessionID:  id,
			AgentID:    "main",
			SessionKey: "k" + id,
		}); err != nil {
			t.Fatalf("PutSession %d: %v", i, err)
		}
	}
	reopened, err := OpenSessionStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	count := 0
	for i := 0; i < maxSessionRecords+5; i++ {
		if _, ok := reopened.GetSession(fmt.Sprintf("s%04d", i)); ok {
			count++
		}
	}
	// the earliest ids (small i) were evicted — only the newest maxSessionRecords remain
	if count != maxSessionRecords {
		t.Fatalf("expected %d records after eviction, got %d", maxSessionRecords, count)
	}
	if _, ok := reopened.GetSession("s0000"); ok {
		t.Fatal("oldest record should have been evicted")
	}
	if _, ok := reopened.GetSession(fmt.Sprintf("s%04d", maxSessionRecords+4)); !ok {
		t.Fatal("newest record should be present")
	}
}

// --- session/load ---

func testServerWithStore(t *testing.T, opts Options) (*Server, *fakeRunner, *fakeConn, *SessionStore) {
	t.Helper()
	store, err := OpenSessionStore(t.TempDir() + "/acp-sessions.json")
	if err != nil {
		t.Fatalf("OpenSessionStore: %v", err)
	}
	opts.Sessions = store
	srv, runner, conn := testServer(t, opts)
	return srv, runner, conn, store
}

func TestInitializeAdvertisesLoadSession(t *testing.T) {
	srv, _, _, store := testServerWithStore(t, Options{})
	_ = store
	resp, err := srv.Initialize(context.Background(), acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !resp.AgentCapabilities.LoadSession {
		t.Fatal("loadSession should be advertised when a store is configured")
	}
}

func TestLoadSessionReplaysHistory(t *testing.T) {
	srv, _, conn, store := testServerWithStore(t, Options{})
	sid := newTestSession(t, srv)
	sess, _ := srv.sessionByID(sid)

	inst, ok := srv.runner.GetRegistry().GetAgent("main")
	if !ok || inst.Sessions == nil {
		t.Fatal("default agent has no session store")
	}
	inst.Sessions.AddMessage(sess.key, "user", "earlier question")
	inst.Sessions.AddMessage(sess.key, "assistant", "earlier answer")
	inst.Sessions.AddMessage(sess.key, "system", "skip me")

	// Close the live session — the record stays for load.
	if _, err := srv.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: sid}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if _, ok := store.GetSession(string(sid)); !ok {
		t.Fatal("session record should persist after close")
	}

	if _, err := srv.LoadSession(context.Background(), acpsdk.LoadSessionRequest{
		SessionId:  sid,
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	loaded, ok := srv.sessionByID(sid)
	if !ok {
		t.Fatal("loaded session not registered")
	}
	if loaded.key != sess.key {
		t.Fatalf("loaded key = %q, want %q", loaded.key, sess.key)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	var users, agents []string
	for _, u := range conn.updates {
		if u.update.UserMessageChunk != nil && u.update.UserMessageChunk.Content.Text != nil {
			users = append(users, u.update.UserMessageChunk.Content.Text.Text)
		}
		if u.update.AgentMessageChunk != nil && u.update.AgentMessageChunk.Content.Text != nil {
			agents = append(agents, u.update.AgentMessageChunk.Content.Text.Text)
		}
	}
	if len(users) != 1 || users[0] != "earlier question" {
		t.Fatalf("replayed user msgs = %v", users)
	}
	if len(agents) != 1 || agents[0] != "earlier answer" {
		t.Fatalf("replayed agent msgs = %v", agents)
	}
}

func TestLoadSessionUnknown(t *testing.T) {
	srv, _, _, store := testServerWithStore(t, Options{})
	_ = store
	_, err := srv.LoadSession(context.Background(), acpsdk.LoadSessionRequest{
		SessionId:  "nope",
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestLoadSessionRestoresDecisions(t *testing.T) {
	srv, runner, conn, _ := testServerWithStore(t, Options{Policy: PermissionPrompt})
	sid := newTestSession(t, srv)

	conn.permOutcome = acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(optAllowAlways))
	approver := runner.approver()
	if _, err := approver.ApproveTool(context.Background(), &agent.ToolApprovalRequest{
		Context: &agent.TurnContext{
			Inbound: &bus.InboundContext{Channel: ChannelName, ChatID: string(sid)},
		},
		Tool: "write_file",
	}); err != nil {
		t.Fatalf("ApproveTool: %v", err)
	}

	if _, err := srv.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: sid}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if _, err := srv.LoadSession(context.Background(), acpsdk.LoadSessionRequest{
		SessionId:  sid,
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	loaded, _ := srv.sessionByID(sid)
	if ok, hit := loaded.cachedDecision("write_file"); !hit || !ok {
		t.Fatal("allow_always decision should be restored after load")
	}
}

// --- session MCP passthrough ---

type fakeMCPManager struct {
	mu       sync.Mutex
	servers  map[string]*mcp.ServerConnection
	closed   bool
	callArgs map[string]any
	calls    int
}

func newFakeMCPManager() *fakeMCPManager {
	return &fakeMCPManager{servers: make(map[string]*mcp.ServerConnection)}
}

func (f *fakeMCPManager) ConnectServer(
	_ context.Context,
	name string,
	cfg config.MCPServerConfig,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.servers[name] = &mcp.ServerConnection{
		Name:   name,
		Config: cfg,
		Tools: []*mcpgo.Tool{
			{Name: "echo", Description: "echo back", InputSchema: map[string]any{"type": "object"}},
		},
	}
	return nil
}

func (f *fakeMCPManager) GetServer(name string) (*mcp.ServerConnection, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.servers[name]
	return c, ok
}

func (f *fakeMCPManager) CallTool(
	_ context.Context,
	serverName, toolName string,
	arguments map[string]any,
) (*mcpgo.CallToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.callArgs = arguments
	return &mcpgo.CallToolResult{
		Content: []mcpgo.Content{&mcpgo.TextContent{Text: "ok"}},
	}, nil
}

func (f *fakeMCPManager) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func acpStdioServer(name, command string) acpsdk.McpServer {
	srv := acpsdk.McpServer{Stdio: &acpsdk.McpServerStdio{
		Name:    name,
		Command: command,
	}}
	return srv
}

func TestSessionMCPRegistersScopedTools(t *testing.T) {
	srv, _, _ := testServer(t, Options{})
	fake := newFakeMCPManager()
	srv.newMCPManager = func() sessionMCPManager { return fake }

	resp, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{acpStdioServer("fs", "fake-server")},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sid := resp.SessionId

	inst, _ := srv.runner.GetRegistry().GetAgent("main")
	names := inst.Tools.List()
	var scoped []string
	for _, n := range names {
		if strings.HasPrefix(n, "mcp_acp-") {
			scoped = append(scoped, n)
		}
	}
	if len(scoped) != 1 {
		t.Fatalf("expected 1 session tool, got %v", scoped)
	}

	// Advertised only to the owning session's chat scope.
	own := inst.Tools.ToProviderDefsForChat(string(sid))
	other := inst.Tools.ToProviderDefsForChat("someone-else")
	countIn := func(defs []providers.ToolDefinition, name string) int {
		c := 0
		for _, d := range defs {
			if d.Function.Name == name {
				c++
			}
		}
		return c
	}
	if countIn(own, scoped[0]) != 1 {
		t.Fatal("session tool missing from own defs")
	}
	if countIn(other, scoped[0]) != 0 {
		t.Fatal("session tool leaked into foreign defs")
	}

	// Execution guard: foreign chat id refused, owning chat id calls through.
	tool, ok := inst.Tools.Get(scoped[0])
	if !ok {
		t.Fatal("tool not retrievable")
	}
	bad := tool.Execute(tools.WithToolContext(context.Background(), "acp", "someone-else"), nil)
	if !bad.IsError {
		t.Fatal("foreign-scope execute should be refused")
	}
	good := tool.Execute(tools.WithToolContext(context.Background(), "acp", string(sid)),
		map[string]any{"x": 1})
	if good.IsError {
		t.Fatalf("own-scope execute failed: %v", good.ForLLM)
	}
	if fake.calls != 1 {
		t.Fatalf("expected 1 MCP call, got %d", fake.calls)
	}
}

func TestSessionMCPTeardownOnClose(t *testing.T) {
	srv, _, _ := testServer(t, Options{})
	fake := newFakeMCPManager()
	srv.newMCPManager = func() sessionMCPManager { return fake }

	resp, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{acpStdioServer("fs", "fake")},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	inst, _ := srv.runner.GetRegistry().GetAgent("main")
	before := len(inst.Tools.List())

	closeReq := acpsdk.CloseSessionRequest{SessionId: resp.SessionId}
	if _, err := srv.CloseSession(context.Background(), closeReq); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if !fake.closed {
		t.Fatal("session MCP manager should be closed on session close")
	}
	if len(inst.Tools.List()) != before-1 {
		t.Fatalf("session tools should be unregistered; have %v", inst.Tools.List())
	}
}

func TestSessionMCPRefusesNonStdio(t *testing.T) {
	srv, _, _ := testServer(t, Options{})
	fake := newFakeMCPManager()
	srv.newMCPManager = func() sessionMCPManager { return fake }

	resp, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd: t.TempDir(),
		McpServers: []acpsdk.McpServer{
			// Nested ACP stays refused — no transport for it.
			{Acp: &acpsdk.McpServerAcpInline{}},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if len(fake.servers) != 0 {
		t.Fatalf("nested-acp server should be refused, got %v", fake.servers)
	}
	inst, _ := srv.runner.GetRegistry().GetAgent("main")
	for _, n := range inst.Tools.List() {
		if strings.HasPrefix(n, "mcp_acp-") {
			t.Fatalf("no session tools should register for refused server: %s", n)
		}
	}
	_ = resp
}

// --- terminal bridge ---

func TestTerminalPolicyDeny(t *testing.T) {
	h := &clientHandler{term: nil}
	_, err := h.CreateTerminal(context.Background(), acpsdk.CreateTerminalRequest{
		SessionId: "s1", Command: "echo",
	})
	if err == nil {
		t.Fatal("expected method-not-found when terminal_policy=deny")
	}
	if normalizeTerminalPolicy("") != TerminalPolicyDeny ||
		normalizeTerminalPolicy("bogus") != TerminalPolicyDeny ||
		normalizeTerminalPolicy("allow") != TerminalPolicyAllow {
		t.Fatal("terminal policy normalization wrong")
	}
}

func TestTerminalBridgeLifecycle(t *testing.T) {
	bridge, err := newTerminalBridge(t.TempDir(), false, nil, nil)
	if err != nil {
		t.Fatalf("newTerminalBridge: %v", err)
	}
	defer bridge.close()

	sid := acpsdk.SessionId("sess-1")
	create, err := bridge.create(context.Background(), acpsdk.CreateTerminalRequest{
		SessionId: sid,
		Command:   "echo",
		Args:      []string{"hello-acp"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if create.TerminalId == "" {
		t.Fatal("empty terminal id")
	}

	exit, err := bridge.waitForExit(context.Background(), acpsdk.WaitForTerminalExitRequest{
		SessionId: sid, TerminalId: create.TerminalId,
	})
	if err != nil {
		t.Fatalf("waitForExit: %v", err)
	}
	if exit.ExitCode == nil || *exit.ExitCode != 0 {
		t.Fatalf("exit code = %v", exit.ExitCode)
	}

	out, err := bridge.output(context.Background(), acpsdk.TerminalOutputRequest{
		SessionId: sid, TerminalId: create.TerminalId,
	})
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	if !strings.Contains(out.Output, "hello-acp") {
		t.Fatalf("output = %q", out.Output)
	}
	if out.ExitStatus == nil {
		t.Fatal("expected exit status on finished terminal")
	}

	// Cross-session access refused.
	if _, err := bridge.output(context.Background(), acpsdk.TerminalOutputRequest{
		SessionId: "other", TerminalId: create.TerminalId,
	}); err == nil {
		t.Fatal("cross-session terminal access should fail")
	}

	if _, err := bridge.release(context.Background(), acpsdk.ReleaseTerminalRequest{
		SessionId: sid, TerminalId: create.TerminalId,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := bridge.output(context.Background(), acpsdk.TerminalOutputRequest{
		SessionId: sid, TerminalId: create.TerminalId,
	}); err == nil {
		t.Fatal("released terminal should be gone")
	}
}

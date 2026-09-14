package acp

import (
	"context"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/events"
)

// --- fakes ---

type fakeRunner struct {
	mu       sync.Mutex
	msgs     []bus.InboundMessage
	result   string
	err      error
	evBus    *events.EventBus
	mounted  []agent.HookRegistration
	registry *agent.AgentRegistry
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		result:   "final response",
		evBus:    events.NewBus(),
		registry: agent.NewAgentRegistry(&config.Config{}, nil),
	}
}

func (f *fakeRunner) ProcessInbound(
	_ context.Context,
	msg bus.InboundMessage,
) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, msg)
	return f.result, f.err
}

func (f *fakeRunner) RuntimeEvents() events.EventChannel { return f.evBus.Channel() }

func (f *fakeRunner) MountHook(reg agent.HookRegistration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mounted = append(f.mounted, reg)
	return nil
}

func (f *fakeRunner) GetRegistry() *agent.AgentRegistry { return f.registry }

func (f *fakeRunner) approver() agent.ToolApprover {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, reg := range f.mounted {
		if a, ok := reg.Hook.(agent.ToolApprover); ok {
			return a
		}
	}
	return nil
}

type capturedUpdate struct {
	session acpsdk.SessionId
	update  acpsdk.SessionUpdate
}

type fakeConn struct {
	mu          sync.Mutex
	updates     []capturedUpdate
	permCalls   []acpsdk.RequestPermissionRequest
	permOutcome acpsdk.RequestPermissionOutcome
	permErr     error
	updateErr   error
}

func (f *fakeConn) SessionUpdate(
	_ context.Context,
	params acpsdk.SessionNotification,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, capturedUpdate{session: params.SessionId, update: params.Update})
	return nil
}

func (f *fakeConn) RequestPermission(
	_ context.Context,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permCalls = append(f.permCalls, params)
	if f.permErr != nil {
		return acpsdk.RequestPermissionResponse{}, f.permErr
	}
	outcome := f.permOutcome
	if outcome.Selected == nil && outcome.Cancelled == nil {
		outcome = acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(optAllowOnce))
	}
	return acpsdk.RequestPermissionResponse{Outcome: outcome}, nil
}

func (f *fakeConn) messageTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, u := range f.updates {
		if u.update.AgentMessageChunk != nil && u.update.AgentMessageChunk.Content.Text != nil {
			out = append(out, u.update.AgentMessageChunk.Content.Text.Text)
		}
	}
	return out
}

// --- helpers ---

func testServer(t *testing.T, opts Options) (*Server, *fakeRunner, *fakeConn) {
	t.Helper()
	runner := newFakeRunner()
	conn := &fakeConn{}
	srv := NewServer(runner, opts)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	srv.Bind(conn)
	t.Cleanup(srv.Close)
	return srv, runner, conn
}

func newTestSession(t *testing.T, srv *Server) acpsdk.SessionId {
	t.Helper()
	resp, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return resp.SessionId
}

// --- tests ---

func TestInitializeCapabilities(t *testing.T) {
	srv, _, _ := testServer(t, Options{Version: "1.2.3"})
	resp, err := srv.Initialize(context.Background(), acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if resp.ProtocolVersion != acpsdk.ProtocolVersionNumber {
		t.Fatalf("protocol version = %v", resp.ProtocolVersion)
	}
	if resp.AgentInfo == nil || resp.AgentInfo.Name != "rhizome" || resp.AgentInfo.Version != "1.2.3" {
		t.Fatalf("agent info = %+v", resp.AgentInfo)
	}
	if resp.AgentCapabilities.LoadSession {
		t.Fatal("loadSession should be false")
	}
	if resp.AgentCapabilities.PromptCapabilities.Image {
		t.Fatal("image capability should be off without a media store")
	}
}

func TestNewSessionKeys(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "worker"})
	sid := newTestSession(t, srv)

	sess, ok := srv.sessionByID(sid)
	if !ok {
		t.Fatal("session not registered")
	}
	want := "agent:worker:acp:" + string(sid)
	if sess.key != want {
		t.Fatalf("session key = %q, want %q", sess.key, want)
	}
}

func TestNewSessionDefaultAgent(t *testing.T) {
	srv, _, _ := testServer(t, Options{})
	sid := newTestSession(t, srv)
	sess, _ := srv.sessionByID(sid)
	want := "agent:main:acp:" + string(sid)
	if sess.key != want {
		t.Fatalf("session key = %q, want %q (implicit default agent)", sess.key, want)
	}
}

func TestPromptRunsInboundPipeline(t *testing.T) {
	srv, runner, _ := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)

	resp, err := srv.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sid,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("stop reason = %v", resp.StopReason)
	}
	if len(runner.msgs) != 1 {
		t.Fatalf("expected 1 inbound message, got %d", len(runner.msgs))
	}
	msg := runner.msgs[0]
	if msg.Context.Channel != ChannelName {
		t.Fatalf("channel = %q", msg.Context.Channel)
	}
	if msg.Context.ChatID != string(sid) {
		t.Fatalf("chat id = %q", msg.Context.ChatID)
	}
	if msg.SessionKey != "agent:a1:acp:"+string(sid) {
		t.Fatalf("session key = %q", msg.SessionKey)
	}
	if msg.Content != "hello" {
		t.Fatalf("content = %q", msg.Content)
	}
}

func TestPromptFlushesUnstreamedResponse(t *testing.T) {
	srv, runner, conn := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)
	runner.result = "the answer"

	_, err := srv.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sid,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("q")},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	texts := conn.messageTexts()
	if len(texts) != 1 || texts[0] != "the answer" {
		t.Fatalf("final flush texts = %v", texts)
	}
}

func TestPromptUnknownSession(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "a1"})
	_, err := srv.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: "nope",
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("q")},
	})
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestPromptCancelled(t *testing.T) {
	srv, runner, _ := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)
	runner.err = context.Canceled

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := srv.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: sid,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("q")},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonCancelled {
		t.Fatalf("stop reason = %v, want cancelled", resp.StopReason)
	}
}

func TestStreamerDiffsAccumulated(t *testing.T) {
	srv, _, conn := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)

	streamer, ok := srv.GetStreamer(context.Background(), ChannelName, string(sid), "key")
	if !ok || streamer == nil {
		t.Fatal("no streamer for acp channel")
	}
	ctx := context.Background()
	if err := streamer.Update(ctx, "hel"); err != nil {
		t.Fatal(err)
	}
	if err := streamer.Update(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := streamer.Update(ctx, "hello"); err != nil {
		t.Fatal(err) // no-op: no growth
	}
	if err := streamer.Finalize(ctx, "hello world"); err != nil {
		t.Fatal(err)
	}
	texts := conn.messageTexts()
	want := []string{"hel", "lo", " world"}
	if len(texts) != len(want) {
		t.Fatalf("texts = %v", texts)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Fatalf("texts = %v, want %v", texts, want)
		}
	}
}

func TestStreamerReasoning(t *testing.T) {
	srv, _, conn := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)

	streamer, ok := srv.GetStreamer(context.Background(), ChannelName, string(sid), "key")
	if !ok {
		t.Fatal("no streamer")
	}
	rs, ok := streamer.(bus.ReasoningStreamer)
	if !ok {
		t.Fatal("streamer is not a ReasoningStreamer")
	}
	if err := rs.UpdateReasoning(context.Background(), "thinking"); err != nil {
		t.Fatal(err)
	}
	if err := rs.FinalizeReasoning(context.Background(), "thinking hard"); err != nil {
		t.Fatal(err)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	var thoughts []string
	for _, u := range conn.updates {
		if u.update.AgentThoughtChunk != nil && u.update.AgentThoughtChunk.Content.Text != nil {
			thoughts = append(thoughts, u.update.AgentThoughtChunk.Content.Text.Text)
		}
	}
	if len(thoughts) != 2 || thoughts[0] != "thinking" || thoughts[1] != " hard" {
		t.Fatalf("thoughts = %v", thoughts)
	}
}

func TestGetStreamerDeclinesForeignChannel(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "a1"})
	if _, ok := srv.GetStreamer(context.Background(), "telegram", "x", "k"); ok {
		t.Fatal("streamer should be declined for non-acp channel")
	}
	if _, ok := srv.GetStreamer(context.Background(), ChannelName, "unknown-session", "k"); ok {
		t.Fatal("streamer should be declined for unknown session")
	}
}

func TestToolEventsForward(t *testing.T) {
	srv, runner, conn := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)
	sess, _ := srv.sessionByID(sid)

	ctx := context.Background()
	key := sess.key
	runner.evBus.Publish(ctx, events.Event{
		Kind:  events.KindAgentToolExecStart,
		Scope: events.Scope{SessionKey: key},
		Payload: agent.ToolExecStartPayload{
			Tool:      "read_file",
			CallID:    "call-1",
			Arguments: map[string]any{"path": "/tmp/x"},
		},
	})
	runner.evBus.Publish(ctx, events.Event{
		Kind:  events.KindAgentToolExecEnd,
		Scope: events.Scope{SessionKey: key},
		Payload: agent.ToolExecEndPayload{
			Tool:    "read_file",
			CallID:  "call-1",
			IsError: false,
		},
	})
	// An event for a foreign session key must not leak.
	runner.evBus.Publish(ctx, events.Event{
		Kind:  events.KindAgentToolExecStart,
		Scope: events.Scope{SessionKey: "agent:other:cli:zzz"},
		Payload: agent.ToolExecStartPayload{
			Tool:   "exec_command",
			CallID: "call-x",
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		conn.mu.Lock()
		n := len(conn.updates)
		conn.mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.updates) != 2 {
		t.Fatalf("expected 2 tool updates, got %d (%+v)", len(conn.updates), conn.updates)
	}
	start := conn.updates[0].update.ToolCall
	if start == nil {
		t.Fatalf("first update not a tool_call: %+v", conn.updates[0].update)
	}
	if start.ToolCallId != "call-1" || start.Title != "read_file" || start.Status != acpsdk.ToolCallStatusInProgress {
		t.Fatalf("tool_call = %+v", start)
	}
	if start.Kind != acpsdk.ToolKindRead {
		t.Fatalf("kind = %v", start.Kind)
	}
	if len(start.Locations) != 1 || start.Locations[0].Path != "/tmp/x" {
		t.Fatalf("locations = %+v", start.Locations)
	}
	end := conn.updates[1].update.ToolCallUpdate
	if end == nil {
		t.Fatalf("second update not a tool_call_update: %+v", conn.updates[1].update)
	}
	if end.ToolCallId != "call-1" || end.Status == nil || *end.Status != acpsdk.ToolCallStatusCompleted {
		t.Fatalf("tool_call_update = %+v", end)
	}
}

func TestApproverPromptsAndCaches(t *testing.T) {
	srv, runner, conn := testServer(t, Options{AgentID: "a1", Policy: PermissionPrompt})
	sid := newTestSession(t, srv)

	approver := runner.approver()
	if approver == nil {
		t.Fatal("no approver mounted")
	}

	conn.permOutcome = acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(optAllowAlways))

	req := &agent.ToolApprovalRequest{
		Context: &agent.TurnContext{
			Inbound: &bus.InboundContext{Channel: ChannelName, ChatID: string(sid)},
		},
		Tool:   "write_file",
		CallID: "call-9",
	}
	d, err := approver.ApproveTool(context.Background(), req)
	if err != nil || !d.Approved {
		t.Fatalf("decision = %+v err = %v", d, err)
	}
	if len(conn.permCalls) != 1 {
		t.Fatalf("permCalls = %d", len(conn.permCalls))
	}
	pc := conn.permCalls[0]
	if pc.SessionId != sid || pc.ToolCall.ToolCallId != "call-9" || len(pc.Options) != 4 {
		t.Fatalf("perm request = %+v", pc)
	}

	// Second call: cached allow_always — no new prompt.
	d, err = approver.ApproveTool(context.Background(), req)
	if err != nil || !d.Approved {
		t.Fatalf("cached decision = %+v err = %v", d, err)
	}
	if len(conn.permCalls) != 1 {
		t.Fatalf("expected cached decision, permCalls = %d", len(conn.permCalls))
	}
}

func TestApproverDenyPolicy(t *testing.T) {
	srv, runner, _ := testServer(t, Options{AgentID: "a1", Policy: PermissionDeny})
	sid := newTestSession(t, srv)

	d, err := runner.approver().ApproveTool(context.Background(), &agent.ToolApprovalRequest{
		Context: &agent.TurnContext{
			Inbound: &bus.InboundContext{Channel: ChannelName, ChatID: string(sid)},
		},
		Tool: "exec_command",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Approved {
		t.Fatal("deny policy should reject")
	}
}

func TestApproverAbstainsForeignChannel(t *testing.T) {
	_, runner, _ := testServer(t, Options{AgentID: "a1", Policy: PermissionDeny})
	d, err := runner.approver().ApproveTool(context.Background(), &agent.ToolApprovalRequest{
		Context: &agent.TurnContext{
			Inbound: &bus.InboundContext{Channel: "telegram", ChatID: "42"},
		},
		Tool: "exec_command",
	})
	if err != nil || !d.Approved {
		t.Fatalf("foreign channel should abstain-approve: %+v %v", d, err)
	}
}

func TestApproverCancelledOutcomeDenies(t *testing.T) {
	srv, runner, conn := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)
	conn.permOutcome = acpsdk.NewRequestPermissionOutcomeCancelled()

	d, err := runner.approver().ApproveTool(context.Background(), &agent.ToolApprovalRequest{
		Context: &agent.TurnContext{
			Inbound: &bus.InboundContext{Channel: ChannelName, ChatID: string(sid)},
		},
		Tool: "exec_command",
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Approved {
		t.Fatal("cancelled permission must deny")
	}
}

func TestCloseSession(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)

	if _, err := srv.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: sid}); err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.sessionByID(sid); ok {
		t.Fatal("session should be removed after close")
	}
	if _, ok := srv.GetStreamer(context.Background(), ChannelName, string(sid), "k"); ok {
		t.Fatal("streamer should be gone for closed session")
	}
}

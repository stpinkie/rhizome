package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
)

// --- session modes (session/set_mode) ---

func approvalReq(sid acpsdk.SessionId, tool string) *agent.ToolApprovalRequest {
	return &agent.ToolApprovalRequest{
		Context: &agent.TurnContext{
			Inbound: &bus.InboundContext{Channel: ChannelName, ChatID: string(sid)},
		},
		Tool: tool,
	}
}

func TestNewSessionAdvertisesModes(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "a1"})

	resp, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if resp.Modes == nil {
		t.Fatal("session response should advertise modes")
	}
	if resp.Modes.CurrentModeId != modeAsk {
		t.Fatalf("current mode = %q, want ask", resp.Modes.CurrentModeId)
	}
	ids := make([]acpsdk.SessionModeId, 0, len(resp.Modes.AvailableModes))
	for _, m := range resp.Modes.AvailableModes {
		ids = append(ids, m.Id)
	}
	want := []acpsdk.SessionModeId{modeAsk, modeAuto, modeReadOnly}
	if len(ids) != len(want) {
		t.Fatalf("available modes = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("available modes = %v, want %v", ids, want)
		}
	}
}

func TestDenyPolicyCapsAdvertisedModes(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "a1", Policy: PermissionDeny})

	resp, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if resp.Modes == nil || len(resp.Modes.AvailableModes) != 1 {
		t.Fatalf("deny should advertise exactly one mode, got %+v", resp.Modes)
	}
	if resp.Modes.AvailableModes[0].Id != modeReadOnly {
		t.Fatalf("advertised mode = %q, want read-only", resp.Modes.AvailableModes[0].Id)
	}
	if resp.Modes.CurrentModeId != modeReadOnly {
		t.Fatalf("current mode = %q, want read-only", resp.Modes.CurrentModeId)
	}
}

func TestSetSessionModeSwitchesPolicy(t *testing.T) {
	srv, runner, conn := testServer(t, Options{AgentID: "a1", Policy: PermissionPrompt})
	sid := newTestSession(t, srv)
	approver := runner.approver()

	// ask → auto: approval without a permission prompt.
	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: modeAuto,
	}); err != nil {
		t.Fatalf("SetSessionMode auto: %v", err)
	}
	d, err := approver.ApproveTool(context.Background(), approvalReq(sid, "write_file"))
	if err != nil || !d.Approved {
		t.Fatalf("auto mode should approve: %+v %v", d, err)
	}
	if len(conn.permCalls) != 0 {
		t.Fatalf("auto mode should not prompt, got %d calls", len(conn.permCalls))
	}

	// auto → read-only: denied.
	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: modeReadOnly,
	}); err != nil {
		t.Fatalf("SetSessionMode read-only: %v", err)
	}
	d, err = approver.ApproveTool(context.Background(), approvalReq(sid, "write_file"))
	if err != nil || d.Approved {
		t.Fatalf("read-only mode should deny: %+v %v", d, err)
	}

	// read-only → ask: prompts again.
	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: modeAsk,
	}); err != nil {
		t.Fatalf("SetSessionMode ask: %v", err)
	}
	d, err = approver.ApproveTool(context.Background(), approvalReq(sid, "write_file"))
	if err != nil || !d.Approved {
		t.Fatalf("ask mode should prompt+approve: %+v %v", d, err)
	}
	if len(conn.permCalls) != 1 {
		t.Fatalf("ask mode should prompt once, got %d calls", len(conn.permCalls))
	}
}

func TestSetSessionModeClearsCachedDecisions(t *testing.T) {
	srv, runner, conn := testServer(t, Options{AgentID: "a1", Policy: PermissionPrompt})
	sid := newTestSession(t, srv)
	approver := runner.approver()

	conn.permOutcome = acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(optAllowAlways))
	req := approvalReq(sid, "write_file")
	if _, err := approver.ApproveTool(context.Background(), req); err != nil {
		t.Fatalf("ApproveTool: %v", err)
	}
	sess, _ := srv.sessionByID(sid)
	if _, ok := sess.cachedDecision("write_file"); !ok {
		t.Fatal("allow_always should be cached")
	}

	// Switching modes clears the cache (and the persisted record via
	// onDecisions), so returning to ask prompts again.
	for _, m := range []acpsdk.SessionModeId{modeAuto, modeAsk} {
		if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
			SessionId: sid, ModeId: m,
		}); err != nil {
			t.Fatalf("SetSessionMode %s: %v", m, err)
		}
	}
	if _, ok := sess.cachedDecision("write_file"); ok {
		t.Fatal("mode switch should clear cached decisions")
	}
	if _, err := approver.ApproveTool(context.Background(), req); err != nil {
		t.Fatalf("ApproveTool: %v", err)
	}
	if len(conn.permCalls) != 2 {
		t.Fatalf("expected a fresh prompt after mode round-trip, got %d calls", len(conn.permCalls))
	}
}

func TestSetSessionModeValidates(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)

	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: "nope", ModeId: modeAuto,
	}); err == nil {
		t.Fatal("unknown session should error")
	}
	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: "god-mode",
	}); err == nil {
		t.Fatal("unknown mode should error")
	}
}

func TestSetSessionModeUnderDenyOnlyAcceptsReadOnly(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "a1", Policy: PermissionDeny})
	sid := newTestSession(t, srv)

	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: modeAuto,
	}); err == nil {
		t.Fatal("deny server must refuse widening modes")
	}
	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: modeAsk,
	}); err == nil {
		t.Fatal("deny server must refuse ask")
	}
	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: modeReadOnly,
	}); err != nil {
		t.Fatalf("read-only should be accepted under deny: %v", err)
	}
}

func TestSetSessionModeNotifies(t *testing.T) {
	srv, _, conn := testServer(t, Options{AgentID: "a1"})
	sid := newTestSession(t, srv)

	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: modeAuto,
	}); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()
	var found bool
	for _, u := range conn.updates {
		if u.session == sid && u.update.CurrentModeUpdate != nil {
			if u.update.CurrentModeUpdate.CurrentModeId != modeAuto {
				t.Fatalf("current_mode_update id = %q", u.update.CurrentModeUpdate.CurrentModeId)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("expected a current_mode_update notification")
	}
}

func TestLoadSessionRestoresMode(t *testing.T) {
	srv, runner, _, store := testServerWithStore(t, Options{AgentID: "a1", Policy: PermissionPrompt})
	sid := newTestSession(t, srv)

	if _, err := srv.SetSessionMode(context.Background(), acpsdk.SetSessionModeRequest{
		SessionId: sid, ModeId: modeAuto,
	}); err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}
	rec, ok := store.GetSession(string(sid))
	if !ok || rec.Mode != string(modeAuto) {
		t.Fatalf("persisted mode = %q, want auto", rec.Mode)
	}

	if _, err := srv.CloseSession(context.Background(), acpsdk.CloseSessionRequest{SessionId: sid}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	resp, err := srv.LoadSession(context.Background(), acpsdk.LoadSessionRequest{
		SessionId:  sid,
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if resp.Modes == nil || resp.Modes.CurrentModeId != modeAuto {
		t.Fatalf("loaded modes = %+v, want current auto", resp.Modes)
	}

	// The restored session approves without prompting.
	d, err := runner.approver().ApproveTool(context.Background(), approvalReq(sid, "write_file"))
	if err != nil || !d.Approved {
		t.Fatalf("restored auto mode should approve: %+v %v", d, err)
	}
}

func TestLoadSessionDropsModeNotOffered(t *testing.T) {
	// A record written under prompt carries mode=auto; loading it on a deny
	// server must not restore a mode the policy no longer offers.
	path := t.TempDir() + "/acp-sessions.json"
	store, err := OpenSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSessionStore: %v", err)
	}
	if err := store.PutSession(SessionRecord{
		SessionID:  "s1",
		AgentID:    "main",
		SessionKey: "agent:main:acp:s1",
		Mode:       string(modeAuto),
	}); err != nil {
		t.Fatalf("PutSession: %v", err)
	}

	srv, _, _ := testServer(t, Options{Policy: PermissionDeny, Sessions: store})
	resp, err := srv.LoadSession(context.Background(), acpsdk.LoadSessionRequest{
		SessionId:  "s1",
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if resp.Modes == nil || resp.Modes.CurrentModeId != modeReadOnly {
		t.Fatalf("mode should fall back to read-only under deny, got %+v", resp.Modes)
	}
	sess, _ := srv.sessionByID("s1")
	if sess.effectivePolicy(PermissionDeny) != PermissionDeny {
		t.Fatal("restored session must not widen a deny server")
	}
}

package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// --- session config options (session/set_config_option) ---

func setAgentModel(t *testing.T, srv *Server, agentID, model string) {
	t.Helper()
	inst, ok := srv.runner.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil {
		t.Fatalf("agent %q not found", agentID)
	}
	inst.Model = model
}

func modelOption(opts []acpsdk.SessionConfigOption) *acpsdk.SessionConfigOptionSelect {
	for _, o := range opts {
		if o.Select != nil && o.Select.Id == modelConfigID {
			return o.Select
		}
	}
	return nil
}

func TestNewSessionAdvertisesModelOption(t *testing.T) {
	srv, _, _ := testServer(t, Options{
		Models: []string{"m1", "m2"},
	})
	setAgentModel(t, srv, "main", "m1")

	resp, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sel := modelOption(resp.ConfigOptions)
	if sel == nil {
		t.Fatalf("no model select in %+v", resp.ConfigOptions)
	}
	if sel.Category == nil || *sel.Category != acpsdk.SessionConfigOptionCategoryModel {
		t.Fatalf("category = %+v", sel.Category)
	}
	if sel.CurrentValue != "m1" {
		t.Fatalf("current value = %q, want agent's model m1", sel.CurrentValue)
	}
	if sel.Options.Ungrouped == nil {
		t.Fatal("expected ungrouped options")
	}
	values := *sel.Options.Ungrouped
	if len(values) != 3 {
		t.Fatalf("expected inherit + 2 models, got %+v", values)
	}
	if values[0].Value != inheritConfigValue {
		t.Fatalf("first value = %q, want inherit", values[0].Value)
	}
	if values[1].Value != "m1" || values[2].Value != "m2" {
		t.Fatalf("model values = %+v", values)
	}
}

func TestNewSessionNoModelOptionWithoutModels(t *testing.T) {
	srv, _, _ := testServer(t, Options{AgentID: "a1"})
	resp, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// No models configured → no model option (thought_level is always offered).
	if modelOption(resp.ConfigOptions) != nil {
		t.Fatalf("no models configured → no model option, got %+v", resp.ConfigOptions)
	}
}

func TestSetSessionConfigOptionSetsModel(t *testing.T) {
	srv, runner, _ := testServer(t, Options{
		AgentID: "a1",
		Models:  []string{"m1", "m2"},
	})
	sid := newTestSession(t, srv)

	resp, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid,
			ConfigId:  modelConfigID,
			Value:     "m2",
		},
	})
	if err != nil {
		t.Fatalf("SetSessionConfigOption: %v", err)
	}
	sel := modelOption(resp.ConfigOptions)
	if sel == nil || sel.CurrentValue != "m2" {
		t.Fatalf("response current = %+v, want m2", sel)
	}
	sess, _ := srv.sessionByID(sid)
	if sess.modelOverride() != "m2" {
		t.Fatalf("session model = %q", sess.modelOverride())
	}

	// The next prompt carries the override into the inbound message.
	if _, err := srv.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sid,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(runner.msgs) != 1 || runner.msgs[0].ModelOverride != "m2" {
		t.Fatalf("inbound ModelOverride = %+v", runner.msgs)
	}
}

func TestSetSessionConfigOptionInheritClears(t *testing.T) {
	srv, _, _ := testServer(t, Options{
		Models: []string{"m1", "m2"},
	})
	setAgentModel(t, srv, "main", "m1")
	sid := newTestSession(t, srv)

	set := func(v acpsdk.SessionConfigValueId) acpsdk.SetSessionConfigOptionResponse {
		resp, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
			ValueId: &acpsdk.SetSessionConfigOptionValueId{
				SessionId: sid, ConfigId: modelConfigID, Value: v,
			},
		})
		if err != nil {
			t.Fatalf("SetSessionConfigOption %q: %v", v, err)
		}
		return resp
	}

	set("m2")
	resp := set(inheritConfigValue)
	sel := modelOption(resp.ConfigOptions)
	if sel == nil || sel.CurrentValue != "m1" {
		t.Fatalf("inherit should show agent's model m1, got %+v", sel)
	}
	sess, _ := srv.sessionByID(sid)
	if sess.modelOverride() != "" {
		t.Fatalf("inherit should clear the override, got %q", sess.modelOverride())
	}
}

func TestSetSessionConfigOptionValidates(t *testing.T) {
	srv, _, _ := testServer(t, Options{
		AgentID: "a1",
		Models:  []string{"m1"},
	})
	sid := newTestSession(t, srv)

	if _, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: "nope", ConfigId: modelConfigID, Value: "m1",
		},
	}); err == nil {
		t.Fatal("unknown session should error")
	}
	if _, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: "bogus", Value: "m1",
		},
	}); err == nil {
		t.Fatal("unknown configId should error")
	}
	if _, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: modelConfigID, Value: "not-a-model",
		},
	}); err == nil {
		t.Fatal("unknown model value should error")
	}
	if _, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		Boolean: &acpsdk.SetSessionConfigOptionBoolean{
			SessionId: sid, ConfigId: modelConfigID, Value: true,
		},
	}); err == nil {
		t.Fatal("boolean payload should error — no boolean options exist")
	}
}

func TestModelOptionPersistedAndRestored(t *testing.T) {
	srv, _, _, store := testServerWithStore(t, Options{
		AgentID: "a1",
		Models:  []string{"m1", "m2"},
	})
	sid := newTestSession(t, srv)

	if _, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: modelConfigID, Value: "m2",
		},
	}); err != nil {
		t.Fatalf("SetSessionConfigOption: %v", err)
	}
	rec, ok := store.GetSession(string(sid))
	if !ok || rec.Model != "m2" {
		t.Fatalf("persisted model = %q, want m2", rec.Model)
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
	sel := modelOption(resp.ConfigOptions)
	if sel == nil || sel.CurrentValue != "m2" {
		t.Fatalf("loaded current = %+v, want m2", sel)
	}
	sess, _ := srv.sessionByID(sid)
	if sess.modelOverride() != "m2" {
		t.Fatalf("restored model = %q", sess.modelOverride())
	}
}

func TestLoadSessionDropsModelNotOffered(t *testing.T) {
	// A record written when "gone" was in model_list must not restore it
	// after the entry was removed.
	path := t.TempDir() + "/acp-sessions.json"
	store, err := OpenSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSessionStore: %v", err)
	}
	if err := store.PutSession(SessionRecord{
		SessionID:  "s1",
		AgentID:    "main",
		SessionKey: "agent:main:acp:s1",
		Model:      "gone",
	}); err != nil {
		t.Fatalf("PutSession: %v", err)
	}

	srv, _, _ := testServer(t, Options{
		Models:   []string{"m1"},
		Sessions: store,
	})
	if _, err := srv.LoadSession(context.Background(), acpsdk.LoadSessionRequest{
		SessionId:  "s1",
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	}); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	sess, _ := srv.sessionByID("s1")
	if sess.modelOverride() != "" {
		t.Fatalf("unoffered model should drop to inherit, got %q", sess.modelOverride())
	}
}

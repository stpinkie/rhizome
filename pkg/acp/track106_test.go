package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
)

// thoughtOption extracts the thought_level select from a config-option set.
func thoughtOption(opts []acpsdk.SessionConfigOption) *acpsdk.SessionConfigOptionSelect {
	for _, o := range opts {
		if o.Select != nil && o.Select.Id == thoughtLevelConfigID {
			return o.Select
		}
	}
	return nil
}

func TestNewSessionAdvertisesThoughtLevelOption(t *testing.T) {
	srv, _, _ := testServer(t, Options{})
	sid := newTestSession(t, srv)

	sess, ok := srv.sessionByID(sid)
	if !ok {
		t.Fatal("session not registered")
	}
	sel := thoughtOption(srv.configOptions(sess))
	if sel == nil {
		t.Fatal("thought_level option missing from session config options")
	}
	if sel.Category == nil || *sel.Category != acpsdk.SessionConfigOptionCategoryThoughtLevel {
		t.Fatalf("category = %v, want thought_level", sel.Category)
	}
	if sel.CurrentValue != inheritConfigValue {
		t.Fatalf("current = %q, want inherit", sel.CurrentValue)
	}
	if sel.Options.Ungrouped == nil {
		t.Fatal("select options missing")
	}
	values := *sel.Options.Ungrouped
	if len(values) != len(acpThoughtLevels)+1 {
		t.Fatalf("select values = %d, want %d", len(values), len(acpThoughtLevels)+1)
	}
	if values[0].Value != inheritConfigValue {
		t.Fatalf("first value = %q, want inherit", values[0].Value)
	}
	seen := map[string]bool{}
	for _, v := range values[1:] {
		seen[string(v.Value)] = true
	}
	for _, level := range acpThoughtLevels {
		if !seen[level] {
			t.Fatalf("thought level %q not offered", level)
		}
	}
}

func TestSetSessionConfigOptionThoughtLevel(t *testing.T) {
	srv, runner, _ := testServer(t, Options{})
	sid := newTestSession(t, srv)

	resp, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid,
			ConfigId:  thoughtLevelConfigID,
			Value:     "high",
		},
	})
	if err != nil {
		t.Fatalf("SetSessionConfigOption: %v", err)
	}
	sel := thoughtOption(resp.ConfigOptions)
	if sel == nil || sel.CurrentValue != "high" {
		t.Fatalf("response current = %+v, want high", sel)
	}
	sess, _ := srv.sessionByID(sid)
	if sess.thinkingLevelOverride() != "high" {
		t.Fatalf("session thinking = %q", sess.thinkingLevelOverride())
	}

	// The next prompt carries the override into the inbound message.
	if _, err := srv.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sid,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	}); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if len(runner.msgs) != 1 || runner.msgs[0].ThinkingLevelOverride != "high" {
		t.Fatalf("inbound ThinkingLevelOverride = %+v", runner.msgs)
	}

	// A second session is unaffected.
	sid2 := newTestSession(t, srv)
	sess2, _ := srv.sessionByID(sid2)
	if sess2.thinkingLevelOverride() != "" {
		t.Fatalf("second session thinking = %q, want inherit", sess2.thinkingLevelOverride())
	}

	// Unknown values are refused.
	if _, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: thoughtLevelConfigID, Value: "ludicrous",
		},
	}); err == nil {
		t.Fatal("bogus thought level should be refused")
	}
	if sess.thinkingLevelOverride() != "high" {
		t.Fatal("refused value should not change the override")
	}

	// Inherit clears the override.
	if _, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: thoughtLevelConfigID, Value: inheritConfigValue,
		},
	}); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	if sess.thinkingLevelOverride() != "" {
		t.Fatal("inherit should clear the override")
	}
}

func TestSetSessionConfigOptionUnknownConfigID(t *testing.T) {
	srv, _, _ := testServer(t, Options{})
	sid := newTestSession(t, srv)

	_, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: "bogus", Value: "x",
		},
	})
	if err == nil {
		t.Fatal("unknown configId should be refused")
	}
}

func TestLoadSessionRestoresThinkingLevel(t *testing.T) {
	srv, _, _, store := testServerWithStore(t, Options{})
	sid := newTestSession(t, srv)

	if _, err := srv.SetSessionConfigOption(context.Background(), acpsdk.SetSessionConfigOptionRequest{
		ValueId: &acpsdk.SetSessionConfigOptionValueId{
			SessionId: sid, ConfigId: thoughtLevelConfigID, Value: "low",
		},
	}); err != nil {
		t.Fatalf("SetSessionConfigOption: %v", err)
	}
	rec, ok := store.GetSession(string(sid))
	if !ok || rec.ThinkingLevel != "low" {
		t.Fatalf("stored record = %+v", rec)
	}

	// Close + reload — the override is restored.
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
	loaded, ok := srv.sessionByID(sid)
	if !ok {
		t.Fatal("loaded session not registered")
	}
	if loaded.thinkingLevelOverride() != "low" {
		t.Fatalf("loaded thinking = %q, want low", loaded.thinkingLevelOverride())
	}
}

func TestInitializeAdvertisesMCPHTTPAndSSE(t *testing.T) {
	srv, _, _ := testServer(t, Options{})
	resp, err := srv.Initialize(context.Background(), acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	caps := resp.AgentCapabilities.McpCapabilities
	if !caps.Http || !caps.Sse {
		t.Fatalf("mcpCapabilities = %+v, want http+sse advertised", caps)
	}
}

func TestSessionMCPHTTPAndSSEConnect(t *testing.T) {
	srv, _, _ := testServer(t, Options{})
	fake := newFakeMCPManager()
	srv.newMCPManager = func() sessionMCPManager { return fake }

	_, err := srv.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd: t.TempDir(),
		McpServers: []acpsdk.McpServer{
			{Http: &acpsdk.McpServerHttpInline{
				Name: "remote-http",
				Url:  "https://mcp.example.com/rpc",
				Headers: []acpsdk.HttpHeader{
					{Name: "Authorization", Value: "Bearer tok"},
				},
			}},
			{Sse: &acpsdk.McpServerSseInline{
				Name: "remote-sse",
				Url:  "http://127.0.0.1:8788/sse",
			}},
			// Nested ACP stays refused.
			{Acp: &acpsdk.McpServerAcpInline{}},
			// Malformed URLs refused too.
			{Http: &acpsdk.McpServerHttpInline{Name: "bad", Url: "file:///etc/passwd"}},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.servers) != 2 {
		t.Fatalf("connected servers = %d, want 2 (http+sse; acp and bad-url refused)", len(fake.servers))
	}
	var httpCfg, sseCfg *config.MCPServerConfig
	for _, conn := range fake.servers {
		cfg := conn.Config
		switch cfg.Type {
		case "http":
			c := cfg
			httpCfg = &c
		case "sse":
			c := cfg
			sseCfg = &c
		}
	}
	if httpCfg == nil || sseCfg == nil {
		t.Fatalf("missing http or sse server: %+v", fake.servers)
	}
	if httpCfg.URL != "https://mcp.example.com/rpc" || httpCfg.Headers["Authorization"] != "Bearer tok" {
		t.Fatalf("http cfg = %+v", httpCfg)
	}
	if sseCfg.URL != "http://127.0.0.1:8788/sse" {
		t.Fatalf("sse cfg = %+v", sseCfg)
	}
}

// The agent-side application is exercised in pkg/agent's
// thinking_override_test.go — here we only verify the wire field flows.
func TestThinkingLevelInvalidOnAgentSide(t *testing.T) {
	if agent.IsConfiguredThinkingLevel("ludicrous") {
		t.Fatal("ludicrous should not be a configured level")
	}
	if !agent.IsConfiguredThinkingLevel("XHIGH") {
		t.Fatal("levels are case-insensitive")
	}
}

// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package mcp

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stpinkie/rhizome/pkg/tools"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "echoes args back" }
func (echoTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	}
}

func (echoTool) Execute(_ context.Context, args map[string]any) *toolshared.ToolResult {
	text, _ := args["text"].(string)
	return &toolshared.ToolResult{ForLLM: "echo:" + text}
}

type failTool struct{}

func (failTool) Name() string        { return "fail" }
func (failTool) Description() string { return "always errors" }
func (failTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}

func (failTool) Execute(context.Context, map[string]any) *toolshared.ToolResult {
	return &toolshared.ToolResult{ForLLM: "boom", IsError: true}
}

func newTrack89Registry() *tools.ToolRegistry {
	reg := tools.NewToolRegistry()
	reg.Register(echoTool{})
	reg.Register(failTool{})
	return reg
}

// connectClient wires a real MCP client to the serve server over the SDK's
// in-memory transport — the same JSON-RPC path stdio carries.
func connectClient(t *testing.T, server *sdkmcp.Server) *sdkmcp.ClientSession {
	t.Helper()
	st, ct := sdkmcp.NewInMemoryTransports()
	ctx := context.Background()

	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestTrack89ServeListsOnlyAllowed(t *testing.T) {
	reg := newTrack89Registry()
	server := newServeServer(reg, []string{"echo", "nonexistent"}, "test")
	cs := connectClient(t, server)

	res, err := cs.ListTools(context.Background(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 1 || res.Tools[0].Name != "echo" {
		names := make([]string, 0, len(res.Tools))
		for _, tool := range res.Tools {
			names = append(names, tool.Name)
		}
		t.Fatalf("tools = %v, want [echo] only", names)
	}
}

func TestTrack89ServeEmptyAllowExposesNothing(t *testing.T) {
	reg := newTrack89Registry()
	server := newServeServer(reg, nil, "test")
	cs := connectClient(t, server)

	res, err := cs.ListTools(context.Background(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(res.Tools) != 0 {
		t.Fatalf("tools = %d, want 0 (deny-all default)", len(res.Tools))
	}
}

func TestTrack89ServeCallToolRoundTrip(t *testing.T) {
	reg := newTrack89Registry()
	server := newServeServer(reg, []string{"echo", "fail"}, "test")
	cs := connectClient(t, server)

	ctx := context.Background()
	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "hello"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("echo reported error: %+v", res)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}
	tc, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok || tc.Text != "echo:hello" {
		t.Fatalf("content = %+v, want TextContent{echo:hello}", res.Content[0])
	}

	// Tool errors arrive in-band (IsError + text), not as protocol errors.
	res, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: "fail"})
	if err != nil {
		t.Fatalf("CallTool(fail): %v", err)
	}
	if !res.IsError {
		t.Fatal("fail tool should set IsError")
	}

	// A tool absent from the allow list is unreachable.
	if _, err = cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": "x"},
	}); err != nil {
		t.Fatalf("allowed echo call failed: %v", err)
	}
	server2 := newServeServer(reg, []string{"fail"}, "test")
	cs2 := connectClient(t, server2)
	if _, err = cs2.CallTool(ctx, &sdkmcp.CallToolParams{Name: "echo"}); err == nil {
		t.Fatal("echo should be unreachable when not allowlisted")
	}
}

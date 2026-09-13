package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSwarmContextTool_LocalFallback(t *testing.T) {
	SetSwarmHooks(nil)
	t.Cleanup(func() { SetSwarmHooks(nil) })

	tool := NewSwarmContextTool(t.TempDir())

	// Post without a swarm layer: writes the "local" shard.
	res := tool.Execute(context.Background(), map[string]any{
		"swarm_id": "ops",
		"action":   "post",
		"kind":     "decision",
		"content":  "use REST for the API",
	})
	if res.IsError {
		t.Fatalf("post failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "locally") {
		t.Fatalf("expected local-fallback message, got %q", res.ForLLM)
	}

	res = tool.Execute(context.Background(), map[string]any{
		"swarm_id": "ops",
		"action":   "read",
	})
	if res.IsError || !strings.Contains(res.ForLLM, "use REST") {
		t.Fatalf("read should show the note: %q", res.ForLLM)
	}
}

func TestSwarmContextTool_PostViaHooks(t *testing.T) {
	var posted string
	SetSwarmHooks(&SwarmHooks{
		PeerID:   func() string { return "peer123" },
		IsMember: func(string) bool { return true },
		PostNote: func(ctx context.Context, swarmID, kind, key, content string, ttl time.Duration) error {
			posted = content
			return nil
		},
	})
	t.Cleanup(func() { SetSwarmHooks(nil) })

	tool := NewSwarmContextTool(t.TempDir())
	res := tool.Execute(context.Background(), map[string]any{
		"swarm_id": "ops",
		"action":   "post",
		"content":  "broadcast me",
	})
	if res.IsError {
		t.Fatalf("post failed: %s", res.ForLLM)
	}
	if posted != "broadcast me" {
		t.Fatalf("expected hook broadcast, got %q", posted)
	}
	if !strings.Contains(res.ForLLM, "broadcast") {
		t.Fatalf("expected broadcast confirmation, got %q", res.ForLLM)
	}
}

func TestSwarmContextTool_NotMember(t *testing.T) {
	SetSwarmHooks(&SwarmHooks{
		PeerID:   func() string { return "peer123" },
		IsMember: func(string) bool { return false },
		PostNote: func(ctx context.Context, swarmID, kind, key, content string, ttl time.Duration) error {
			return nil
		},
	})
	t.Cleanup(func() { SetSwarmHooks(nil) })

	tool := NewSwarmContextTool(t.TempDir())
	res := tool.Execute(context.Background(), map[string]any{
		"swarm_id": "ops",
		"action":   "post",
		"content":  "hello",
	})
	if !res.IsError || !strings.Contains(res.ForLLM, "not a member") {
		t.Fatalf("expected membership error, got %+v", res)
	}
}

func TestSwarmContextTool_Validation(t *testing.T) {
	SetSwarmHooks(nil)
	tool := NewSwarmContextTool(t.TempDir())

	res := tool.Execute(context.Background(), map[string]any{
		"swarm_id": "bad/id",
		"action":   "read",
	})
	if !res.IsError {
		t.Fatal("expected invalid swarm id error")
	}

	res = tool.Execute(context.Background(), map[string]any{
		"swarm_id": "ops",
		"action":   "post",
		"content":  "  ",
	})
	if !res.IsError {
		t.Fatal("expected empty content error")
	}
}

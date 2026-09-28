package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/config"
)

// testHandler is the minimal acpsdk.Client SpawnBound validation accepts.
type testHandler struct{}

func (testHandler) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, nil
}

func (testHandler) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, nil
}

func (testHandler) RequestPermission(
	context.Context, acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{}, nil
}

func (testHandler) SessionUpdate(context.Context, acpsdk.SessionNotification) error { return nil }

func (testHandler) CreateTerminal(
	context.Context, acpsdk.CreateTerminalRequest,
) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, nil
}

func (testHandler) KillTerminal(
	context.Context, acpsdk.KillTerminalRequest,
) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, nil
}

func (testHandler) TerminalOutput(
	context.Context, acpsdk.TerminalOutputRequest,
) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, nil
}

func (testHandler) ReleaseTerminal(
	context.Context, acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, nil
}

func (testHandler) WaitForTerminalExit(
	context.Context, acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, nil
}

// SpawnBound validation happens before any process starts — a bound
// session must never spawn an agent the caller didn't configure.
func TestSpawnBound_Validation(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	h := testHandler{}

	if _, err := SpawnBound(ctx, cfg, BoundSource{ID: "a"}, BoundSpawn{}); err == nil {
		t.Fatal("nil binding accepted")
	}
	if _, err := SpawnBound(ctx, cfg,
		BoundSource{ID: "a", ACP: &config.ACPAgentConfig{Remote: "wss://x"}},
		BoundSpawn{Handler: h}); err == nil {
		t.Fatal("remote binding accepted for a bound spawn")
	}
	if _, err := SpawnBound(ctx, cfg,
		BoundSource{ID: "a", ACP: &config.ACPAgentConfig{Command: "x"}},
		BoundSpawn{}); err == nil {
		t.Fatal("nil handler accepted")
	}
}

// containerConfigFor is the no-egress enforcement point: the bound
// (market) caller requires it; bridge mode must never leak into a paid
// session.
func TestContainerConfigFor_NoEgress(t *testing.T) {
	cfg := &config.Config{}
	cfg.ACP.Client.Container = &config.ACPContainerConfig{
		Image: "img:latest", Network: "bridge",
	}
	cc, err := containerConfigFor(cfg, "a", false)
	if err != nil || cc.Network != "bridge" {
		t.Fatalf("unforced: %v %v", cc, err)
	}
	cc, err = containerConfigFor(cfg, "a", true)
	if err != nil || cc.Network != "none" {
		t.Fatalf("forced: %v %v", cc, err)
	}
	// The operator's config object must not be mutated.
	if cfg.ACP.Client.Container.Network != "bridge" {
		t.Fatal("noEgress mutated the shared config")
	}
	// Already-none stays none.
	cfg.ACP.Client.Container.Network = "none"
	cc, err = containerConfigFor(cfg, "a", true)
	if err != nil || cc.Network != "none" {
		t.Fatalf("none stays none: %v %v", cc, err)
	}
	// Missing image fails regardless.
	cfg.ACP.Client.Container.Image = ""
	if _, err := containerConfigFor(cfg, "a", true); err == nil {
		t.Fatal("empty image accepted")
	}
}

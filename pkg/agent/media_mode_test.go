package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/providers"
)

var testPNGHeader = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
	0x00, 0x00, 0x00, 0x0D, // IHDR length
	0x49, 0x48, 0x44, 0x52, // "IHDR"
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, // 1x1 RGB
	0x00, 0x00, 0x00, // no interlace
	0x90, 0x77, 0x53, 0xDE, // CRC
}

func writeTestPNG(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "img.png")
	if err := os.WriteFile(p, testPNGHeader, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveMediaRefsMode_AutoInlinesUserImage(t *testing.T) {
	store := media.NewFileMediaStore()
	ref, err := store.Store(writeTestPNG(t), media.MediaMeta{}, "test")
	if err != nil {
		t.Fatal(err)
	}

	messages := []providers.Message{
		{Role: "user", Content: "describe this", Media: []string{ref}},
	}
	result := resolveMediaRefsMode(messages, store, config.DefaultMaxMediaSize, 0, inlineAuto)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if len(result[0].Media) != 1 || !strings.HasPrefix(result[0].Media[0], "data:image/") {
		t.Fatalf("expected inline data URL on user message, got %v", result[0].Media)
	}
	localPath, _, _ := store.ResolveWithMeta(ref)
	if !strings.Contains(result[0].Content, "[image:"+localPath+"]") {
		t.Fatalf("expected path tag kept in content, got %q", result[0].Content)
	}
}

func TestResolveMediaRefsMode_OffKeepsPathTagsOnly(t *testing.T) {
	store := media.NewFileMediaStore()
	ref, err := store.Store(writeTestPNG(t), media.MediaMeta{}, "test")
	if err != nil {
		t.Fatal(err)
	}

	messages := []providers.Message{
		toolResultPromptMessage("Image loaded", "call_1", []string{ref}),
	}
	result := resolveMediaRefsMode(messages, store, config.DefaultMaxMediaSize, 0, inlineOff)

	if len(result) != 1 {
		t.Fatalf("expected no synthetic follow-up in off mode, got %d messages", len(result))
	}
	if len(result[0].Media) != 0 {
		t.Fatalf("expected no media in off mode, got %v", result[0].Media)
	}
}

func TestImageInlineModeForAgent(t *testing.T) {
	cfg := config.DefaultConfig()
	agent := &AgentInstance{Model: "gpt-4o-mini"}

	// Default mode is auto; a vision-capable model inlines user images.
	if got := imageInlineModeForAgent(cfg, agent, "gpt-4o-mini"); got != inlineAuto {
		t.Fatalf("expected inlineAuto for gpt-4o-mini, got %d", got)
	}
	// Non-vision model without image_model fallback stays tool-only.
	if got := imageInlineModeForAgent(cfg, agent, "deepseek-v4-flash"); got != inlineTool {
		t.Fatalf("expected inlineTool for text-only model, got %d", got)
	}
	// An image_model fallback enables auto even for text-only defaults.
	agent.ImageCandidates = []providers.FallbackCandidate{{Model: "vision-model"}}
	if got := imageInlineModeForAgent(cfg, agent, "deepseek-v4-flash"); got != inlineAuto {
		t.Fatalf("expected inlineAuto with image candidates, got %d", got)
	}
	// Explicit modes override detection.
	cfg.Tools.Media.VisionMode = "tool"
	if got := imageInlineModeForAgent(cfg, agent, "gpt-4o"); got != inlineTool {
		t.Fatalf("expected inlineTool in tool mode, got %d", got)
	}
	cfg.Tools.Media.VisionMode = "off"
	if got := imageInlineModeForAgent(cfg, agent, "gpt-4o"); got != inlineOff {
		t.Fatalf("expected inlineOff in off mode, got %d", got)
	}
}

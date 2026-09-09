package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/media"
)

func TestLoadVideoToolValidation(t *testing.T) {
	dir := t.TempDir()
	tool := NewLoadVideoTool(dir, false, 0, 4, "", media.NewFileMediaStore())
	tool.SetContext("ch", "chat")

	res := tool.Execute(context.Background(), map[string]any{})
	if res == nil || res.ForLLM == "" {
		t.Fatal("expected error result for missing path")
	}

	res = tool.Execute(context.Background(), map[string]any{"path": "nope.mp4"})
	if res == nil || !strings.Contains(res.ForLLM, "not found") {
		t.Fatalf("expected not-found error, got %v", res)
	}
}

func TestLoadVideoToolRejectsNonVideo(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "a.png")
	if err := os.WriteFile(img, []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	}, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewLoadVideoTool(dir, false, 0, 4, "", media.NewFileMediaStore())
	tool.SetContext("ch", "chat")

	res := tool.Execute(context.Background(), map[string]any{"path": "a.png"})
	if res == nil || !strings.Contains(res.ForLLM, "not appear to be a video") {
		t.Fatalf("expected non-video rejection, got %v", res)
	}
}

func TestLoadVideoToolNoFFmpeg(t *testing.T) {
	if media.FFmpegAvailable("") {
		t.Skip("ffmpeg installed; cannot test missing-ffmpeg path")
	}
	dir := t.TempDir()
	vid := filepath.Join(dir, "clip.mp4")
	// Minimal ftyp box so detectMediaType sees video.
	if err := os.WriteFile(vid, []byte{
		0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm',
		0x00, 0x00, 0x02, 0x00, 'i', 's', 'o', 'm', 'm', 'p', '4', '1',
	}, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewLoadVideoTool(dir, false, 0, 4, "", media.NewFileMediaStore())
	tool.SetContext("ch", "chat")

	res := tool.Execute(context.Background(), map[string]any{"path": "clip.mp4"})
	if res == nil || !strings.Contains(res.ForLLM, "ffmpeg") {
		t.Fatalf("expected ffmpeg-missing error, got %v", res)
	}
}

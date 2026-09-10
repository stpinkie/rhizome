package fstools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/media"
)

// failAfterStore is a MediaStore wrapper that fails after n successful Store
// calls so we can exercise partial-failure cleanup in load_video.
type failAfterStore struct {
	media.MediaStore
	failAfter int
	count     int
}

func (s *failAfterStore) Store(localPath string, meta media.MediaMeta, scope string) (string, error) {
	s.count++
	if s.count > s.failAfter {
		return "", fmt.Errorf("injected store failure")
	}
	return s.MediaStore.Store(localPath, meta, scope)
}

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

func TestLoadVideoToolPartialStoreCleansUnregisteredFrames(t *testing.T) {
	dir := t.TempDir()
	baseStore := media.NewFileMediaStore()
	store := &failAfterStore{MediaStore: baseStore, failAfter: 1}
	tool := NewLoadVideoTool(dir, false, 0, 4, "", store)
	tool.SetContext("ch", "chat")

	// Create a minimal video file so validation/detectMediaType passes.
	vid := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(vid, []byte{
		0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm',
		0x00, 0x00, 0x02, 0x00, 'i', 's', 'o', 'm', 'm', 'p', '4', '1',
	}, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create two fake frame files that the mock extractor will return.
	frame1 := filepath.Join(dir, "frame-001.jpg")
	frame2 := filepath.Join(dir, "frame-002.jpg")
	if err := os.WriteFile(frame1, []byte("frame1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(frame2, []byte("frame2"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool.extractFrames = func(context.Context, string, string, string, int) ([]string, error) {
		return []string{frame1, frame2}, nil
	}

	res := tool.Execute(context.Background(), map[string]any{"path": "video.mp4"})
	if res == nil || res.ForLLM == "" {
		t.Fatal("expected result for partial store")
	}
	if len(res.Media) != 1 {
		t.Fatalf("expected exactly one stored ref, got %d", len(res.Media))
	}

	// The first frame should still be tracked by the store; the second
	// (unregistered due to the injected failure) should have been removed.
	if _, err := os.Stat(frame1); err != nil {
		t.Fatalf("first frame should still exist: %v", err)
	}
	if _, err := os.Stat(frame2); !os.IsNotExist(err) {
		t.Fatalf("second frame should have been cleaned up: %v", err)
	}

	// Releasing the scope should clean the stored frame and the temp dir.
	scope := "tool:load_video:ch:chat"
	if err := baseStore.ReleaseAll(scope); err != nil {
		t.Fatalf("ReleaseAll failed: %v", err)
	}
	if _, err := os.Stat(frame1); !os.IsNotExist(err) {
		t.Fatalf("first frame should be removed after scope release: %v", err)
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

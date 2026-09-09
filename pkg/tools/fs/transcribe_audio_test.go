package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/audio/asr"
)

type fakeTranscriber struct {
	text string
	err  error
}

func (f *fakeTranscriber) Name() string { return "fake" }

func (f *fakeTranscriber) Transcribe(
	_ context.Context, _ string,
) (*asr.TranscriptionResponse, error) {
	return &asr.TranscriptionResponse{Text: f.text}, f.err
}

func writeWav(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "clip.wav")
	// RIFF/WAVE header.
	if err := os.WriteFile(p, []byte(
		"RIFF\x24\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTranscribeAudioToolNoTranscriber(t *testing.T) {
	dir := t.TempDir()
	writeWav(t, dir)
	tool := NewTranscribeAudioTool(dir, false, 0)

	res := tool.Execute(context.Background(), map[string]any{"path": "clip.wav"})
	if res == nil || !strings.Contains(res.ForLLM, "no transcription provider") {
		t.Fatalf("expected missing-provider error, got %v", res)
	}
}

func TestTranscribeAudioToolSuccess(t *testing.T) {
	dir := t.TempDir()
	writeWav(t, dir)
	tool := NewTranscribeAudioTool(dir, false, 0)
	tool.SetTranscriber(&fakeTranscriber{text: " hello mesh "})

	res := tool.Execute(context.Background(), map[string]any{"path": "clip.wav"})
	if res == nil || !strings.Contains(res.ForLLM, "hello mesh") {
		t.Fatalf("expected transcript text, got %v", res)
	}
}

func TestTranscribeAudioToolRejectsNonAudio(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewTranscribeAudioTool(dir, false, 0)
	tool.SetTranscriber(&fakeTranscriber{text: "x"})

	res := tool.Execute(context.Background(), map[string]any{"path": "a.txt"})
	if res == nil || !strings.Contains(res.ForLLM, "not appear to be audio") {
		t.Fatalf("expected non-audio rejection, got %v", res)
	}
}

package blackboard

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAppendAndNotes(t *testing.T) {
	bb := New(t.TempDir(), Options{})

	err := bb.Append("peer-a", Note{Kind: "decision", Key: "api", Content: "use REST"})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	err = bb.Append("peer-b", Note{Content: "second note"})
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	notes := bb.Notes(time.Time{})
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
	if notes[0].Author != "peer-a" || notes[1].Author != "peer-b" {
		t.Fatalf("unexpected authors: %+v", notes)
	}
}

func TestAppendValidation(t *testing.T) {
	bb := New(t.TempDir(), Options{})

	if err := bb.Append("", Note{Content: "x"}); err == nil {
		t.Fatal("expected error for empty author")
	}
	if err := bb.Append("../escape", Note{Content: "x"}); err == nil {
		t.Fatal("expected error for path-escape author")
	}
	if err := bb.Append("peer", Note{Content: "  "}); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestNoteExpiry(t *testing.T) {
	bb := New(t.TempDir(), Options{})

	if err := bb.Append("a", Note{
		Content:   "expired",
		ExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := bb.Append("a", Note{
		Content:   "live",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := bb.Append("a", Note{Content: "forever"}); err != nil {
		t.Fatal(err)
	}

	notes := bb.Notes(time.Time{})
	if len(notes) != 2 {
		t.Fatalf("expected 2 live notes, got %d", len(notes))
	}
	for _, n := range notes {
		if n.Content == "expired" {
			t.Fatal("expired note should be filtered")
		}
	}
}

func TestNoteTTLDefault(t *testing.T) {
	bb := New(t.TempDir(), Options{NoteTTL: time.Hour})
	if err := bb.Append("a", Note{Content: "x"}); err != nil {
		t.Fatal(err)
	}
	notes := bb.Notes(time.Time{})
	if len(notes) != 1 || notes[0].ExpiresAt.IsZero() {
		t.Fatal("expected default TTL to set expires_at")
	}
}

func TestNoteSizeCap(t *testing.T) {
	bb := New(t.TempDir(), Options{MaxNoteBytes: 10})
	if err := bb.Append("a", Note{Content: strings.Repeat("x", 100)}); err != nil {
		t.Fatal(err)
	}
	notes := bb.Notes(time.Time{})
	if len(notes) != 1 || len(notes[0].Content) != 10 {
		t.Fatalf("expected content truncated to 10 bytes, got %d", len(notes[0].Content))
	}
}

func TestShardTruncation(t *testing.T) {
	bb := New(t.TempDir(), Options{MaxNotesPerShard: 5})
	for i := 0; i < 10; i++ {
		if err := bb.Append("a", Note{Content: fmt.Sprintf("note-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	notes := bb.Notes(time.Time{})
	if len(notes) != 5 {
		t.Fatalf("expected shard capped at 5, got %d", len(notes))
	}
	if notes[0].Content != "note-5" || notes[4].Content != "note-9" {
		t.Fatalf("expected newest notes kept, got %q..%q", notes[0].Content, notes[4].Content)
	}
}

func TestContextDocument(t *testing.T) {
	bb := New(t.TempDir(), Options{})
	if bb.ReadContext() != "" {
		t.Fatal("expected empty context initially")
	}
	if err := bb.WriteContext("# Ops context\n\nCurrent focus: deploy v2."); err != nil {
		t.Fatal(err)
	}
	if got := bb.ReadContext(); !strings.Contains(got, "deploy v2") {
		t.Fatalf("context = %q", got)
	}
}

func TestDigest(t *testing.T) {
	bb := New(t.TempDir(), Options{})
	if bb.Digest() != "" {
		t.Fatal("expected empty digest on empty board")
	}
	if err := bb.WriteContext("Focus: release v0.9"); err != nil {
		t.Fatal(err)
	}
	if err := bb.Append("peer-a", Note{Kind: "blocker", Content: "CI flaky"}); err != nil {
		t.Fatal(err)
	}
	d := bb.Digest()
	if !strings.Contains(d, "release v0.9") || !strings.Contains(d, "CI flaky") || !strings.Contains(d, "peer-a") {
		t.Fatalf("digest missing context or notes:\n%s", d)
	}
}

func TestNotesSince(t *testing.T) {
	bb := New(t.TempDir(), Options{})
	old := Note{Content: "old", TS: time.Now().Add(-time.Hour)}
	if err := bb.Append("a", old); err != nil {
		t.Fatal(err)
	}
	if err := bb.Append("a", Note{Content: "new"}); err != nil {
		t.Fatal(err)
	}
	notes := bb.Notes(time.Now().Add(-time.Minute))
	if len(notes) != 1 || notes[0].Content != "new" {
		t.Fatalf("since filter failed: %+v", notes)
	}
}

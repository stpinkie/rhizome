package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stpinkie/rhizome/pkg/fileutil"
)

func TestOpenOrInitCreatesRepo(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo, w, err := OpenOrInit(dir)
	if err != nil {
		t.Fatalf("OpenOrInit: %v", err)
	}
	if repo == nil || w == nil {
		t.Fatal("expected repo and worktree")
	}

	hash, err := Head(repo)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if hash.IsZero() {
		t.Fatal("expected non-zero initial commit")
	}

	// Verify the file is tracked.
	c, err := repo.CommitObject(hash)
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	tree, err := c.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if _, err := tree.File("AGENT.md"); err != nil {
		t.Fatalf("AGENT.md not tracked: %v", err)
	}
}

func TestCommit(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo, w, err := OpenOrInit(dir)
	if err != nil {
		t.Fatalf("OpenOrInit: %v", err)
	}

	// Edit a file after the initial commit.
	if err := fileutil.WriteFileAtomic(filepath.Join(dir, "AGENT.md"), []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := Commit(w, "node-a", "update AGENT.md")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	c, err := repo.CommitObject(hash)
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if c.Message != "update AGENT.md" {
		t.Fatalf("unexpected message: %s", c.Message)
	}
	if c.Author.Name != "node-a" {
		t.Fatalf("unexpected author: %s", c.Author.Name)
	}

	// Verify content.
	tree, err := c.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	f, err := tree.File("AGENT.md")
	if err != nil {
		t.Fatalf("AGENT.md not in commit: %v", err)
	}
	content, err := f.Contents()
	if err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if content != "updated\n" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestHasUncommitted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, w, err := OpenOrInit(dir)
	if err != nil {
		t.Fatalf("OpenOrInit: %v", err)
	}

	if dirty, _ := HasUncommitted(w); dirty {
		t.Fatalf("expected clean worktree after initial commit")
	}

	if err := os.WriteFile(filepath.Join(dir, "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if dirty, _ := HasUncommitted(w); !dirty {
		t.Fatalf("expected dirty worktree after new file")
	}
}

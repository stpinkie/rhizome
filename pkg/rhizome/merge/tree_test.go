package merge

import (
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

var testTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newInMemoryRepo(t *testing.T) (*git.Repository, *git.Worktree) {
	t.Helper()
	repo, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		t.Fatalf("init repo: %v", err)
	}
	w, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	return repo, w
}

func writeFile(t *testing.T, w *git.Worktree, path, content string) {
	t.Helper()
	fs := w.Filesystem
	f, err := fs.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

func commitFiles(
	t *testing.T,
	w *git.Worktree,
	repo *git.Repository,
	files map[string]string,
	msg string,
) plumbing.Hash {
	t.Helper()
	for path, content := range files {
		writeFile(t, w, path, content)
		if _, err := w.Add(path); err != nil {
			t.Fatalf("add %s: %v", path, err)
		}
	}

	hash, err := w.Commit(msg, &git.CommitOptions{
		Author:            &object.Signature{Name: "test", Email: "test@example.com", When: testTime},
		Committer:         &object.Signature{Name: "test", Email: "test@example.com", When: testTime},
		AllowEmptyCommits: true,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return hash
}

func storeCommit(
	t *testing.T,
	repo *git.Repository,
	tree plumbing.Hash,
	parents []plumbing.Hash,
	msg string,
) plumbing.Hash {
	t.Helper()
	c := &object.Commit{
		TreeHash:     tree,
		ParentHashes: parents,
		Author:       object.Signature{Name: "test", Email: "test@example.com", When: testTime},
		Committer:    object.Signature{Name: "test", Email: "test@example.com", When: testTime},
		Message:      msg,
	}
	obj := &plumbing.MemoryObject{}
	obj.SetType(plumbing.CommitObject)
	if err := c.Encode(obj); err != nil {
		t.Fatalf("encode commit: %v", err)
	}
	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("store commit: %v", err)
	}
	return hash
}

func listFiles(t *testing.T, repo *git.Repository, commit plumbing.Hash) []string {
	t.Helper()
	c, err := repo.CommitObject(commit)
	if err != nil {
		t.Fatalf("commit object: %v", err)
	}
	tree, err := c.Tree()
	if err != nil {
		t.Fatalf("tree: %v", err)
	}

	var files []string
	iter := tree.Files()
	if err := iter.ForEach(func(f *object.File) error {
		files = append(files, f.Name)
		return nil
	}); err != nil {
		t.Fatalf("iterate files: %v", err)
	}
	sort.Strings(files)
	return files
}

func fileContent(t *testing.T, repo *git.Repository, commit plumbing.Hash, path string) []byte {
	t.Helper()
	c, err := repo.CommitObject(commit)
	if err != nil {
		t.Fatalf("commit object: %v", err)
	}
	tree, err := c.Tree()
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	f, err := tree.File(path)
	if err != nil {
		t.Fatalf("file %s: %v", path, err)
	}
	r, err := f.Reader()
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	defer r.Close()

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return data
}

func TestMergeTreesFastForward(t *testing.T) {
	repo, w := newInMemoryRepo(t)

	base := commitFiles(t, w, repo, map[string]string{
		"AGENT.md":    "base\n",
		"skills/x.md": "x\n",
	}, "base")

	o := commitFiles(t, w, repo, map[string]string{
		"AGENT.md": "base\n",
	}, "ours")

	th := commitFiles(t, w, repo, map[string]string{
		"AGENT.md":    "theirs\n",
		"skills/y.md": "y\n",
	}, "theirs")

	baseCommit, _ := repo.CommitObject(base)
	oCommit, _ := repo.CommitObject(o)
	thCommit, _ := repo.CommitObject(th)

	mergedTree, conflicts, err := MergeTrees(repo.Storer, baseCommit.TreeHash, oCommit.TreeHash, thCommit.TreeHash)
	if err != nil {
		t.Fatalf("MergeTrees: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflicts, got %v", conflicts)
	}

	mergedCommit := storeCommit(t, repo, mergedTree, []plumbing.Hash{o, th}, "merge")

	files := listFiles(t, repo, mergedCommit)
	want := []string{"AGENT.md", "skills/x.md", "skills/y.md"}
	if !slicesEqual(files, want) {
		t.Fatalf("files: got %v, want %v", files, want)
	}

	if string(fileContent(t, repo, mergedCommit, "AGENT.md")) != "theirs\n" {
		t.Fatalf("AGENT.md should be theirs")
	}
}

func TestMergeTreesConflict(t *testing.T) {
	repo, w := newInMemoryRepo(t)

	base := commitFiles(t, w, repo, map[string]string{
		"AGENT.md": "line1\nline2\n",
	}, "base")

	o := commitFiles(t, w, repo, map[string]string{
		"AGENT.md": "line1\nline2 ours\n",
	}, "ours")

	th := commitFiles(t, w, repo, map[string]string{
		"AGENT.md": "line1\nline2 theirs\n",
	}, "theirs")

	baseCommit, _ := repo.CommitObject(base)
	oCommit, _ := repo.CommitObject(o)
	thCommit, _ := repo.CommitObject(th)

	mergedTree, conflicts, err := MergeTrees(repo.Storer, baseCommit.TreeHash, oCommit.TreeHash, thCommit.TreeHash)
	if err != nil {
		t.Fatalf("MergeTrees: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0] != "AGENT.md" {
		t.Fatalf("expected conflict on AGENT.md, got %v", conflicts)
	}

	mergedCommit := storeCommit(t, repo, mergedTree, []plumbing.Hash{o, th}, "merge")

	content := string(fileContent(t, repo, mergedCommit, "AGENT.md"))
	if !strings.Contains(content, "<<<<<<<") {
		t.Fatalf("expected conflict markers, got:\n%s", content)
	}
}

func TestMergeTreesNestedDirectory(t *testing.T) {
	repo, w := newInMemoryRepo(t)

	base := commitFiles(t, w, repo, map[string]string{
		"skills/weather/SKILL.md": "base weather\n",
	}, "base")

	o := commitFiles(t, w, repo, map[string]string{
		"skills/weather/SKILL.md": "base weather\n",
	}, "ours")

	th := commitFiles(t, w, repo, map[string]string{
		"skills/weather/SKILL.md": "theirs weather\n",
	}, "theirs")

	baseCommit, _ := repo.CommitObject(base)
	oCommit, _ := repo.CommitObject(o)
	thCommit, _ := repo.CommitObject(th)

	mergedTree, conflicts, err := MergeTrees(repo.Storer, baseCommit.TreeHash, oCommit.TreeHash, thCommit.TreeHash)
	if err != nil {
		t.Fatalf("MergeTrees: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("expected no conflict, got %v", conflicts)
	}

	mergedCommit := storeCommit(t, repo, mergedTree, []plumbing.Hash{o, th}, "merge")

	content := string(fileContent(t, repo, mergedCommit, "skills/weather/SKILL.md"))
	if content != "theirs weather\n" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMergeTreesBothAddDifferent(t *testing.T) {
	repo, w := newInMemoryRepo(t)

	base := commitFiles(t, w, repo, map[string]string{}, "base")

	o := commitFiles(t, w, repo, map[string]string{
		"AGENT.md": "ours content\n",
	}, "ours")

	th := commitFiles(t, w, repo, map[string]string{
		"AGENT.md": "theirs content\n",
	}, "theirs")

	baseCommit, _ := repo.CommitObject(base)
	oCommit, _ := repo.CommitObject(o)
	thCommit, _ := repo.CommitObject(th)

	mergedTree, conflicts, err := MergeTrees(repo.Storer, baseCommit.TreeHash, oCommit.TreeHash, thCommit.TreeHash)
	if err != nil {
		t.Fatalf("MergeTrees: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected conflict, got %v", conflicts)
	}

	mergedCommit := storeCommit(t, repo, mergedTree, []plumbing.Hash{o, th}, "merge")
	content := string(fileContent(t, repo, mergedCommit, "AGENT.md"))
	if !strings.Contains(content, "<<<<<<<") {
		t.Fatalf("expected conflict markers, got:\n%s", content)
	}
}

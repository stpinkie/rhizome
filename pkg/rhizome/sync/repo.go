package sync

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// initialCommitTime is fixed so every node bootstrapped with the same workspace
// template produces the same initial commit hash.
var initialCommitTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	// InitialCommitMessage is used for the first workspace commit.
	InitialCommitMessage = "chore: initial Rhizome workspace"

	// DefaultBranch is the workspace git branch.
	DefaultBranch = "main"
)

// defaultSignature returns the deterministic author/committer used for the
// initial commit. Subsequent commits use node-specific names.
func defaultSignature() *object.Signature {
	return &object.Signature{
		Name:  "Rhizome",
		Email: "rhizome@local",
		When:  initialCommitTime,
	}
}

// OpenOrInit opens the workspace git repo, creating it and an initial commit
// when necessary. It also ensures .git/info/exclude contains the volatile
// workspace paths.
func OpenOrInit(workspace string) (*git.Repository, *git.Worktree, error) {
	gitDir := filepath.Join(workspace, ".git")

	var repo *git.Repository
	var err error

	if _, err = os.Stat(gitDir); err != nil {
		if !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("stat git dir: %w", err)
		}
		repo, err = git.PlainInitWithOptions(workspace, &git.PlainInitOptions{
			InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("refs/heads/" + DefaultBranch)},
			Bare:        false,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("init git repo: %w", err)
		}
	} else {
		repo, err = git.PlainOpen(workspace)
		if err != nil {
			return nil, nil, fmt.Errorf("open git repo: %w", err)
		}
	}

	w, err := repo.Worktree()
	if err != nil {
		return nil, nil, fmt.Errorf("worktree: %w", err)
	}

	if err := writeGitExclude(gitDir); err != nil {
		return nil, nil, fmt.Errorf("write git exclude: %w", err)
	}

	hasCommit, err := hasCommit(repo)
	if err != nil {
		return nil, nil, fmt.Errorf("check head: %w", err)
	}
	if !hasCommit {
		if _, err := w.Add("."); err != nil {
			return nil, nil, fmt.Errorf("stage initial files: %w", err)
		}
		if _, err := w.Commit(InitialCommitMessage, &git.CommitOptions{
			Author:            defaultSignature(),
			Committer:         defaultSignature(),
			AllowEmptyCommits: true,
		}); err != nil {
			return nil, nil, fmt.Errorf("initial commit: %w", err)
		}
	}

	return repo, w, nil
}

// Commit stages all workspace changes and creates a commit. It returns the
// new commit hash.
func Commit(w *git.Worktree, nodeName, message string) (plumbing.Hash, error) {
	if _, err := w.Add("."); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("stage: %w", err)
	}

	sig := &object.Signature{
		Name:  nodeName,
		Email: nodeName + "@rhizome.local",
		When:  time.Now().UTC(),
	}

	hash, err := w.Commit(message, &git.CommitOptions{
		Author:            sig,
		Committer:         sig,
		AllowEmptyCommits: false,
	})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("commit: %w", err)
	}
	return hash, nil
}

// Head returns the current workspace HEAD commit hash.
func Head(repo *git.Repository) (plumbing.Hash, error) {
	head, err := repo.Head()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	return head.Hash(), nil
}

// HasUncommitted returns true if the workspace has uncommitted changes.
func HasUncommitted(w *git.Worktree) (bool, error) {
	status, err := w.Status()
	if err != nil {
		return false, err
	}
	return !status.IsClean(), nil
}

// ConflictPaths returns the set of files containing conflict markers in the
// worktree. This is a cheap scan that does not inspect the git index.
func ConflictPaths(w *git.Worktree) ([]string, error) {
	var out []string
	err := filepath.WalkDir(w.Filesystem.Root(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // ignore transient files
		}
		if containsConflictMarkers(data) {
			rel, _ := filepath.Rel(w.Filesystem.Root(), path)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func containsConflictMarkers(data []byte) bool {
	return bytes.Contains(data, []byte("<<<<<<<")) &&
		bytes.Contains(data, []byte("=====")) &&
		bytes.Contains(data, []byte(">>>>>>>"))
}

func hasCommit(repo *git.Repository) (bool, error) {
	_, err := repo.Head()
	if err == plumbing.ErrReferenceNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

const gitExclude = `# Rhizome local-only files. Shared via workspace/.gitignore as well.
.git/
state/
state.json
sessions/
cron/
media/
logs/
.artifacts/
whatsapp/
matrix/
HEARTBEAT.md
heartbeat.log
*.sqlite
*.db
tmp/
*.tmp
`

func writeGitExclude(gitDir string) error {
	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(infoDir, "exclude"), []byte(gitExclude), 0o644)
}

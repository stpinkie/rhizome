package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg"
	"github.com/stpinkie/rhizome/pkg/config"
)

func TestWorkspacePath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.EnvHome, tmpDir)

	got := workspacePath()
	want := filepath.Join(tmpDir, pkg.WorkspaceName)
	assert.Equal(t, want, got)
}

func TestOpenRepoCreatesRepo(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.EnvHome, tmpDir)

	repo, w, err := openRepo()
	require.NoError(t, err)
	require.NotNil(t, repo)
	require.NotNil(t, w)

	gitDir := filepath.Join(workspacePath(), ".git")
	_, err = os.Stat(gitDir)
	require.NoError(t, err, "workspace should contain a .git directory")
}

func TestLoadIdentityMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv(config.EnvHome, tmpDir)

	_, _, err := loadIdentity()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node.json")
}

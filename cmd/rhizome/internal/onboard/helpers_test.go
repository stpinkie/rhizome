package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopyEmbeddedToTargetUsesStructuredAgentFiles(t *testing.T) {
	targetDir := t.TempDir()

	if err := copyEmbeddedToTarget(targetDir); err != nil {
		t.Fatalf("copyEmbeddedToTarget() error = %v", err)
	}

	for _, name := range []string{"AGENT.md", "SOUL.md", "USER.md"} {
		p := filepath.Join(targetDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}

	skillsDir := filepath.Join(targetDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	require.NoError(t, err, "expected skills directory to be created")
	require.NotEmpty(t, entries, "expected at least one skill in the embedded workspace")

	for _, legacy := range []string{"AGENTS.md", "IDENTITY.md"} {
		legacyPath := filepath.Join(targetDir, legacy)
		if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
			t.Fatalf("expected legacy file %s to be absent, got err=%v", legacyPath, err)
		}
	}
}

func TestCopyEmbeddedToTargetSkipsExistingFiles(t *testing.T) {
	targetDir := t.TempDir()

	if err := copyEmbeddedToTarget(targetDir); err != nil {
		t.Fatalf("copyEmbeddedToTarget() error = %v", err)
	}

	agentPath := filepath.Join(targetDir, "AGENT.md")

	// Mutate the copied file to prove a second copy does not overwrite it.
	if err := os.WriteFile(agentPath, []byte("DO NOT OVERWRITE"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := copyEmbeddedToTarget(targetDir); err != nil {
		t.Fatalf("copyEmbeddedToTarget() second call error = %v", err)
	}

	updated, err := os.ReadFile(agentPath)
	require.NoError(t, err)
	require.Equal(t, "DO NOT OVERWRITE", string(updated), "copyEmbeddedToTarget should not overwrite existing files")

	// Files that did not exist before should still be restored/created on a second run.
	for _, name := range []string{"SOUL.md", "USER.md"} {
		p := filepath.Join(targetDir, name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist after second copy: %v", p, err)
		}
	}
}

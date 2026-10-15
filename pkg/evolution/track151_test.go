package evolution_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

// tickingNow returns distinct successive timestamps so backup dir names
// never collide.
func tickingNow(start int64) func() time.Time {
	var counter int64
	return func() time.Time {
		counter++
		return time.Unix(start+counter, 0).UTC()
	}
}

func skillBody(name, desc, extra string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\n# " + name + "\n\n" + extra + "\n"
}

// newRollbackRuntime builds a runtime whose store/applier share one paths
// root, plus a helper that seeds and accepts candidate drafts through the
// production apply path.
func newRollbackRuntime(t *testing.T, cfg config.EvolutionConfig) (*evolution.Runtime, *evolution.Store, string) {
	t.Helper()
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config:  cfg,
		Now:     tickingNow(1700000000),
		Store:   store,
		Applier: evolution.NewApplier(paths, tickingNow(1700050000)),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt, store, root
}

func acceptDraft(t *testing.T, rt *evolution.Runtime, store *evolution.Store, workspace string,
	draft evolution.SkillDraft,
) {
	t.Helper()
	draft.WorkspaceID = workspace
	draft.Status = evolution.DraftStatusCandidate
	if err := store.SaveDrafts([]evolution.SkillDraft{draft}); err != nil {
		t.Fatalf("SaveDrafts: %v", err)
	}
	if _, err := rt.AcceptDraft(context.Background(), workspace, draft.ID, false); err != nil {
		t.Fatalf("AcceptDraft(%s): %v", draft.ID, err)
	}
}

func readSkill(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "skills", name, "SKILL.md"))
	if err != nil {
		t.Fatalf("read skill %s: %v", name, err)
	}
	return string(data)
}

// sameBody compares rendered content — the applier trims the trailing
// newline, so fixtures compare whitespace-normalized.
func sameBody(got, want string) bool {
	return strings.TrimSpace(got) == strings.TrimSpace(want)
}

func TestTrack151_RollbackRestoresPriorContent(t *testing.T) {
	rt, store, root := newRollbackRuntime(t, config.DefaultConfig().Evolution)

	v1 := skillBody("weather", "v1 weather", "Use native names.")
	v2 := skillBody("weather", "v2 weather", "Use native names.\n\n## Extras\nCache lookups.")
	acceptDraft(t, rt, store, root, evolution.SkillDraft{
		ID: "draft-v1", TargetSkillName: "weather", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindCreate, HumanSummary: "v1", BodyOrPatch: v1,
	})
	acceptDraft(t, rt, store, root, evolution.SkillDraft{
		ID: "draft-v2", TargetSkillName: "weather", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindReplace, HumanSummary: "v2", BodyOrPatch: v2,
	})
	if got := readSkill(t, root, "weather"); !sameBody(got, v2) {
		t.Fatalf("after applies content = %q, want v2 body", got)
	}

	result, err := rt.RollbackSkill(context.Background(), root, "weather", evolution.RollbackRequest{})
	if err != nil {
		t.Fatalf("RollbackSkill: %v", err)
	}
	if !result.Restored || result.Removed {
		t.Fatalf("result = %+v, want restored", result)
	}
	if got := readSkill(t, root, "weather"); !sameBody(got, v1) {
		t.Fatalf("after rollback content = %q, want v1 body", got)
	}
	if result.RestoredVersion != "draft-v1" {
		t.Fatalf("RestoredVersion = %q, want draft-v1", result.RestoredVersion)
	}
	if result.BackedUpTo == "" {
		t.Fatal("BackedUpTo should name the pre-rollback snapshot")
	}
}

func TestTrack151_RollbackToNamedVersion(t *testing.T) {
	rt, store, root := newRollbackRuntime(t, config.DefaultConfig().Evolution)

	v1 := skillBody("weather", "v1", "first")
	v2 := skillBody("weather", "v2", "second")
	v3 := skillBody("weather", "v3", "third")
	for i, body := range []string{v1, v2, v3} {
		kind := evolution.ChangeKindReplace
		if i == 0 {
			kind = evolution.ChangeKindCreate
		}
		acceptDraft(t, rt, store, root, evolution.SkillDraft{
			ID: "draft-v" + string(rune('1'+i)), TargetSkillName: "weather",
			DraftType: evolution.DraftTypeShortcut, ChangeKind: kind,
			HumanSummary: "v", BodyOrPatch: body,
		})
	}

	result, err := rt.RollbackSkill(context.Background(), root, "weather", evolution.RollbackRequest{To: "draft-v1"})
	if err != nil {
		t.Fatalf("RollbackSkill --to: %v", err)
	}
	if result.RestoredVersion != "draft-v1" {
		t.Fatalf("RestoredVersion = %q, want draft-v1", result.RestoredVersion)
	}
	if got := readSkill(t, root, "weather"); !sameBody(got, v1) {
		t.Fatalf("content = %q, want v1 body", got)
	}

	// --to draft-v3 from there lands on the latest snapshot.
	if _, err := rt.RollbackSkill(context.Background(), root, "weather",
		evolution.RollbackRequest{To: "draft-v3"}); err != nil {
		t.Fatalf("RollbackSkill --to v3: %v", err)
	}
	if got := readSkill(t, root, "weather"); !sameBody(got, v3) {
		t.Fatalf("content = %q, want v3 body", got)
	}
}

func TestTrack151_RollbackWritesHistoryEntry(t *testing.T) {
	rt, store, root := newRollbackRuntime(t, config.DefaultConfig().Evolution)

	v1 := skillBody("weather", "v1", "first")
	v2 := skillBody("weather", "v2", "second")
	acceptDraft(t, rt, store, root, evolution.SkillDraft{
		ID: "draft-v1", TargetSkillName: "weather", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindCreate, HumanSummary: "v1", BodyOrPatch: v1,
	})
	acceptDraft(t, rt, store, root, evolution.SkillDraft{
		ID: "draft-v2", TargetSkillName: "weather", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindReplace, HumanSummary: "v2", BodyOrPatch: v2,
	})
	if _, err := rt.RollbackSkill(context.Background(), root, "weather", evolution.RollbackRequest{}); err != nil {
		t.Fatalf("RollbackSkill: %v", err)
	}

	profile, err := store.LoadProfile("weather")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.CurrentVersion != "draft-v1" {
		t.Fatalf("CurrentVersion = %q, want draft-v1", profile.CurrentVersion)
	}
	last := profile.VersionHistory[len(profile.VersionHistory)-1]
	if !last.Rollback || last.RollbackReason == "" {
		t.Fatalf("last entry = %+v, want Rollback=true with reason", last)
	}
	if last.Action != "rollback" {
		t.Fatalf("last entry Action = %q, want rollback", last.Action)
	}
}

func TestTrack151_RollbackMissingBackupErrors(t *testing.T) {
	rt, _, root := newRollbackRuntime(t, config.DefaultConfig().Evolution)

	_, err := rt.RollbackSkill(context.Background(), root, "never-applied", evolution.RollbackRequest{})
	if err == nil {
		t.Fatal("expected error for skill with no backups")
	}
	if !errors.Is(err, evolution.ErrRollbackUnavailable) {
		t.Fatalf("err = %v, want ErrRollbackUnavailable", err)
	}
	if !strings.Contains(err.Error(), "backup_keep") {
		t.Fatalf("err = %v, want archived-path hint mentioning backup_keep", err)
	}

	_, err = rt.RollbackSkill(context.Background(), root, "never-applied", evolution.RollbackRequest{To: "draft-x"})
	if !errors.Is(err, evolution.ErrRollbackUnavailable) {
		t.Fatalf("--to err = %v, want ErrRollbackUnavailable", err)
	}
}

func TestTrack151_RollbackDryRunWritesNothing(t *testing.T) {
	rt, store, root := newRollbackRuntime(t, config.DefaultConfig().Evolution)

	v1 := skillBody("weather", "v1", "first")
	v2 := skillBody("weather", "v2", "second")
	acceptDraft(t, rt, store, root, evolution.SkillDraft{
		ID: "draft-v1", TargetSkillName: "weather", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindCreate, HumanSummary: "v1", BodyOrPatch: v1,
	})
	acceptDraft(t, rt, store, root, evolution.SkillDraft{
		ID: "draft-v2", TargetSkillName: "weather", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindReplace, HumanSummary: "v2", BodyOrPatch: v2,
	})

	result, err := rt.RollbackSkill(context.Background(), root, "weather", evolution.RollbackRequest{DryRun: true})
	if err != nil {
		t.Fatalf("RollbackSkill dry-run: %v", err)
	}
	if result.Restored {
		t.Fatal("dry-run must not report restored")
	}
	if got := readSkill(t, root, "weather"); !sameBody(got, v2) {
		t.Fatalf("dry-run changed content to %q, want v2 untouched", got)
	}
	profile, err := store.LoadProfile("weather")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	for _, e := range profile.VersionHistory {
		if e.Rollback {
			t.Fatal("dry-run must not append a rollback entry")
		}
	}
}

func TestTrack151_RollbackCreatedSkillRemovesFile(t *testing.T) {
	rt, store, root := newRollbackRuntime(t, config.DefaultConfig().Evolution)

	v1 := skillBody("fresh-skill", "v1", "first")
	acceptDraft(t, rt, store, root, evolution.SkillDraft{
		ID: "draft-v1", TargetSkillName: "fresh-skill", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindCreate, HumanSummary: "v1", BodyOrPatch: v1,
	})

	// The create left no pre-apply backup — but its post-apply version
	// snapshot exists, so the newest *timestamped* backup is absent and the
	// pre-create absence is the restore target.
	result, err := rt.RollbackSkill(context.Background(), root, "fresh-skill", evolution.RollbackRequest{})
	if err != nil {
		t.Fatalf("RollbackSkill: %v", err)
	}
	if !result.Removed {
		t.Fatalf("result = %+v, want removed", result)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "fresh-skill", "SKILL.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("skill file still present after pre-create rollback: %v", err)
	}
	profile, err := store.LoadProfile("fresh-skill")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.Status != evolution.SkillStatusDeleted || profile.CurrentVersion != "" {
		t.Fatalf("profile = %+v, want deleted with empty version", profile)
	}
}

func TestTrack151_BackupKeepPrunesSnapshots(t *testing.T) {
	cfg := config.DefaultConfig().Evolution
	cfg.BackupKeep = 2
	rt, store, root := newRollbackRuntime(t, cfg)

	for i := 1; i <= 4; i++ {
		kind := evolution.ChangeKindReplace
		if i == 1 {
			kind = evolution.ChangeKindCreate
		}
		acceptDraft(t, rt, store, root, evolution.SkillDraft{
			ID: "draft-v" + string(rune('0'+i)), TargetSkillName: "weather",
			DraftType: evolution.DraftTypeShortcut, ChangeKind: kind,
			HumanSummary: "v", BodyOrPatch: skillBody("weather", "v", "body"),
		})
	}

	backupsRoot := evolution.NewPaths(root, "").BackupsDir
	// Count surviving snapshots by position in the tree:
	// <backups>/<wsScope>/weather/<ts>/ and <backups>/<wsScope>/weather/versions/<ver>/.
	versionCount := 0
	timestampedCount := 0
	if walkErr := filepath.Walk(backupsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		switch filepath.Base(filepath.Dir(path)) {
		case "versions":
			versionCount++
		case "weather":
			if filepath.Base(path) != "versions" {
				timestampedCount++
			}
		}
		return nil
	}); walkErr != nil {
		t.Fatalf("walk backups: %v", walkErr)
	}
	if versionCount > 2 {
		t.Fatalf("version snapshots = %d, want <= 2", versionCount)
	}
	if timestampedCount > 2 {
		t.Fatalf("timestamped backups = %d, want <= 2", timestampedCount)
	}

	// The pruned oldest version is no longer a --to target.
	_, err := rt.RollbackSkill(context.Background(), root, "weather", evolution.RollbackRequest{To: "draft-v1"})
	if !errors.Is(err, evolution.ErrRollbackUnavailable) {
		t.Fatalf("--to pruned version err = %v, want ErrRollbackUnavailable", err)
	}
}

func TestTrack151_RollbackRejectsCorruptSnapshot(t *testing.T) {
	rt, store, root := newRollbackRuntime(t, config.DefaultConfig().Evolution)

	// Apply over a hand-written skill so a timestamped backup exists.
	manual := skillBody("weather", "manual", "hand written")
	if err := os.MkdirAll(filepath.Join(root, "skills", "weather"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "weather", "SKILL.md"), []byte(manual), 0o644); err != nil {
		t.Fatal(err)
	}
	acceptDraft(t, rt, store, root, evolution.SkillDraft{
		ID: "draft-v1", TargetSkillName: "weather", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindReplace, HumanSummary: "v1",
		BodyOrPatch: skillBody("weather", "v1", "first"),
	})

	// Corrupt the newest timestamped backup — a name mismatch must fail
	// the same validation an apply runs.
	backupsDir := evolution.NewPaths(root, "").BackupsDir
	var backupFile string
	_ = filepath.Walk(backupsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(path) == "SKILL.md" &&
			filepath.Base(filepath.Dir(filepath.Dir(path))) != "versions" {
			backupFile = path
		}
		return nil
	})
	if backupFile == "" {
		t.Fatal("expected a timestamped backup")
	}
	if err := os.WriteFile(backupFile, []byte(skillBody("other-name", "x", "y")), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := rt.RollbackSkill(context.Background(), root, "weather", evolution.RollbackRequest{})
	if err == nil || !strings.Contains(err.Error(), "failed validation") {
		t.Fatalf("err = %v, want validation failure", err)
	}
	if got := readSkill(t, root, "weather"); !strings.Contains(got, "first") {
		t.Fatalf("content changed despite rejected snapshot: %q", got)
	}
}

package evolution_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

var track153Now = time.Unix(1700002000, 0).UTC()

func newTrack153Store(t *testing.T, root string) *evolution.Store {
	t.Helper()
	return evolution.NewStore(evolution.NewPaths(root, ""))
}

func seedTrack153Profile(t *testing.T, store *evolution.Store, root, name string, status evolution.SkillStatus) {
	t.Helper()
	if err := store.SaveProfile(evolution.SkillProfile{
		SkillName:      name,
		WorkspaceID:    root,
		CurrentVersion: "v1",
		Status:         status,
		Origin:         "evolved",
		LastUsedAt:     track153Now,
		RetentionScore: 0.9,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
}

func TestTransitionSkillArchiveAndRestore(t *testing.T) {
	root := t.TempDir()
	store := newTrack153Store(t, root)
	seedTrack153Profile(t, store, root, "weather", evolution.SkillStatusActive)

	profile, err := evolution.TransitionSkill(store, root, "weather", evolution.SkillStatusArchived, track153Now)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if profile.Status != evolution.SkillStatusArchived {
		t.Fatalf("status = %q", profile.Status)
	}
	last := profile.VersionHistory[len(profile.VersionHistory)-1]
	if last.Action != "lifecycle:archived" {
		t.Fatalf("history action = %q", last.Action)
	}

	profile, err = evolution.TransitionSkill(store, root, "weather", evolution.SkillStatusActive, track153Now)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if profile.Status != evolution.SkillStatusActive {
		t.Fatalf("status = %q", profile.Status)
	}
	if n := len(profile.VersionHistory); n != 2 {
		t.Fatalf("history entries = %d, want 2", n)
	}
}

func TestTransitionSkillRejectsInvalidTransitions(t *testing.T) {
	root := t.TempDir()
	store := newTrack153Store(t, root)
	seedTrack153Profile(t, store, root, "weather", evolution.SkillStatusActive)

	if _, err := evolution.TransitionSkill(
		store, root, "weather", evolution.SkillStatusActive, track153Now,
	); !errors.Is(err, evolution.ErrSkillTransition) {
		t.Fatalf("restore active: %v", err)
	}
	if _, err := evolution.TransitionSkill(
		store, root, "weather", evolution.SkillStatusDeleted, track153Now,
	); !errors.Is(err, evolution.ErrSkillTransition) {
		t.Fatalf("manual delete: %v", err)
	}

	seedTrack153Profile(t, store, root, "gone", evolution.SkillStatusDeleted)
	if _, err := evolution.TransitionSkill(
		store, root, "gone", evolution.SkillStatusArchived, track153Now,
	); !errors.Is(err, evolution.ErrSkillTransition) {
		t.Fatalf("archive deleted: %v", err)
	}
	if _, err := evolution.TransitionSkill(
		store, root, "gone", evolution.SkillStatusActive, track153Now,
	); !errors.Is(err, evolution.ErrSkillTransition) {
		t.Fatalf("restore deleted: %v", err)
	}
	if _, err := evolution.TransitionSkill(
		store, root, "ghost", evolution.SkillStatusArchived, track153Now,
	); err == nil {
		t.Fatal("expected no-profile error")
	}
}

func TestPruneExpiredTaskRecords(t *testing.T) {
	root := t.TempDir()
	store := newTrack153Store(t, root)
	old := track153Now.AddDate(0, 0, -100)
	fresh := track153Now.AddDate(0, 0, -1)

	records := []evolution.LearningRecord{
		{
			ID: "old-1", Kind: evolution.RecordKindTask, WorkspaceID: root,
			CreatedAt: old, Status: evolution.RecordStatus("new"),
		},
		{
			ID: "fresh-1", Kind: evolution.RecordKindTask, WorkspaceID: root,
			CreatedAt: fresh, Status: evolution.RecordStatus("new"),
		},
		// Clustered records are the accepted history patterns cite —
		// expired or not, they are never pruned.
		{
			ID: "old-clustered", Kind: evolution.RecordKindTask, WorkspaceID: root,
			CreatedAt: old, Status: evolution.RecordStatus("clustered"),
		},
		// Another workspace's expired record is not ours to prune.
		{
			ID: "other-old", Kind: evolution.RecordKindTask, WorkspaceID: t.TempDir(),
			CreatedAt: old, Status: evolution.RecordStatus("new"),
		},
	}
	for _, r := range records {
		if err := store.AppendTaskRecord(context.Background(), r); err != nil {
			t.Fatalf("AppendTaskRecord: %v", err)
		}
	}

	summary, err := evolution.PruneExpired(store, root, track153Now, 90, 30, false)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if summary.TaskRecordsPruned != 1 {
		t.Fatalf("pruned = %d, want 1", summary.TaskRecordsPruned)
	}
	loaded, err := store.LoadTaskRecords()
	if err != nil {
		t.Fatalf("LoadTaskRecords: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range loaded {
		ids[r.ID] = true
	}
	if ids["old-1"] || !ids["fresh-1"] || !ids["other-old"] || !ids["old-clustered"] {
		t.Fatalf("unexpected survivors: %v", ids)
	}
}

func TestPruneExpiredMigratesLegacyLearningLog(t *testing.T) {
	root := t.TempDir()
	store := newTrack153Store(t, root)
	old := track153Now.AddDate(0, 0, -100)

	// Seed the legacy learning-records.jsonl directly — the file today's
	// append path no longer writes but LoadTaskRecords still merges.
	legacy := filepath.Join(evolution.NewPaths(root, "").RootDir, "learning-records.jsonl")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(evolution.LearningRecord{
		ID: "legacy-old", Kind: evolution.RecordKindTask, WorkspaceID: root,
		CreatedAt: old, Status: evolution.RecordStatus("completed"), Summary: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	summary, err := evolution.PruneExpired(store, root, track153Now, 90, 0, false)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if summary.TaskRecordsPruned != 1 {
		t.Fatalf("pruned = %d, want 1", summary.TaskRecordsPruned)
	}
	loaded, err := store.LoadTaskRecords()
	if err != nil {
		t.Fatalf("LoadTaskRecords: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("pruned legacy record reappeared: %v", loaded)
	}
}

func TestPruneExpiredDraftsKeepsUnsettledAndAccepted(t *testing.T) {
	root := t.TempDir()
	store := newTrack153Store(t, root)
	old := track153Now.AddDate(0, 0, -60)

	drafts := []evolution.SkillDraft{
		{
			ID: "rejected-old", WorkspaceID: root, TargetSkillName: "a",
			Status: evolution.DraftStatusRejected, CreatedAt: old,
		},
		{
			ID: "candidate-old", WorkspaceID: root, TargetSkillName: "b",
			Status: evolution.DraftStatusCandidate, CreatedAt: old,
		},
		{
			ID: "quarantined-old", WorkspaceID: root, TargetSkillName: "c",
			Status: evolution.DraftStatusQuarantined, CreatedAt: old,
		},
		{
			ID: "accepted-old", WorkspaceID: root, TargetSkillName: "d",
			Status: evolution.DraftStatusAccepted, CreatedAt: old,
		},
		{
			ID: "rejected-fresh", WorkspaceID: root, TargetSkillName: "e",
			Status: evolution.DraftStatusRejected, CreatedAt: track153Now,
		},
	}
	if err := store.ReplaceDrafts(drafts); err != nil {
		t.Fatalf("ReplaceDrafts: %v", err)
	}

	summary, err := evolution.PruneExpired(store, root, track153Now, 90, 30, false)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if summary.DraftsPruned != 1 {
		t.Fatalf("pruned = %d, want 1", summary.DraftsPruned)
	}
	loaded, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	ids := map[string]bool{}
	for _, d := range loaded {
		ids[d.ID] = true
	}
	if ids["rejected-old"] {
		t.Fatal("rejected draft survived")
	}
	for _, keep := range []string{"candidate-old", "quarantined-old", "accepted-old", "rejected-fresh"} {
		if !ids[keep] {
			t.Fatalf("%s pruned", keep)
		}
	}
}

func TestPruneExpiredDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	store := newTrack153Store(t, root)
	old := track153Now.AddDate(0, 0, -100)
	if err := store.AppendTaskRecord(context.Background(), evolution.LearningRecord{
		ID: "old-1", Kind: evolution.RecordKindTask, WorkspaceID: root,
		CreatedAt: old, Status: evolution.RecordStatus("completed"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDrafts([]evolution.SkillDraft{
		{
			ID: "rejected-old", WorkspaceID: root, TargetSkillName: "a",
			Status: evolution.DraftStatusRejected, CreatedAt: old,
		},
	}); err != nil {
		t.Fatal(err)
	}

	summary, err := evolution.PruneExpired(store, root, track153Now, 90, 30, true)
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if !summary.DryRun || summary.TaskRecordsPruned != 1 || summary.DraftsPruned != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	records, _ := store.LoadTaskRecords()
	drafts, _ := store.LoadDrafts()
	if len(records) != 1 || len(drafts) != 1 {
		t.Fatalf("dry run wrote: %d records, %d drafts", len(records), len(drafts))
	}
}

func TestRunLifecycleOncePrunesWithConfiguredWindows(t *testing.T) {
	root := t.TempDir()
	store := newTrack153Store(t, root)
	old := time.Now().UTC().AddDate(0, 0, -100)
	if err := store.AppendTaskRecord(context.Background(), evolution.LearningRecord{
		ID: "old-1", Kind: evolution.RecordKindTask, WorkspaceID: root,
		CreatedAt: old, Status: evolution.RecordStatus("completed"),
	}); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	cfg.Evolution.RetentionTaskRecordsDays = 90
	cfg.Evolution.RetentionDraftsDays = 30
	config.SetGlobal(cfg)
	defer config.SetGlobal(nil)

	summary, err := evolution.RunLifecycleOnce(store, evolution.NewPaths(root, ""), root, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunLifecycleOnce: %v", err)
	}
	if summary.PrunedTaskRecords != 1 {
		t.Fatalf("pruned = %d, want 1", summary.PrunedTaskRecords)
	}
}

func TestRuntimeArchiveRestoreAndPrune(t *testing.T) {
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	seedTrack153Profile(t, store, root, "weather", evolution.SkillStatusActive)

	old := track153Now.AddDate(0, 0, -100)
	if err := store.AppendTaskRecord(context.Background(), evolution.LearningRecord{
		ID: "old-1", Kind: evolution.RecordKindTask, WorkspaceID: root,
		CreatedAt: old, Status: evolution.RecordStatus("completed"),
	}); err != nil {
		t.Fatal(err)
	}

	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{Enabled: true, Mode: "draft"},
		Now:    func() time.Time { return track153Now },
		Store:  store,
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	profile, err := rt.ArchiveSkill(root, "weather")
	if err != nil || profile.Status != evolution.SkillStatusArchived {
		t.Fatalf("ArchiveSkill: %v %q", err, profile.Status)
	}
	profile, err = rt.RestoreSkill(root, "weather")
	if err != nil || profile.Status != evolution.SkillStatusActive {
		t.Fatalf("RestoreSkill: %v %q", err, profile.Status)
	}
	summary, err := rt.PruneState(root, false)
	if err != nil {
		t.Fatalf("PruneState: %v", err)
	}
	if summary.TaskRecordsPruned != 1 {
		t.Fatalf("pruned = %d, want 1", summary.TaskRecordsPruned)
	}
}

package evolution_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

const testSkillBody = "---\nname: weather\ndescription: weather helper\n---\n# Weather\nUse current workspace evidence.\n"

func newAdminRuntime(t *testing.T, cfg config.EvolutionConfig, opts evolution.RuntimeOptions) *evolution.Runtime {
	t.Helper()
	opts.Config = cfg
	rt, err := evolution.NewRuntime(opts)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt
}

func seedPatternRecord(t *testing.T, store *evolution.Store, workspace, id string) {
	t.Helper()
	rule := evolution.LearningRecord{
		ID:          id,
		Kind:        evolution.RecordKindPattern,
		WorkspaceID: workspace,
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		Summary:     "weather native-name path",
		Status:      evolution.RecordStatus("ready"),
		EventCount:  4,
	}
	if err := store.AppendPatternRecords([]evolution.LearningRecord{rule}); err != nil {
		t.Fatalf("AppendPatternRecords: %v", err)
	}
}

func TestPreviewColdPath_Disabled(t *testing.T) {
	root := t.TempDir()
	rt := newAdminRuntime(t, config.EvolutionConfig{Enabled: false}, evolution.RuntimeOptions{})

	s := rt.PreviewColdPath(root)
	if !s.DryRun || s.Enabled || s.Ran {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if s.Note != "evolution is disabled" {
		t.Fatalf("expected disabled note, got %q", s.Note)
	}
}

func TestPreviewColdPath_ObserveMode(t *testing.T) {
	root := t.TempDir()
	rt := newAdminRuntime(t, config.EvolutionConfig{Enabled: true, Mode: "observe"}, evolution.RuntimeOptions{})

	s := rt.PreviewColdPath(root)
	if s.Note != "observe mode produces no drafts" {
		t.Fatalf("expected observe note, got %q", s.Note)
	}
}

func TestPreviewColdPath_Counts(t *testing.T) {
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	seedPatternRecord(t, store, root, "rule-1")
	if err := store.AppendTaskRecord(context.Background(), evolution.LearningRecord{
		ID: "t-1", Kind: evolution.RecordKindTask, WorkspaceID: root,
		Status: evolution.RecordStatus("new"), Summary: "did a thing",
	}); err != nil {
		t.Fatalf("AppendTaskRecord: %v", err)
	}

	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "draft"},
		evolution.RuntimeOptions{Store: store})

	s := rt.PreviewColdPath(root)
	if s.TaskRecords != 1 || s.UnclusteredTasks != 1 || s.PatternRecords != 1 || s.ReadyPatterns != 1 {
		t.Fatalf("unexpected counts: %+v", s)
	}
	if s.Note != "" {
		t.Fatalf("unexpected note: %q", s.Note)
	}
}

func TestRunColdPathSummary_ReportsDrafts(t *testing.T) {
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	seedPatternRecord(t, store, root, "rule-1")

	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "draft"},
		evolution.RuntimeOptions{
			Store: store,
			DraftGenerator: stubDraftGenerator{draft: evolution.SkillDraft{
				ID:              "draft-1",
				TargetSkillName: "weather",
				DraftType:       evolution.DraftTypeShortcut,
				ChangeKind:      evolution.ChangeKindCreate,
				HumanSummary:    "weather helper",
				BodyOrPatch:     testSkillBody,
			}},
		})

	s := rt.RunColdPathSummary(context.Background(), root)
	if s.Error != "" {
		t.Fatalf("run error: %s", s.Error)
	}
	if !s.Ran || s.DraftsCreated != 1 || s.DraftsApplied != 0 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if s.CandidateDrafts != 1 {
		t.Fatalf("expected 1 candidate draft, got %d", s.CandidateDrafts)
	}
}

func TestRunColdPathSummary_ObserveModeSkips(t *testing.T) {
	root := t.TempDir()
	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "observe"},
		evolution.RuntimeOptions{})

	s := rt.RunColdPathSummary(context.Background(), root)
	if s.Ran || s.DraftsCreated != 0 {
		t.Fatalf("observe mode should not run: %+v", s)
	}
	if s.Note != "observe mode produces no drafts" {
		t.Fatalf("expected observe note, got %q", s.Note)
	}
}

func saveCandidateDraft(t *testing.T, store *evolution.Store, workspace, id, status string) evolution.SkillDraft {
	t.Helper()
	draft := evolution.SkillDraft{
		ID:              id,
		WorkspaceID:     workspace,
		CreatedAt:       time.Unix(1700000000, 0).UTC(),
		SourceRecordID:  "rule-1",
		TargetSkillName: "weather",
		DraftType:       evolution.DraftTypeShortcut,
		ChangeKind:      evolution.ChangeKindCreate,
		HumanSummary:    "weather helper",
		BodyOrPatch:     testSkillBody,
		Status:          evolution.DraftStatus(status),
	}
	if err := store.SaveDrafts([]evolution.SkillDraft{draft}); err != nil {
		t.Fatalf("SaveDrafts: %v", err)
	}
	return draft
}

func TestAcceptDraft_AppliesCandidate(t *testing.T) {
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	saveCandidateDraft(t, store, root, "draft-1", "candidate")

	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "draft"},
		evolution.RuntimeOptions{
			Store: store,
			ApplierFactory: func(workspace string) *evolution.Applier {
				return evolution.NewApplier(evolution.NewPaths(workspace, ""), nil)
			},
		})

	draft, err := rt.AcceptDraft(context.Background(), root, "draft-1", false)
	if err != nil {
		t.Fatalf("AcceptDraft: %v", err)
	}
	if draft.Status != evolution.DraftStatusAccepted {
		t.Fatalf("expected accepted, got %s", draft.Status)
	}
	body, err := os.ReadFile(filepath.Join(root, "skills", "weather", "SKILL.md"))
	if err != nil {
		t.Fatalf("applied skill missing: %v", err)
	}
	if !strings.Contains(string(body), "# Weather") {
		t.Fatalf("applied body missing content: %q", body)
	}

	// Profile should carry the version entry.
	profile, err := store.LoadProfile("weather")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.Status != evolution.SkillStatusActive {
		t.Fatalf("expected active profile, got %s", profile.Status)
	}
}

func TestAcceptDraft_QuarantinedNeedsForce(t *testing.T) {
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	saveCandidateDraft(t, store, root, "draft-1", "quarantined")

	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "draft"},
		evolution.RuntimeOptions{
			Store: store,
			ApplierFactory: func(workspace string) *evolution.Applier {
				return evolution.NewApplier(evolution.NewPaths(workspace, ""), nil)
			},
		})

	if _, err := rt.AcceptDraft(context.Background(), root, "draft-1", false); err == nil {
		t.Fatal("expected quarantined draft to refuse accept without force")
	}
	draft, err := rt.AcceptDraft(context.Background(), root, "draft-1", true)
	if err != nil {
		t.Fatalf("forced accept failed: %v", err)
	}
	if draft.Status != evolution.DraftStatusAccepted {
		t.Fatalf("expected accepted, got %s", draft.Status)
	}
}

func TestAcceptDraft_TerminalStatesRefused(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	saveCandidateDraft(t, store, root, "draft-1", "accepted")
	saveCandidateDraft(t, store, root, "draft-2", "rejected")

	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "draft"},
		evolution.RuntimeOptions{Store: store})

	for _, id := range []string{"draft-1", "draft-2"} {
		if _, err := rt.AcceptDraft(context.Background(), root, id, false); err == nil {
			t.Fatalf("expected not-reviewable error for %s", id)
		}
	}
}

func TestRejectDraft_MarksRejected(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	saveCandidateDraft(t, store, root, "draft-1", "candidate")

	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "draft"},
		evolution.RuntimeOptions{Store: store})

	draft, err := rt.RejectDraft(root, "draft-1", "not useful")
	if err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	if draft.Status != evolution.DraftStatusRejected {
		t.Fatalf("expected rejected, got %s", draft.Status)
	}
	found := false
	for _, n := range draft.ReviewNotes {
		if strings.Contains(n, "not useful") {
			found = true
		}
	}
	if !found {
		t.Fatalf("rejection reason not recorded: %v", draft.ReviewNotes)
	}

	// Reloaded state must show the terminal status.
	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	if drafts[0].Status != evolution.DraftStatusRejected {
		t.Fatalf("persisted status = %s", drafts[0].Status)
	}
}

func TestRejectDraft_AcceptedRefused(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	saveCandidateDraft(t, store, root, "draft-1", "accepted")

	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "draft"},
		evolution.RuntimeOptions{Store: store})

	if _, err := rt.RejectDraft(root, "draft-1", ""); err == nil {
		t.Fatal("expected rejecting an accepted draft to fail")
	}
}

// A rejected draft keeps occupying its source-pattern slot: the cold path
// must not regenerate a draft for a pattern the operator refused.
func TestRejectDraft_BlocksRegeneration(t *testing.T) {
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	seedPatternRecord(t, store, root, "rule-1")
	draft := saveCandidateDraft(t, store, root, "draft-1", "candidate")
	_ = draft

	rt := newAdminRuntime(t,
		config.EvolutionConfig{Enabled: true, Mode: "draft"},
		evolution.RuntimeOptions{
			Store: store,
			DraftGenerator: stubDraftGenerator{draft: evolution.SkillDraft{
				ID:              "draft-2",
				TargetSkillName: "weather-v2",
				DraftType:       evolution.DraftTypeShortcut,
				ChangeKind:      evolution.ChangeKindCreate,
				BodyOrPatch:     "---\nname: weather-v2\ndescription: x\n---\n# V2\n",
			}},
		})

	if _, err := rt.RejectDraft(root, "draft-1", "no"); err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	s := rt.RunColdPathSummary(context.Background(), root)
	if s.Error != "" {
		t.Fatalf("run error: %s", s.Error)
	}
	if s.DraftsCreated != 0 {
		t.Fatalf("rejected pattern re-drafted: %+v", s)
	}
}

func TestRenderDraftDiff_CreateShowsBody(t *testing.T) {
	root := t.TempDir()
	draft := evolution.SkillDraft{
		ID:              "d1",
		WorkspaceID:     root,
		TargetSkillName: "weather",
		ChangeKind:      evolution.ChangeKindCreate,
		BodyOrPatch:     testSkillBody,
	}
	out, err := evolution.RenderDraftDiff(root, draft)
	if err != nil {
		t.Fatalf("RenderDraftDiff: %v", err)
	}
	if !strings.Contains(out, "# Weather") {
		t.Fatalf("expected full body render, got %q", out)
	}
}

func TestRenderDraftDiff_AppendShowsHunk(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skills", "weather")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := "---\nname: weather\ndescription: weather helper\n---\n# Weather\nOriginal body.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(base), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	draft := evolution.SkillDraft{
		ID:              "d1",
		WorkspaceID:     root,
		TargetSkillName: "weather",
		ChangeKind:      evolution.ChangeKindAppend,
		BodyOrPatch:     "## Extra\nNew section.\n",
	}
	out, err := evolution.RenderDraftDiff(root, draft)
	if err != nil {
		t.Fatalf("RenderDraftDiff: %v", err)
	}
	if !strings.Contains(out, "+ ## Extra") {
		t.Fatalf("expected append hunk, got %q", out)
	}
}

func TestSummarizeWorkspace_Counts(t *testing.T) {
	root := t.TempDir()
	paths := evolution.NewPaths(root, "")
	store := evolution.NewStore(paths)
	seedPatternRecord(t, store, root, "rule-1")
	saveCandidateDraft(t, store, root, "draft-1", "candidate")

	st, err := evolution.SummarizeWorkspace(store, root)
	if err != nil {
		t.Fatalf("SummarizeWorkspace: %v", err)
	}
	if st.TaskRecords != 0 || st.PatternRecords != 1 || st.ReadyPatterns != 1 {
		t.Fatalf("unexpected status: %+v", st)
	}
	if st.Drafts["candidate"] != 1 {
		t.Fatalf("expected 1 candidate draft, got %v", st.Drafts)
	}
}

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
	"github.com/stpinkie/rhizome/pkg/skills"
)

// track152Now is a fixed clock — auto-apply window arithmetic needs a
// stable "now" to reason about.
var track152Now = time.Unix(1700001000, 0).UTC()

func newTrack152Runtime(t *testing.T, root string, cfg config.EvolutionConfig,
	draft evolution.SkillDraft,
) *evolution.Runtime {
	t.Helper()
	paths := evolution.NewPaths(root, "")
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: cfg,
		Now:    func() time.Time { return track152Now },
		Store:  evolution.NewStore(paths),
		Applier: evolution.NewApplier(paths, func() time.Time {
			return track152Now
		}),
		DraftGenerator: stubDraftGenerator{draft: draft},
		Organizer: evolution.NewOrganizer(
			evolution.OrganizerOptions{MinCaseCount: 3, MinSuccessRate: 0.7}),
		SkillsRecaller: evolution.NewSkillsRecaller(root),
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt
}

func seedReadyRule(t *testing.T, root string, store *evolution.Store, ruleID string) {
	t.Helper()
	rule := evolution.LearningRecord{
		ID:          ruleID,
		Kind:        evolution.RecordKindRule,
		WorkspaceID: root,
		CreatedAt:   time.Unix(1700000000, 0).UTC(),
		Summary:     "weather native-name path",
		Status:      evolution.RecordStatus("ready"),
		EventCount:  4,
	}
	if err := store.AppendLearningRecords([]evolution.LearningRecord{rule}); err != nil {
		t.Fatalf("AppendLearningRecords: %v", err)
	}
}

func track152Draft(root, id, ruleID, target string) evolution.SkillDraft {
	return evolution.SkillDraft{
		ID:              id,
		WorkspaceID:     root,
		SourceRecordID:  ruleID,
		TargetSkillName: target,
		DraftType:       evolution.DraftTypeShortcut,
		ChangeKind:      evolution.ChangeKindCreate,
		HumanSummary:    "weather helper",
		BodyOrPatch: "---\nname: " + target + "\ndescription: weather helper\n---\n# " +
			target + "\n## Start Here\nUse native-name query first.\n",
	}
}

func loadOnlyDraft(t *testing.T, store *evolution.Store) evolution.SkillDraft {
	t.Helper()
	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	if len(drafts) != 1 {
		t.Fatalf("len(drafts) = %d, want 1", len(drafts))
	}
	return drafts[0]
}

// Apply mode with the default review gate must park candidates exactly like
// draft mode — only draft accept applies them.
func TestTrack152_RequireReviewParksApplyModeDrafts(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedReadyRule(t, root, store, "rule-1")

	cfg := config.DefaultConfig().Evolution
	cfg.Enabled = true
	cfg.Mode = "apply"
	if !cfg.ApplyRequireReview {
		t.Fatal("test assumes apply_require_review defaults true")
	}

	rt := newTrack152Runtime(t, root, cfg, track152Draft(root, "draft-1", "rule-1", "weather"))
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	draft := loadOnlyDraft(t, store)
	if draft.Status != evolution.DraftStatusCandidate {
		t.Fatalf("status = %q, want candidate (parked for review)", draft.Status)
	}
	if !strings.Contains(strings.Join(draft.ReviewNotes, "\n"), "apply_require_review") {
		t.Fatalf("ReviewNotes = %v, want a require_review park note", draft.ReviewNotes)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "weather", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("skill file exists despite review gate: %v", err)
	}
}

// mode:apply + apply_require_review=false reproduces pre-sprint auto-apply:
// draft accepts, provenance sidecar lands, profile reports evolution origin.
func TestTrack152_AutoApplyWithoutReviewWritesProvenance(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedReadyRule(t, root, store, "rule-1")

	cfg := config.EvolutionConfig{
		Enabled:        true,
		Mode:           "apply",
		ApplyMaxPerDay: 3,
	}
	rt := newTrack152Runtime(t, root, cfg, track152Draft(root, "draft-1", "rule-1", "weather"))
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "skills", "weather", "SKILL.md")); err != nil {
		t.Fatalf("expected applied skill file: %v", err)
	}
	draft := loadOnlyDraft(t, store)
	if draft.Status != evolution.DraftStatusAccepted {
		t.Fatalf("status = %q, want accepted", draft.Status)
	}
	if draft.AppliedVia != "auto" {
		t.Fatalf("AppliedVia = %q, want auto", draft.AppliedVia)
	}
	if draft.AppliedAt == nil || !draft.AppliedAt.Equal(track152Now) {
		t.Fatalf("AppliedAt = %v, want %v", draft.AppliedAt, track152Now)
	}
	if draft.UpdatedAt == nil {
		t.Fatal("UpdatedAt should be stamped on accepted drafts")
	}

	meta, err := skills.ReadEvolutionOrigin(filepath.Join(root, "skills", "weather"))
	if err != nil {
		t.Fatalf("ReadEvolutionOrigin: %v", err)
	}
	if meta.OriginKind != "evolution" {
		t.Fatalf("origin_kind = %q, want evolution", meta.OriginKind)
	}
	if meta.DraftID != "draft-1" || meta.SourcePatternID != "rule-1" {
		t.Fatalf("meta = %+v, want draft-1 / rule-1", meta)
	}
	if meta.AppliedAt != track152Now.UnixMilli() {
		t.Fatalf("applied_at = %d, want %d", meta.AppliedAt, track152Now.UnixMilli())
	}
	if meta.Review != "auto" {
		t.Fatalf("review = %q, want auto", meta.Review)
	}

	profile, err := store.LoadProfile("weather")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if profile.Origin != "evolution" {
		t.Fatalf("profile Origin = %q, want evolution", profile.Origin)
	}
}

// Over-cap auto-applies park as candidates; drafts stamped auto inside the
// rolling window consume the budget, older ones (or pre-policy applies with
// no provenance) don't.
func TestTrack152_DayCapParksOverLimitDraft(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedReadyRule(t, root, store, "rule-1")

	recent := track152Now.Add(-time.Hour)
	if err := store.SaveDrafts([]evolution.SkillDraft{{
		ID: "draft-old", WorkspaceID: root, SourceRecordID: "rule-old",
		TargetSkillName: "oldskill", DraftType: evolution.DraftTypeShortcut,
		ChangeKind: evolution.ChangeKindCreate, HumanSummary: "older auto apply",
		Status: evolution.DraftStatusAccepted, AppliedVia: "auto", AppliedAt: &recent,
	}}); err != nil {
		t.Fatalf("SaveDrafts seed: %v", err)
	}

	cfg := config.EvolutionConfig{Enabled: true, Mode: "apply", ApplyMaxPerDay: 1}
	rt := newTrack152Runtime(t, root, cfg, track152Draft(root, "draft-1", "rule-1", "weather"))
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	var parked evolution.SkillDraft
	for _, d := range drafts {
		if d.ID == "draft-1" {
			parked = d
		}
	}
	if parked.Status != evolution.DraftStatusCandidate {
		t.Fatalf("status = %q, want candidate (over day cap)", parked.Status)
	}
	if !strings.Contains(strings.Join(parked.ReviewNotes, "\n"), "apply_max_per_day") {
		t.Fatalf("ReviewNotes = %v, want a day-cap park note", parked.ReviewNotes)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "weather", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("skill file exists despite day cap: %v", err)
	}
}

func TestTrack152_DayCapIgnoresExpiredAndUnmarkedApplies(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedReadyRule(t, root, store, "rule-1")

	// One auto apply 25h back (outside the window) and one accepted draft
	// with no provenance at all (pre-Track-152) — neither counts.
	expired := track152Now.Add(-25 * time.Hour)
	if err := store.SaveDrafts([]evolution.SkillDraft{
		{
			ID: "draft-expired", WorkspaceID: root, SourceRecordID: "rule-old",
			TargetSkillName: "oldskill", DraftType: evolution.DraftTypeShortcut,
			ChangeKind: evolution.ChangeKindCreate, HumanSummary: "expired auto apply",
			Status: evolution.DraftStatusAccepted, AppliedVia: "auto", AppliedAt: &expired,
		},
		{
			ID: "draft-legacy", WorkspaceID: root, SourceRecordID: "rule-legacy",
			TargetSkillName: "legacyskill", DraftType: evolution.DraftTypeShortcut,
			ChangeKind: evolution.ChangeKindCreate, HumanSummary: "pre-policy apply",
			Status: evolution.DraftStatusAccepted,
		},
	}); err != nil {
		t.Fatalf("SaveDrafts seed: %v", err)
	}

	cfg := config.EvolutionConfig{Enabled: true, Mode: "apply", ApplyMaxPerDay: 1}
	rt := newTrack152Runtime(t, root, cfg, track152Draft(root, "draft-1", "rule-1", "weather"))
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	drafts, err := store.LoadDrafts()
	if err != nil {
		t.Fatalf("LoadDrafts: %v", err)
	}
	var applied evolution.SkillDraft
	for _, d := range drafts {
		if d.ID == "draft-1" {
			applied = d
		}
	}
	if applied.Status != evolution.DraftStatusAccepted {
		t.Fatalf("status = %q, want accepted (window cleared)", applied.Status)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "weather", "SKILL.md")); err != nil {
		t.Fatalf("expected applied skill file: %v", err)
	}
}

// Protected targets park even when auto-apply is on; the manual accept path
// applies them and records review:"accept".
func TestTrack152_ProtectedSkillParksThenAcceptApplies(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedReadyRule(t, root, store, "rule-1")

	cfg := config.EvolutionConfig{
		Enabled:         true,
		Mode:            "apply",
		ProtectedSkills: []string{"weather"},
	}
	rt := newTrack152Runtime(t, root, cfg, track152Draft(root, "draft-1", "rule-1", "weather"))
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	draft := loadOnlyDraft(t, store)
	if draft.Status != evolution.DraftStatusCandidate {
		t.Fatalf("status = %q, want candidate (protected target)", draft.Status)
	}
	if !strings.Contains(strings.Join(draft.ReviewNotes, "\n"), "protected_skills") {
		t.Fatalf("ReviewNotes = %v, want a protected-skill park note", draft.ReviewNotes)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "weather", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("protected skill file exists: %v", err)
	}

	accepted, err := rt.AcceptDraft(context.Background(), root, "draft-1", false)
	if err != nil {
		t.Fatalf("AcceptDraft: %v", err)
	}
	if accepted.Status != evolution.DraftStatusAccepted {
		t.Fatalf("accepted status = %q, want accepted", accepted.Status)
	}
	if accepted.AppliedVia != "accept" {
		t.Fatalf("AppliedVia = %q, want accept", accepted.AppliedVia)
	}

	meta, err := skills.ReadEvolutionOrigin(filepath.Join(root, "skills", "weather"))
	if err != nil {
		t.Fatalf("ReadEvolutionOrigin: %v", err)
	}
	if meta.Review != "accept" {
		t.Fatalf("review = %q, want accept", meta.Review)
	}
	if meta.DraftID != "draft-1" || meta.SourcePatternID != "rule-1" {
		t.Fatalf("meta = %+v, want draft-1 / rule-1", meta)
	}
}

// A failed provenance write is an apply failure: the committed SKILL.md
// rolls back and the draft quarantines rather than ship an unprovenanced
// evolved skill.
func TestTrack152_ProvenanceWriteFailureRollsBack(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedReadyRule(t, root, store, "rule-1")

	// A non-empty directory where the marker file must land makes
	// WriteEvolutionOrigin fail after SKILL.md is written.
	markerDir := filepath.Join(root, "skills", "weather", ".evolution-origin.json")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed marker dir: %v", err)
	}

	cfg := config.EvolutionConfig{Enabled: true, Mode: "apply", ApplyMaxPerDay: 3}
	rt := newTrack152Runtime(t, root, cfg, track152Draft(root, "draft-1", "rule-1", "weather"))
	err := rt.RunColdPathOnce(context.Background(), root)
	if err == nil || !errors.Is(err, evolution.ErrApplyDraftFailed) {
		t.Fatalf("RunColdPathOnce = %v, want ErrApplyDraftFailed", err)
	}

	if _, statErr := os.Stat(filepath.Join(root, "skills", "weather", "SKILL.md")); !os.IsNotExist(statErr) {
		t.Fatalf("SKILL.md survives failed provenance write: %v", statErr)
	}
	draft := loadOnlyDraft(t, store)
	if draft.Status != evolution.DraftStatusQuarantined {
		t.Fatalf("status = %q, want quarantined", draft.Status)
	}
	if !strings.Contains(strings.Join(draft.ScanFindings, "\n"), "provenance write failed") {
		t.Fatalf("ScanFindings = %v, want a provenance failure finding", draft.ScanFindings)
	}
}

// Rollback rewrites the marker to match the restored version's draft record
// — never leaves the outgoing apply's provenance on restored content.
func TestTrack152_RollbackReconcilesProvenance(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedReadyRule(t, root, store, "rule-1")

	cfg := config.EvolutionConfig{Enabled: true, Mode: "apply", ApplyMaxPerDay: 3}
	rt := newTrack152Runtime(t, root, cfg, track152Draft(root, "draft-1", "rule-1", "weather"))
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	draft2 := track152Draft(root, "draft-2", "rule-2", "weather")
	draft2.ChangeKind = evolution.ChangeKindReplace
	draft2.Status = evolution.DraftStatusCandidate
	draft2.BodyOrPatch = "---\nname: weather\ndescription: weather helper v2\n---\n# weather\n" +
		"## Start Here\nUse the v2 flow.\n"
	if err := store.SaveDrafts([]evolution.SkillDraft{draft2}); err != nil {
		t.Fatalf("SaveDrafts draft-2: %v", err)
	}
	if _, err := rt.AcceptDraft(context.Background(), root, "draft-2", false); err != nil {
		t.Fatalf("AcceptDraft draft-2: %v", err)
	}
	skillDir := filepath.Join(root, "skills", "weather")
	meta, err := skills.ReadEvolutionOrigin(skillDir)
	if err != nil || meta.DraftID != "draft-2" || meta.Review != "accept" {
		t.Fatalf("pre-rollback meta = %+v, %v — want draft-2/accept", meta, err)
	}

	// --to draft-1 restores draft-1's snapshot; provenance must follow the
	// content back to draft-1's auto apply.
	res, err := rt.RollbackSkill(context.Background(), root, "weather",
		evolution.RollbackRequest{To: "draft-1"})
	if err != nil {
		t.Fatalf("RollbackSkill --to draft-1: %v", err)
	}
	if !res.Restored {
		t.Fatal("expected a restore")
	}
	meta, err = skills.ReadEvolutionOrigin(skillDir)
	if err != nil {
		t.Fatalf("ReadEvolutionOrigin after rollback: %v", err)
	}
	if meta.DraftID != "draft-1" || meta.SourcePatternID != "rule-1" || meta.Review != "auto" {
		t.Fatalf("meta = %+v, want draft-1 / rule-1 / auto", meta)
	}

	// Bare rollback restores the newest pre-mutation backup (the draft-2
	// state snapshotted before the previous rollback) — provenance follows
	// again, back to draft-2's accept.
	if _, err = rt.RollbackSkill(context.Background(), root, "weather", evolution.RollbackRequest{}); err != nil {
		t.Fatalf("RollbackSkill bare: %v", err)
	}
	meta, err = skills.ReadEvolutionOrigin(skillDir)
	if err != nil {
		t.Fatalf("ReadEvolutionOrigin after bare rollback: %v", err)
	}
	if meta.DraftID != "draft-2" || meta.Review != "accept" {
		t.Fatalf("meta = %+v, want draft-2 / accept", meta)
	}
}

// Rolling a create-applied skill back to pre-create absence removes the
// skill dir and its marker — nothing may claim provenance for a skill that
// no longer exists.
func TestTrack152_RollbackToPreCreateRemovesProvenance(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedReadyRule(t, root, store, "rule-1")

	cfg := config.EvolutionConfig{Enabled: true, Mode: "apply", ApplyMaxPerDay: 3}
	rt := newTrack152Runtime(t, root, cfg, track152Draft(root, "draft-1", "rule-1", "weather"))
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}
	skillDir := filepath.Join(root, "skills", "weather")
	if _, err := skills.ReadEvolutionOrigin(skillDir); err != nil {
		t.Fatalf("ReadEvolutionOrigin: %v", err)
	}

	res, err := rt.RollbackSkill(context.Background(), root, "weather", evolution.RollbackRequest{})
	if err != nil {
		t.Fatalf("RollbackSkill: %v", err)
	}
	if !res.Removed {
		t.Fatal("expected pre-create removal")
	}
	if _, err := os.Stat(filepath.Join(skillDir, ".evolution-origin.json")); !os.IsNotExist(err) {
		t.Fatalf("marker survives pre-create rollback: %v", err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("skill dir survives pre-create rollback: %v", err)
	}
}

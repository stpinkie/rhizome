package evolution

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/skills"
)

type LifecycleRunSummary struct {
	EvaluatedProfiles    int
	TransitionedProfiles int
	DeletedSkills        int
	PrunedTaskRecords    int
	PrunedDrafts         int
	// Transitions lists the profile status changes the sweep applied — the
	// observability hook for skill.lifecycle events.
	Transitions []SkillTransition
}

// SkillTransition records one lifecycle status change the sweep applied.
type SkillTransition struct {
	Skill string
	From  SkillStatus
	To    SkillStatus
}

// PruneSummary reports what a retention sweep removed (or would remove
// under DryRun) from one workspace's evolution state.
type PruneSummary struct {
	Workspace         string `json:"workspace"`
	DryRun            bool   `json:"dry_run,omitempty"`
	TaskRecordsPruned int    `json:"task_records_pruned"`
	DraftsPruned      int    `json:"drafts_pruned"`
}

// ErrSkillTransition is returned when a manual lifecycle transition is not
// valid for the profile's current status (e.g. restoring a deleted skill —
// recover with `evolution rollback` instead).
var ErrSkillTransition = errors.New("invalid lifecycle transition")

func NextLifecycleState(profile SkillProfile, now time.Time) SkillStatus {
	if profile.Origin == "manual" || profile.LastUsedAt.IsZero() {
		return profile.Status
	}

	idle := now.Sub(profile.LastUsedAt)
	switch profile.Status {
	case SkillStatusActive:
		if idle > config.Global().EvolutionSkillColdThreshold() && profile.RetentionScore < 0.3 {
			return SkillStatusCold
		}
	case SkillStatusCold:
		if idle > config.Global().EvolutionSkillArchivedThreshold() && profile.RetentionScore < 0.2 {
			return SkillStatusArchived
		}
	case SkillStatusArchived:
		if idle > config.Global().EvolutionSkillDeletedThreshold() && profile.RetentionScore < 0.1 {
			return SkillStatusDeleted
		}
	}

	return profile.Status
}

func ApplyLifecycleState(paths Paths, profile SkillProfile, next SkillStatus) error {
	if next != SkillStatusDeleted {
		return nil
	}

	workspace := profile.WorkspaceID
	if workspace == "" {
		workspace = inferWorkspaceFromPaths(paths)
	}
	if workspace == "" {
		return fmt.Errorf("resolve lifecycle delete workspace for skill %q: workspace is required", profile.SkillName)
	}
	if err := skills.ValidateSkillName(profile.SkillName); err != nil {
		return fmt.Errorf("resolve lifecycle delete skill name: %w", err)
	}

	skillPath := filepath.Join(workspace, "skills", profile.SkillName, "SKILL.md")
	err := os.Remove(skillPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func RunLifecycleOnce(store *Store, paths Paths, workspace string, now time.Time) (LifecycleRunSummary, error) {
	evCfg := config.Global().Evolution
	return RunLifecycleOnceWithRetention(store, paths, workspace, now,
		evCfg.EffectiveRetentionTaskRecordsDays(),
		evCfg.EffectiveRetentionDraftsDays())
}

// RunLifecycleOnceWithRetention is RunLifecycleOnce with explicit
// retention windows — the runtime passes its own config here.
func RunLifecycleOnceWithRetention(
	store *Store,
	paths Paths,
	workspace string,
	now time.Time,
	taskDays, draftsDays int,
) (LifecycleRunSummary, error) {
	if store == nil {
		return LifecycleRunSummary{}, nil
	}

	profiles, err := store.LoadProfiles()
	if err != nil {
		return LifecycleRunSummary{}, err
	}

	summary := LifecycleRunSummary{}
	for _, profile := range profiles {
		if !profileBelongsToWorkspace(paths, workspace, profile) {
			continue
		}

		summary.EvaluatedProfiles++
		next := NextLifecycleState(profile, now)
		if next == profile.Status {
			continue
		}

		if err := ApplyLifecycleState(paths, profile, next); err != nil {
			return summary, err
		}
		from := profile.Status
		profile.VersionHistory = append(profile.VersionHistory, SkillVersionEntry{
			Version:   profile.CurrentVersion,
			Action:    "lifecycle:" + string(next),
			Timestamp: now,
			Summary:   fmt.Sprintf("lifecycle transition: %s -> %s", profile.Status, next),
		})
		profile.Status = next
		if err := store.SaveProfile(profile); err != nil {
			return summary, err
		}

		summary.TransitionedProfiles++
		summary.Transitions = append(summary.Transitions, SkillTransition{
			Skill: profile.SkillName,
			From:  from,
			To:    next,
		})
		if next == SkillStatusDeleted {
			summary.DeletedSkills++
		}
	}

	pruned, err := PruneExpired(store, workspace, now, taskDays, draftsDays, false)
	if err != nil {
		return summary, err
	}
	summary.PrunedTaskRecords = pruned.TaskRecordsPruned
	summary.PrunedDrafts = pruned.DraftsPruned

	return summary, nil
}

// TransitionSkill performs an operator-requested lifecycle transition on a
// skill profile: "archive" parks an active/cold skill, "restore" returns a
// cold/archived skill to active. Thresholds are bypassed — the operator's
// call is the signal. Already-at-target is a no-op returning the profile
// unchanged; anything else (archiving a deleted skill, restoring an active
// one, restoring deleted) is ErrSkillTransition.
func TransitionSkill(
	store *Store,
	workspace, skillName string,
	target SkillStatus,
	now time.Time,
) (SkillProfile, error) {
	if store == nil {
		return SkillProfile{}, fmt.Errorf("evolution store unavailable")
	}
	var (
		out      SkillProfile
		previous SkillStatus
	)
	err := store.UpdateProfile(workspace, skillName, func(profile *SkillProfile, exists bool) error {
		if !exists {
			return fmt.Errorf("no profile for skill %q", skillName)
		}
		previous = profile.Status
		switch target {
		case SkillStatusArchived:
			if profile.Status != SkillStatusActive && profile.Status != SkillStatusCold {
				return fmt.Errorf("%w: cannot archive a %s skill", ErrSkillTransition, profile.Status)
			}
		case SkillStatusActive:
			if profile.Status != SkillStatusArchived && profile.Status != SkillStatusCold {
				return fmt.Errorf("%w: cannot restore a %s skill", ErrSkillTransition, profile.Status)
			}
		default:
			return fmt.Errorf("%w: manual transitions support archive|restore", ErrSkillTransition)
		}
		if profile.Status == target {
			out = *profile
			return nil
		}
		profile.VersionHistory = append(profile.VersionHistory, SkillVersionEntry{
			Version:   profile.CurrentVersion,
			Action:    "lifecycle:" + string(target),
			Timestamp: now,
			Summary:   fmt.Sprintf("manual lifecycle transition: %s -> %s", profile.Status, target),
		})
		profile.Status = target
		out = *profile
		return nil
	})
	if err != nil {
		return SkillProfile{}, err
	}
	if previous == target {
		return out, nil
	}
	// ApplyLifecycleState owns the file side of transitions; manual
	// archive/restore never target deleted so this is a no-op today, but
	// routing through it keeps a single file-effects path.
	if err := ApplyLifecycleState(store.paths, out, target); err != nil {
		return SkillProfile{}, err
	}
	return out, nil
}

// PruneExpired applies the retention windows to a workspace's evolution
// state: unclustered task-stream records older than taskDays and rejected
// drafts older than draftsDays are removed. Clustered records (the
// accepted history patterns cite via TaskRecordIDs), unsettled drafts
// (candidate/quarantined), accepted drafts, and pattern records are never
// pruned. DryRun reports counts without writing.
func PruneExpired(
	store *Store,
	workspace string,
	now time.Time,
	taskDays, draftsDays int,
	dryRun bool,
) (PruneSummary, error) {
	summary := PruneSummary{Workspace: workspace, DryRun: dryRun}
	if store == nil || (taskDays <= 0 && draftsDays <= 0) {
		return summary, nil
	}

	if taskDays > 0 {
		cutoff := now.AddDate(0, 0, -taskDays)
		records, err := store.LoadTaskRecords()
		if err != nil {
			return summary, err
		}
		kept := make([]LearningRecord, 0, len(records))
		for _, record := range records {
			if recordBelongsToWorkspace(store.paths, workspace, record) &&
				record.Status != RecordStatus("clustered") &&
				recordCreatedAt(record).Before(cutoff) {
				summary.TaskRecordsPruned++
				continue
			}
			kept = append(kept, record)
		}
		if !dryRun && summary.TaskRecordsPruned > 0 {
			if err := store.SaveTaskRecords(kept); err != nil {
				return summary, err
			}
			// LoadTaskRecords merges the legacy learning log — strip its
			// task-kind lines or pruned records reappear on the next load.
			if err := store.DropTaskRecordsFromLearningLog(); err != nil {
				return summary, err
			}
		}
	}

	if draftsDays > 0 {
		cutoff := now.AddDate(0, 0, -draftsDays)
		drafts, err := store.LoadDrafts()
		if err != nil {
			return summary, err
		}
		kept := make([]SkillDraft, 0, len(drafts))
		for _, draft := range drafts {
			if draftBelongsToWorkspace(store.paths, workspace, draft) &&
				draft.Status == DraftStatusRejected &&
				draftUpdatedAt(draft).Before(cutoff) {
				summary.DraftsPruned++
				continue
			}
			kept = append(kept, draft)
		}
		if !dryRun && summary.DraftsPruned > 0 {
			if err := store.ReplaceDrafts(kept); err != nil {
				return summary, err
			}
		}
	}

	return summary, nil
}

func recordCreatedAt(record LearningRecord) time.Time {
	if record.UpdatedAt != nil && record.UpdatedAt.After(record.CreatedAt) {
		return *record.UpdatedAt
	}
	return record.CreatedAt
}

func draftUpdatedAt(draft SkillDraft) time.Time {
	if draft.UpdatedAt != nil && draft.UpdatedAt.After(draft.CreatedAt) {
		return *draft.UpdatedAt
	}
	return draft.CreatedAt
}

func recordBelongsToWorkspace(paths Paths, workspace string, record LearningRecord) bool {
	if record.WorkspaceID == workspace {
		return true
	}
	return record.WorkspaceID == "" && usesDefaultWorkspaceState(paths, workspace)
}

func draftBelongsToWorkspace(paths Paths, workspace string, draft SkillDraft) bool {
	if draft.WorkspaceID == workspace {
		return true
	}
	return draft.WorkspaceID == "" && usesDefaultWorkspaceState(paths, workspace)
}

func inferWorkspaceFromPaths(paths Paths) string {
	root := filepath.Clean(paths.RootDir)
	if filepath.Base(root) != "evolution" {
		return ""
	}
	stateDir := filepath.Dir(root)
	if filepath.Base(stateDir) != "state" {
		return ""
	}
	return filepath.Dir(stateDir)
}

func profileBelongsToWorkspace(paths Paths, workspace string, profile SkillProfile) bool {
	if profile.WorkspaceID == workspace {
		return true
	}
	return profile.WorkspaceID == "" && usesDefaultWorkspaceState(paths, workspace)
}

func usesDefaultWorkspaceState(paths Paths, workspace string) bool {
	return paths.RootDir == NewPaths(workspace, "").RootDir
}

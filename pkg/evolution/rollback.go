package evolution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stpinkie/rhizome/pkg/fileutil"
	"github.com/stpinkie/rhizome/pkg/skills"
)

// RollbackRequest carries the operator's targeting for a manual rollback:
// To names a version (draft id) whose post-apply snapshot to restore; empty
// restores the newest pre-mutation backup. DryRun resolves and validates the
// target without writing anything.
type RollbackRequest struct {
	To     string `json:"to"`
	DryRun bool   `json:"dry_run"`
}

// RollbackResult reports what a rollback did — or would do under DryRun.
type RollbackResult struct {
	Skill           string `json:"skill"`
	Workspace       string `json:"workspace"`
	DryRun          bool   `json:"dry_run,omitempty"`
	Restored        bool   `json:"restored"`
	Removed         bool   `json:"removed,omitempty"`
	Source          string `json:"source"`
	BackedUpTo      string `json:"backed_up_to,omitempty"`
	PreviousVersion string `json:"previous_version,omitempty"`
	RestoredVersion string `json:"restored_version,omitempty"`
}

// ErrRollbackUnavailable marks a rollback that found nothing to restore.
var ErrRollbackUnavailable = errors.New("no rollback target")

// RollbackSkill restores a skill to a prior snapshot: either the named
// version's post-apply snapshot (req.To) or the newest pre-mutation backup.
// Restored content passes through the same validateAppliedSkillBody gate an
// apply does. The current content is itself snapshotted first, so a rollback
// can be undone by rolling back again. A SkillVersionEntry{Rollback:true}
// records the transition and CurrentVersion moves to the restored version
// (empty when the skill returns to pre-create absence).
func (rt *Runtime) RollbackSkill(
	ctx context.Context,
	workspace, skillName string,
	req RollbackRequest,
) (RollbackResult, error) {
	result := RollbackResult{Skill: skillName, Workspace: workspace, DryRun: req.DryRun}
	if rt == nil {
		return result, errors.New("evolution runtime unavailable")
	}
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	default:
	}
	if err := skills.ValidateSkillName(skillName); err != nil {
		return result, err
	}
	workspace = strings.TrimSpace(workspace)
	skillName = strings.TrimSpace(skillName)

	applier := rt.applierForWorkspace(workspace)
	if applier == nil {
		return result, errors.New("no applier available for workspace")
	}
	store := rt.storeForWorkspace(workspace)

	profile, profileErr := store.loadProfileForWorkspace(workspace, skillName)
	switch {
	case profileErr == nil:
		result.PreviousVersion = profile.CurrentVersion
	case errors.Is(profileErr, os.ErrNotExist):
		// No profile — file-level restore still possible from backups.
	default:
		return result, profileErr
	}

	snapshot, removeSkill, err := rt.resolveRollbackTarget(applier, profile, workspace, skillName, req.To)
	if err != nil {
		return result, err
	}
	result.Source = snapshot

	var body []byte
	if removeSkill {
		result.Removed = true
		result.Source = "pre-create"
	} else {
		body, err = os.ReadFile(snapshot)
		if err != nil {
			return result, fmt.Errorf("read snapshot %s: %w", snapshot, err)
		}
		// Restored content is previously-live material, not draft output —
		// extra frontmatter fields are legitimate; the name must still match.
		if err := validateAppliedSkillBody(string(body), skillName, true); err != nil {
			return result, fmt.Errorf("snapshot %s failed validation: %w", snapshot, err)
		}
		result.RestoredVersion = versionForSnapshot(applier, workspace, skillName, body)
	}
	if req.To != "" {
		result.RestoredVersion = req.To
	}

	if req.DryRun {
		return result, nil
	}

	// Snapshot the current content first — the new timestamped backup becomes
	// the newest undo target, so a rollback is itself reversible.
	skillPath := filepath.Join(workspace, "skills", skillName, "SKILL.md")
	backedUpTo, snapErr := applier.snapshotCurrentSkill(workspace, skillName)
	if snapErr != nil {
		return result, fmt.Errorf("pre-rollback backup: %w", snapErr)
	}
	result.BackedUpTo = backedUpTo

	if removeSkill {
		if err := applier.rollbackSkill(skillPath, "", false); err != nil {
			return result, fmt.Errorf("remove restored skill: %w", err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
			return result, err
		}
		if err := fileutil.WriteFileAtomic(skillPath, body, 0o644); err != nil {
			return result, err
		}
	}
	result.Restored = true

	if err := rt.recordRollback(store, result, req.To); err != nil {
		return result, fmt.Errorf("record rollback history: %w", err)
	}
	if err := applier.PruneBackups(workspace, skillName, rt.cfg.EffectiveBackupKeep()); err != nil {
		return result, fmt.Errorf("prune backups: %w", err)
	}
	return result, nil
}

// resolveRollbackTarget picks the snapshot to restore: the named version's
// snapshot for --to, else the newest pre-mutation backup, else the
// pre-create absence when the only apply was a create-on-missing.
func (rt *Runtime) resolveRollbackTarget(
	applier *Applier,
	profile SkillProfile,
	workspace, skillName, toVersion string,
) (snapshot string, removeSkill bool, err error) {
	backupDir := applier.skillBackupDir(workspace, skillName)
	if toVersion = strings.TrimSpace(toVersion); toVersion != "" {
		// sanitizeWorkspaceComponent keeps '.' — a literal "." / ".." input
		// would traverse out of versions/ via filepath.Join, so reject it
		// outright; every written snapshot dir is itself a sanitized name.
		versionDir := sanitizeWorkspaceComponent(toVersion)
		if versionDir == "" || versionDir == "." || versionDir == ".." {
			return "", false, fmt.Errorf(
				"%w: invalid version label %q", ErrRollbackUnavailable, toVersion)
		}
		snapshot = filepath.Join(
			backupDir, versionsSubdir, versionDir, "SKILL.md")
		if _, statErr := os.Stat(snapshot); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return "", false, fmt.Errorf(
					"%w: no kept snapshot for version %q — at most the last %d backups per skill are kept "+
						"(evolution.backup_keep); older snapshots may be recoverable from an archived copy of %s",
					ErrRollbackUnavailable, toVersion, rt.cfg.EffectiveBackupKeep(), backupDir)
			}
			return "", false, statErr
		}
		return snapshot, false, nil
	}

	timestamped, err := applier.timestampedBackups(workspace, skillName)
	if err != nil {
		return "", false, err
	}
	if len(timestamped) > 0 {
		return filepath.Join(timestamped[len(timestamped)-1], "SKILL.md"), false, nil
	}
	if lastApplyAction(profile) == string(ChangeKindCreate) {
		return "", true, nil
	}
	return "", false, fmt.Errorf(
		"%w: skill %q has no backups — at most the last %d are kept (evolution.backup_keep); "+
			"older snapshots may be recoverable from an archived copy of %s",
		ErrRollbackUnavailable, skillName, rt.cfg.EffectiveBackupKeep(), backupDir)
}

// versionForSnapshot names the version whose recorded snapshot bytes match
// the restored content — "" when none does (e.g. a pre-first-apply manual
// snapshot, or a version whose snapshot was pruned).
func versionForSnapshot(applier *Applier, workspace, skillName string, body []byte) string {
	versionsDir := filepath.Join(applier.skillBackupDir(workspace, skillName), versionsSubdir)
	entries, err := os.ReadDir(versionsDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(versionsDir, entry.Name(), "SKILL.md"))
		if err == nil && string(data) == string(body) {
			return entry.Name()
		}
	}
	return ""
}

// lastApplyAction returns the most recent apply-mode action in the profile's
// history — rollback/lifecycle entries are not applies.
func lastApplyAction(profile SkillProfile) string {
	for i := len(profile.VersionHistory) - 1; i >= 0; i-- {
		switch profile.VersionHistory[i].Action {
		case string(ChangeKindCreate), string(ChangeKindAppend),
			string(ChangeKindMerge), string(ChangeKindReplace):
			return profile.VersionHistory[i].Action
		}
	}
	return ""
}

// recordRollback appends the SkillVersionEntry{Rollback:true} ledger row and
// moves CurrentVersion to the restored version.
func (rt *Runtime) recordRollback(store *Store, result RollbackResult, toVersion string) error {
	now := rt.now()
	summary := fmt.Sprintf("manual rollback to version %s", toVersion)
	if toVersion == "" {
		summary = fmt.Sprintf("manual rollback to %s", result.Source)
	}
	return store.UpdateProfile(result.Workspace, result.Skill, func(p *SkillProfile, exists bool) error {
		if !exists {
			*p = SkillProfile{
				SkillName:   result.Skill,
				WorkspaceID: result.Workspace,
				Origin:      "evolved",
			}
		}
		p.CurrentVersion = result.RestoredVersion
		// A pre-create rollback removes the file — the profile follows the
		// lifecycle's terminal state rather than claiming an active skill.
		p.Status = SkillStatusActive
		if result.Removed {
			p.Status = SkillStatusDeleted
		}
		p.LastUsedAt = now
		p.VersionHistory = append(p.VersionHistory, SkillVersionEntry{
			Version:   result.RestoredVersion,
			Action:    "rollback",
			Timestamp: now,
			// Version snapshots are keyed by draft id, so the restored
			// version name doubles as the draft link ("" for pre-create).
			DraftID:  result.RestoredVersion,
			Summary:  summary,
			Rollback: true,
			RollbackReason: fmt.Sprintf(
				"operator rollback (previous version %s)", result.PreviousVersion),
		})
		return nil
	})
}

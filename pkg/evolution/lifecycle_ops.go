package evolution

import (
	"errors"
	"fmt"
)

// ArchiveSkill parks an active or cold skill — the manual form of the
// cold path's idle-driven lifecycle transition.
func (rt *Runtime) ArchiveSkill(workspace, skillName string) (SkillProfile, error) {
	return rt.transitionSkill(workspace, skillName, SkillStatusArchived)
}

// RestoreSkill returns a cold or archived skill to active.
func (rt *Runtime) RestoreSkill(workspace, skillName string) (SkillProfile, error) {
	return rt.transitionSkill(workspace, skillName, SkillStatusActive)
}

func (rt *Runtime) transitionSkill(workspace, skillName string, target SkillStatus) (SkillProfile, error) {
	if rt == nil {
		return SkillProfile{}, errors.New("evolution runtime unavailable")
	}
	if workspace == "" || skillName == "" {
		return SkillProfile{}, fmt.Errorf("%w: workspace and skill name required", ErrSkillTransition)
	}
	store := rt.storeForWorkspace(workspace)
	return TransitionSkill(store, workspace, skillName, target, rt.now())
}

// PruneState runs the retention sweep for one workspace outside the cold
// path — the manual `evolution prune` path. Uses the runtime's configured
// retention windows.
func (rt *Runtime) PruneState(workspace string, dryRun bool) (PruneSummary, error) {
	if rt == nil {
		return PruneSummary{}, errors.New("evolution runtime unavailable")
	}
	if workspace == "" {
		return PruneSummary{}, errors.New("workspace required")
	}
	store := rt.storeForWorkspace(workspace)
	return PruneExpired(store, workspace, rt.now(),
		rt.cfg.EffectiveRetentionTaskRecordsDays(),
		rt.cfg.EffectiveRetentionDraftsDays(),
		dryRun,
	)
}

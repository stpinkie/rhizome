package evolution

import (
	"errors"
	"fmt"
	"time"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
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
	from := ""
	if profiles, err := store.LoadProfiles(); err == nil {
		for _, p := range profiles {
			if p.SkillName == skillName && profileBelongsToWorkspace(store.paths, workspace, p) {
				from = string(p.Status)
				break
			}
		}
	}
	profile, err := TransitionSkill(store, workspace, skillName, target, rt.now())
	if err == nil && from != "" && from != string(profile.Status) {
		rt.observe(runtimeevents.KindEvolutionSkillLifecycle, "skill.lifecycle", workspace,
			time.Time{}, map[string]any{
				"skill":   skillName,
				"from":    from,
				"to":      string(profile.Status),
				"trigger": "manual",
			})
	}
	return profile, err
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
	summary, err := PruneExpired(store, workspace, rt.now(),
		rt.cfg.EffectiveRetentionTaskRecordsDays(),
		rt.cfg.EffectiveRetentionDraftsDays(),
		dryRun,
	)
	if err == nil && (summary.TaskRecordsPruned > 0 || summary.DraftsPruned > 0) {
		rt.observe(runtimeevents.KindEvolutionPrune, "prune", workspace, time.Time{}, map[string]any{
			"trigger":             "manual",
			"dry_run":             dryRun,
			"task_records_pruned": summary.TaskRecordsPruned,
			"drafts_pruned":       summary.DraftsPruned,
		})
	}
	return summary, err
}

package agent

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

var errEvolutionUnavailable = errors.New("evolution runtime unavailable")

// EvolutionRunRecord describes the most recent cold-path activity for one
// workspace: either a triggered async run or a completed manual run.
type EvolutionRunRecord struct {
	At            time.Time `json:"at"`
	Source        string    `json:"source"` // after_turn | scheduled | manual
	Kind          string    `json:"kind"`   // trigger | run
	DraftsCreated int       `json:"drafts_created,omitempty"`
	DraftsApplied int       `json:"drafts_applied,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// EvolutionConfigSnapshot returns the evolution config the running agent
// loop was started with.
func (al *AgentLoop) EvolutionConfigSnapshot() config.EvolutionConfig {
	if al == nil {
		return config.EvolutionConfig{}
	}
	al.mu.RLock()
	defer al.mu.RUnlock()
	if al.cfg == nil {
		return config.EvolutionConfig{}
	}
	return al.cfg.Evolution
}

// EvolutionWorkspaces returns every workspace with a live agent plus every
// workspace named in config — the set `evolution` commands operate over.
func (al *AgentLoop) EvolutionWorkspaces() []string {
	if al == nil {
		return nil
	}
	al.mu.RLock()
	cfg := al.cfg
	al.mu.RUnlock()

	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	add := func(ws string) {
		if ws == "" {
			return
		}
		if _, ok := seen[ws]; ok {
			return
		}
		seen[ws] = struct{}{}
		out = append(out, ws)
	}
	for _, ws := range evolution.ConfigWorkspaces(cfg) {
		add(ws)
	}
	if bridge := al.currentEvolutionBridge(); bridge != nil {
		for _, ws := range bridge.workspaces() {
			add(ws)
		}
	}
	if len(out) > 1 {
		sort.Strings(out[1:])
	}
	return out
}

// RunEvolutionColdPath runs the cold path once for workspace, synchronously,
// and reports the store-derived diff. The caller's ctx bounds the run.
func (al *AgentLoop) RunEvolutionColdPath(ctx context.Context, workspace string) (evolution.ColdPathRunSummary, error) {
	bridge := al.currentEvolutionBridge()
	if bridge == nil {
		return evolution.ColdPathRunSummary{Workspace: workspace}, errEvolutionUnavailable
	}
	return bridge.runColdPathSummary(ctx, workspace)
}

// PreviewEvolutionColdPath reports what a run would process without writing.
func (al *AgentLoop) PreviewEvolutionColdPath(workspace string) (evolution.ColdPathRunSummary, error) {
	bridge := al.currentEvolutionBridge()
	if bridge == nil {
		return evolution.ColdPathRunSummary{Workspace: workspace}, errEvolutionUnavailable
	}
	return bridge.previewColdPath(workspace), nil
}

// AcceptEvolutionDraft applies a draft through the runtime's production
// apply path (backup + validate + write + profile sync + history).
func (al *AgentLoop) AcceptEvolutionDraft(
	ctx context.Context,
	workspace, draftID string,
	force bool,
) (evolution.SkillDraft, error) {
	bridge := al.currentEvolutionBridge()
	if bridge == nil {
		return evolution.SkillDraft{}, errEvolutionUnavailable
	}
	return bridge.acceptDraft(ctx, workspace, draftID, force)
}

// RejectEvolutionDraft marks a reviewable draft rejected.
func (al *AgentLoop) RejectEvolutionDraft(workspace, draftID, reason string) (evolution.SkillDraft, error) {
	bridge := al.currentEvolutionBridge()
	if bridge == nil {
		return evolution.SkillDraft{}, errEvolutionUnavailable
	}
	return bridge.rejectDraft(workspace, draftID, reason)
}

// EvolutionLastRuns returns the per-workspace record of the most recent
// cold-path activity the bridge observed.
func (al *AgentLoop) EvolutionLastRuns() map[string]EvolutionRunRecord {
	bridge := al.currentEvolutionBridge()
	if bridge == nil {
		return nil
	}
	return bridge.lastRuns()
}

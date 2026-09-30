package evolution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ColdPathRunSummary reports what a manual cold-path run did — or, for a
// DryRun preview, what it would process — for one workspace. Counts are
// computed from the store around the run so the caller does not need the
// runtime's internal logging to reconstruct the outcome.
type ColdPathRunSummary struct {
	Workspace        string `json:"workspace"`
	Mode             string `json:"mode"`
	Enabled          bool   `json:"enabled"`
	DryRun           bool   `json:"dry_run"`
	Ran              bool   `json:"ran"`
	Note             string `json:"note,omitempty"`
	TaskRecords      int    `json:"task_records"`
	UnclusteredTasks int    `json:"unclustered_tasks"`
	PatternRecords   int    `json:"pattern_records"`
	ReadyPatterns    int    `json:"ready_patterns"`
	CandidateDrafts  int    `json:"candidate_drafts"`
	DraftsCreated    int    `json:"drafts_created"`
	DraftsApplied    int    `json:"drafts_applied"`
	Error            string `json:"error,omitempty"`
}

// ErrDraftNotReviewable is returned when accept/reject targets a draft that is
// not in a reviewable state.
var ErrDraftNotReviewable = errors.New("draft is not reviewable")

// PreviewColdPath reports what a cold-path run for workspace would process,
// without writing anything. It is the read-only side of RunColdPathSummary
// and works regardless of whether the daemon (or even this runtime) is
// driving real runs — callers may also use PreviewColdPathForStore on a
// store built directly from config.
func (rt *Runtime) PreviewColdPath(workspace string) ColdPathRunSummary {
	summary := ColdPathRunSummary{Workspace: workspace, DryRun: true}
	if rt == nil {
		summary.Note = "evolution runtime unavailable"
		return summary
	}
	summary.Enabled = rt.cfg.Enabled
	summary.Mode = rt.cfg.EffectiveMode()
	if !rt.cfg.Enabled {
		summary.Note = "evolution is disabled"
		return summary
	}
	if summary.Mode != "draft" && summary.Mode != "apply" {
		summary.Note = "observe mode produces no drafts"
	}
	store := rt.storeForWorkspace(workspace)
	if err := fillColdPathCounts(&summary, store, workspace); err != nil {
		summary.Error = err.Error()
	}
	return summary
}

// PreviewColdPathForStore is the store-level form of PreviewColdPath for
// callers that hold config but no runtime (daemonless reads).
func PreviewColdPathForStore(cfgMode string, enabled bool, store *Store, workspace string) ColdPathRunSummary {
	summary := ColdPathRunSummary{Workspace: workspace, DryRun: true, Enabled: enabled, Mode: cfgMode}
	if !enabled {
		summary.Note = "evolution is disabled"
		return summary
	}
	if cfgMode != "draft" && cfgMode != "apply" {
		summary.Note = "observe mode produces no drafts"
	}
	if err := fillColdPathCounts(&summary, store, workspace); err != nil {
		summary.Error = err.Error()
	}
	return summary
}

// RunColdPathSummary runs the cold path once for workspace (synchronously —
// the caller bounds ctx) and reports a store-derived diff of the outcome.
func (rt *Runtime) RunColdPathSummary(ctx context.Context, workspace string) ColdPathRunSummary {
	summary := ColdPathRunSummary{Workspace: workspace}
	if rt == nil {
		summary.Error = "evolution runtime unavailable"
		return summary
	}
	summary.Enabled = rt.cfg.Enabled
	summary.Mode = rt.cfg.EffectiveMode()
	if !rt.cfg.Enabled {
		summary.Note = "evolution is disabled"
		return summary
	}
	if summary.Mode != "draft" && summary.Mode != "apply" {
		summary.Note = "observe mode produces no drafts"
		return summary
	}

	store := rt.storeForWorkspace(workspace)
	before, err := draftSnapshot(store, workspace)
	if err != nil {
		summary.Error = err.Error()
		return summary
	}
	summary.Ran = true
	if err := rt.RunColdPathOnce(ctx, workspace); err != nil {
		summary.Error = err.Error()
	}
	after, snapErr := draftSnapshot(store, workspace)
	if snapErr != nil {
		if summary.Error == "" {
			summary.Error = snapErr.Error()
		}
		return summary
	}
	summary.DraftsCreated = countCreatedDrafts(before, after)
	summary.DraftsApplied = countAppliedDrafts(before, after)
	_ = fillColdPathCounts(&summary, store, workspace)
	return summary
}

// AcceptDraft applies a candidate (or quarantined, when force is set) draft
// through the same path the cold path's apply mode uses — backup, validate,
// write, profile sync, history — then marks it accepted.
func (rt *Runtime) AcceptDraft(ctx context.Context, workspace, draftID string, force bool) (SkillDraft, error) {
	if rt == nil {
		return SkillDraft{}, errors.New("evolution runtime unavailable")
	}
	store := rt.storeForWorkspace(workspace)
	draft, err := findDraft(store, workspace, draftID)
	if err != nil {
		return SkillDraft{}, err
	}
	switch draft.Status {
	case DraftStatusCandidate:
	case DraftStatusQuarantined:
		if !force {
			return SkillDraft{}, fmt.Errorf("%w: draft %s is quarantined (findings: %s); pass force to override",
				ErrDraftNotReviewable, draft.ID, strings.Join(draft.ScanFindings, "; "))
		}
	default:
		return SkillDraft{}, fmt.Errorf("%w: draft %s is %s", ErrDraftNotReviewable, draft.ID, draft.Status)
	}
	applier := rt.applierForWorkspace(workspace)
	if applier == nil {
		return SkillDraft{}, errors.New("no applier available for workspace")
	}
	runID := fmt.Sprintf("manual-%d", rt.now().UnixNano())
	return rt.applyCandidateDraft(ctx, workspace, store, applier, draft, runID)
}

// RejectDraft marks a candidate or quarantined draft rejected — a terminal
// human decision recorded in ReviewNotes. A rejected draft keeps occupying
// its source-pattern slot, so the cold path will not auto-regenerate a
// draft the operator already refused.
func (rt *Runtime) RejectDraft(workspace, draftID, reason string) (SkillDraft, error) {
	if rt == nil {
		return SkillDraft{}, errors.New("evolution runtime unavailable")
	}
	store := rt.storeForWorkspace(workspace)
	draft, err := findDraft(store, workspace, draftID)
	if err != nil {
		return SkillDraft{}, err
	}
	switch draft.Status {
	case DraftStatusCandidate, DraftStatusQuarantined:
	default:
		return SkillDraft{}, fmt.Errorf("%w: draft %s is %s", ErrDraftNotReviewable, draft.ID, draft.Status)
	}
	now := rt.now()
	draft.Status = DraftStatusRejected
	draft.UpdatedAt = &now
	note := "rejected by operator"
	if reason = strings.TrimSpace(reason); reason != "" {
		note = fmt.Sprintf("rejected by operator: %s", reason)
	}
	draft.ReviewNotes = appendUniqueStrings(draft.ReviewNotes, note)
	if err := store.SaveDrafts([]SkillDraft{draft}); err != nil {
		return SkillDraft{}, err
	}
	return draft, nil
}

func findDraft(store *Store, workspace, draftID string) (SkillDraft, error) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return SkillDraft{}, errors.New("draft id required")
	}
	drafts, err := store.LoadDrafts()
	if err != nil {
		return SkillDraft{}, err
	}
	var match *SkillDraft
	for i := range drafts {
		d := drafts[i]
		if d.ID != draftID && !strings.HasPrefix(d.ID, draftID) {
			continue
		}
		if workspace != "" && d.WorkspaceID != workspace {
			continue
		}
		if match != nil {
			return SkillDraft{}, fmt.Errorf("draft id %q is ambiguous across workspaces; pass workspace", draftID)
		}
		m := d
		match = &m
	}
	if match == nil {
		return SkillDraft{}, fmt.Errorf("draft %q not found", draftID)
	}
	return *match, nil
}

func draftSnapshot(store *Store, workspace string) (map[string]DraftStatus, error) {
	drafts, err := store.LoadDrafts()
	if err != nil {
		return nil, err
	}
	out := make(map[string]DraftStatus, len(drafts))
	for _, d := range drafts {
		if d.WorkspaceID == workspace {
			out[d.ID] = d.Status
		}
	}
	return out, nil
}

func countCreatedDrafts(before, after map[string]DraftStatus) int {
	n := 0
	for id := range after {
		if _, ok := before[id]; !ok {
			n++
		}
	}
	return n
}

func countAppliedDrafts(before, after map[string]DraftStatus) int {
	n := 0
	for id, status := range after {
		if status != DraftStatusAccepted {
			continue
		}
		if before[id] != DraftStatusAccepted {
			n++
		}
	}
	return n
}

func fillColdPathCounts(summary *ColdPathRunSummary, store *Store, workspace string) error {
	taskRecords, err := store.LoadTaskRecords()
	if err != nil {
		return err
	}
	patternRecords, err := store.LoadPatternRecords()
	if err != nil {
		return err
	}
	drafts, err := store.LoadDrafts()
	if err != nil {
		return err
	}
	for _, record := range taskRecords {
		if record.WorkspaceID != workspace {
			continue
		}
		summary.TaskRecords++
		if record.Status != RecordStatus("clustered") {
			summary.UnclusteredTasks++
		}
	}
	for _, record := range patternRecords {
		if record.WorkspaceID == workspace {
			summary.PatternRecords++
		}
	}
	summary.ReadyPatterns = len(filterReadyRules(patternRecords, workspace))
	for _, draft := range drafts {
		if draft.WorkspaceID == workspace && draft.Status == DraftStatusCandidate {
			summary.CandidateDrafts++
		}
	}
	return nil
}

// RenderDraftDiff renders a compact before/after view of what accepting the
// draft would produce. The "after" body is the exact output of the applier's
// renderer (preview — nothing is written). When the diff is meaningless
// (create kind or no base), the full resulting body is shown.
func RenderDraftDiff(workspace string, draft SkillDraft) (string, error) {
	skillPath := filepath.Join(workspace, "skills", draft.TargetSkillName, "SKILL.md")
	existing, err := os.ReadFile(skillPath)
	hadOriginal := err == nil
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	rendered, err := renderAppliedBody(draft, string(existing), hadOriginal)
	if err != nil {
		return "", fmt.Errorf("render preview: %w", err)
	}
	if !hadOriginal || draft.ChangeKind == ChangeKindCreate {
		return rendered, nil
	}
	return simpleLineDiff(string(existing), rendered), nil
}

// simpleLineDiff shows the smallest differing middle hunk between two texts,
// line-oriented, with common prefix/suffix trimmed.
func simpleLineDiff(before, after string) string {
	a := strings.Split(before, "\n")
	b := strings.Split(after, "\n")
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for endA > start && endB > start && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	var sb strings.Builder
	if start > 0 {
		fmt.Fprintf(&sb, "  (%d unchanged lines above)\n", start)
	}
	for _, line := range a[start:endA] {
		fmt.Fprintf(&sb, "- %s\n", line)
	}
	for _, line := range b[start:endB] {
		fmt.Fprintf(&sb, "+ %s\n", line)
	}
	if tail := len(a) - endA; tail > 0 {
		fmt.Fprintf(&sb, "  (%d unchanged lines below)\n", tail)
	}
	return sb.String()
}

// WorkspaceStatus is the per-workspace view `evolution status` reports.
type WorkspaceStatus struct {
	Workspace        string         `json:"workspace"`
	TaskRecords      int            `json:"task_records"`
	UnclusteredTasks int            `json:"unclustered_tasks"`
	PatternRecords   int            `json:"pattern_records"`
	ReadyPatterns    int            `json:"ready_patterns"`
	Drafts           map[string]int `json:"drafts"`
	Skills           map[string]int `json:"skills"`
}

// SummarizeWorkspace computes the status counters for one workspace's
// evolution state directory.
func SummarizeWorkspace(store *Store, workspace string) (WorkspaceStatus, error) {
	out := WorkspaceStatus{
		Workspace: workspace,
		Drafts:    map[string]int{},
		Skills:    map[string]int{},
	}
	var s ColdPathRunSummary
	if err := fillColdPathCounts(&s, store, workspace); err != nil {
		return out, err
	}
	out.TaskRecords = s.TaskRecords
	out.UnclusteredTasks = s.UnclusteredTasks
	out.PatternRecords = s.PatternRecords
	out.ReadyPatterns = s.ReadyPatterns

	drafts, err := store.LoadDrafts()
	if err != nil {
		return out, err
	}
	for _, d := range drafts {
		if d.WorkspaceID == workspace {
			out.Drafts[string(d.Status)]++
		}
	}
	profiles, err := store.LoadProfiles()
	if err != nil {
		return out, err
	}
	for _, p := range profiles {
		if p.WorkspaceID == workspace || (p.WorkspaceID == "" && usesDefaultWorkspaceState(store.paths, workspace)) {
			out.Skills[string(p.Status)]++
			out.Skills["total"]++
		}
	}
	return out, nil
}

// LoadRecordsByKind resolves a CLI/API --kind filter to the record streams.
// "task"/"pattern" select a file; "feedback" (Track 154) and any other
// non-pattern kind live in the task stream and are filtered by kind value.
func LoadRecordsByKind(store *Store, kind string) ([]LearningRecord, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "all":
		return store.LoadLearningRecords()
	case "task", "case":
		return store.LoadTaskRecords()
	case "pattern", "rule":
		return store.LoadPatternRecords()
	case "feedback":
		records, err := store.LoadTaskRecords()
		if err != nil {
			return nil, err
		}
		out := make([]LearningRecord, 0, len(records))
		for _, r := range records {
			if string(r.Kind) == "feedback" {
				out = append(out, r)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("kind must be task|pattern|feedback")
	}
}

// SortedDrafts returns drafts filtered by workspace and state, newest first.
func SortedDrafts(store *Store, workspace string, states ...DraftStatus) ([]SkillDraft, error) {
	drafts, err := store.LoadDrafts()
	if err != nil {
		return nil, err
	}
	allow := make(map[DraftStatus]struct{}, len(states))
	for _, s := range states {
		allow[s] = struct{}{}
	}
	out := make([]SkillDraft, 0, len(drafts))
	for _, d := range drafts {
		if workspace != "" && d.WorkspaceID != workspace {
			continue
		}
		if len(allow) > 0 {
			if _, ok := allow[d.Status]; !ok {
				continue
			}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

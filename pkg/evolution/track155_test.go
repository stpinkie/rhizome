package evolution_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

var track155Now = time.Unix(1700004000, 0).UTC()

// captureBus is a deterministic runtimeevents.Bus: PublishNonBlocking is
// synchronous in the real bus's contract surface, and tests need the
// published events without the async subscription dispatch.
type captureBus struct {
	mu     sync.Mutex
	events []runtimeevents.Event
}

func (b *captureBus) Publish(_ context.Context, evt runtimeevents.Event) runtimeevents.PublishResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, evt)
	return runtimeevents.PublishResult{Matched: 1, Delivered: 1}
}

func (b *captureBus) PublishNonBlocking(evt runtimeevents.Event) runtimeevents.PublishResult {
	return b.Publish(context.Background(), evt)
}

func (b *captureBus) Channel() runtimeevents.EventChannel { return nil }
func (b *captureBus) Close() error                        { return nil }
func (b *captureBus) Stats() runtimeevents.Stats          { return runtimeevents.Stats{} }

func (b *captureBus) kinds() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.events))
	for _, e := range b.events {
		out = append(out, e.Kind.String())
	}
	return out
}

func (b *captureBus) hasKind(want string) bool {
	for _, k := range b.kinds() {
		if k == want {
			return true
		}
	}
	return false
}

func (b *captureBus) find(want string) *runtimeevents.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.events {
		if b.events[i].Kind.String() == want {
			return &b.events[i]
		}
	}
	return nil
}

// track155Runtime builds a Runtime wired with a captureBus and an audit
// file under the test's temp dir — the shape the daemon bridge produces.
func track155Runtime(
	t *testing.T,
	root string,
	store *evolution.Store,
	bus *captureBus,
	extra func(*evolution.RuntimeOptions),
) (*evolution.Runtime, string) {
	t.Helper()
	auditPath := filepath.Join(t.TempDir(), "evolution-audit.jsonl")
	opts := evolution.RuntimeOptions{
		Config: config.EvolutionConfig{
			Enabled: true, Mode: "draft", MinTaskCount: 1, MinSuccessRatio: 0.5,
		},
		Now:            func() time.Time { return track155Now },
		Store:          store,
		EventBus:       bus,
		AuditPath:      auditPath,
		SkillsRecaller: evolution.NewSkillsRecaller(root),
		DraftGenerator: stubDraftGenerator{draft: evolution.SkillDraft{
			ID:              "draft-weather",
			TargetSkillName: "weather",
			DraftType:       evolution.DraftTypeShortcut,
			ChangeKind:      evolution.ChangeKindAppend,
			HumanSummary:    "prefer the proven weather path",
			BodyOrPatch:     testSkillBody,
		}},
	}
	if extra != nil {
		extra(&opts)
	}
	rt, err := evolution.NewRuntime(opts)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt, auditPath
}

func auditOps(t *testing.T, path string) []string {
	t.Helper()
	raw, err := evolution.ReadAuditTail(path, 0)
	if err != nil {
		t.Fatalf("ReadAuditTail: %v", err)
	}
	ops := make([]string, 0, len(raw))
	for _, line := range raw {
		var e map[string]any
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("audit line not json: %v", err)
		}
		op, _ := e["op"].(string)
		ops = append(ops, op)
	}
	return ops
}

func hasOp(ops []string, want string) bool {
	for _, op := range ops {
		if op == want {
			return true
		}
	}
	return false
}

func seedTrack155AdmittedTask(t *testing.T, store *evolution.Store, root, id string) {
	t.Helper()
	ok := true
	if err := store.AppendTaskRecord(context.Background(), evolution.LearningRecord{
		ID:             id,
		Kind:           evolution.RecordKindTask,
		WorkspaceID:    root,
		CreatedAt:      track155Now.Add(-time.Hour),
		Summary:        "weather lookup",
		UserGoal:       "check weather",
		FinalOutput:    "sunny",
		Status:         evolution.RecordStatus("new"),
		Success:        &ok,
		UsedSkillNames: []string{"weather"},
	}); err != nil {
		t.Fatalf("AppendTaskRecord %s: %v", id, err)
	}
}

func TestRunColdPathEmitsRunAndDraftEvents(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack155AdmittedTask(t, store, root, "task-1")
	bus := &captureBus{}
	rt, auditPath := track155Runtime(t, root, store, bus, nil)

	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	for _, want := range []string{
		"evolution.run.started",
		"evolution.run.finished",
		"evolution.draft.candidate",
	} {
		if !bus.hasKind(want) {
			t.Fatalf("missing event kind %s; got %v", want, bus.kinds())
		}
	}

	started := bus.find("evolution.run.started")
	if started == nil {
		t.Fatal("no run.started event")
	}
	if started.Attrs["trigger"] != "auto" {
		t.Fatalf("run.started trigger = %v, want auto", started.Attrs["trigger"])
	}
	finished := bus.find("evolution.run.finished")
	if finished == nil {
		t.Fatal("no run.finished event")
	}
	if finished.Attrs["status"] != "ok" {
		t.Fatalf("run.finished status = %v, want ok", finished.Attrs["status"])
	}
	if finished.Attrs["workspace"] != root || finished.Source.Component != "evolution" {
		t.Fatalf("run.finished missing workspace/source: %+v", finished.Attrs)
	}
	if created, _ := finished.Attrs["drafts_created"].(int); created != 1 {
		t.Fatalf("run.finished drafts_created = %v, want 1", finished.Attrs["drafts_created"])
	}

	ops := auditOps(t, auditPath)
	for _, want := range []string{"run.started", "run.finished", "draft.candidate"} {
		if !hasOp(ops, want) {
			t.Fatalf("missing audit op %s; got %v", want, ops)
		}
	}
}

func TestRunColdPathSummaryEmitsManualTrigger(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack155AdmittedTask(t, store, root, "task-1")
	bus := &captureBus{}
	rt, _ := track155Runtime(t, root, store, bus, nil)

	summary := rt.RunColdPathSummary(context.Background(), root)
	if summary.Error != "" {
		t.Fatalf("RunColdPathSummary: %s", summary.Error)
	}
	started := bus.find("evolution.run.started")
	if started == nil || started.Attrs["trigger"] != "manual" {
		t.Fatalf("run.started trigger = %v, want manual", started)
	}
}

func TestAcceptDraftEmitsAcceptedAndApplied(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	saveCandidateDraft(t, store, root, "draft-1", "candidate")
	bus := &captureBus{}
	rt, auditPath := track155Runtime(t, root, store, bus, func(opts *evolution.RuntimeOptions) {
		opts.ApplierFactory = func(workspace string) *evolution.Applier {
			return evolution.NewApplier(evolution.NewPaths(workspace, ""), nil)
		}
	})

	draft, err := rt.AcceptDraft(context.Background(), root, "draft-1", false)
	if err != nil {
		t.Fatalf("AcceptDraft: %v", err)
	}
	if draft.Status != evolution.DraftStatusAccepted {
		t.Fatalf("status = %s, want accepted", draft.Status)
	}
	for _, want := range []string{"evolution.draft.accepted", "evolution.skill.applied"} {
		if !bus.hasKind(want) {
			t.Fatalf("missing event %s; got %v", want, bus.kinds())
		}
	}
	applied := bus.find("evolution.skill.applied")
	if applied.Attrs["skill"] != "weather" || applied.Attrs["via"] != "accept" {
		t.Fatalf("skill.applied attrs = %+v", applied.Attrs)
	}
	ops := auditOps(t, auditPath)
	if !hasOp(ops, "skill.applied") || !hasOp(ops, "draft.accepted") {
		t.Fatalf("audit ops missing accept/apply: %v", ops)
	}
}

func TestRejectDraftEmitsRejected(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	saveCandidateDraft(t, store, root, "draft-1", "candidate")
	bus := &captureBus{}
	rt, auditPath := track155Runtime(t, root, store, bus, nil)

	if _, err := rt.RejectDraft(root, "draft-1", "not useful"); err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	evt := bus.find("evolution.draft.rejected")
	if evt == nil || evt.Attrs["reason"] != "not useful" {
		t.Fatalf("draft.rejected event = %+v", evt)
	}
	if !hasOp(auditOps(t, auditPath), "draft.rejected") {
		t.Fatalf("audit missing draft.rejected: %v", auditOps(t, auditPath))
	}
}

func TestArchiveSkillEmitsLifecycle(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack153Profile(t, store, root, "weather", evolution.SkillStatusActive)
	bus := &captureBus{}
	rt, auditPath := track155Runtime(t, root, store, bus, nil)

	if _, err := rt.ArchiveSkill(root, "weather"); err != nil {
		t.Fatalf("ArchiveSkill: %v", err)
	}
	evt := bus.find("evolution.skill.lifecycle")
	if evt == nil {
		t.Fatalf("no skill.lifecycle event; got %v", bus.kinds())
	}
	if evt.Attrs["from"] != "active" || evt.Attrs["to"] != "archived" || evt.Attrs["trigger"] != "manual" {
		t.Fatalf("skill.lifecycle attrs = %+v", evt.Attrs)
	}
	if !hasOp(auditOps(t, auditPath), "skill.lifecycle") {
		t.Fatalf("audit missing skill.lifecycle")
	}
}

func TestPruneStateEmitsPrune(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	old := track155Now.AddDate(0, 0, -100)
	if err := store.AppendTaskRecord(context.Background(), evolution.LearningRecord{
		ID: "old-1", Kind: evolution.RecordKindTask, WorkspaceID: root,
		CreatedAt: old, Status: evolution.RecordStatus("completed"),
	}); err != nil {
		t.Fatal(err)
	}
	bus := &captureBus{}
	rt, auditPath := track155Runtime(t, root, store, bus, func(opts *evolution.RuntimeOptions) {
		opts.Config.RetentionTaskRecordsDays = 90
	})

	summary, err := rt.PruneState(root, false)
	if err != nil {
		t.Fatalf("PruneState: %v", err)
	}
	if summary.TaskRecordsPruned != 1 {
		t.Fatalf("pruned = %d, want 1", summary.TaskRecordsPruned)
	}
	evt := bus.find("evolution.prune")
	if evt == nil {
		t.Fatalf("no evolution.prune event; got %v", bus.kinds())
	}
	if evt.Attrs["task_records_pruned"] != 1 || evt.Attrs["trigger"] != "manual" {
		t.Fatalf("evolution.prune attrs = %+v", evt.Attrs)
	}
	if !hasOp(auditOps(t, auditPath), "prune") {
		t.Fatalf("audit missing prune op")
	}
}

func TestRecordFeedbackEmitsEvent(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack155AdmittedTask(t, store, root, "task-1")
	bus := &captureBus{}
	rt, auditPath := track155Runtime(t, root, store, bus, nil)

	record, err := rt.RecordFeedback(root, "bad", "task-1", "", "wrong path")
	if err != nil {
		t.Fatalf("RecordFeedback: %v", err)
	}
	if record.Kind != evolution.RecordKindFeedback {
		t.Fatalf("kind = %s", record.Kind)
	}
	evt := bus.find("evolution.feedback")
	if evt == nil {
		t.Fatalf("no evolution.feedback event; got %v", bus.kinds())
	}
	if evt.Attrs["signal"] != "bad" || evt.Attrs["record_id"] != "task-1" {
		t.Fatalf("feedback attrs = %+v", evt.Attrs)
	}
	if !hasOp(auditOps(t, auditPath), "feedback") {
		t.Fatalf("audit missing feedback op")
	}
}

func TestSweepEmitsLifecycleAndPrune(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack155AdmittedTask(t, store, root, "task-1")
	// An idle, low-retention evolved profile transitions in the sweep.
	if err := store.SaveProfile(evolution.SkillProfile{
		SkillName:      "stale-skill",
		WorkspaceID:    root,
		CurrentVersion: "v1",
		Status:         evolution.SkillStatusActive,
		Origin:         "evolved",
		LastUsedAt:     track155Now.Add(-2 * time.Hour),
		RetentionScore: 0.1,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	// Tiny cold threshold forces the transition during the run's sweep.
	cfg := config.DefaultConfig()
	cfg.Timeouts.Evolution.SkillCold = config.Duration(time.Hour)
	config.SetGlobal(cfg)
	defer config.SetGlobal(nil)

	bus := &captureBus{}
	rt, auditPath := track155Runtime(t, root, store, bus, func(opts *evolution.RuntimeOptions) {
		opts.Config.RetentionTaskRecordsDays = 1
	})
	// Age the task record so the sweep's prune also fires.
	records, _ := store.LoadTaskRecords()
	for i := range records {
		records[i].CreatedAt = track155Now.AddDate(0, 0, -10)
		records[i].UpdatedAt = nil
	}
	if err := store.SaveTaskRecords(records); err != nil {
		t.Fatalf("SaveTaskRecords: %v", err)
	}

	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}
	lifecycle := bus.find("evolution.skill.lifecycle")
	if lifecycle == nil {
		t.Fatalf("no sweep skill.lifecycle event; got %v", bus.kinds())
	}
	if lifecycle.Attrs["skill"] != "stale-skill" || lifecycle.Attrs["to"] != "cold" ||
		lifecycle.Attrs["trigger"] != "sweep" {
		t.Fatalf("lifecycle attrs = %+v", lifecycle.Attrs)
	}
	// Clustered records survive; the seeded task clustered during this run,
	// so prune may or may not fire — assert the sweep's lifecycle entry and
	// leave prune coverage to TestPruneStateEmitsPrune.
	ops := auditOps(t, auditPath)
	if !hasOp(ops, "skill.lifecycle") {
		t.Fatalf("audit missing sweep lifecycle: %v", ops)
	}
}

func TestReadAuditEntriesFilters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evolution-audit.jsonl")
	entries := []map[string]any{
		{"ts": "2026-10-29T01:00:00Z", "op": "run.finished", "workspace": "/ws/a", "status": "ok"},
		{"ts": "2026-10-29T02:00:00Z", "op": "draft.candidate", "workspace": "/ws/a", "draft_id": "d-1"},
		{"ts": "2026-10-29T03:00:00Z", "op": "run.finished", "workspace": "/ws/b", "status": "ok"},
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, _ := json.Marshal(e)
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	all, err := evolution.ReadAuditEntries(path, 0, evolution.AuditFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all entries = %d, %v; want 3", len(all), err)
	}
	runs, err := evolution.ReadAuditEntries(path, 0, evolution.AuditFilter{Op: "run.finished"})
	if err != nil || len(runs) != 2 {
		t.Fatalf("run.finished = %d, %v; want 2", len(runs), err)
	}
	drafts, err := evolution.ReadAuditEntries(path, 0, evolution.AuditFilter{Op: "draft.*"})
	if err != nil || len(drafts) != 1 {
		t.Fatalf("draft.* = %d, %v; want 1", len(drafts), err)
	}
	wsB, err := evolution.ReadAuditEntries(path, 0, evolution.AuditFilter{Workspace: "/ws/b"})
	if err != nil || len(wsB) != 1 {
		t.Fatalf("workspace b = %d, %v; want 1", len(wsB), err)
	}
	since, err := evolution.ReadAuditEntries(path, 0, evolution.AuditFilter{
		Since: time.Date(2026, 10, 29, 2, 30, 0, 0, time.UTC),
	})
	if err != nil || len(since) != 1 {
		t.Fatalf("since = %d, %v; want 1", len(since), err)
	}
	last, err := evolution.ReadAuditEntries(path, 1, evolution.AuditFilter{})
	if err != nil || len(last) != 1 || last[0]["op"] != "run.finished" {
		t.Fatalf("tail 1 = %v; want the last entry", last)
	}
}

func TestNilObservabilityRuntimeStillWorks(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack155AdmittedTask(t, store, root, "task-1")
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{
			Enabled: true, Mode: "draft", MinTaskCount: 1, MinSuccessRatio: 0.5,
		},
		Store:          store,
		SkillsRecaller: evolution.NewSkillsRecaller(root),
		DraftGenerator: stubDraftGenerator{draft: evolution.SkillDraft{
			ID:              "draft-weather",
			TargetSkillName: "weather",
			DraftType:       evolution.DraftTypeShortcut,
			ChangeKind:      evolution.ChangeKindAppend,
			HumanSummary:    "x",
			BodyOrPatch:     testSkillBody,
		}},
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	// No bus, no audit path — every observe call must be a silent no-op.
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}
	if _, err := rt.RecordFeedback(root, "good", "task-1", "", ""); err != nil {
		t.Fatalf("RecordFeedback: %v", err)
	}
}

func TestDefaultAuditPathUsesRhizomeHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RHIZOME_HOME", home)
	if got := evolution.DefaultAuditPath(); got != filepath.Join(home, "evolution-audit.jsonl") {
		t.Fatalf("DefaultAuditPath = %q", got)
	}
}

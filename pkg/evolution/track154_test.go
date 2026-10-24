package evolution_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

var track154Now = time.Unix(1700003000, 0).UTC()

func seedTrack154Task(t *testing.T, store *evolution.Store, root, id, session, summary string, createdAt time.Time) {
	t.Helper()
	ok := true
	if err := store.AppendTaskRecord(context.Background(), evolution.LearningRecord{
		ID: id, Kind: evolution.RecordKindTask, WorkspaceID: root,
		CreatedAt: createdAt, SessionKey: session,
		Summary: summary, FinalOutput: "did the thing",
		Status: evolution.RecordStatus("new"), Success: &ok,
	}); err != nil {
		t.Fatalf("AppendTaskRecord %s: %v", id, err)
	}
}

func newTrack154Runtime(t *testing.T, root string, store *evolution.Store, judge *stubSuccessJudge) *evolution.Runtime {
	t.Helper()
	clusterer := evolution.NewLLMPatternClusterer(
		&llmClusterTestProvider{content: `not-json`, defaultModel: "test-model"},
		"test-model",
		evolution.NewHeuristicPatternClusterer(1, nil),
		1,
		func() time.Time { return track154Now },
	)
	rt, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config: config.EvolutionConfig{
			Enabled: true, Mode: "draft", MinTaskCount: 1, MinSuccessRatio: 0.5,
		},
		Store:            store,
		PatternClusterer: clusterer,
		SuccessJudge:     judge,
		SkillsRecaller:   evolution.NewSkillsRecaller(root),
		DraftGenerator: stubDraftGenerator{draft: evolution.SkillDraft{
			ID:              "draft-weather",
			TargetSkillName: "weather",
			DraftType:       evolution.DraftTypeShortcut,
			ChangeKind:      evolution.ChangeKindAppend,
			HumanSummary:    "prefer the proven weather path",
			BodyOrPatch:     "## Start Here\nUse the weather path directly.",
		}},
	})
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt
}

func appendTrack154Feedback(t *testing.T, store *evolution.Store, root, signal, recordID, sessionKey, reason string) {
	t.Helper()
	record, err := evolution.NewFeedbackRecord(root, signal, recordID, sessionKey, reason, track154Now)
	if err != nil {
		t.Fatalf("NewFeedbackRecord: %v", err)
	}
	if err := store.AppendTaskRecord(context.Background(), record); err != nil {
		t.Fatalf("AppendTaskRecord feedback: %v", err)
	}
}

func loadTrack154TaskRecords(t *testing.T, store *evolution.Store) map[string]evolution.LearningRecord {
	t.Helper()
	records, err := store.LoadTaskRecords()
	if err != nil {
		t.Fatalf("LoadTaskRecords: %v", err)
	}
	out := make(map[string]evolution.LearningRecord, len(records))
	for _, r := range records {
		out[r.ID] = r
	}
	return out
}

func patternTaskIDs(t *testing.T, store *evolution.Store) map[string]bool {
	t.Helper()
	patterns, err := store.LoadPatternRecords()
	if err != nil {
		t.Fatalf("LoadPatternRecords: %v", err)
	}
	out := map[string]bool{}
	for _, p := range patterns {
		for _, id := range p.TaskRecordIDs {
			out[id] = true
		}
	}
	return out
}

func TestNewFeedbackRecordShape(t *testing.T) {
	record, err := evolution.NewFeedbackRecord(
		"ws", "bad", "task-abc", "", "gave a wrong answer", track154Now)
	if err != nil {
		t.Fatalf("NewFeedbackRecord: %v", err)
	}
	if record.Kind != evolution.RecordKindFeedback {
		t.Fatalf("kind = %q", record.Kind)
	}
	if evolution.FeedbackSignal(record) != "bad" {
		t.Fatalf("signal = %q", evolution.FeedbackSignal(record))
	}
	if len(record.SourceRecordIDs) != 1 || record.SourceRecordIDs[0] != "task-abc" {
		t.Fatalf("link = %v", record.SourceRecordIDs)
	}
	if !strings.Contains(record.Summary, "wrong answer") {
		t.Fatalf("summary missing reason: %q", record.Summary)
	}

	if _, err := evolution.NewFeedbackRecord("ws", "meh", "task-abc", "", "", track154Now); err == nil {
		t.Fatal("expected invalid-signal error")
	}
	if _, err := evolution.NewFeedbackRecord("ws", "good", "", "", "", track154Now); err == nil {
		t.Fatal("expected missing-link error")
	}
}

func TestRuntimeRecordFeedbackLinks(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack154Task(t, store, root, "task-old", "sess-1", "weather lookup", track154Now.Add(-time.Hour))
	seedTrack154Task(t, store, root, "task-new", "sess-2", "weather lookup", track154Now)
	rt := newTrack154Runtime(t, root, store, nil)

	// No link flags → the most recent task record is the target.
	record, err := rt.RecordFeedback(root, "good", "", "", "")
	if err != nil {
		t.Fatalf("RecordFeedback default: %v", err)
	}
	if len(record.SourceRecordIDs) != 1 || record.SourceRecordIDs[0] != "task-new" {
		t.Fatalf("default link = %v", record.SourceRecordIDs)
	}

	// Prefix resolution works like the draft verbs.
	record, err = rt.RecordFeedback(root, "bad", "task-ol", "", "stale approach")
	if err != nil {
		t.Fatalf("RecordFeedback prefix: %v", err)
	}
	if record.SourceRecordIDs[0] != "task-old" {
		t.Fatalf("prefix link = %v", record.SourceRecordIDs)
	}

	// Session-only needs no record resolution.
	record, err = rt.RecordFeedback(root, "bad", "", "sess-9", "")
	if err != nil {
		t.Fatalf("RecordFeedback session: %v", err)
	}
	if record.SessionKey != "sess-9" || len(record.SourceRecordIDs) != 0 {
		t.Fatalf("session link = %v / %v", record.SessionKey, record.SourceRecordIDs)
	}

	if _, err := rt.RecordFeedback(root, "bad", "ghost-id", "", ""); err == nil {
		t.Fatal("expected unresolved-record error")
	}
}

func TestColdPathBadFeedbackExcludesAdmitted(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack154Task(t, store, root, "task-success", "sess-1", "weather lookup 100", track154Now)
	seedTrack154Task(t, store, root, "task-marked-bad", "sess-2", "weather lookup 200", track154Now)
	appendTrack154Feedback(t, store, root, "bad", "task-marked-bad", "", "wrong path")

	rt := newTrack154Runtime(t, root, store, &stubSuccessJudge{
		decisions: map[string]evolution.TaskSuccessDecision{},
	})
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	linked := patternTaskIDs(t, store)
	if linked["task-marked-bad"] {
		t.Fatal("bad-linked record reached a pattern's task ids")
	}
	if !linked["task-success"] {
		t.Fatal("unlinked success record lost its pattern linkage")
	}

	// The operator mark demotes it to failure evidence — the bad record
	// still clusters as evidence (the avoid-pattern path) but never as
	// an admitted success. The feedback entry itself is never clustered.
	byID := loadTrack154TaskRecords(t, store)
	for id, r := range byID {
		if r.Kind == evolution.RecordKindFeedback && r.Status == evolution.RecordStatus("clustered") {
			t.Fatalf("feedback record %s clustered — feedback informs, never clusters", id)
		}
	}
}

func TestColdPathGoodFeedbackSkipsJudgeVeto(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack154Task(t, store, root, "task-vetoed", "sess-1", "weather lookup 100", track154Now)
	seedTrack154Task(t, store, root, "task-peer", "sess-2", "weather lookup 200", track154Now)
	appendTrack154Feedback(t, store, root, "good", "task-vetoed", "", "actually correct")

	judge := &stubSuccessJudge{
		decisions: map[string]evolution.TaskSuccessDecision{
			"task-vetoed": {Success: false, Reason: "heuristic miss"},
		},
	}
	rt := newTrack154Runtime(t, root, store, judge)
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	linked := patternTaskIDs(t, store)
	if !linked["task-vetoed"] {
		t.Fatal("good-linked record lost pattern linkage to the judge veto")
	}
	for _, called := range judge.calls {
		if called == "task-vetoed" {
			t.Fatal("judge consulted on a good-marked record")
		}
	}
}

func TestSessionBadFeedbackExcludesWholeSession(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack154Task(t, store, root, "task-a", "sess-bad", "weather lookup 100", track154Now)
	seedTrack154Task(t, store, root, "task-b", "sess-bad", "weather lookup 200", track154Now.Add(time.Minute))
	// A different cluster key keeps task-c out of the dragged-down ratio —
	// a bad session must not cost an unrelated task its admission.
	seedTrack154Task(t, store, root, "task-c", "sess-ok", "calendar lookup 300", track154Now)
	appendTrack154Feedback(t, store, root, "bad", "", "sess-bad", "whole session off-rails")

	rt := newTrack154Runtime(t, root, store, &stubSuccessJudge{
		decisions: map[string]evolution.TaskSuccessDecision{},
	})
	if err := rt.RunColdPathOnce(context.Background(), root); err != nil {
		t.Fatalf("RunColdPathOnce: %v", err)
	}

	linked := patternTaskIDs(t, store)
	if linked["task-a"] || linked["task-b"] {
		t.Fatalf("bad-session records reached pattern linkage: %v", linked)
	}
	if !linked["task-c"] {
		t.Fatal("unrelated record lost its pattern linkage")
	}
}

func TestRecordsByKindSeparatesFeedback(t *testing.T) {
	root := t.TempDir()
	store := evolution.NewStore(evolution.NewPaths(root, ""))
	seedTrack154Task(t, store, root, "task-1", "sess-1", "weather lookup", track154Now)
	fb, err := evolution.NewFeedbackRecord(root, "good", "task-1", "", "", track154Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTaskRecord(context.Background(), fb); err != nil {
		t.Fatal(err)
	}

	tasks, err := evolution.LoadRecordsByKind(store, "task")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tasks {
		if r.Kind == evolution.RecordKindFeedback {
			t.Fatal("feedback leaked into --kind task")
		}
	}
	feedback, err := evolution.LoadRecordsByKind(store, "feedback")
	if err != nil {
		t.Fatal(err)
	}
	if len(feedback) != 1 || feedback[0].ID != fb.ID {
		t.Fatalf("--kind feedback = %v", feedback)
	}
}

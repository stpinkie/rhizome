package evolution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Feedback signals an operator can attach to a task record or session.
// A bad link demotes the record to failure evidence — excluded from the
// admitted set even when its outcome read completed — and a good mark
// lifts the judge's veto. Feedback records inform, never cluster.
const (
	FeedbackSignalGood = "good"
	FeedbackSignalBad  = "bad"
)

// ErrFeedbackTarget is returned when a feedback write cannot resolve the
// record it should link to.
var ErrFeedbackTarget = errors.New("feedback target not found")

// IsValidFeedbackSignal reports whether signal is a known feedback value.
func IsValidFeedbackSignal(signal string) bool {
	return signal == FeedbackSignalGood || signal == FeedbackSignalBad
}

// NewFeedbackRecord builds the RecordKindFeedback record for an operator
// signal. The record rides the task stream (LoadRecordsByKind "feedback")
// so it shares task-record retention; kind keeps it out of clustering.
// Linking is record-first: a resolved record id is the strong link, the
// session key the loose one.
func NewFeedbackRecord(
	workspace, signal, recordID, sessionKey, reason string,
	now time.Time,
) (LearningRecord, error) {
	if !IsValidFeedbackSignal(signal) {
		return LearningRecord{}, fmt.Errorf(
			"signal must be good|bad, got %q", signal)
	}
	recordID = strings.TrimSpace(recordID)
	sessionKey = strings.TrimSpace(sessionKey)
	if recordID == "" && sessionKey == "" {
		return LearningRecord{}, fmt.Errorf(
			"feedback needs a link: --record or --session")
	}
	record := LearningRecord{
		ID:          fmt.Sprintf("fb-%d", now.UnixNano()),
		Kind:        RecordKindFeedback,
		WorkspaceID: workspace,
		CreatedAt:   now,
		SessionKey:  sessionKey,
		Signals:     []string{signal},
		Status:      RecordStatus("new"),
	}
	if recordID != "" {
		record.SourceRecordIDs = []string{recordID}
	}
	summary := "operator marked " + signal
	if recordID != "" {
		summary += " on record " + recordID
	} else {
		summary += " on session " + sessionKey
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		summary += ": " + reason
	}
	record.Summary = summary
	return record, nil
}

// FeedbackSignal returns the signal on a feedback record, or "" when the
// record is not a feedback entry.
func FeedbackSignal(record LearningRecord) string {
	if record.Kind != RecordKindFeedback {
		return ""
	}
	for _, s := range record.Signals {
		if IsValidFeedbackSignal(s) {
			return s
		}
	}
	return ""
}

// feedbackLinks returns the record IDs and session keys feedback records
// with the given signal link to.
func feedbackLinks(
	records []LearningRecord,
	signal string,
) (map[string]struct{}, map[string]struct{}) {
	ids := make(map[string]struct{})
	sessions := make(map[string]struct{})
	for _, record := range records {
		if FeedbackSignal(record) != signal {
			continue
		}
		for _, id := range record.SourceRecordIDs {
			if id = strings.TrimSpace(id); id != "" {
				ids[id] = struct{}{}
			}
		}
		if key := strings.TrimSpace(record.SessionKey); key != "" {
			sessions[key] = struct{}{}
		}
	}
	return ids, sessions
}

// BadFeedbackLinks returns the record IDs and session keys marked bad.
func BadFeedbackLinks(records []LearningRecord) (map[string]struct{}, map[string]struct{}) {
	return feedbackLinks(records, FeedbackSignalBad)
}

// GoodFeedbackLinks returns the record IDs and session keys marked good.
func GoodFeedbackLinks(records []LearningRecord) (map[string]struct{}, map[string]struct{}) {
	return feedbackLinks(records, FeedbackSignalGood)
}

// feedbackLinked reports whether a task record is covered by a feedback
// link set — exact record id, or the session it ran in.
func feedbackLinked(ids, sessions map[string]struct{}, record LearningRecord) bool {
	if _, ok := ids[record.ID]; ok {
		return true
	}
	if key := strings.TrimSpace(record.SessionKey); key != "" {
		_, ok := sessions[key]
		return ok
	}
	return false
}

// RecordFeedback writes an operator feedback record against a task record
// or session. With no explicit link the most recent task record in the
// workspace is the target — the last turn, per the verb's contract.
func (rt *Runtime) RecordFeedback(
	workspace, signal, recordID, sessionKey, reason string,
) (LearningRecord, error) {
	if rt == nil {
		return LearningRecord{}, errors.New("evolution runtime unavailable")
	}
	if !IsValidFeedbackSignal(signal) {
		return LearningRecord{}, fmt.Errorf("signal must be good|bad, got %q", signal)
	}
	store := rt.storeForWorkspace(workspace)
	resolvedID, err := resolveFeedbackTarget(store, workspace, recordID, sessionKey)
	if err != nil {
		return LearningRecord{}, err
	}
	record, err := NewFeedbackRecord(workspace, signal, resolvedID, sessionKey, reason, rt.now())
	if err != nil {
		return LearningRecord{}, err
	}
	if err := store.AppendTaskRecord(context.Background(), record); err != nil {
		return LearningRecord{}, err
	}
	return record, nil
}

// resolveFeedbackTarget turns the operator's link into a full record id:
// an explicit --record (prefix match allowed), else "" — session-only
// feedback needs no record link; with neither, the workspace's most recent
// task record.
func resolveFeedbackTarget(
	store *Store,
	workspace, recordID, sessionKey string,
) (string, error) {
	recordID = strings.TrimSpace(recordID)
	sessionKey = strings.TrimSpace(sessionKey)
	if recordID == "" && sessionKey != "" {
		return "", nil
	}

	records, err := store.LoadTaskRecords()
	if err != nil {
		return "", err
	}
	var latest *LearningRecord
	for i := range records {
		r := records[i]
		if !isTaskRecordKind(r.Kind) || r.WorkspaceID != workspace {
			continue
		}
		if latest == nil || r.CreatedAt.After(latest.CreatedAt) {
			cp := r
			latest = &cp
		}
	}

	if recordID == "" {
		if latest == nil {
			return "", fmt.Errorf("%w: no task records in workspace; pass --record or --session",
				ErrFeedbackTarget)
		}
		return latest.ID, nil
	}

	var match *LearningRecord
	for i := range records {
		r := records[i]
		if !isTaskRecordKind(r.Kind) || (r.WorkspaceID != workspace && r.WorkspaceID != "") {
			continue
		}
		if r.ID != recordID && !strings.HasPrefix(r.ID, recordID) {
			continue
		}
		if match != nil && match.ID != r.ID {
			return "", fmt.Errorf("%w: record id %q is ambiguous", ErrFeedbackTarget, recordID)
		}
		cp := r
		match = &cp
	}
	if match == nil {
		return "", fmt.Errorf("%w: record %q", ErrFeedbackTarget, recordID)
	}
	return match.ID, nil
}

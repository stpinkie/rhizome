package econ

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

func testEntry(peerID string) Entry {
	return Entry{
		PeerID:    peerID,
		Direction: DirectionPayable,
		TaskID:    "task-1",
		Unit:      "credits",
		Amount:    "0.012",
		Usage:     &toolshared.RemoteUsage{PromptTokens: 1000, TotalTokens: 1000},
	}
}

func TestLedgerRecordAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	l, err := OpenLedger(path)
	require.NoError(t, err)

	e, err := l.Record(testEntry("peer-a"))
	require.NoError(t, err)
	assert.NotEmpty(t, e.EntryID)
	assert.Equal(t, StateAccrued, e.State)
	assert.False(t, e.TS.IsZero())

	// Second record with a different peer+direction dedupe key.
	recv := testEntry("peer-a")
	recv.Direction = DirectionReceivable
	recv.Amount = "0.5"
	_, err = l.Record(recv)
	require.NoError(t, err)

	// Reload: same state, same ids.
	l2, err := OpenLedger(path)
	require.NoError(t, err)
	ents := l2.Entries()
	require.Len(t, ents, 2)
	assert.Equal(t, e.EntryID, ents[0].EntryID)
	assert.Equal(t, "0.012", ents[0].Amount)
	assert.Equal(t, StateAccrued, ents[0].State)
}

func TestLedgerDedupe(t *testing.T) {
	l, err := OpenLedger("")
	require.NoError(t, err)

	first, err := l.Record(testEntry("peer-a"))
	require.NoError(t, err)
	// Same task id + direction + peer: resubmit/re-fetch is not a second
	// accrual — the existing entry is returned.
	second, err := l.Record(testEntry("peer-a"))
	require.NoError(t, err)
	assert.Equal(t, first.EntryID, second.EntryID)
	assert.Len(t, l.Entries(), 1)

	// Same task on the receivable side is a different obligation.
	recv := testEntry("peer-a")
	recv.Direction = DirectionReceivable
	_, err = l.Record(recv)
	require.NoError(t, err)
	assert.Len(t, l.Entries(), 2)
}

func TestLedgerTransitions(t *testing.T) {
	l, err := OpenLedger("")
	require.NoError(t, err)

	e, err := l.Record(testEntry("peer-a"))
	require.NoError(t, err)

	// accrued → settled carries the settle round + tx reference.
	got, err := l.Transition(e.EntryID, StateSettled, "settle-7", "0xtx")
	require.NoError(t, err)
	assert.Equal(t, StateSettled, got.State)
	assert.Equal(t, "settle-7", got.SettleID)
	assert.Equal(t, "0xtx", got.SettleTX)

	// settled is terminal.
	_, err = l.Transition(e.EntryID, StateDisputed, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "settled")

	// Unknown id.
	_, err = l.Transition("entry-nope", StateSettled, "", "")
	require.Error(t, err)

	// accrued → written_off is not a legal move — disputes gate write-off.
	d, err := l.Record(Entry{
		PeerID: "peer-a", Direction: DirectionReceivable,
		TaskID: "task-9", Unit: "credits", Amount: "1",
	})
	require.NoError(t, err)
	_, err = l.Transition(d.EntryID, StateWrittenOff, "", "")
	require.Error(t, err)

	// disputed → accrued (--credit) clears stale settle markers;
	// disputed → written_off (--drop) is the other resolution.
	_, err = l.Transition(d.EntryID, StateDisputed, "", "")
	require.NoError(t, err)
	back, err := l.Transition(d.EntryID, StateAccrued, "", "")
	require.NoError(t, err)
	assert.Empty(t, back.SettleID)
	_, err = l.Transition(d.EntryID, StateDisputed, "", "")
	require.NoError(t, err)
	drop, err := l.Transition(d.EntryID, StateWrittenOff, "", "")
	require.NoError(t, err)
	assert.Equal(t, StateWrittenOff, drop.State)
	// disputed never settles directly.
	_, err = l.Transition(d.EntryID, StateSettled, "", "")
	require.Error(t, err)
}

func TestLedgerBalances(t *testing.T) {
	l, err := OpenLedger("")
	require.NoError(t, err)

	// payable: 0.012 accrued + 0.5 settled; receivable: 0.25 accrued +
	// 0.1 written off (excluded) + 0.3 settled.
	mustRecordT140(t, l, "peer-a", DirectionPayable, "t1", "credits", "0.012")
	s := mustRecordT140(t, l, "peer-a", DirectionPayable, "t2", "credits", "0.5")
	_, err = l.Transition(s.EntryID, StateSettled, "settle-1", "")
	require.NoError(t, err)
	mustRecordT140(t, l, "peer-a", DirectionReceivable, "t3", "credits", "0.25")
	d := mustRecordT140(t, l, "peer-a", DirectionReceivable, "t4", "credits", "0.1")
	_, err = l.Transition(d.EntryID, StateDisputed, "", "")
	require.NoError(t, err)
	_, err = l.Transition(d.EntryID, StateWrittenOff, "", "")
	require.NoError(t, err)
	rs := mustRecordT140(t, l, "peer-a", DirectionReceivable, "t5", "credits", "0.3")
	_, err = l.Transition(rs.EntryID, StateSettled, "settle-2", "")
	require.NoError(t, err)
	// Different unit is a separate balance row.
	mustRecordT140(t, l, "peer-a", DirectionPayable, "t6", "usdc", "7")

	bal := l.Balance("peer-a")
	require.Len(t, bal, 2)
	var credits *UnitBalance
	for i := range bal {
		if bal[i].Unit == "credits" {
			credits = &bal[i]
		}
	}
	require.NotNil(t, credits)
	assert.Equal(t, "0.012", credits.PayableAccrued)
	assert.Equal(t, "0.5", credits.PayableSettled)
	assert.Equal(t, "0.25", credits.ReceivableAccrued)
	assert.Equal(t, "0.3", credits.ReceivableSettled)

	all := l.Balances()
	require.Contains(t, all, "peer-a")
	assert.Empty(t, l.Balance("peer-nobody"))
}

func mustRecordT140(t *testing.T, l *Ledger, peerID string, dir Direction, taskID, unit, amount string) *Entry {
	t.Helper()
	e, err := l.Record(Entry{
		PeerID: peerID, Direction: dir, TaskID: taskID,
		Unit: unit, Amount: amount,
	})
	require.NoError(t, err)
	return e
}

func TestLedgerCommittedSince(t *testing.T) {
	l, err := OpenLedger("")
	require.NoError(t, err)

	old := mustRecordT140(t, l, "peer-a", DirectionPayable, "old", "credits", "9")
	old.TS = time.Now().Add(-48 * time.Hour)
	l.entries[old.EntryID].TS = old.TS // age the in-memory view; file keeps write order

	mustRecordT140(t, l, "peer-a", DirectionPayable, "new1", "credits", "0.5")
	mustRecordT140(t, l, "peer-a", DirectionReceivable, "new2", "credits", "0.7")
	wo := mustRecordT140(t, l, "peer-a", DirectionPayable, "wo", "credits", "0.4")
	_, err = l.Transition(wo.EntryID, StateDisputed, "", "")
	require.NoError(t, err)
	_, err = l.Transition(wo.EntryID, StateWrittenOff, "", "")
	require.NoError(t, err)
	mustRecordT140(t, l, "peer-b", DirectionPayable, "other-peer", "credits", "0.9")

	since := time.Now().Add(-24 * time.Hour)
	committed := l.CommittedSince("peer-a", since)
	// new1 only: old is outside the window, new2 is receivable, wo is
	// written off, peer-b's payable doesn't count against peer-a.
	assert.Equal(t, map[string]string{"credits": "0.5"}, committed)
}

func TestLedgerRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerFileName)
	l, err := OpenLedger(path)
	require.NoError(t, err)
	l.maxSize = 200 // tiny rotation budget

	for i := 0; i < 20; i++ {
		_, err := l.Record(Entry{
			PeerID: "peer-a", Direction: DirectionPayable,
			TaskID: strings.Repeat("t", 8) + string(rune('a'+i)),
			Unit:   "credits", Amount: "0.001",
		})
		require.NoError(t, err)
	}
	assert.FileExists(t, path+".1")

	// The in-memory view is unaffected by rotation; a fresh open sees the
	// live file's records.
	l2, err := OpenLedger(path)
	require.NoError(t, err)
	assert.NotEmpty(t, l2.Entries())
}

func TestLedgerCorruptTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LedgerFileName)
	l, err := OpenLedger(path)
	require.NoError(t, err)
	_, err = l.Record(testEntry("peer-a"))
	require.NoError(t, err)

	// Torn write at the tail.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("{\"entry_id\":\"entry-bad\",\"peer_id\"")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	ents, err := ReadEntries(path)
	require.NoError(t, err)
	assert.Len(t, ents, 1, "corrupt tail line is skipped")
}

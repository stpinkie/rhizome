package econ

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Track 143 — the in-flight journal is what makes an interrupted web3
// close-out resumable instead of a double-pay risk: a live record owns
// (peer, unit) until it reaches a terminal state, and the pending-queue
// id / signed raw / tx hash are journaled before the side effect.

func t143Offer(issuer, peerID string) *SettleOffer {
	return &SettleOffer{
		Protocol: "econ/1.0.0",
		Issuer:   issuer, PeerID: peerID,
		Unit: "USDC", Entries: []string{"task:task-1"},
		Total: "2.5", Cutoff: time.Now().UTC(),
		Nonce: "abcd", TS: time.Now().UTC(),
		Backend: "web3",
		Payment: &SettlePayment{
			ChainID: "31337", Address: "0x2222222222222222222222222222222222222222",
			Asset: "0x1111111111111111111111111111111111111111",
			From:  "0x3333333333333333333333333333333333333333",
		},
	}
}

func TestInflightAcquireDedupsLiveRecord(t *testing.T) {
	j := OpenInflightJournal(filepath.Join(t.TempDir(), "inflight.json"))
	offer := t143Offer("issuerA", "peerB")

	rec, owned, err := j.Acquire("peerB", "USDC", offer, offer.Payment)
	require.NoError(t, err)
	require.True(t, owned)
	assert.Equal(t, SettleOffered, rec.State)
	assert.Equal(t, "peerB|USDC", rec.Key)

	// A second acquire returns the same live record, unowned — the
	// caller must resume it, not submit a second payment. The journaled
	// offer wins over whatever the new caller built.
	other := t143Offer("issuerA", "peerB")
	other.Nonce = "DIFFERENT"
	rec2, owned2, err := j.Acquire("peerB", "USDC", other, nil)
	require.NoError(t, err)
	assert.False(t, owned2)
	assert.Equal(t, "abcd", rec2.Offer.Nonce, "live record keeps the journaled offer")
}

func TestInflightTerminalAllowsReacquire(t *testing.T) {
	j := OpenInflightJournal(filepath.Join(t.TempDir(), "inflight.json"))
	require.NoError(t, func() error {
		_, owned, err := j.Acquire("peerB", "USDC", t143Offer("issuerA", "peerB"), nil)
		if !owned {
			t.Fatal("first acquire must own")
		}
		return err
	}())
	require.NoError(t, j.Update("peerB", "USDC", func(r *InflightRecord) {
		r.State = SettleSettled
	}))

	live, err := j.Live()
	require.NoError(t, err)
	assert.Empty(t, live, "settled is terminal — nothing live")

	_, owned, err := j.Acquire("peerB", "USDC", t143Offer("issuerA", "peerB"), nil)
	require.NoError(t, err)
	assert.True(t, owned, "a terminal record must not block a fresh close-out")
}

func TestInflightPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inflight.json")
	j := OpenInflightJournal(path)
	offer := t143Offer("issuerA", "peerB")
	_, owned, err := j.Acquire("peerB", "USDC", offer, offer.Payment)
	require.NoError(t, err)
	require.True(t, owned)
	require.NoError(t, j.Update("peerB", "USDC", func(r *InflightRecord) {
		r.State = SettleAwaitingApproval
		r.PendingID = "0123456789abcdef"
	}))

	// Reopen (restart): the live record is intact with its pending id —
	// this is what prevents a second transfer after a crash.
	j2 := OpenInflightJournal(path)
	live, err := j2.Live()
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, SettleAwaitingApproval, live[0].State)
	assert.Equal(t, "0123456789abcdef", live[0].PendingID)
	require.NotNil(t, live[0].Payment)
	assert.Equal(t, "0x2222222222222222222222222222222222222222", live[0].Payment.Address)
}

func TestInflightReapDropsOldTerminal(t *testing.T) {
	j := OpenInflightJournal(filepath.Join(t.TempDir(), "inflight.json"))
	_, owned, err := j.Acquire("peerB", "USDC", t143Offer("issuerA", "peerB"), nil)
	require.NoError(t, err)
	require.True(t, owned)
	require.NoError(t, j.Update("peerB", "USDC", func(r *InflightRecord) {
		r.State = SettleRejected
	}))
	// A fresh terminal record survives reap...
	require.NoError(t, j.Reap())
	all, err := j.List()
	require.NoError(t, err)
	require.Len(t, all, 1)
	// ...but a journal read one reap-age later drops it.
	j.now = func() time.Time { return time.Now().UTC().Add(journalReapAge + time.Hour) }
	require.NoError(t, j.Reap())
	all, err = j.List()
	require.NoError(t, err)
	assert.Empty(t, all, "aged terminal records are reaped")
}

func TestInflightMintPendingIDShape(t *testing.T) {
	j := OpenInflightJournal("")
	id, err := j.MintPendingID()
	require.NoError(t, err)
	assert.Len(t, id, 16)
	id2, err := j.MintPendingID()
	require.NoError(t, err)
	assert.NotEqual(t, id, id2)
}

func TestInflightRedactedStripsSignedRaw(t *testing.T) {
	rec := &InflightRecord{
		Key: "peerB|USDC", PeerID: "peerB", Unit: "USDC",
		State:     SettleBroadcast,
		Offer:     t143Offer("issuerA", "peerB"),
		TxHash:    "0xabc",
		SignedRaw: "0xdeadbeef",
	}
	out := rec.Redacted()
	require.NotNil(t, out)
	assert.Empty(t, out.SignedRaw, "signed raw is crash-resume material, not display data")
	assert.Equal(t, "0xabc", out.TxHash)
	assert.Equal(t, SettleBroadcast, out.State)
	// The source record keeps its resume material untouched.
	assert.Equal(t, "0xdeadbeef", rec.SignedRaw)
	assert.Nil(t, (*InflightRecord)(nil).Redacted())
}

func TestInflightUpdateByKeyReachesDesyncedRecord(t *testing.T) {
	j := OpenInflightJournal(filepath.Join(t.TempDir(), "inflight.json"))
	offer := t143Offer("issuerA", "peerB")
	rec, owned, err := j.Acquire("peerB", "USDC", offer, offer.Payment)
	require.NoError(t, err)
	require.True(t, owned)

	// Corrupt the peer field: the derived-key Update can no longer
	// reach the record (it looks up peerX|USDC), so without a key-
	// direct path the desynced record would stay live forever.
	require.NoError(t, j.Update("peerB", "USDC", func(r *InflightRecord) {
		r.PeerID = "peerX"
	}))
	require.Error(t, j.Update("peerX", "USDC", func(r *InflightRecord) {}),
		"derived key no longer matches the stored journal key")

	require.NoError(t, j.UpdateByKey(rec.Key, func(r *InflightRecord) {
		r.State = SettleFailed
		r.Error = "desynced"
	}))
	live, err := j.Live()
	require.NoError(t, err)
	assert.Empty(t, live, "UpdateByKey marks a desynced record terminal")
	require.Error(t, j.UpdateByKey(rec.Key, func(r *InflightRecord) {}),
		"terminal records are immutable")
}

// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package mesh

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scoreTestPeer(t *testing.T) peer.ID {
	t.Helper()
	pid, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)
	return pid
}

func TestRecordOutcomeCounters(t *testing.T) {
	s := NewPeerScoreStore()
	pid := scoreTestPeer(t)

	s.RecordOutcome(pid, OpMarketBuy, OutcomeCompleted, "sess-1", "vh1")
	s.RecordOutcome(pid, OpMarketBuy, OutcomeFailed, "sess-2", "vh2")
	s.RecordOutcome(pid, OpMarketSell, OutcomeResolvedForPeer, "sess-3", "vh3")
	// Neutral outcomes land in Outcomes but move no counters.
	s.RecordOutcome(pid, OpMarketBuy, OutcomeDisputed, "sess-4", "vh4")
	s.RecordOutcome(pid, OpMarketBuy, OutcomeResolved, "sess-5", "vh5")
	s.RecordOutcome(pid, OpMarketSell, OutcomeRefunded, "sess-6", "vh6")
	s.RecordOutcome(pid, OpMarketSell, OutcomeExpired, "sess-7", "vh7")

	sc, ok := s.Get(pid)
	require.True(t, ok)
	assert.Equal(t, 2, sc.Successes) // completed + resolved_for_peer
	assert.Equal(t, 1, sc.Failures)  // failed

	buy := sc.OpStats[OpMarketBuy]
	assert.Equal(t, 1, buy.Successes)
	assert.Equal(t, 1, buy.Failures)
	sell := sc.OpStats[OpMarketSell]
	assert.Equal(t, 1, sell.Successes)
	assert.Equal(t, 0, sell.Failures)

	require.Len(t, sc.Outcomes, 7)
	assert.Equal(t, OutcomeDisputed, sc.Outcomes[3].Outcome)
	assert.Equal(t, "sess-4", sc.Outcomes[3].SessionID)
	assert.Equal(t, "vh4", sc.Outcomes[3].ValueHash)
	assert.NotZero(t, sc.Outcomes[0].At)
}

func TestRecordOutcomeBoundsRefs(t *testing.T) {
	s := NewPeerScoreStore()
	pid := scoreTestPeer(t)

	for i := 0; i < maxPeerOutcomes+5; i++ {
		s.RecordOutcome(pid, OpMarketBuy, OutcomeCompleted, "s", "v")
	}
	sc, ok := s.Get(pid)
	require.True(t, ok)
	assert.Len(t, sc.Outcomes, maxPeerOutcomes)
}

func TestRecordOutcomeRoundTripsDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scores.json")
	s := NewPeerScoreStoreWithPath(path)
	pid := scoreTestPeer(t)
	s.RecordOutcome(pid, OpMarketBuy, OutcomeCompleted, "sess-1", "abcd1234")
	s.Close()

	s2 := NewPeerScoreStoreWithPath(path)
	require.NoError(t, s2.Load())
	sc, ok := s2.Get(pid)
	require.True(t, ok)
	require.Len(t, sc.Outcomes, 1)
	assert.Equal(t, OutcomeCompleted, sc.Outcomes[0].Outcome)
	assert.Equal(t, "abcd1234", sc.Outcomes[0].ValueHash)
}

func TestMeshRecordPeerOutcomeValidation(t *testing.T) {
	pid := scoreTestPeer(t)

	// Nil mesh refuses.
	var mNil *Mesh
	require.Error(t, mNil.RecordPeerOutcome(pid, OpMarketBuy, OutcomeCompleted, "s", "v"))

	m := &Mesh{scoreStore: NewPeerScoreStore()}

	require.Error(t, m.RecordPeerOutcome(pid, "not_a_market_op", OutcomeCompleted, "s", "v"))
	require.Error(t, m.RecordPeerOutcome(pid, OpMarketBuy, "not_an_outcome", "s", "v"))
	require.Error(t, m.RecordPeerOutcome(
		pid, OpMarketBuy, OutcomeCompleted, strings.Repeat("s", maxOutcomeRefLen+1), "v"))
	require.Error(t, m.RecordPeerOutcome(
		pid, OpMarketBuy, OutcomeCompleted, "s", strings.Repeat("v", maxOutcomeValueHashLn+1)))

	require.NoError(t, m.RecordPeerOutcome(pid, OpMarketBuy, OutcomeCompleted, "s", "v"))
	sc, ok := m.scoreStore.Get(pid)
	require.True(t, ok)
	require.Len(t, sc.Outcomes, 1)
}

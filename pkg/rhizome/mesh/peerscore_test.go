package mesh

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerScoreStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mesh-peer-scores.json")
	s := NewPeerScoreStoreWithPath(path)

	pid, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	s.Record(pid, true, 100*time.Millisecond, nil)
	s.Record(pid, true, 50*time.Millisecond, nil)
	s.Record(pid, false, 200*time.Millisecond, errors.New("timeout"))

	require.FileExists(t, path)

	s2 := NewPeerScoreStoreWithPath(path)
	require.NoError(t, s2.Load())

	sc, ok := s2.Get(pid)
	require.True(t, ok)
	assert.Equal(t, 2, sc.Successes)
	assert.Equal(t, 1, sc.Failures)
	assert.NotZero(t, sc.AvgLatency)
	assert.Equal(t, "timeout", sc.LastError)
}

func TestPeerScoreStoreScore(t *testing.T) {
	s := NewPeerScoreStore()
	pid, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	good, err := peer.Decode("12D3KooWGRcjvRUBXU3bJvCKkQvR5ME7zByZNddT5d5nhCFoHVDx")
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		s.Record(pid, false, 5*time.Second, errors.New("slow"))
		s.Record(good, true, 10*time.Millisecond, nil)
	}

	assert.Less(t, s.scores[pid].Score(), s.scores[good].Score())
	ranked := s.Ranked()
	require.Len(t, ranked, 2)
	assert.Equal(t, good, ranked[0])
}

func TestPeerScoreStoreRecordDecay(t *testing.T) {
	s := NewPeerScoreStore()
	pid, err := peer.Decode("12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv")
	require.NoError(t, err)

	for i := 0; i < maxScoreSamples*2; i++ {
		s.Record(pid, true, time.Millisecond, nil)
	}

	sc, ok := s.Get(pid)
	require.True(t, ok)
	assert.LessOrEqual(t, sc.Successes, maxScoreSamples)
	assert.LessOrEqual(t, sc.Successes+sc.Failures, maxScoreSamples+1)
}

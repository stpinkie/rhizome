package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
)

// TestSyncerStatusPersistence exercises lastSyncError, the persisted
// sync-status.json, and LoadSyncStatus — all without a network node.
func TestSyncerStatusPersistence(t *testing.T) {
	ws := newTestWorkspace(t)

	s, err := NewSyncer(context.Background(), Config{
		Workspace:        ws,
		NodeName:         "solo",
		AutoSync:         false,
		CommitInterval:   time.Hour,
		AnnounceInterval: time.Hour,
	})
	require.NoError(t, err)

	// Nothing persisted yet.
	st, err := LoadSyncStatus(ws)
	require.NoError(t, err)
	assert.Empty(t, st.LastSyncError)
	assert.Empty(t, st.PeerHeads)

	// Record a peer head and an error; both are persisted to
	// <workspace>/../sync-status.json.
	id, _, err := identity.FromMnemonic(syncTestMnemonic, 60)
	require.NoError(t, err)
	pid, err := peer.Decode(id.PeerID)
	require.NoError(t, err)

	head := testHash(0x77)
	_, changed := s.setPeerHead(pid, head)
	require.True(t, changed)
	s.setLastSyncError(errors.New("fetch broke"), pid)

	st, err = LoadSyncStatus(ws)
	require.NoError(t, err)
	assert.Equal(t, "fetch broke", st.LastSyncError)
	assert.False(t, st.LastErrorTime.IsZero())
	assert.Equal(t, head.String(), st.PeerHeads[pid.String()])

	// The in-memory accessor agrees.
	last := s.LastSyncError()
	assert.Equal(t, "fetch broke", last.Message)
	assert.False(t, last.Time.IsZero())

	// A repeated identical head is a no-op.
	_, changed = s.setPeerHead(pid, head)
	assert.False(t, changed)
}

// TestFetchRetryDelayBounds verifies the exponential backoff grows, is capped,
// and stays within the documented jitter envelope.
func TestFetchRetryDelayBounds(t *testing.T) {
	s := &Syncer{} // timeouts nil → defaults (fetchRetry = 300ms)

	for i := 0; i < 50; i++ {
		d0 := s.fetchRetryDelay(0)
		assert.GreaterOrEqual(t, d0, 200*time.Millisecond)
		assert.LessOrEqual(t, d0, 400*time.Millisecond)

		// 300ms * 2^5 = 9.6s base → jitter up to ~11.6s.
		d5 := s.fetchRetryDelay(5)
		assert.GreaterOrEqual(t, d5, 5*time.Second)
		assert.LessOrEqual(t, d5, 13*time.Second)

		// The 10s cap applies → jittered max ~12s.
		d50 := s.fetchRetryDelay(50)
		assert.LessOrEqual(t, d50, 13*time.Second)
	}
}

// TestPeerHeadsCopy verifies PeerHeads returns a copy keyed by peer id string.
func TestPeerHeadsCopy(t *testing.T) {
	ws := newTestWorkspace(t)
	s, err := NewSyncer(context.Background(), Config{
		Workspace:        ws,
		NodeName:         "solo",
		AutoSync:         false,
		CommitInterval:   time.Hour,
		AnnounceInterval: time.Hour,
	})
	require.NoError(t, err)

	id, _, err := identity.FromMnemonic(syncTestMnemonic, 61)
	require.NoError(t, err)
	pid, err := peer.Decode(id.PeerID)
	require.NoError(t, err)

	head := testHash(0x99)
	_, _ = s.setPeerHead(pid, head)

	heads := s.PeerHeads()
	assert.Equal(t, head.String(), heads[pid.String()])

	// Mutating the returned map must not affect internal state.
	heads[pid.String()] = plumbing.ZeroHash.String()
	assert.Equal(t, head.String(), s.PeerHeads()[pid.String()])
}

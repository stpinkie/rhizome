package swarm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSaveConcurrentKeepsRoster proves concurrent saves always leave a valid
// swarms.json behind. Before saveMu, every save wrote the same fixed temp
// path: one saver could rename it into place and the next would delete that
// destination, then fail to rename its own already-consumed temp file —
// leaving no roster on disk at all. Announce acks, pings, and expiry all
// persist from their own goroutines, so this interleaving is reachable.
func TestSaveConcurrentKeepsRoster(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "swarms.json")

	s := &Swarm{path: path, swarms: map[string]*swarmState{
		"ops": {ID: "ops", Joined: true, JoinedAt: time.Now(), Members: map[string]Member{
			"peer-a": {PeerID: "peer-a", LastSeen: time.Now(), Source: "direct"},
		}},
		"dev": {ID: "dev", Joined: true, JoinedAt: time.Now(), Members: map[string]Member{
			"peer-b": {PeerID: "peer-b", LastSeen: time.Now(), Source: "gossip"},
		}},
	}}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.save()
		}()
	}
	wg.Wait()

	// The roster must exist and be reloadable.
	require.FileExists(t, path, "concurrent saves must leave swarms.json in place")
	reloaded := &Swarm{path: path, swarms: make(map[string]*swarmState)}
	reloaded.load()
	assert.NotEmpty(t, reloaded.Members("ops"))
	assert.NotEmpty(t, reloaded.Members("dev"))

	// No temp files may be left behind.
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "save must not leave temp files behind")
	}
}

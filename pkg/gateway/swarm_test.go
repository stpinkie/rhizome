package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	swarm "github.com/stpinkie/rhizome/pkg/rhizome/swarm"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

func TestParseSubtasksPassesNewFields(t *testing.T) {
	raw := `[
		{"id":"a","agent_id":"main","task":"first","model":"llama3",
		 "tools":["web_search"],"timeout":"2m",
		 "requires":{"models":["llama3"],"skills":["search"]}},
		{"id":"b","agent_id":"writer","task":"second","depends_on":["a"]}
	]`
	subs := parseSubtasks(raw)
	require.Len(t, subs, 2)

	assert.Equal(t, "llama3", subs[0].Model)
	assert.Equal(t, []string{"web_search"}, subs[0].Tools)
	assert.Equal(t, 2*time.Minute, subs[0].Timeout)
	require.NotNil(t, subs[0].Requires)
	assert.Equal(t, []string{"llama3"}, subs[0].Requires.Models)
	assert.Equal(t, []string{"search"}, subs[0].Requires.Skills)

	// Sparse subtasks still parse; unknown-but-wellformed extras are ignored.
	assert.Equal(t, "writer", subs[1].AgentID)
	assert.Equal(t, []string{"a"}, subs[1].DependsOn)
	assert.Nil(t, subs[1].Requires)
	assert.Zero(t, subs[1].Timeout)
}

// TestSwarmHealthServesDoctorReport verifies the dashboard's resource-
// oriented /health alias dispatches to the same doctor report as /doctor.
func TestSwarmHealthServesDoctorReport(t *testing.T) {
	ctx := context.Background()
	id := testutil.NewIdentity(t)
	node, err := rnet.NewNode(ctx, id.Libp2pPrivKey, rnet.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { node.Close() })

	sw := swarm.New(node, id, config.SwarmConfig{Enabled: true},
		func(peer.ID) bool { return true }, nil, t.TempDir())
	require.NoError(t, sw.Join(ctx, "ops"))

	SetSwarm(sw)
	t.Cleanup(func() { SetSwarm(nil) })

	h := &networkSwarmsHandler{authToken: testTasksToken}
	for _, sub := range []string{"doctor", "health"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authedRequest(http.MethodGet,
			"/network/swarms/ops/"+sub, nil))
		require.Equal(t, http.StatusOK, rec.Code, "sub=%s", sub)

		var report struct {
			SwarmID string `json:"swarm_id"`
			Queried int    `json:"queried"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
		assert.Equal(t, "ops", report.SwarmID, "sub=%s", sub)
	}
}

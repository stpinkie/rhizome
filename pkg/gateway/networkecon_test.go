package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// newEconMesh builds a mesh with a live ledger under a temp RHIZOME_HOME.
func newEconMesh(t *testing.T) (*mesh.Mesh, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	id := testutil.NewIdentity(t)
	node, err := rnet.NewNode(context.Background(), id.Libp2pPrivKey, rnet.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = node.Close() })
	cfg := config.DefaultMeshConfig()
	cfg.Enabled = true
	cfg.AuditLog = false
	cfg.ActivityLog = false
	m := mesh.NewMesh(node, nil, id, cfg, nil)
	require.NotNil(t, m.EconomyLedger())
	return m, home
}

func seedEconEntry(t *testing.T, m *mesh.Mesh, peerID, taskID, amount string) econ.Entry {
	t.Helper()
	rec, err := m.EconomyLedger().Record(econ.Entry{
		PeerID:    peerID,
		Direction: econ.DirectionPayable,
		TaskID:    taskID,
		Unit:      "credits",
		Amount:    amount,
		State:     econ.StateAccrued,
		TS:        time.Now().UTC(),
	})
	require.NoError(t, err)
	return *rec
}

func TestNetworkEconomyAuthAndRoute(t *testing.T) {
	h := newNetworkEconomyHandler(&mesh.Mesh{}, testTasksToken)

	// No token → 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/network/economy/dispute",
		strings.NewReader(`{"task_id":"x"}`)))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// GET → 405.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/economy/dispute", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	// Unknown sub-resource → 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/economy/bogus",
		strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Nil mesh → 503.
	h2 := newNetworkEconomyHandler(nil, testTasksToken)
	rec = httptest.NewRecorder()
	h2.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/economy/dispute",
		strings.NewReader(`{"task_id":"x"}`)))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestNetworkEconomyDisputeResolveFlow(t *testing.T) {
	m, _ := newEconMesh(t)
	peerID := testutil.NewIdentity(t).PeerID
	rec := seedEconEntry(t, m, peerID, "task-gw1", "0.5")
	h := newNetworkEconomyHandler(m, testTasksToken)

	// Dispute.
	r := httptest.NewRecorder()
	h.ServeHTTP(r, authedRequest(http.MethodPost, "/network/economy/dispute",
		strings.NewReader(`{"task_id":"task-gw1","reason":"double billed"}`)))
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	got, ok := m.EconomyLedger().Get(rec.EntryID)
	require.True(t, ok)
	assert.Equal(t, econ.StateDisputed, got.State)
	assert.Equal(t, "double billed", got.Note)

	// Resolve credit → accrued.
	r = httptest.NewRecorder()
	h.ServeHTTP(r, authedRequest(http.MethodPost, "/network/economy/resolve",
		strings.NewReader(`{"task_id":"task-gw1","action":"credit"}`)))
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	got, _ = m.EconomyLedger().Get(rec.EntryID)
	assert.Equal(t, econ.StateAccrued, got.State)

	// Dispute again, resolve drop → written_off.
	r = httptest.NewRecorder()
	h.ServeHTTP(r, authedRequest(http.MethodPost, "/network/economy/dispute",
		strings.NewReader(`{"task_id":"task-gw1"}`)))
	require.Equal(t, http.StatusOK, r.Code)
	r = httptest.NewRecorder()
	h.ServeHTTP(r, authedRequest(http.MethodPost, "/network/economy/resolve",
		strings.NewReader(`{"task_id":"task-gw1","action":"drop"}`)))
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	got, _ = m.EconomyLedger().Get(rec.EntryID)
	assert.Equal(t, econ.StateWrittenOff, got.State)
}

func TestNetworkEconomyHandlerValidation(t *testing.T) {
	m, _ := newEconMesh(t)
	h := newNetworkEconomyHandler(m, testTasksToken)

	cases := []struct {
		name   string
		target string
		body   string
		want   int
	}{
		{"dispute missing task", "/network/economy/dispute", `{"reason":"x"}`, http.StatusBadRequest},
		{"resolve bad action", "/network/economy/resolve", `{"task_id":"t","action":"bogus"}`, http.StatusBadRequest},
		{"settle missing peer", "/network/economy/settle", `{"mark_only":true}`, http.StatusBadRequest},
		{"settle bad peer", "/network/economy/settle", `{"peer":"nope","mark_only":true}`, http.StatusBadRequest},
		{
			"settle bad backend",
			"/network/economy/settle",
			`{"peer":"x","backend":"fiat","mark_only":true}`,
			http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, authedRequest(http.MethodPost, tc.target,
				strings.NewReader(tc.body)))
			assert.Equal(t, tc.want, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

func TestNetworkEconomySettleMarkOnly(t *testing.T) {
	m, _ := newEconMesh(t)
	payee := testutil.NewIdentity(t).PeerID
	seedEconEntry(t, m, payee, "task-s1", "0.5")
	seedEconEntry(t, m, payee, "task-s2", "0.25")
	h := newNetworkEconomyHandler(m, testTasksToken)

	// Handshake path → 501 until Track 142.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/economy/settle",
		strings.NewReader(`{"peer":"`+payee+`"}`)))
	assert.Equal(t, http.StatusNotImplemented, rec.Code)

	// web3 backend → 501 until Track 143.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/economy/settle",
		strings.NewReader(`{"peer":"`+payee+`","backend":"web3","mark_only":true}`)))
	assert.Equal(t, http.StatusNotImplemented, rec.Code)

	// mark_only settles the accrued payables under a signed offer id.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/economy/settle",
		strings.NewReader(`{"peer":"`+payee+`","mark_only":true}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		SettleID string       `json:"settle_id"`
		Entries  []econ.Entry `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, strings.HasPrefix(resp.SettleID, "sha256:"))
	require.Len(t, resp.Entries, 2)
	for _, e := range resp.Entries {
		assert.Equal(t, econ.StateSettled, e.State)
		assert.Equal(t, econ.MarkOnlySettleTX, e.SettleTX)
	}
}

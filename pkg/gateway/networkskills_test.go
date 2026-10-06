package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
	"github.com/stpinkie/rhizome/pkg/skills"
)

func TestNetworkSkillsHandlerPushValidation(t *testing.T) {
	h := newNetworkSkillsHandler(&mesh.Mesh{}, testTasksToken)

	cases := []struct {
		name   string
		method string
		target string
		body   string
		want   int
	}{
		{"get push method", http.MethodGet, "/network/skills/push", "", http.StatusMethodNotAllowed},
		{"bad json", http.MethodPost, "/network/skills/push", "{", http.StatusBadRequest},
		{
			"bad peer", http.MethodPost, "/network/skills/push",
			`{"peer":"nope","name":"x"}`, http.StatusBadRequest,
		},
		{
			"missing name", http.MethodPost, "/network/skills/push",
			`{"peer":"12D3KooWGRcjvRUBXU3bJvCKkQvR5ME7zByZNddT5d5nhCFoHVDx"}`, http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != "" {
				req = authedRequest(tc.method, tc.target, strings.NewReader(tc.body))
			} else {
				req = authedRequest(tc.method, tc.target, nil)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/network/skills/push",
		strings.NewReader(`{"peer":"x","name":"y"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestNetworkSkillsHandlerPushOffers runs the full offer flow over HTTP:
// the daemon mesh offers a shared skill to a peer mesh, which pulls and
// installs it asynchronously.
func TestNetworkSkillsHandlerPushOffers(t *testing.T) {
	ctx := context.Background()

	idA := testutil.NewIdentity(t)
	idB := testutil.NewIdentity(t)

	nodeA, err := rnet.NewNode(ctx, idA.Libp2pPrivKey, rnet.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeA.Close()
	nodeB, err := rnet.NewNode(ctx, idB.Libp2pPrivKey, rnet.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    []string{nodeA.BootstrapAddrs()[0]},
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	require.NoError(t, err)
	defer nodeB.Close()

	// A shares demo-skill; B installs into an empty global root.
	globalA := t.TempDir()
	skillDir := filepath.Join(globalA, "demo-skill")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: demo-skill\ndescription: demo\n---\n# Demo\n"), 0o644))

	cfgA := config.DefaultMeshConfig()
	cfgA.Enabled = true
	cfgA.AuditLog = false
	cfgA.SkillShare = []string{"demo-skill"}
	meshA := mesh.NewMesh(nodeA, nil, idA, cfgA, nil)
	meshA.SetBlobDir(t.TempDir())
	meshA.SetSkillsLoader(skills.NewSkillsLoader(t.TempDir(), globalA, ""))
	require.NoError(t, meshA.Start(ctx))
	defer meshA.Stop()

	globalB := t.TempDir()
	cfgB := config.DefaultMeshConfig()
	cfgB.Enabled = true
	cfgB.AuditLog = false
	meshB := mesh.NewMesh(nodeB, nil, idB, cfgB, nil)
	meshB.SetBlobDir(t.TempDir())
	meshB.SetSkillsLoader(skills.NewSkillsLoader(t.TempDir(), globalB, ""))
	require.NoError(t, meshB.Start(ctx))
	defer meshB.Stop()

	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	h := newNetworkSkillsHandler(meshA, testTasksToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/skills/push",
		strings.NewReader(`{"peer":"`+nodeB.PeerID()+`","name":"demo-skill"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	assert.Contains(t, rec.Body.String(), "offered")

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(globalB, "demo-skill", "SKILL.md"))
		return err == nil
	}, 15*time.Second, 100*time.Millisecond)
}

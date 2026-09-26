package market

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
)

// setupHome points RHIZOME_HOME at a temp dir and writes a minimal
// config.json so LoadConfig succeeds.
func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "config.json"), []byte(`{}`), 0o600))
	return home
}

func moduleDir(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, "modules", marketModuleID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	return dir
}

func TestResolveClientNotInstalled(t *testing.T) {
	setupHome(t)
	_, err := resolveClient()
	require.ErrorIs(t, err, errNotInstalled)
	assert.Equal(t,
		"rhizome-market module not installed — run `rhizome module install rhizome-market`",
		err.Error())
}

func TestResolveClientInstalledNoAPI(t *testing.T) {
	home := setupHome(t)
	moduleDir(t, home) // dir exists but no api.addr — installed, not serving
	_, err := resolveClient()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "installed but its API isn't up")
	assert.Contains(t, err.Error(), "rhizome module status")
}

func TestResolveClientRejectsNonLoopback(t *testing.T) {
	home := setupHome(t)
	dir := moduleDir(t, home)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, apiAddrFile), []byte("10.0.0.5:9000\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, bridgeTokenFile), []byte("tok\n"), 0o600))
	_, err := resolveClient()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a loopback")
}

func TestMarketVerbRoundTrip(t *testing.T) {
	home := setupHome(t)

	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"offers":[]}`))
	}))
	defer srv.Close()

	dir := moduleDir(t, home)
	addr := strings.TrimPrefix(srv.URL, "http://")
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, apiAddrFile), []byte(addr+"\n"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, bridgeTokenFile), []byte("deadbeef\n"), 0o600))

	client, err := resolveClient()
	require.NoError(t, err)

	resp, code, err := client.call(t.Context(), "buy", map[string]any{
		"provider": "peer1", "offer": "web-search", "task": "summarize", "max_cost": "1.00",
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"ok":true,"offers":[]}`, string(resp))
	assert.Equal(t, "/v1/buy", gotPath)
	assert.Equal(t, "Bearer deadbeef", gotAuth)
	assert.Equal(t, map[string]any{
		"provider": "peer1", "offer": "web-search", "task": "summarize", "max_cost": "1.00",
	}, gotBody)
}

func TestMarketCommandTree(t *testing.T) {
	root := NewMarketCommand()
	assert.Equal(t, "market", root.Use)
	verbs := map[string]bool{}
	for _, c := range root.Commands() {
		verbs[strings.Fields(c.Use)[0]] = true
	}
	for _, want := range []string{"find", "buy", "session", "receipt"} {
		assert.True(t, verbs[want], "missing verb %q", want)
	}
}

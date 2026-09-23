// Rhizome - Ultra-lightweight personal agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/sigverify"
)

// stubIndexKey swaps in a throwaway Ed25519 keypair as the release trust
// root for the duration of a test. Returns the base64 seed for signing.
func stubIndexKey(t *testing.T) string {
	t.Helper()
	pub, seedB64, err := sigverify.GenerateKeypair()
	require.NoError(t, err)
	old := sigverify.ReleasePubKeyB64
	sigverify.ReleasePubKeyB64 = pub
	t.Cleanup(func() { sigverify.ReleasePubKeyB64 = old })
	return seedB64
}

func indexEntry(slug, github, version string) skillIndexEntry {
	return skillIndexEntry{
		Slug:        slug,
		DisplayName: "Display " + slug,
		Summary:     "Summary of " + slug,
		Version:     version,
		Source:      skillIndexSource{GitHub: github},
	}
}

func marshalIndex(t *testing.T, entries []skillIndexEntry) []byte {
	t.Helper()
	data, err := json.MarshalIndent(skillIndexEnvelope{
		IndexVersion: 1,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		Skills:       entries,
	}, "", "  ")
	require.NoError(t, err)
	return data
}

// indexServer serves a signed (or tampered) curated index over a loopback
// httptest server and counts index.json fetches.
func indexServer(
	t *testing.T, data, sig []byte, dropSig, tamper bool,
) (*httptest.Server, *int) {
	t.Helper()
	fetches := new(int)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			*fetches++
			if tamper {
				w.Write(append(data, ' '))
				return
			}
			w.Write(data)
		case "/index.json.sig":
			if dropSig {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(sig)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, fetches
}

func newTestRhizomeRegistry(t *testing.T, baseURL, githubBase, cacheDir string) *RhizomeRegistry {
	t.Helper()
	installer, err := NewSkillInstallerWithBaseURL("", githubBase, "", "")
	require.NoError(t, err)
	return &RhizomeRegistry{
		baseURL:   baseURL,
		installer: installer,
		client:    &http.Client{Timeout: 10 * time.Second},
		ttl:       time.Hour,
		cacheDir:  cacheDir,
	}
}

func signIndex(t *testing.T, data []byte, seedB64 string) []byte {
	t.Helper()
	sig, err := sigverify.SignRelease(data, seedB64)
	require.NoError(t, err)
	return []byte(sig)
}

func TestMarshalCuratedIndexValidates(t *testing.T) {
	data, err := MarshalCuratedIndex()
	require.NoError(t, err)

	idx, err := parseSkillIndex(data, "")
	require.NoError(t, err)
	require.NotEmpty(t, idx.entries)
	for _, e := range idx.entries {
		assert.NotEmpty(t, e.Source.GitHub, e.Slug)
		assert.NotEmpty(t, e.Summary, e.Slug)
	}
	// Canonical emit stamps generated_at.
	var env skillIndexEnvelope
	require.NoError(t, json.Unmarshal(data, &env))
	assert.NotEmpty(t, env.GeneratedAt)
}

func TestRhizomeRegistrySearchAndMeta(t *testing.T) {
	seedB64 := stubIndexKey(t)
	entries := []skillIndexEntry{
		indexEntry("summarize", "stpinkie/rhizome@main/workspace/skills/summarize", "1.0.0"),
		indexEntry("weather", "stpinkie/rhizome@main/workspace/skills/weather", "1.0.0"),
	}
	data := marshalIndex(t, entries)
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	r := newTestRhizomeRegistry(t, srv.URL, "", t.TempDir())

	results, err := r.Search(context.Background(), "summarize", 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "summarize", results[0].Slug)
	assert.Equal(t, "rhizome", results[0].RegistryName)
	assert.Equal(t, "1.0.0", results[0].Version)

	// Empty query browses the whole index.
	results, err = r.Search(context.Background(), "", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	meta, err := r.GetSkillMeta(context.Background(), "weather")
	require.NoError(t, err)
	assert.Equal(t, "weather", meta.Slug)
	assert.Equal(t, "1.0.0", meta.LatestVersion)
	assert.Equal(t, "rhizome", meta.RegistryName)

	_, err = r.GetSkillMeta(context.Background(), "nonexistent")
	assert.ErrorContains(t, err, "not in the curated index")
}

func TestRhizomeRegistryRefusesUnsignedIndex(t *testing.T) {
	seedB64 := stubIndexKey(t)
	data := marshalIndex(t, []skillIndexEntry{
		indexEntry("x", "owner/repo", "1.0.0"),
	})
	// Signature endpoint 404s — the index is unsigned.
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), true, false)

	r := newTestRhizomeRegistry(t, srv.URL, "", t.TempDir())
	_, err := r.Search(context.Background(), "x", 5)
	assert.ErrorContains(t, err, "signature fetch")
}

func TestRhizomeRegistryRefusesBadSignature(t *testing.T) {
	stubIndexKey(t)
	_, otherSeed, err := sigverify.GenerateKeypair()
	require.NoError(t, err)

	data := marshalIndex(t, []skillIndexEntry{
		indexEntry("x", "owner/repo", "1.0.0"),
	})
	// Signature produced by a different key — verification fails.
	bad := signIndex(t, data, otherSeed)
	srv, _ := indexServer(t, data, bad, false, false)

	r := newTestRhizomeRegistry(t, srv.URL, "", t.TempDir())
	_, err = r.Search(context.Background(), "x", 5)
	assert.ErrorContains(t, err, "signature does not verify")

	// A trust failure must not write the cache.
	assert.NoFileExists(t, filepath.Join(r.indexCacheDir(), "index.json"))
}

func TestRhizomeRegistryRefusesTamperedIndex(t *testing.T) {
	seedB64 := stubIndexKey(t)
	data := marshalIndex(t, []skillIndexEntry{
		indexEntry("x", "owner/repo", "1.0.0"),
	})
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, true)

	r := newTestRhizomeRegistry(t, srv.URL, "", t.TempDir())
	_, err := r.Search(context.Background(), "x", 5)
	assert.ErrorContains(t, err, "signature does not verify")
}

func TestRhizomeRegistryRefusesNewerIndexVersion(t *testing.T) {
	seedB64 := stubIndexKey(t)
	env := skillIndexEnvelope{
		IndexVersion: 99,
		Skills:       []skillIndexEntry{indexEntry("x", "owner/repo", "1.0.0")},
	}
	data, err := json.MarshalIndent(env, "", "  ")
	require.NoError(t, err)
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	r := newTestRhizomeRegistry(t, srv.URL, "", t.TempDir())
	_, err = r.Search(context.Background(), "x", 5)
	assert.ErrorContains(t, err, "unsupported index_version")
}

func TestRhizomeRegistryRefusesNonHTTPSBase(t *testing.T) {
	stubIndexKey(t)
	r := newTestRhizomeRegistry(t, "http://index.example.com", "", t.TempDir())
	_, err := r.Search(context.Background(), "x", 5)
	assert.ErrorContains(t, err, "must be HTTPS")
}

func TestRhizomeRegistryServesStaleCacheOnFetchFailure(t *testing.T) {
	seedB64 := stubIndexKey(t)
	data := marshalIndex(t, []skillIndexEntry{
		indexEntry("summarize", "owner/repo", "1.0.0"),
	})
	srv, fetches := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	cacheDir := t.TempDir()
	r := newTestRhizomeRegistry(t, srv.URL, "", cacheDir)
	r.ttl = 50 * time.Millisecond

	// First load populates cache + emits merged.
	_, err := r.Search(context.Background(), "summarize", 5)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(cacheDir, "index.json"))
	require.FileExists(t, filepath.Join(cacheDir, "index.json.sig"))
	assert.Equal(t, 1, *fetches)

	// Kill the index and expire TTL — the stale verified cache is served.
	srv.Close()
	time.Sleep(60 * time.Millisecond)
	results, err := r.Search(context.Background(), "summarize", 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "summarize", results[0].Slug)
}

func TestRhizomeRegistryNoStaleServeOnTrustFailure(t *testing.T) {
	seedB64 := stubIndexKey(t)
	data := marshalIndex(t, []skillIndexEntry{
		indexEntry("summarize", "owner/repo", "1.0.0"),
	})
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	cacheDir := t.TempDir()
	r := newTestRhizomeRegistry(t, srv.URL, "", cacheDir)
	r.ttl = 50 * time.Millisecond

	_, err := r.Search(context.Background(), "summarize", 5)
	require.NoError(t, err)

	// Server starts serving tampered bytes — signature verification fails,
	// and the (expired) verified cache must NOT be served.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/index.json" {
			w.Write(append(data, ' '))
			return
		}
		w.Write(signIndex(t, data, seedB64))
	})
	time.Sleep(60 * time.Millisecond)
	_, err = r.Search(context.Background(), "summarize", 5)
	assert.ErrorContains(t, err, "signature does not verify")
}

func TestRhizomeRegistryEmitsIndexEvents(t *testing.T) {
	seedB64 := stubIndexKey(t)
	bus := events.NewBus()
	t.Cleanup(func() { _ = bus.Close() })

	var mu sync.Mutex
	kinds := []string{}
	sub, err := bus.Channel().KindPrefix("skill.index.").Subscribe(
		context.Background(),
		events.SubscribeOptions{Name: "test", Buffer: 16},
		func(_ context.Context, evt events.Event) error {
			mu.Lock()
			kinds = append(kinds, string(evt.Kind))
			mu.Unlock()
			return nil
		},
	)
	require.NoError(t, err)
	defer func() { _ = sub.Close() }()

	data := marshalIndex(t, []skillIndexEntry{
		indexEntry("x", "owner/repo", "1.0.0"),
	})
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	r := newTestRhizomeRegistry(t, srv.URL, "", t.TempDir())
	r.SetEventBus(bus)

	_, err = r.Search(context.Background(), "x", 5)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(kinds) == 1 && kinds[0] == "skill.index.merged"
	}, 3*time.Second, 10*time.Millisecond)
}

func TestRhizomeRegistryInstallDelegatesToGitHub(t *testing.T) {
	seedB64 := stubIndexKey(t)
	var ghSrv *httptest.Server
	ghSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/repos/org/repo/contents/skills/myskill":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"type":"file","name":"SKILL.md","download_url":"` + ghSrv.URL + `/raw/org/repo/main/skills/myskill/SKILL.md"},
				{"type":"dir","name":"scripts","url":"` + ghSrv.URL + `/api/v3/repos/org/repo/contents/skills/myskill/scripts?ref=main"}
			]`))
		case "/api/v3/repos/org/repo/contents/skills/myskill/scripts":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"type":"file","name":"run.sh","download_url":"` + ghSrv.URL + `/raw/org/repo/main/skills/myskill/scripts/run.sh"}
			]`))
		case "/raw/org/repo/main/skills/myskill/SKILL.md":
			_, _ = w.Write([]byte("---\nname: myskill\ndescription: curated test skill\n---\n# My Skill\n"))
		case "/raw/org/repo/main/skills/myskill/scripts/run.sh":
			_, _ = w.Write([]byte("#!/bin/sh\nexit 0\n"))
		default:
			t.Fatalf("unexpected github path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(ghSrv.Close)

	entries := []skillIndexEntry{
		indexEntry("myskill", "org/repo@main/skills/myskill", "1.2.3"),
	}
	data := marshalIndex(t, entries)
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	r := newTestRhizomeRegistry(t, srv.URL, ghSrv.URL, t.TempDir())

	targetDir := filepath.Join(t.TempDir(), "myskill")
	res, err := r.DownloadAndInstall(context.Background(), "myskill", "", targetDir)
	require.NoError(t, err)
	assert.Equal(t, "main", res.Version)
	assert.Equal(t, "Summary of myskill", res.Summary)

	content, err := os.ReadFile(filepath.Join(targetDir, "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "name: myskill")
	require.FileExists(t, filepath.Join(targetDir, "scripts", "run.sh"))

	// Index-pinned version refuses a caller override.
	_, err = r.DownloadAndInstall(context.Background(), "myskill", "9.9.9", targetDir+"2")
	assert.ErrorContains(t, err, "pins")
}

// buildSkillTarGz renders a GitHub-style repo tarball: every member lives
// under a single top-level directory.
func buildSkillTarGz(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: top + "/", Typeflag: tar.TypeDir, Mode: 0o755,
	}))
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: top + "/" + name, Typeflag: tar.TypeReg,
			Mode: 0o644, Size: int64(len(body)),
		}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestRhizomeRegistryInstallPinnedSHA256(t *testing.T) {
	seedB64 := stubIndexKey(t)

	tarball := buildSkillTarGz(t, "repo-v123", map[string]string{
		"skills/myskill/SKILL.md":       "---\nname: myskill\n---\n# My Skill\n",
		"skills/myskill/scripts/run.sh": "#!/bin/sh\nexit 0\n",
		"skills/other/SKILL.md":         "---\nname: other\n---\n",
		"README.md":                     "top-level file not under subpath\n",
	})
	sum := sha256.Sum256(tarball)

	var ghSrv *httptest.Server
	ghSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/org/repo/archive/v1.2.3.tar.gz":
			w.Write(tarball)
		default:
			t.Fatalf("unexpected github path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(ghSrv.Close)

	entry := indexEntry("myskill", "org/repo@v1.2.3/skills/myskill", "1.2.3")
	entry.SHA256 = hex.EncodeToString(sum[:])
	data := marshalIndex(t, []skillIndexEntry{entry})
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	r := newTestRhizomeRegistry(t, srv.URL, ghSrv.URL, t.TempDir())

	targetDir := filepath.Join(t.TempDir(), "myskill")
	res, err := r.DownloadAndInstall(context.Background(), "myskill", "", targetDir)
	require.NoError(t, err)
	assert.Equal(t, "v1.2.3", res.Version)

	// Only the declared subpath lands; the rest of the repo is skipped.
	require.FileExists(t, filepath.Join(targetDir, "SKILL.md"))
	require.FileExists(t, filepath.Join(targetDir, "scripts", "run.sh"))
	assert.NoFileExists(t, filepath.Join(targetDir, "README.md"))
	assert.NoDirExists(t, filepath.Join(targetDir, "other"))
}

func TestRhizomeRegistryInstallPinnedSHA256Mismatch(t *testing.T) {
	seedB64 := stubIndexKey(t)

	tarball := buildSkillTarGz(t, "repo-v123", map[string]string{
		"skills/myskill/SKILL.md": "---\nname: myskill\n---\n",
	})

	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarball)
	}))
	t.Cleanup(ghSrv.Close)

	entry := indexEntry("myskill", "org/repo@v1.2.3/skills/myskill", "1.2.3")
	entry.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	data := marshalIndex(t, []skillIndexEntry{entry})
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	r := newTestRhizomeRegistry(t, srv.URL, ghSrv.URL, t.TempDir())
	_, err := r.DownloadAndInstall(context.Background(), "myskill", "", filepath.Join(t.TempDir(), "x"))
	assert.ErrorContains(t, err, "sha256 mismatch")
}

func TestParseCuratedGitHubSource(t *testing.T) {
	cases := []struct {
		raw        string
		wantTarget string
		wantRef    string
		wantErr    bool
	}{
		{"owner/repo", "owner/repo", "", false},
		{"owner/repo@v1.2.3", "owner/repo", "v1.2.3", false},
		{"owner/repo@main/skills/x", "owner/repo/skills/x", "main", false},
		{"owner/repo/skills/x", "owner/repo/skills/x", "", false},
		{
			"https://github.com/owner/repo/tree/main/skills/x",
			"https://github.com/owner/repo/tree/main/skills/x", "", false,
		},
		{"owner/repo@", "", "", true},
		{"owner/@v1", "", "", true},
		{"single-segment", "", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		target, ref, err := parseCuratedGitHubSource(c.raw)
		if c.wantErr {
			assert.Error(t, err, c.raw)
			continue
		}
		require.NoError(t, err, c.raw)
		assert.Equal(t, c.wantTarget, target, c.raw)
		assert.Equal(t, c.wantRef, ref, c.raw)
	}
}

func TestRhizomeRegistryProviderDecodesParams(t *testing.T) {
	provider := buildRegistryProvider("rhizome", config.SkillRegistryConfig{
		Name:      "rhizome",
		Enabled:   true,
		BaseURL:   "https://index.example.com/",
		AuthToken: *config.NewSecureString("gh-token"),
		Param: map[string]any{
			"proxy":             "http://127.0.0.1:7890",
			"cache_ttl_seconds": 300,
		},
	})
	require.NotNil(t, provider)
	registry := provider.BuildRegistry()
	require.NotNil(t, registry)
	r, ok := registry.(*RhizomeRegistry)
	require.True(t, ok)
	assert.Equal(t, "https://index.example.com", r.baseURL)
	assert.Equal(t, 300*time.Second, r.ttl)
	assert.Equal(t, "gh-token", r.installer.githubToken)
}

func TestRhizomeRegistryCompatInheritsGithubCreds(t *testing.T) {
	var skillsCfg config.SkillsToolsConfig
	skillsCfg.Github.Token = *config.NewSecureString("shared-gh-token")
	skillsCfg.Github.Proxy = "http://127.0.0.1:7890"
	skillsCfg.Registries = config.SkillsRegistriesConfig{
		&config.SkillRegistryConfig{Name: "rhizome", Enabled: true},
	}

	rhizomeConfig := func(cfg config.SkillsToolsConfig) config.SkillRegistryConfig {
		for _, rc := range effectiveRegistryConfigsFromToolsConfig(cfg) {
			if rc.Name == "rhizome" {
				return rc
			}
		}
		t.Fatal("rhizome registry config missing")
		return config.SkillRegistryConfig{}
	}

	rc := rhizomeConfig(skillsCfg)
	assert.Equal(t, "shared-gh-token", rc.AuthToken.String())
	assert.Equal(t, "http://127.0.0.1:7890", rc.Param["proxy"])
	// Index base_url is NOT overwritten by the github compat path.
	assert.Equal(t, "", rc.BaseURL)

	// An explicit auth_token wins over the github fallback.
	skillsCfg.Registries = config.SkillsRegistriesConfig{
		&config.SkillRegistryConfig{
			Name:      "rhizome",
			Enabled:   true,
			AuthToken: *config.NewSecureString("own-token"),
		},
	}
	rc = rhizomeConfig(skillsCfg)
	assert.Equal(t, "own-token", rc.AuthToken.String())
}

func TestRegistryManagerSetEventBusReachesRhizome(t *testing.T) {
	seedB64 := stubIndexKey(t)
	data := marshalIndex(t, []skillIndexEntry{
		indexEntry("x", "owner/repo", "1.0.0"),
	})
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	rm := NewRegistryManager()
	rm.AddRegistry(newTestRhizomeRegistry(t, srv.URL, "", t.TempDir()))

	bus := events.NewBus()
	t.Cleanup(func() { _ = bus.Close() })
	rm.SetEventBus(bus)

	var mu sync.Mutex
	kinds := []string{}
	sub, err := bus.Channel().KindPrefix("skill.index.").Subscribe(
		context.Background(),
		events.SubscribeOptions{Name: "test", Buffer: 8},
		func(_ context.Context, evt events.Event) error {
			mu.Lock()
			kinds = append(kinds, string(evt.Kind))
			mu.Unlock()
			return nil
		},
	)
	require.NoError(t, err)
	defer func() { _ = sub.Close() }()

	_, err = rm.GetRegistry("rhizome").Search(context.Background(), "x", 5)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(kinds) > 0
	}, 3*time.Second, 10*time.Millisecond)
}

func TestOriginKindForRegistry(t *testing.T) {
	assert.Equal(t, "curated", OriginKindForRegistry("rhizome"))
	assert.Equal(t, "third_party", OriginKindForRegistry("github"))
	assert.Equal(t, "third_party", OriginKindForRegistry("clawhub"))
}

func TestSkillURLResolvesCuratedSource(t *testing.T) {
	seedB64 := stubIndexKey(t)
	entries := []skillIndexEntry{
		indexEntry("myskill", "org/repo@v1/skills/myskill", "1.0.0"),
	}
	data := marshalIndex(t, entries)
	srv, _ := indexServer(t, data, signIndex(t, data, seedB64), false, false)

	r := newTestRhizomeRegistry(t, srv.URL, "", t.TempDir())
	// SkillURL consults the warm index — load it first via Search.
	_, err := r.Search(context.Background(), "myskill", 5)
	require.NoError(t, err)

	u := r.SkillURL("myskill", "")
	assert.Equal(t, "https://github.com/org/repo/tree/v1/skills/myskill", u)
}

// base64 sanity: the signing seed produced by stubIndexKey must decode —
// a broken seed would silently sign nothing.
func TestStubIndexKeySeedDecodes(t *testing.T) {
	seedB64 := stubIndexKey(t)
	raw, err := base64.StdEncoding.DecodeString(seedB64)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
}

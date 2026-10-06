package blob

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Resumable writer + store listing ---

func TestResumableWriterResume(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 1<<20, 0)
	content := []byte("resumable content across two attempts")
	hash := hashOf(content)

	// First attempt stages the prefix, then suspends (stream dropped).
	w, err := s.NewResumableWriter(hash, int64(len(content)))
	require.NoError(t, err)
	_, err = w.Write(content[:10])
	require.NoError(t, err)
	assert.Equal(t, int64(10), w.Staged())
	w.Suspend()
	assert.Equal(t, int64(10), s.PartialSize(hash))
	assert.NoFileExists(t, s.blobPath(hash))

	// Second attempt reopens the same partial and appends the tail.
	w2, err := s.NewResumableWriter(hash, int64(len(content)))
	require.NoError(t, err)
	assert.Equal(t, int64(10), w2.Staged())
	_, err = w2.Write(content[10:])
	require.NoError(t, err)
	sum, err := w2.Complete()
	require.NoError(t, err)
	assert.Equal(t, hash, sum)
	assert.True(t, s.Has(hash))
	assert.NoFileExists(t, s.partialPath(hash))
}

func TestResumableWriterOversizedPartial(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 1<<20, 0)
	content := []byte("short blob")
	hash := hashOf(content)

	// A stale partial larger than the announced size is restarted empty.
	require.NoError(t, os.WriteFile(s.partialPath(hash), bytes.Repeat([]byte("x"), 99), 0o600))
	w, err := s.NewResumableWriter(hash, int64(len(content)))
	require.NoError(t, err)
	assert.Equal(t, int64(0), w.Staged())
	_, err = w.Write(content)
	require.NoError(t, err)
	_, err = w.Complete()
	require.NoError(t, err)
	assert.True(t, s.Has(hash))
}

func TestStoreList(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 1<<20, time.Hour)

	content := []byte("listed blob")
	hash, err := s.Put(bytes.NewReader(content), Meta{Name: "l.txt", Owner: "p-owner"}, "")
	require.NoError(t, err)

	// An in-flight resumable transfer shows as a partial entry.
	other := []byte("partial in flight")
	ph := hashOf(other)
	w, err := s.NewResumableWriter(ph, int64(len(other)))
	require.NoError(t, err)
	_, err = w.Write(other[:6])
	require.NoError(t, err)
	w.Suspend()

	entries, err := s.List()
	require.NoError(t, err)
	require.Len(t, entries, 2)

	var full, part Entry
	for _, e := range entries {
		if e.Partial > 0 {
			part = e
		} else {
			full = e
		}
	}
	assert.Equal(t, hash, full.Hash)
	assert.Equal(t, "l.txt", full.Name)
	assert.Equal(t, "p-owner", full.Owner)
	assert.Equal(t, int64(len(content)), full.Size)
	assert.NotZero(t, full.StoredAt)
	assert.Equal(t, full.StoredAt+int64(time.Hour.Seconds()), full.ExpiresAt)

	assert.Equal(t, ph, part.Hash)
	assert.Equal(t, int64(6), part.Partial)
}

func TestReapStalePartials(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 1<<20, time.Hour)

	content := []byte("x")
	h := hashOf(content)
	stale := s.partialPath(h)
	w, err := s.NewResumableWriter(h, int64(len(content)))
	require.NoError(t, err)
	w.Suspend()
	fresh, err := os.CreateTemp(dir, ".incoming-*")
	require.NoError(t, err)
	require.NoError(t, fresh.Close())

	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))
	require.NoError(t, os.Chtimes(fresh.Name(), old, old))

	s.reap()
	assert.NoFileExists(t, stale)
	assert.NoFileExists(t, fresh.Name())
}

// --- Transport resume / dedup ---

// newResumePair wires the standard test transport pair: B serves, A calls.
func newResumePair(t *testing.T, ctx context.Context) (*Transport, *Transport, *Store, *Store) {
	t.Helper()
	hA, hB := newTestHosts(t, ctx)

	storeA := NewStore(t.TempDir(), 1<<20, 0)
	storeB := NewStore(t.TempDir(), 1<<20, 0)
	signA, _ := testSigner(hA)
	_, signRespB := testSigner(hB)

	srv := NewTransport(hB, storeB, TransportConfig{
		Authorize:     func(peer.ID, Request) error { return nil },
		SignResponse:  signRespB,
		VerifyRequest: testVerifyReq,
	})
	go func() { _ = srv.Start(ctx) }()

	cli := NewTransport(hA, storeA, TransportConfig{
		SignRequest:    signA,
		VerifyResponse: testVerifyResp,
	})
	require.Eventually(t, func() bool { return cli.Supported(ctx, hB.ID(), time.Second) },
		5*time.Second, 50*time.Millisecond)
	return cli, srv, storeA, storeB
}

func TestGetResumesFromOffset(t *testing.T) {
	ctx := context.Background()
	cli, srv, storeA, storeB := newResumePair(t, ctx)

	content := bytes.Repeat([]byte("chunk"), 20000) // ~100 KB
	_, err := storeB.Put(bytes.NewReader(content), Meta{Name: "big.bin"}, "")
	require.NoError(t, err)
	hash := hashOf(content)

	// Stage the correct prefix on the client, as a dropped get left it.
	w, err := storeA.NewResumableWriter(hash, int64(len(content)))
	require.NoError(t, err)
	_, err = w.Write(content[:40000])
	require.NoError(t, err)
	w.Suspend()

	path, meta, err := cli.Get(ctx, srv.host.ID(), hash)
	require.NoError(t, err)
	assert.Equal(t, "big.bin", meta.Name)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
	assert.Equal(t, int64(0), storeA.PartialSize(hash))
}

// TestGetResumeServerSeeks proves the offset verb round-trips: a corrupt
// staged prefix can only produce a hash mismatch when the server honored
// the offset and sent the tail (a full re-send would verify cleanly).
func TestGetResumeServerSeeks(t *testing.T) {
	ctx := context.Background()
	cli, srv, storeA, storeB := newResumePair(t, ctx)

	content := bytes.Repeat([]byte("y"), 8000)
	_, err := storeB.Put(bytes.NewReader(content), Meta{}, "")
	require.NoError(t, err)
	hash := hashOf(content)

	// Stage WRONG prefix bytes — the resumed blob must fail verification.
	w, err := storeA.NewResumableWriter(hash, int64(len(content)))
	require.NoError(t, err)
	_, err = w.Write(bytes.Repeat([]byte("z"), 4000))
	require.NoError(t, err)
	w.Suspend()

	_, _, err = cli.getOnce(ctx, srv.host.ID(), hash)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hash mismatch")
	// The failed attempt clears the corrupt partial for the retry loop.
	assert.Equal(t, int64(0), storeA.PartialSize(hash))
}

func TestPutResumesServerPartial(t *testing.T) {
	ctx := context.Background()
	cli, srv, storeA, storeB := newResumePair(t, ctx)

	content := bytes.Repeat([]byte("put-resume-"), 8000)
	src := filepath.Join(t.TempDir(), "u.bin")
	require.NoError(t, os.WriteFile(src, content, 0o600))
	hash, err := storeA.PutFile(src, Meta{Name: "u.bin"})
	require.NoError(t, err)

	// Stage the first bytes server-side, as a dropped put left them.
	w, err := storeB.NewResumableWriter(hash, int64(len(content)))
	require.NoError(t, err)
	_, err = w.Write(content[:30000])
	require.NoError(t, err)
	w.Suspend()

	// If the server ignored its staged offset the appended tail would
	// overshoot the announced size and the commit would fail.
	require.NoError(t, cli.Put(ctx, srv.host.ID(), hash))
	assert.True(t, storeB.Has(hash))
}

func TestPutDedupSkipsStreaming(t *testing.T) {
	ctx := context.Background()
	cli, srv, storeA, storeB := newResumePair(t, ctx)

	content := bytes.Repeat([]byte("dup"), 5000)
	src := filepath.Join(t.TempDir(), "d.bin")
	require.NoError(t, os.WriteFile(src, content, 0o600))
	hash, err := storeA.PutFile(src, Meta{Name: "d.bin"})
	require.NoError(t, err)

	// The server already stores the hash: the handshake reports
	// Offset==Size and the client goes straight to commit.
	_, err = storeB.Put(bytes.NewReader(content), Meta{Name: "old-name"}, "")
	require.NoError(t, err)

	require.NoError(t, cli.Put(ctx, srv.host.ID(), hash))
	meta, err := storeB.Stat(hash)
	require.NoError(t, err)
	// Commit refreshes metadata — owner records the sender.
	assert.Equal(t, cli.host.ID().String(), meta.Owner)
	assert.Equal(t, "d.bin", meta.Name)
}

func TestGetDedupLocal(t *testing.T) {
	ctx := context.Background()
	hA, hB := newTestHosts(t, ctx)

	storeA := NewStore(t.TempDir(), 1<<20, 0)
	hash, err := storeA.Put(bytes.NewReader([]byte("already here")), Meta{Name: "a.txt"}, "")
	require.NoError(t, err)

	// B serves nothing — any wire call fails, so success proves dedup.
	cli := NewTransport(hA, storeA, TransportConfig{})
	path, meta, err := cli.Get(ctx, hB.ID(), hash)
	require.NoError(t, err)
	assert.Equal(t, "a.txt", meta.Name)
	assert.FileExists(t, path)
}

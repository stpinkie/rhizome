package blob

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRef(t *testing.T) {
	hash := "a1b2c3d4" + "00000000000000000000000000000000000000000000000000000000"[:56]
	pid, h, err := ParseRef(MakeRef("12D3KooWabc", hash))
	require.NoError(t, err)
	assert.Equal(t, "12D3KooWabc", pid)
	assert.Equal(t, hash, h)

	pid, h, err = ParseRef(hash)
	require.NoError(t, err)
	assert.Empty(t, pid)
	assert.Equal(t, hash, h)

	_, _, err = ParseRef("blob://peer/nothex")
	assert.Error(t, err)
	_, _, err = ParseRef("")
	assert.Error(t, err)
}

func TestStorePutGet(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 1<<20, 0)

	content := []byte("hello blob world")
	hash, err := s.Put(bytes.NewReader(content), Meta{Name: "greeting.txt", ContentType: "text/plain"}, "")
	require.NoError(t, err)
	require.Len(t, hash, 64)

	assert.True(t, s.Has(hash))
	meta, err := s.Stat(hash)
	require.NoError(t, err)
	assert.Equal(t, "greeting.txt", meta.Name)
	assert.Equal(t, int64(len(content)), meta.Size)

	f, _, err := s.Open(hash)
	require.NoError(t, err)
	got, err := readAll(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, content, got)

	p, err := s.Path(hash)
	require.NoError(t, err)
	assert.FileExists(t, p)

	// Idempotent: same content maps to the same blob.
	hash2, err := s.Put(bytes.NewReader(content), Meta{}, "")
	require.NoError(t, err)
	assert.Equal(t, hash, hash2)
}

func TestStorePutHashMismatch(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 1<<20, 0)
	_, err := s.Put(
		bytes.NewReader([]byte("data")),
		Meta{},
		"0000000000000000000000000000000000000000000000000000000000000000",
	)
	assert.ErrorContains(t, err, "hash mismatch")
}

func TestStoreSizeLimit(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 8, 0)
	_, err := s.Put(bytes.NewReader([]byte("0123456789abcdef")), Meta{}, "")
	assert.ErrorContains(t, err, "exceeds max size")
}

func TestBlobWriter(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 1<<20, 0)

	content := []byte("chunked content for writer test")
	w, err := s.NewBlobWriter(hashOf(content), int64(len(content)))
	require.NoError(t, err)
	_, err = w.Write(content[:10])
	require.NoError(t, err)
	_, err = w.Write(content[10:])
	require.NoError(t, err)
	sum, err := w.Complete()
	require.NoError(t, err)
	assert.Equal(t, hashOf(content), sum)
	assert.True(t, s.Has(sum))

	// Truncated content must be rejected.
	w2, err := s.NewBlobWriter(hashOf(content), int64(len(content)))
	require.NoError(t, err)
	_, err = w2.Write(content[:5])
	require.NoError(t, err)
	_, err = w2.Complete()
	assert.ErrorContains(t, err, "truncated")

	// Overflow is rejected at Write time.
	w3, err := s.NewBlobWriter(hashOf(content), int64(len(content)))
	require.NoError(t, err)
	_, err = w3.Write(append(content, 'x'))
	assert.ErrorContains(t, err, "exceeds announced size")
	w3.Abort()
}

func TestStoreReap(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, 1<<20, time.Millisecond)
	hash, err := s.Put(bytes.NewReader([]byte("old")), Meta{}, "")
	require.NoError(t, err)
	require.True(t, s.Has(hash))

	// Age the blob past the TTL by rewriting its stored_at metadata.
	require.NoError(t, s.WriteMeta(hash, Meta{StoredAt: time.Now().Add(-time.Hour).Unix()}))
	s.reap()
	assert.False(t, s.Has(hash))
	assert.NoFileExists(t, s.metaPath(hash))
}

// --- Transport tests over real libp2p hosts ---

func newTestHosts(t *testing.T, ctx context.Context) (host.Host, host.Host) {
	t.Helper()
	hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = hA.Close() })
	hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = hB.Close() })
	require.NoError(t, hA.Connect(ctx, peer.AddrInfo{ID: hB.ID(), Addrs: hB.Addrs()}))
	return hA, hB
}

// testSigner returns request/response signers bound to a host's identity key.
func testSigner(h host.Host) (SignFunc, SignResponseFunc) {
	sign := func(payload []byte) []byte {
		priv := h.Peerstore().PrivKey(h.ID())
		if priv == nil {
			return nil
		}
		sig, err := priv.Sign(payload)
		if err != nil {
			return nil
		}
		return sig
	}
	return func(req *Request) error {
			req.Signature = nil
			req.Nonce = "n-" + randHex()
			req.Timestamp = time.Now().Unix()
			payload, err := json.Marshal(req)
			if err != nil {
				return err
			}
			req.Signature = sign(payload)
			return nil
		}, func(resp *Response) {
			resp.Signature = nil
			payload, err := json.Marshal(resp)
			if err != nil {
				return
			}
			resp.Signature = sign(payload)
		}
}

func testVerifyReq(from peer.ID, req *Request) error {
	if len(req.Signature) == 0 {
		return testBlobError("missing signature")
	}
	sig := req.Signature
	req.Signature = nil
	defer func() { req.Signature = sig }()
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	pub, err := from.ExtractPublicKey()
	if err != nil {
		return err
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return err
	}
	if !ok {
		return testBlobError("bad signature")
	}
	return nil
}

func testVerifyResp(pid peer.ID, resp *Response) error {
	if len(resp.Signature) == 0 {
		return testBlobError("missing response signature")
	}
	sig := resp.Signature
	resp.Signature = nil
	defer func() { resp.Signature = sig }()
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	pub, err := pid.ExtractPublicKey()
	if err != nil {
		return err
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return err
	}
	if !ok {
		return testBlobError("bad response signature")
	}
	return nil
}

type testBlobError string

func (e testBlobError) Error() string { return string(e) }

func randHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func readAll(f io.Reader) ([]byte, error) {
	return io.ReadAll(f)
}

func TestBlobPutGetRoundTrip(t *testing.T) {
	ctx := context.Background()
	hA, hB := newTestHosts(t, ctx)

	storeA := NewStore(t.TempDir(), 1<<20, 0)
	storeB := NewStore(t.TempDir(), 1<<20, 0)
	storeA.Start(ctx)
	storeB.Start(ctx)
	t.Cleanup(storeA.Stop)
	t.Cleanup(storeB.Stop)

	signA, _ := testSigner(hA)
	_, signRespB := testSigner(hB)

	trusted := map[peer.ID]bool{hA.ID(): true, hB.ID(): true}
	authz := func(from peer.ID, _ Request) error {
		if !trusted[from] {
			return testBlobError("untrusted")
		}
		return nil
	}

	srv := NewTransport(hB, storeB, TransportConfig{
		Authorize:     authz,
		SignResponse:  signRespB,
		VerifyRequest: testVerifyReq,
	})
	go func() { _ = srv.Start(ctx) }()

	cli := NewTransport(hA, storeA, TransportConfig{
		SignRequest:    signA,
		VerifyResponse: testVerifyResp,
	})

	// Seed a blob in A's store and push it to B.
	src := filepath.Join(t.TempDir(), "photo.jpg")
	content := bytes.Repeat([]byte("jpeg-data-"), 50000) // ~500 KB, multi-chunk
	require.NoError(t, os.WriteFile(src, content, 0o600))
	hash, err := storeA.PutFile(src, Meta{Name: "photo.jpg", ContentType: "image/jpeg"})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return cli.Supported(ctx, hB.ID(), time.Second) },
		5*time.Second, 50*time.Millisecond)

	require.NoError(t, cli.Put(ctx, hB.ID(), hash))
	assert.True(t, storeB.Has(hash))

	meta, err := cli.Stat(ctx, hB.ID(), hash)
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), meta.Size)
	assert.Equal(t, "photo.jpg", meta.Name)

	// Pull the same blob back into a fresh store on a third logical reader —
	// simplest is to remove it from A and fetch from B.
	require.NoError(t, os.Remove(storeA.blobPath(hash)))
	require.NoError(t, os.Remove(storeA.metaPath(hash)))
	path, gotMeta, err := cli.Get(ctx, hB.ID(), hash)
	require.NoError(t, err)
	assert.Equal(t, "photo.jpg", gotMeta.Name)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestBlobUntrustedRejected(t *testing.T) {
	ctx := context.Background()
	hA, hB := newTestHosts(t, ctx)

	storeA := NewStore(t.TempDir(), 1<<20, 0)
	storeB := NewStore(t.TempDir(), 1<<20, 0)
	signA, _ := testSigner(hA)
	_, signRespB := testSigner(hB)

	srv := NewTransport(hB, storeB, TransportConfig{
		Authorize:     func(from peer.ID, _ Request) error { return testBlobError("untrusted") },
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

	src := filepath.Join(t.TempDir(), "x.bin")
	require.NoError(t, os.WriteFile(src, []byte("nope"), 0o600))
	hash, err := storeA.PutFile(src, Meta{})
	require.NoError(t, err)

	err = cli.Put(ctx, hB.ID(), hash)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "untrusted")
	assert.False(t, storeB.Has(hash))
}

func TestBlobMissing(t *testing.T) {
	ctx := context.Background()
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

	missing := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	_, err := cli.Stat(ctx, hB.ID(), missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

package mesh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/blob"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/network"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// newBlobTestPair starts two in-process nodes with mesh layers that trust
// each other and blob stores enabled in temp dirs.
func newBlobTestPair(t *testing.T, ctx context.Context) (*Mesh, *Mesh) {
	t.Helper()

	idA, _, err := identity.FromMnemonic(testMnemonic, 10)
	require.NoError(t, err)
	idB, _, err := identity.FromMnemonic(testMnemonic, 11)
	require.NoError(t, err)

	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeA.Close() })

	nodeB, err := network.NewNode(ctx, idB.Libp2pPrivKey, network.Config{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers: []string{nodeA.BootstrapAddrs()[0]},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeB.Close() })

	require.Eventually(t, func() bool {
		return network.IsConnectednessUp(nodeA.Connectedness(nodeB.ID()))
	}, 10*time.Second, 50*time.Millisecond)

	cfg := config.DefaultMeshConfig()
	cfg.Enabled = true
	cfg.AllowRemoteSpawn = true
	cfg.AllowRemoteDelegate = true
	cfg.AuditLog = false

	meshA := NewMesh(nodeA, nil, idA, cfg, nil)
	meshA.SetBlobDir(t.TempDir())
	require.NoError(t, meshA.Start(ctx))
	t.Cleanup(func() { _ = meshA.Stop() })

	meshB := NewMesh(nodeB, nil, idB, cfg, nil)
	meshB.SetBlobDir(t.TempDir())
	require.NoError(t, meshB.Start(ctx))
	t.Cleanup(func() { _ = meshB.Stop() })

	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())
	return meshA, meshB
}

func TestMeshBlobPushAndFetch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meshA, meshB := newBlobTestPair(t, ctx)

	// A pushes a file to B; the ref is qualified with B's peer id.
	src := filepath.Join(t.TempDir(), "note.txt")
	content := []byte("blob over the mesh")
	require.NoError(t, os.WriteFile(src, content, 0o600))

	ref, err := meshA.PushBlob(ctx, meshB.host.ID(), src, "note.txt", "text/plain")
	require.NoError(t, err)

	// A third-party reader on A's side can pull it back from B via the ref —
	// B stores the received copy, so fetching the ref from B resolves locally.
	meta, err := meshB.StatBlob(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, "note.txt", meta.Name)
	assert.Equal(t, int64(len(content)), meta.Size)

	path, _, err := meshB.FetchBlob(ctx, ref)
	require.NoError(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	// A fetches the same ref back from B over the wire (remote GET path) —
	// delete A's local copy first so the pull actually transfers.
	_, hash, err := blob.ParseRef(ref)
	require.NoError(t, err)
	local, err := meshA.blobStore.Path(hash)
	require.NoError(t, err)
	require.NoError(t, os.Remove(local))
	require.NoError(t, os.Remove(local+".json"))

	pulled, _, err := meshA.FetchBlob(ctx, ref)
	require.NoError(t, err)
	got, err = os.ReadFile(pulled)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestMeshBlobUntrustedRejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meshA, meshB := newBlobTestPair(t, ctx)

	// B drops A from its trust set: pushes must now be rejected.
	meshB.UntrustPeer(meshA.host.ID())

	src := filepath.Join(t.TempDir(), "x.bin")
	require.NoError(t, os.WriteFile(src, []byte("nope"), 0o600))
	_, err := meshA.PushBlob(ctx, meshB.host.ID(), src, "x.bin", "application/octet-stream")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not trusted")
}

func TestMeshBlobDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	idA, _, err := identity.FromMnemonic(testMnemonic, 12)
	require.NoError(t, err)
	nodeA, err := network.NewNode(ctx, idA.Libp2pPrivKey, network.Config{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	})
	require.NoError(t, err)
	defer nodeA.Close()

	cfg := config.DefaultMeshConfig()
	cfg.Enabled = true
	cfg.BlobEnabled = false

	m := NewMesh(nodeA, nil, idA, cfg, nil)
	m.SetBlobDir(t.TempDir())
	require.NoError(t, m.Start(ctx))
	defer m.Stop()

	_, _, err = m.FetchBlob(ctx, "blob://peer/"+hash64())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not enabled")
}

func hash64() string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// TestMeshTaskAttachments exercises the full attachment round trip: A pushes
// a file to B at submit time, B's run sees it as a media:// ref, and B's
// result artifact comes back as a media:// ref on A.
func TestMeshTaskAttachments(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meshA, meshB := newBlobTestPair(t, ctx)

	storeA := media.NewFileMediaStore()
	t.Cleanup(storeA.Stop)
	storeB := media.NewFileMediaStore()
	t.Cleanup(storeB.Stop)
	meshA.SetMediaStore(storeA)
	meshB.SetMediaStore(storeB)

	meshB.SetRunFunc(func(_ context.Context, req agentrpc.Request) (*toolshared.ToolResult, error) {
		if len(req.Media) != 1 {
			return nil, fmt.Errorf("expected 1 media ref, got %d", len(req.Media))
		}
		path, err := storeB.Resolve(req.Media[0])
		if err != nil {
			return nil, fmt.Errorf("resolve media: %w", err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read media: %w", err)
		}
		// Produce a result artifact for the caller to pull back.
		artPath := filepath.Join(t.TempDir(), "result.md")
		if err := os.WriteFile(artPath, []byte("artifact from callee"), 0o600); err != nil {
			return nil, err
		}
		ref, err := storeB.Store(artPath, media.MediaMeta{
			Filename:      "result.md",
			ContentType:   "text/markdown",
			Source:        "test",
			CleanupPolicy: media.CleanupPolicyForgetOnly,
		}, "out")
		if err != nil {
			return nil, err
		}
		res := toolshared.NewToolResult("got:" + string(content))
		res.Media = []string{ref}
		return res, nil
	})

	src := filepath.Join(t.TempDir(), "attach.txt")
	require.NoError(t, os.WriteFile(src, []byte("attach me"), 0o600))

	pidB := meshB.host.ID()
	_, taskID, err := meshA.SubmitRemoteTaskWithPeer(ctx, pidB, RemoteCall{
		TargetAgentID: "main",
		SystemPrompt:  "echo",
		Async:         true,
		Media:         []MediaAttachment{{Path: src}},
	})
	require.NoError(t, err)

	resp, err := meshA.RemoteTaskResult(ctx, pidB, taskID, 15*time.Second)
	require.NoError(t, err)
	require.Equal(t, agenttask.StatusDone, resp.Status)
	require.NotNil(t, resp.Result)
	assert.Equal(t, "got:attach me", resp.Result.ForLLM)

	// The result artifact was published as a blob ref and localized into a
	// media:// ref on the caller's store.
	require.Len(t, resp.Result.Media, 1)
	require.True(t, strings.HasPrefix(resp.Result.Media[0], "media://"))
	pulled, err := storeA.Resolve(resp.Result.Media[0])
	require.NoError(t, err)
	got, err := os.ReadFile(pulled)
	require.NoError(t, err)
	assert.Equal(t, "artifact from callee", string(got))
}

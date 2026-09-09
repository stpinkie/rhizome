package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/media"
	"github.com/stpinkie/rhizome/pkg/rhizome/blob"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

// maxTaskAttachments bounds the media refs carried by one remote request so
// a caller cannot force unbounded blob fetches on a callee.
const maxTaskAttachments = 16

// --- Blob transport wiring ---

// SetBlobDir enables the blob store at the given directory. It must be
// called before Mesh.Start. The blob protocol serves and transfers
// content-addressed files between trusted peers so remote tasks can carry
// attachments and return artifacts.
func (m *Mesh) SetBlobDir(dir string) {
	if dir == "" {
		return
	}
	m.blobStore = blob.NewStore(dir, m.cfg.BlobMaxBytes, m.cfg.BlobTTL)
}

// startBlob registers the blob protocol handler when enabled. Called from
// Start; the transport is only available while the mesh is running.
func (m *Mesh) startBlob() {
	if m.blobStore == nil || !m.cfg.BlobEnabled {
		return
	}
	m.blob = blob.NewTransport(m.host, m.blobStore, blob.TransportConfig{
		Authorize:      m.authorizeBlob,
		SignRequest:    m.signBlobRequest,
		SignResponse:   m.signBlobResponse,
		VerifyRequest:  m.verifyBlobRequest,
		VerifyResponse: m.verifyBlobResponse,
		OnEvent:        m.blobEvent,
	})
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		_ = m.blob.Start(m.ctx)
	}()
	m.blobStore.Start(m.ctx)
}

// BlobEnabled reports whether the blob protocol is enabled locally.
func (m *Mesh) BlobEnabled() bool {
	return m != nil && m.blobStore != nil && m.cfg.BlobEnabled
}

// checkBlobAllowed enforces the per-peer ACL for blob transfer. Trusted
// peers may exchange blobs by default; an ACL rule may deny it explicitly.
func (m *Mesh) checkBlobAllowed(pid peer.ID) error {
	if rule := m.aclRuleFor(pid); rule != nil && rule.AllowBlob != nil && !*rule.AllowBlob {
		return fmt.Errorf("peer %s is not allowed blob transfer", pid)
	}
	return nil
}

// authorizeBlob is the inbound gate for blob requests: trusted peer, ACL,
// rate limit, and replay protection on the signed nonce+timestamp.
func (m *Mesh) authorizeBlob(from peer.ID, req blob.Request) error {
	if !m.isTrusted(from) {
		return fmt.Errorf("peer %s is not trusted", from)
	}
	if err := m.checkBlobAllowed(from); err != nil {
		return err
	}
	if err := m.replay.check(from, req.Nonce, req.Timestamp); err != nil {
		return fmt.Errorf("replay check failed: %v", err)
	}
	if !m.allowRate(from) {
		return fmt.Errorf("rate_limited: too many requests")
	}
	return nil
}

// blobEvent reports a blob operation on the runtime bus and audit trail.
func (m *Mesh) blobEvent(op blob.Op, from peer.ID, hash string, err error) {
	kind := runtimeevents.KindMeshBlobPut
	if op == blob.OpGet || op == blob.OpStat {
		kind = runtimeevents.KindMeshBlobGet
	}
	attrs := map[string]any{
		"peer_id": from.String(),
		"op":      string(op),
		"hash":    hash,
	}
	status := "ok"
	if err != nil {
		status = "error"
		attrs["error"] = err.Error()
	}
	m.publishMeshEvent(kind, attrs)
	m.auditMesh(from, "blob."+string(op), "", hash, status, time.Now(), errString(err))
}

// --- Request/response signing ---

func (m *Mesh) signBlobRequest(req *blob.Request) error {
	req.Signature = nil
	req.Timestamp = time.Now().Unix()
	if req.Nonce == "" {
		req.Nonce = newTaskNonce()
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode blob request: %w", err)
	}
	req.Signature = identity.Sign(m.id.PrivateKey, payload)
	return nil
}

func (m *Mesh) verifyBlobRequest(from peer.ID, req *blob.Request) error {
	if len(req.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}
	sig := req.Signature
	req.Signature = nil
	defer func() { req.Signature = sig }()

	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	pub := m.host.Peerstore().PubKey(from)
	if pub == nil {
		return fmt.Errorf("no public key for peer %s", from)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify blob request signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid blob request signature from peer %s", from)
	}
	return nil
}

func (m *Mesh) signBlobResponse(resp *blob.Response) {
	resp.Signature = nil
	payload, err := json.Marshal(resp)
	if err != nil {
		return
	}
	resp.Signature = identity.Sign(m.id.PrivateKey, payload)
}

func (m *Mesh) verifyBlobResponse(pid peer.ID, resp *blob.Response) error {
	if len(resp.Signature) == 0 {
		return fmt.Errorf("missing response signature")
	}
	sig := resp.Signature
	resp.Signature = nil
	defer func() { resp.Signature = sig }()

	payload, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	pub := m.host.Peerstore().PubKey(pid)
	if pub == nil {
		return fmt.Errorf("no public key for peer %s", pid)
	}
	ok, err := pub.Verify(payload, sig)
	if err != nil {
		return fmt.Errorf("verify blob response signature: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid blob response signature from peer %s", pid)
	}
	return nil
}

// --- Task attachment plumbing ---

// remoteMediaRefs converts call media into blob refs for the target peer:
// local paths are pushed to pid over the blob protocol; existing blob://
// refs pass through for the callee to pull from the hosting peer.
func (m *Mesh) remoteMediaRefs(
	ctx context.Context,
	pid peer.ID,
	media []MediaAttachment,
) ([]string, error) {
	if len(media) == 0 {
		return nil, nil
	}
	if len(media) > maxTaskAttachments {
		return nil, fmt.Errorf("too many attachments: %d (max %d)", len(media), maxTaskAttachments)
	}
	refs := make([]string, 0, len(media))
	for i, a := range media {
		if a.Ref != "" {
			if _, _, err := blob.ParseRef(a.Ref); err != nil {
				return nil, fmt.Errorf("attachment %d: invalid ref: %w", i, err)
			}
			refs = append(refs, a.Ref)
			continue
		}
		if a.Path == "" {
			return nil, fmt.Errorf("attachment %d: path or ref is required", i)
		}
		if !m.BlobEnabled() {
			return nil, fmt.Errorf("blob transfer is not enabled")
		}
		name := a.Name
		if name == "" {
			name = filepath.Base(a.Path)
		}
		hash, err := m.blobStore.PutFile(a.Path, blob.Meta{Name: name, ContentType: a.ContentType})
		if err != nil {
			return nil, fmt.Errorf("attachment %d: %w", i, err)
		}
		if err := m.blob.Put(ctx, pid, hash); err != nil {
			return nil, fmt.Errorf("attachment %d: push to %s: %w", i, pid, err)
		}
		refs = append(refs, blob.MakeRef(pid.String(), hash))
	}
	return refs, nil
}

// fetchInboundMedia resolves the blob:// refs on an inbound remote request
// into local media:// refs registered under the given scope. Returns an
// error if any attachment cannot be fetched so the task fails explicitly
// instead of running with silently missing context.
func (m *Mesh) fetchInboundMedia(ctx context.Context, refs []string, scope string) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if len(refs) > maxTaskAttachments {
		return nil, fmt.Errorf("too many attachments: %d (max %d)", len(refs), maxTaskAttachments)
	}
	if m.mediaStore == nil {
		return nil, fmt.Errorf("media store not configured for attachments")
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		path, meta, err := m.FetchBlob(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("fetch attachment %q: %w", ref, err)
		}
		mref, err := m.mediaStore.Store(path, media.MediaMeta{
			Filename:      meta.Name,
			ContentType:   meta.ContentType,
			Source:        "mesh",
			CleanupPolicy: media.CleanupPolicyForgetOnly,
		}, scope)
		if err != nil {
			return nil, fmt.Errorf("register attachment %q: %w", ref, err)
		}
		out = append(out, mref)
	}
	return out, nil
}

// publishOutboundMedia rewrites media:// refs in a remote-call result into
// blob://<self>/<hash> refs the caller can pull back.
func (m *Mesh) publishOutboundMedia(result *toolshared.ToolResult) {
	if result == nil || len(result.Media) == 0 || m.mediaStore == nil || m.blobStore == nil {
		return
	}
	seen := make(map[string]struct{}, len(result.Media))
	out := result.Media[:0]
	for _, ref := range result.Media {
		if !strings.HasPrefix(ref, "media://") {
			out = append(out, ref)
			continue
		}
		path, meta, err := m.mediaStore.ResolveWithMeta(ref)
		if err != nil {
			continue
		}
		hash, err := m.blobStore.PutFile(path, blob.Meta{
			Name:        meta.Filename,
			ContentType: meta.ContentType,
		})
		if err != nil {
			continue
		}
		bref := blob.MakeRef(m.host.ID().String(), hash)
		if _, dup := seen[bref]; dup {
			continue
		}
		seen[bref] = struct{}{}
		out = append(out, bref)
	}
	result.Media = out
}

// localizeResultMedia fetches blob:// refs in a remote result into local
// media:// refs so they flow through the normal outbound media pipeline.
// It is a no-op when the media store is not configured (e.g. CLI callers),
// leaving the blob refs visible for manual retrieval.
func (m *Mesh) localizeResultMedia(ctx context.Context, result *toolshared.ToolResult, scope string) {
	if result == nil || len(result.Media) == 0 || m.mediaStore == nil {
		return
	}
	for i, ref := range result.Media {
		if !strings.HasPrefix(ref, "blob://") {
			continue
		}
		path, meta, err := m.FetchBlob(ctx, ref)
		if err != nil {
			logger.WarnCF("mesh", "Failed to fetch result artifact", map[string]any{
				"ref":   ref,
				"error": err.Error(),
			})
			continue
		}
		mref, err := m.mediaStore.Store(path, media.MediaMeta{
			Filename:      meta.Name,
			ContentType:   meta.ContentType,
			Source:        "mesh",
			CleanupPolicy: media.CleanupPolicyForgetOnly,
		}, scope)
		if err != nil {
			continue
		}
		result.Media[i] = mref
	}
}

// releaseMediaScope forgets all media refs registered under the given scope.
func (m *Mesh) releaseMediaScope(scope string) {
	if m.mediaStore != nil && scope != "" {
		_ = m.mediaStore.ReleaseAll(scope)
	}
}

// --- Public API ---

// PushBlob stores a local file in the blob store and streams it to a trusted
// peer, returning a fully qualified blob://<peer>/<hash> reference. The peer
// must be trusted and support the blob protocol.
func (m *Mesh) PushBlob(ctx context.Context, pid peer.ID, localPath, name, contentType string) (string, error) {
	if !m.BlobEnabled() {
		return "", fmt.Errorf("blob transfer is not enabled")
	}
	if !m.isTrusted(pid) {
		return "", fmt.Errorf("peer %s is not trusted", pid)
	}
	if _, err := os.Stat(localPath); err != nil {
		return "", fmt.Errorf("blob source: %w", err)
	}
	hash, err := m.blobStore.PutFile(localPath, blob.Meta{Name: name, ContentType: contentType})
	if err != nil {
		return "", err
	}
	if err := m.blob.Put(ctx, pid, hash); err != nil {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "blob.put",
			"peer_id": pid.String(),
			"hash":    hash,
			"error":   err.Error(),
		})
		m.auditMesh(pid, "blob.put", "", hash, "error", time.Now(), err.Error())
		return "", err
	}
	m.publishMeshEvent(runtimeevents.KindMeshBlobPut, map[string]any{
		"peer_id":  pid.String(),
		"hash":     hash,
		"outgoing": true,
	})
	m.auditMesh(pid, "blob.put", "", hash, "ok", time.Now(), "")
	return blob.MakeRef(pid.String(), hash), nil
}

// FetchBlob resolves a blob reference to a local path. Local refs (no peer
// segment, or this node's peer id) resolve directly; remote refs are pulled
// from the owning peer into the local store.
func (m *Mesh) FetchBlob(ctx context.Context, ref string) (string, blob.Meta, error) {
	if !m.BlobEnabled() {
		return "", blob.Meta{}, fmt.Errorf("blob transfer is not enabled")
	}
	peerID, hash, err := blob.ParseRef(ref)
	if err != nil {
		return "", blob.Meta{}, err
	}
	if peerID == "" || peerID == m.host.ID().String() {
		path, err := m.blobStore.Path(hash)
		if err != nil {
			return "", blob.Meta{}, err
		}
		meta, _ := m.blobStore.Stat(hash)
		return path, meta, nil
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return "", blob.Meta{}, fmt.Errorf("invalid blob ref peer: %w", err)
	}
	if !m.isTrusted(pid) {
		return "", blob.Meta{}, fmt.Errorf("peer %s is not trusted", pid)
	}
	path, meta, err := m.blob.Get(ctx, pid, hash)
	if err != nil {
		m.publishMeshEvent(runtimeevents.KindMeshError, map[string]any{
			"stage":   "blob.get",
			"peer_id": peerID,
			"hash":    hash,
			"error":   err.Error(),
		})
		m.auditMesh(pid, "blob.get", "", hash, "error", time.Now(), err.Error())
		return "", blob.Meta{}, err
	}
	m.publishMeshEvent(runtimeevents.KindMeshBlobGet, map[string]any{
		"peer_id":  peerID,
		"hash":     hash,
		"outgoing": true,
	})
	m.auditMesh(pid, "blob.get", "", hash, "ok", time.Now(), "")
	return path, meta, nil
}

// StatBlob returns a remote (or local) blob's metadata.
func (m *Mesh) StatBlob(ctx context.Context, ref string) (blob.Meta, error) {
	if !m.BlobEnabled() {
		return blob.Meta{}, fmt.Errorf("blob transfer is not enabled")
	}
	peerID, hash, err := blob.ParseRef(ref)
	if err != nil {
		return blob.Meta{}, err
	}
	if peerID == "" || peerID == m.host.ID().String() {
		return m.blobStore.Stat(hash)
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return blob.Meta{}, fmt.Errorf("invalid blob ref peer: %w", err)
	}
	if !m.isTrusted(pid) {
		return blob.Meta{}, fmt.Errorf("peer %s is not trusted", pid)
	}
	return m.blob.Stat(ctx, pid, hash)
}

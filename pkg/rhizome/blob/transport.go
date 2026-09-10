package blob

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/p2putil"
	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
)

// serverReadTimeout bounds the server-side reliable reader. Chunk arrivals
// are bounded per-frame, so a stalled upload fails fast instead of pinning
// a stream handler goroutine.
const serverReadTimeout = 90 * time.Second

// Authorize decides whether an inbound blob request may proceed. The mesh
// wires trust, ACL, rate limits, and replay checks here.
type Authorize func(from peer.ID, req Request) error

// SignFunc signs a request before it is sent (sets nonce, timestamp, and
// signature). The mesh wires node-identity signing here.
type SignFunc func(req *Request) error

// SignResponseFunc signs an outbound response.
type SignResponseFunc func(resp *Response)

// VerifyRequestFunc authenticates an inbound request's signature.
type VerifyRequestFunc func(from peer.ID, req *Request) error

// VerifyResponseFunc authenticates an inbound response's signature.
type VerifyResponseFunc func(pid peer.ID, resp *Response) error

// EventHook reports blob operations for audit and runtime events.
// It is optional; errors are reported by the server and client paths.
type EventHook func(op Op, from peer.ID, hash string, err error)

// Transport serves and calls the blob protocol.
type Transport struct {
	host       host.Host
	store      *Store
	authorize  Authorize
	signReq    SignFunc
	signResp   SignResponseFunc
	verifyReq  VerifyRequestFunc
	verifyResp VerifyResponseFunc
	onEvent    EventHook
}

// TransportConfig bundles the optional hooks for a Transport.
type TransportConfig struct {
	// Authorize gates inbound requests (required to serve).
	Authorize Authorize
	// SignRequest signs outbound requests; nil leaves them unsigned.
	SignRequest SignFunc
	// SignResponse signs outbound responses; nil leaves them unsigned.
	SignResponse SignResponseFunc
	// VerifyRequest authenticates inbound requests; nil accepts any request
	// that passes Authorize.
	VerifyRequest VerifyRequestFunc
	// VerifyResponse authenticates inbound responses; nil accepts them.
	VerifyResponse VerifyResponseFunc
	// OnEvent is called for each completed or rejected operation.
	OnEvent EventHook
}

// NewTransport creates a blob transport bound to a local store.
func NewTransport(h host.Host, store *Store, cfg TransportConfig) *Transport {
	return &Transport{
		host:       h,
		store:      store,
		authorize:  cfg.Authorize,
		signReq:    cfg.SignRequest,
		signResp:   cfg.SignResponse,
		verifyReq:  cfg.VerifyRequest,
		verifyResp: cfg.VerifyResponse,
		onEvent:    cfg.OnEvent,
	}
}

// Store returns the local blob store backing this transport.
func (t *Transport) Store() *Store { return t.store }

// Start registers the protocol handler and blocks until ctx ends.
func (t *Transport) Start(ctx context.Context) error {
	t.host.SetStreamHandler(ProtocolID, t.handleStream)
	<-ctx.Done()
	t.host.RemoveStreamHandler(ProtocolID)
	return ctx.Err()
}

// Supported reports whether the peer serves the blob protocol.
func (t *Transport) Supported(ctx context.Context, pid peer.ID, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s, err := p2putil.OpenProtocolStream(ctx, t.host, pid, ProtocolID, timeout)
	if err != nil {
		return false
	}
	_ = s.Close()
	return true
}

// --- Client side ---

// call sends one signed control request and reads the signed response.
// The returned ReliableConn stays open for follow-up frames (chunk
// streaming or a commit request); the caller must close it.
func (t *Transport) call(
	ctx context.Context, pid peer.ID, req Request,
) (*stream.ReliableConn, Response, error) {
	s, err := p2putil.OpenProtocolStream(ctx, t.host, pid, ProtocolID, 10*time.Second)
	if err != nil {
		return nil, Response{}, fmt.Errorf("peer %s does not support %s", pid, ProtocolID)
	}
	rc := stream.NewReliableConn(
		s,
		stream.WithReadTimeout(serverReadTimeout),
		stream.WithWriteTimeout(30*time.Second),
	)
	resp, err := t.roundTrip(rc, req)
	if err != nil {
		_ = rc.Close()
		return nil, Response{}, err
	}
	return rc, resp, nil
}

func (t *Transport) roundTrip(rc *stream.ReliableConn, req Request) (Response, error) {
	if t.signReq != nil {
		if err := t.signReq(&req); err != nil {
			return Response{}, fmt.Errorf("sign blob request: %w", err)
		}
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("encode blob request: %w", err)
	}
	if err := rc.WriteFrame(frameRequest, payload); err != nil {
		return Response{}, fmt.Errorf("write blob request: %w", err)
	}
	typ, raw, err := rc.ReadFrame()
	if err != nil {
		return Response{}, fmt.Errorf("read blob response: %w", err)
	}
	if typ != frameResponse {
		return Response{}, fmt.Errorf("unexpected blob frame type: %d", typ)
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Response{}, fmt.Errorf("decode blob response: %w", err)
	}
	return resp, nil
}

func (t *Transport) verifyResponse(pid peer.ID, resp *Response) error {
	if t.verifyResp != nil {
		if err := t.verifyResp(pid, resp); err != nil {
			return err
		}
	}
	if resp.Error != "" && !resp.OK {
		return fmt.Errorf("blob op rejected: %s", resp.Error)
	}
	return nil
}

// Stat fetches a blob's metadata from a peer without transferring content.
func (t *Transport) Stat(ctx context.Context, pid peer.ID, hash string) (Meta, error) {
	if err := ValidateHash(hash); err != nil {
		return Meta{}, err
	}
	rc, resp, err := t.call(ctx, pid, Request{Op: OpStat, Hash: hash})
	if err != nil {
		return Meta{}, err
	}
	defer func() { _ = rc.Close() }()
	if err := t.verifyResponse(pid, &resp); err != nil {
		return Meta{}, err
	}
	return Meta{
		Name:        resp.Name,
		ContentType: resp.ContentType,
		Size:        resp.Size,
	}, nil
}

// Get fetches a blob from a peer into the local store and returns its local
// path. Content is verified against the announced hash while streaming.
func (t *Transport) Get(ctx context.Context, pid peer.ID, hash string) (string, Meta, error) {
	if err := ValidateHash(hash); err != nil {
		return "", Meta{}, err
	}
	rc, resp, err := t.call(ctx, pid, Request{Op: OpGet, Hash: hash})
	if err != nil {
		return "", Meta{}, err
	}
	defer func() { _ = rc.Close() }()
	if err := t.verifyResponse(pid, &resp); err != nil {
		return "", Meta{}, err
	}

	meta := Meta{Name: resp.Name, ContentType: resp.ContentType, Owner: pid.String()}
	w, err := t.store.NewBlobWriter(hash, resp.Size)
	if err != nil {
		return "", Meta{}, err
	}
	var final Response
	received := int64(0)
	for received < resp.Size {
		typ, raw, err := rc.ReadFrame()
		if err != nil {
			w.Abort()
			return "", Meta{}, fmt.Errorf("read blob chunk: %w", err)
		}
		if typ != frameChunk {
			w.Abort()
			return "", Meta{}, fmt.Errorf("unexpected blob frame type during get: %d", typ)
		}
		if _, err := w.Write(raw); err != nil {
			w.Abort()
			return "", Meta{}, err
		}
		received += int64(len(raw))
	}
	// Read the final status frame the server sends after the last chunk.
	typ, raw, err := rc.ReadFrame()
	if err != nil {
		w.Abort()
		return "", Meta{}, fmt.Errorf("read blob completion: %w", err)
	}
	if typ != frameResponse {
		w.Abort()
		return "", Meta{}, fmt.Errorf("unexpected blob frame type at completion: %d", typ)
	}
	if err := json.Unmarshal(raw, &final); err != nil {
		w.Abort()
		return "", Meta{}, fmt.Errorf("decode blob completion: %w", err)
	}
	if err := t.verifyResponse(pid, &final); err != nil {
		w.Abort()
		return "", Meta{}, err
	}
	got, err := w.Complete()
	if err != nil {
		return "", Meta{}, err
	}
	if err := t.store.WriteMeta(got, meta); err != nil {
		return "", Meta{}, err
	}
	path, err := t.store.Path(got)
	if err != nil {
		return "", Meta{}, err
	}
	return path, meta, nil
}

// Put streams a local blob to a peer. The content must already exist in the
// local store (PutFile) so the announced hash matches the local copy.
func (t *Transport) Put(ctx context.Context, pid peer.ID, hash string) error {
	if err := ValidateHash(hash); err != nil {
		return err
	}
	localPath, err := t.store.Path(hash)
	if err != nil {
		return err
	}
	meta, err := t.store.Stat(hash)
	if err != nil {
		return err
	}

	rc, resp, err := t.call(ctx, pid, Request{
		Op:          OpPut,
		Hash:        hash,
		Size:        meta.Size,
		Name:        meta.Name,
		ContentType: meta.ContentType,
	})
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	if err := t.verifyResponse(pid, &resp); err != nil {
		return err
	}

	//nolint:gosec // G304: localPath is the content-addressed blob path under the store root.
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, chunkSize)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			if err := rc.WriteFrame(frameChunk, buf[:n]); err != nil {
				return fmt.Errorf("write blob chunk: %w", err)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("read blob source: %w", rerr)
		}
	}

	final, err := t.roundTrip(rc, Request{Op: OpCommit, Hash: hash})
	if err != nil {
		return err
	}
	return t.verifyResponse(pid, &final)
}

// --- Server side ---

func (t *Transport) handleStream(s network.Stream) {
	rc := stream.NewReliableConn(
		s,
		stream.WithReadTimeout(serverReadTimeout),
		stream.WithWriteTimeout(30*time.Second),
	)
	defer func() { _ = rc.Close() }()
	from := s.Conn().RemotePeer()

	for {
		typ, payload, err := rc.ReadFrame()
		if err != nil || typ != frameRequest {
			return
		}
		var req Request
		if err := json.Unmarshal(payload, &req); err != nil {
			return
		}
		if err := req.validate(); err != nil {
			t.respond(rc, Response{Error: err.Error()})
			t.report(req.Op, from, req.Hash, err)
			return
		}
		if t.verifyReq != nil {
			if err := t.verifyReq(from, &req); err != nil {
				t.respond(rc, Response{Error: fmt.Sprintf("verify request: %v", err)})
				t.report(req.Op, from, req.Hash, err)
				return
			}
		}
		if t.authorize != nil {
			if err := t.authorize(from, req); err != nil {
				t.respond(rc, Response{Error: fmt.Sprintf("forbidden: %v", err)})
				t.report(req.Op, from, req.Hash, err)
				return
			}
		}

		switch req.Op {
		case OpStat:
			t.serveStat(rc, from, req)
		case OpGet:
			t.serveGet(rc, from, req)
		case OpPut:
			t.servePut(rc, from, req)
		default:
			// OpCommit is only valid mid-put.
			t.respond(rc, Response{Error: "unexpected op"})
			return
		}
		// Keep the stream open after serving: the client closes it once it
		// has read the final response. Closing here first would race the
		// response delivery.
	}
}

func (t *Transport) respond(rc *stream.ReliableConn, resp Response) {
	if t.signResp != nil {
		t.signResp(&resp)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = rc.WriteFrame(frameResponse, data)
}

func (t *Transport) report(op Op, from peer.ID, hash string, err error) {
	if t.onEvent != nil {
		t.onEvent(op, from, hash, err)
	}
}

func (t *Transport) serveStat(rc *stream.ReliableConn, from peer.ID, req Request) {
	meta, err := t.store.Stat(req.Hash)
	if err != nil {
		t.respond(rc, Response{Error: err.Error()})
		t.report(OpStat, from, req.Hash, err)
		return
	}
	t.respond(rc, Response{
		OK:          true,
		Hash:        req.Hash,
		Size:        meta.Size,
		Name:        meta.Name,
		ContentType: meta.ContentType,
	})
	t.report(OpStat, from, req.Hash, nil)
}

func (t *Transport) serveGet(rc *stream.ReliableConn, from peer.ID, req Request) {
	f, meta, err := t.store.Open(req.Hash)
	if err != nil {
		t.respond(rc, Response{Error: err.Error()})
		t.report(OpGet, from, req.Hash, err)
		return
	}
	defer func() { _ = f.Close() }()

	t.respond(rc, Response{
		OK:          true,
		Hash:        req.Hash,
		Size:        meta.Size,
		Name:        meta.Name,
		ContentType: meta.ContentType,
	})

	buf := make([]byte, chunkSize)
	var sendErr error
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			if err := rc.WriteFrame(frameChunk, buf[:n]); err != nil {
				sendErr = err
				break
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			sendErr = rerr
			break
		}
	}
	if sendErr != nil {
		t.respond(rc, Response{Error: sendErr.Error()})
		t.report(OpGet, from, req.Hash, sendErr)
		return
	}
	t.respond(rc, Response{OK: true, Hash: req.Hash})
	t.report(OpGet, from, req.Hash, nil)
}

func (t *Transport) servePut(rc *stream.ReliableConn, from peer.ID, req Request) {
	if req.Size > t.store.MaxBytes() {
		err := fmt.Errorf("blob exceeds max size %d bytes", t.store.MaxBytes())
		t.respond(rc, Response{Error: err.Error()})
		t.report(OpPut, from, req.Hash, err)
		return
	}
	w, err := t.store.NewBlobWriter(req.Hash, req.Size)
	if err != nil {
		t.respond(rc, Response{Error: err.Error()})
		t.report(OpPut, from, req.Hash, err)
		return
	}
	// Signal readiness; the client streams chunk frames next.
	t.respond(rc, Response{OK: true, Hash: req.Hash})

	received := int64(0)
	for received < req.Size {
		typ, raw, rerr := rc.ReadFrame()
		if rerr != nil {
			w.Abort()
			t.report(OpPut, from, req.Hash, rerr)
			return
		}
		if typ != frameChunk {
			w.Abort()
			t.report(OpPut, from, req.Hash, fmt.Errorf("unexpected frame type %d during put", typ))
			return
		}
		if _, werr := w.Write(raw); werr != nil {
			w.Abort()
			t.report(OpPut, from, req.Hash, werr)
			return
		}
		received += int64(len(raw))
	}

	// Expect the signed commit request.
	typ, payload, rerr := rc.ReadFrame()
	if rerr != nil || typ != frameRequest {
		w.Abort()
		t.report(OpPut, from, req.Hash, fmt.Errorf("missing commit"))
		return
	}
	var commit Request
	if err := json.Unmarshal(payload, &commit); err != nil || commit.Op != OpCommit || commit.Hash != req.Hash {
		w.Abort()
		t.respond(rc, Response{Error: "invalid commit"})
		t.report(OpPut, from, req.Hash, fmt.Errorf("invalid commit"))
		return
	}
	if t.verifyReq != nil {
		if err := t.verifyReq(from, &commit); err != nil {
			w.Abort()
			t.respond(rc, Response{Error: fmt.Sprintf("verify commit: %v", err)})
			t.report(OpCommit, from, req.Hash, err)
			return
		}
	}
	if t.authorize != nil {
		if err := t.authorize(from, commit); err != nil {
			w.Abort()
			t.respond(rc, Response{Error: fmt.Sprintf("forbidden: %v", err)})
			t.report(OpCommit, from, req.Hash, err)
			return
		}
	}

	sum, err := w.Complete()
	if err != nil {
		t.respond(rc, Response{Error: err.Error()})
		t.report(OpCommit, from, req.Hash, err)
		return
	}
	_ = t.store.WriteMeta(sum, Meta{
		Name:        req.Name,
		ContentType: req.ContentType,
		Owner:       from.String(),
	})
	t.respond(rc, Response{OK: true, Hash: sum})
	t.report(OpCommit, from, req.Hash, nil)
}

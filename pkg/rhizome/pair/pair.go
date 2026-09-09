// Package pair implements the /rhizome/pair/1.0.0 trust-pairing protocol:
// an inviter mints a short-lived, single-use code wrapped in a shareable
// bundle (peer id + addrs + signature); a joiner verifies the bundle,
// connects, and presents the code over a signed handshake. Both sides then
// trust each other and persist the peer into mesh.trusted_peers and
// mesh.bootstrap_peers.
package pair

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/p2putil"
	"github.com/stpinkie/rhizome/pkg/rhizome/stream"
)

// ProtocolID is the libp2p protocol id for trust pairing.
const ProtocolID = protocol.ID("/rhizome/pair/1.0.0")

const (
	frameRequest  = 0x01
	frameResponse = 0x02

	// DefaultTTL is the default pairing-code lifetime.
	DefaultTTL = 15 * time.Minute
	// maxCodeAge bounds accepted codes even if a caller asks for more.
	maxCodeAge = time.Hour
	// requestMaxSkew bounds accepted clock skew on joiner requests.
	requestMaxSkew = 2 * time.Minute
)

// Bundle is the compact invite the inviter shares out of band.
type Bundle struct {
	PeerID string   `json:"peer_id"`
	Addrs  []string `json:"addrs"`
	Code   string   `json:"code"`
	Exp    int64    `json:"exp"`
	Sig    []byte   `json:"sig"`
}

// Request is the joiner's signed hello presented to the inviter.
type Request struct {
	Code      string   `json:"code"`
	PeerID    string   `json:"peer_id"`
	Addrs     []string `json:"addrs,omitempty"`
	Timestamp int64    `json:"timestamp"`
	Signature []byte   `json:"signature"`
}

// Response is the inviter's signed admission (or rejection).
type Response struct {
	OK        bool     `json:"ok"`
	Error     string   `json:"error,omitempty"`
	PeerID    string   `json:"peer_id,omitempty"`
	Addrs     []string `json:"addrs,omitempty"`
	Signature []byte   `json:"signature,omitempty"`
}

// invitePayload is what the inviter signs inside the bundle.
func invitePayload(peerID, code string, exp int64) []byte {
	return []byte(fmt.Sprintf("rhizome-pair-invite:%s:%s:%d", peerID, code, exp))
}

// joinPayload is what the joiner signs in the pairing request.
func joinPayload(code, peerID string, ts int64) []byte {
	return []byte(fmt.Sprintf("rhizome-pair-join:%s:%s:%d", code, peerID, ts))
}

// admitPayload is what the inviter signs in the admission response.
func admitPayload(code, joinerPeerID string) []byte {
	return []byte(fmt.Sprintf("rhizome-pair-admit:%s:%s", code, joinerPeerID))
}

// Hooks connect the pair manager to trust and persistence.
type Hooks struct {
	// TrustPeer adds the peer to the runtime trust set (e.g. mesh.TrustPeer).
	TrustPeer func(pid peer.ID)
	// Persist writes the new peer into mesh.trusted_peers +
	// mesh.bootstrap_peers in config.
	Persist func(peerID string, addrs []string) error
	// Event publishes a mesh.pair.* runtime event (optional).
	Event func(kind runtimeevents.Kind, attrs map[string]any)
}

// Manager hosts the pairing protocol on the node and tracks outstanding
// single-use codes.
type Manager struct {
	host  host.Host
	id    *identity.Derived
	hooks Hooks

	mu    sync.Mutex
	codes map[string]time.Time // code -> expiry
	path  string               // optional pair-codes.json persistence
}

// New creates a pairing manager. homeDir enables code persistence at
// <homeDir>/pair-codes.json so codes survive a daemon restart.
func New(h host.Host, id *identity.Derived, homeDir string, hooks Hooks) *Manager {
	pm := &Manager{host: h, id: id, hooks: hooks, codes: make(map[string]time.Time)}
	if homeDir != "" {
		pm.path = filepath.Join(homeDir, "pair-codes.json")
		_ = pm.loadCodes()
	}
	return pm
}

// Start registers the stream handler and blocks until ctx is done.
func (pm *Manager) Start(ctx context.Context) error {
	pm.host.SetStreamHandler(ProtocolID, pm.handleStream)
	<-ctx.Done()
	pm.host.RemoveStreamHandler(ProtocolID)
	return ctx.Err()
}

func (pm *Manager) event(kind runtimeevents.Kind, attrs map[string]any) {
	if pm.hooks.Event != nil {
		pm.hooks.Event(kind, attrs)
	}
}

func (pm *Manager) persist(peerID string, addrs []string) {
	if pm.hooks.Persist != nil {
		_ = pm.hooks.Persist(peerID, addrs)
	}
}

func (pm *Manager) trust(pid peer.ID) {
	if pm.hooks.TrustPeer != nil {
		pm.hooks.TrustPeer(pid)
	}
}

func newCode() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// Create mints a single-use pairing code and returns the shareable bundle
// (base64url-encoded JSON). ttl is clamped to [0, 1h]; 0 uses DefaultTTL.
func (pm *Manager) Create(ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if ttl > maxCodeAge {
		ttl = maxCodeAge
	}
	code := newCode()
	exp := time.Now().Add(ttl).Unix()

	peerID := pm.host.ID().String()
	sig := identity.Sign(pm.id.PrivateKey, invitePayload(peerID, code, exp))

	bundle := Bundle{
		PeerID: peerID,
		Addrs:  pm.addrs(),
		Code:   code,
		Exp:    exp,
		Sig:    sig,
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return "", fmt.Errorf("encode bundle: %w", err)
	}

	pm.mu.Lock()
	pm.codes[code] = time.Unix(exp, 0)
	pm.pruneLocked()
	pm.mu.Unlock()
	pm.saveCodes()

	pm.event(runtimeevents.KindMeshPairCreated, map[string]any{"exp": exp, "ttl": ttl.String()})
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// addrs returns the host's public listen addrs with the peer id appended.
func (pm *Manager) addrs() []string {
	out := make([]string, 0, len(pm.host.Addrs()))
	for _, a := range pm.host.Addrs() {
		out = append(out, a.String()+"/p2p/"+pm.host.ID().String())
	}
	return out
}

// DecodeBundle parses and authenticates a bundle: the signature must verify
// against the embedded peer id's public key and the code must be unexpired.
func DecodeBundle(bundleB64 string) (Bundle, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(bundleB64))
	if err != nil {
		return Bundle{}, fmt.Errorf("invalid bundle encoding: %w", err)
	}
	var b Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return Bundle{}, fmt.Errorf("invalid bundle payload: %w", err)
	}
	pid, err := peer.Decode(b.PeerID)
	if err != nil {
		return Bundle{}, fmt.Errorf("invalid bundle peer id: %w", err)
	}
	pub, err := pid.ExtractPublicKey()
	if err != nil {
		return Bundle{}, fmt.Errorf("extract inviter key: %w", err)
	}
	ok, err := pub.Verify(invitePayload(b.PeerID, b.Code, b.Exp), b.Sig)
	if err != nil || !ok {
		return Bundle{}, fmt.Errorf("invalid bundle signature")
	}
	if time.Now().Unix() > b.Exp {
		return Bundle{}, fmt.Errorf("pairing code expired")
	}
	return b, nil
}

// Accept redeems a bundle: it verifies the invite signature, dials the
// inviter, presents the code in a signed hello, and verifies the signed
// admission. On success the inviter is trusted and persisted.
func (pm *Manager) Accept(ctx context.Context, bundleB64 string) (string, error) {
	b, err := DecodeBundle(bundleB64)
	if err != nil {
		return "", err
	}
	pid, err := peer.Decode(b.PeerID)
	if err != nil {
		return "", fmt.Errorf("invalid bundle peer id: %w", err)
	}

	// Dial the inviter on the advertised addrs.
	dialed := false
	for _, addr := range b.Addrs {
		maddr, aerr := multiaddr.NewMultiaddr(strings.TrimSpace(addr))
		if aerr != nil {
			continue
		}
		ai, aerr := peer.AddrInfoFromP2pAddr(maddr)
		if aerr != nil {
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if pm.host.Connect(dctx, *ai) == nil {
			dialed = true
			cancel()
			break
		}
		cancel()
	}
	if !dialed {
		return "", fmt.Errorf("could not reach inviter on advertised addrs")
	}

	ts := time.Now().Unix()
	req := Request{
		Code:      b.Code,
		PeerID:    pm.host.ID().String(),
		Addrs:     pm.addrs(),
		Timestamp: ts,
		Signature: identity.Sign(pm.id.PrivateKey, joinPayload(b.Code, pm.host.ID().String(), ts)),
	}

	s, err := p2putil.OpenProtocolStream(ctx, pm.host, pid, ProtocolID, 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("open pair stream: %w", err)
	}
	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(30*time.Second), stream.WithWriteTimeout(15*time.Second))
	defer rc.Close()

	payload, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}
	if err := rc.WriteFrame(frameRequest, payload); err != nil {
		return "", fmt.Errorf("write request: %w", err)
	}
	typ, raw, err := rc.ReadFrame()
	if err != nil || typ != frameResponse {
		return "", fmt.Errorf("read admission: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("decode admission: %w", err)
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "pairing rejected"
		}
		pm.event(runtimeevents.KindMeshPairFailed, map[string]any{"error": resp.Error})
		return "", fmt.Errorf("pairing rejected: %s", resp.Error)
	}
	if resp.PeerID != b.PeerID {
		return "", fmt.Errorf("admission from unexpected peer %q", resp.PeerID)
	}

	// Verify the inviter's signed admission.
	pub, err := pid.ExtractPublicKey()
	if err != nil {
		return "", fmt.Errorf("extract inviter key: %w", err)
	}
	ok, err := pub.Verify(admitPayload(b.Code, req.PeerID), resp.Signature)
	if err != nil || !ok {
		return "", fmt.Errorf("invalid admission signature")
	}

	pm.trust(pid)
	pm.persist(b.PeerID, b.Addrs)
	pm.event(runtimeevents.KindMeshPairAccepted, map[string]any{"peer_id": b.PeerID, "role": "joiner"})
	return b.PeerID, nil
}

// handleStream services one inbound pairing request.
func (pm *Manager) handleStream(s network.Stream) {
	rc := stream.NewReliableConn(s,
		stream.WithReadTimeout(30*time.Second), stream.WithWriteTimeout(15*time.Second))
	defer rc.Close()

	typ, raw, err := rc.ReadFrame()
	if err != nil || typ != frameRequest {
		return
	}
	var req Request
	if json.Unmarshal(raw, &req) != nil {
		return
	}
	resp := pm.redeem(s.Conn().RemotePeer(), req)
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_ = rc.WriteFrame(frameResponse, data)
}

// redeem validates the presented code and joiner signature, then admits the
// joiner: trust + persist + signed admission. The code is only consumed
// after all validation checks pass, so a replay that fails skew or
// signature verification cannot DoS the legitimate joiner by burning the
// code.
func (pm *Manager) redeem(from peer.ID, req Request) Response {
	fail := func(msg string) Response {
		pm.event(runtimeevents.KindMeshPairFailed, map[string]any{
			"peer_id": from.String(), "error": msg,
		})
		return Response{OK: false, Error: msg}
	}

	if req.PeerID != from.String() {
		return fail("peer id mismatch")
	}

	// Look up the code and check expiry without consuming it yet.
	pm.mu.Lock()
	exp, codeExists := pm.codes[req.Code]
	pm.pruneLocked()
	pm.mu.Unlock()
	if !codeExists {
		return fail("unknown or already-used pairing code")
	}
	if time.Now().After(exp) {
		// Expired — consume it now so it doesn't linger.
		pm.mu.Lock()
		delete(pm.codes, req.Code)
		pm.mu.Unlock()
		pm.saveCodes()
		return fail("pairing code expired")
	}

	// Timestamp skew guard.
	if req.Timestamp == 0 || time.Since(time.Unix(req.Timestamp, 0)) > requestMaxSkew ||
		time.Until(time.Unix(req.Timestamp, 0)) > requestMaxSkew {
		return fail("request timestamp outside accepted skew")
	}

	// The joiner must prove key possession: signature over code+peer+ts.
	pub := pm.host.Peerstore().PubKey(from)
	if pub == nil {
		return fail("no public key for peer")
	}
	valid, err := pub.Verify(joinPayload(req.Code, req.PeerID, req.Timestamp), req.Signature)
	if err != nil || !valid {
		return fail("invalid joiner signature")
	}

	// All checks passed — consume the single-use code.
	pm.mu.Lock()
	delete(pm.codes, req.Code)
	pm.mu.Unlock()
	pm.saveCodes()

	pm.trust(from)
	pm.persist(from.String(), req.Addrs)
	pm.event(runtimeevents.KindMeshPairAccepted, map[string]any{
		"peer_id": from.String(), "role": "inviter",
	})

	return Response{
		OK:        true,
		PeerID:    pm.host.ID().String(),
		Addrs:     pm.addrs(),
		Signature: identity.Sign(pm.id.PrivateKey, admitPayload(req.Code, req.PeerID)),
	}
}

// pruneLocked drops expired codes. Call with pm.mu held.
func (pm *Manager) pruneLocked() {
	now := time.Now()
	for code, exp := range pm.codes {
		if now.After(exp) {
			delete(pm.codes, code)
		}
	}
}

// --- optional pair-codes.json persistence ---

type codesFile struct {
	Codes map[string]int64 `json:"codes"`
}

func (pm *Manager) loadCodes() error {
	data, err := os.ReadFile(pm.path)
	if err != nil {
		return nil // missing is fine
	}
	var f codesFile
	if json.Unmarshal(data, &f) != nil {
		return nil
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	for code, exp := range f.Codes {
		if time.Now().Unix() < exp {
			pm.codes[code] = time.Unix(exp, 0)
		}
	}
	return nil
}

func (pm *Manager) saveCodes() {
	if pm.path == "" {
		return
	}
	pm.mu.Lock()
	f := codesFile{Codes: make(map[string]int64, len(pm.codes))}
	for code, exp := range pm.codes {
		f.Codes[code] = exp.Unix()
	}
	pm.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(pm.path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	tmp := pm.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, pm.path)
}

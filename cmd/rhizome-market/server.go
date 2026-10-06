// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stpinkie/rhizome/pkg/logger"
)

// apiBodyMaxBytes bounds one request body read — matches the client's
// 1 MiB response budget in cmd/rhizome/internal/market.
const apiBodyMaxBytes = 1 << 20

// apiServer is the module's loopback HTTP API — the surface
// `rhizome market find|buy|session|receipt` dials. Every /v1/* route
// requires `Authorization: Bearer <bridge-token>`; the token comes from
// the supervisor env or <module_dir>/bridge-token.
type apiServer struct {
	ln        net.Listener
	srv       *http.Server
	moduleDir string
	token     *tokenProvider
	cfg       atomic.Pointer[marketConfig]
	bridge    func() bridgeStatus // live bridge state for /v1/health
	mgr       *sessionMgr         // live sessions for /v1/session + /v1/receipt
	buyer     *purchaseMgr        // purchases for /v1/find|buy|dispute|refund|release
	attest    *attestationStore   // seller's received attestations (advert feed)
	audit     *auditLogger
	started   time.Time
	version   string
}

// bridgeStatus is the health surface of the bridge listener pair.
type bridgeStatus struct {
	Listener bool `json:"listener"`
	Outbound bool `json:"outbound"`
}

// startAPI binds the loopback listener, publishes api.addr, and serves.
// The returned server owns the listener; Close removes api.addr.
func startAPI(
	moduleDir string, token *tokenProvider, mgr *sessionMgr, buyer *purchaseMgr,
	attest *attestationStore, audit *auditLogger, version string,
) (*apiServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("api listen: %w", err)
	}
	s := &apiServer{
		ln:        ln,
		moduleDir: moduleDir,
		token:     token,
		mgr:       mgr,
		buyer:     buyer,
		attest:    attest,
		audit:     audit,
		started:   time.Now(),
		version:   version,
	}
	s.bridge = func() bridgeStatus { return bridgeStatus{} }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.wrap(s.handleHealth))
	mux.HandleFunc("POST /v1/find", s.wrap(s.handleFind))
	mux.HandleFunc("POST /v1/buy", s.wrap(s.handleBuy))
	mux.HandleFunc("POST /v1/dispute", s.wrap(s.handleDispute))
	mux.HandleFunc("POST /v1/attest", s.wrap(s.handleAttest))
	mux.HandleFunc("POST /v1/attest/verify", s.wrap(s.handleAttestVerify))
	mux.HandleFunc("POST /v1/attest/register", s.wrap(s.handleAttestRegister))
	mux.HandleFunc("POST /v1/escalate", s.wrap(s.handleEscalate))
	mux.HandleFunc("POST /v1/evidence", s.wrap(s.handleEvidence))
	mux.HandleFunc("POST /v1/refund", s.wrap(s.handleRefund))
	mux.HandleFunc("POST /v1/release", s.wrap(s.handleRelease))
	mux.HandleFunc("POST /v1/session", s.wrap(s.handleSession))
	mux.HandleFunc("POST /v1/receipt", s.wrap(s.handleReceipt))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"code": "not_found", "detail": "unknown endpoint"},
		})
	})
	s.srv = &http.Server{ //nolint:gosec // G112: loopback module API; per-handler deadlines below.
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := writeFileAtomic(
		filepath.Join(moduleDir, apiAddrFile),
		[]byte(ln.Addr().String()+"\n"),
	); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("api.addr write: %w", err)
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorCF("market", "api serve failed", map[string]any{"error": err.Error()})
		}
	}()
	return s, nil
}

// setConfig publishes the current resolved config for /v1/health.
func (s *apiServer) setConfig(mc *marketConfig) { s.cfg.Store(mc) }

// Close shuts the server down gracefully and removes api.addr.
func (s *apiServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.srv != nil {
		_ = s.srv.Shutdown(ctx)
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	_ = os.Remove(filepath.Join(s.moduleDir, apiAddrFile))
}

// wrap enforces bearer auth on every /v1/* route and times each request.
func (s *apiServer) wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		tok := s.token.token()
		if tok == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]any{
					"code":   "token_missing",
					"detail": "module bridge token not minted yet — enable the module under the daemon",
				},
			})
			s.auditAPI(r, http.StatusServiceUnavailable, start)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{"code": "unauthorized", "detail": "bad bearer token"},
			})
			s.auditAPI(r, http.StatusUnauthorized, start)
			return
		}
		h(w, r)
	}
}

func (s *apiServer) auditAPI(r *http.Request, status int, start time.Time) {
	s.audit.log("market.api.request", map[string]any{
		"verb":   r.URL.Path,
		"status": status,
		"ms":     time.Since(start).Milliseconds(),
	})
}

func (s *apiServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	mc := s.cfg.Load()
	resp := map[string]any{
		"status":         "ok",
		"version":        s.version,
		"uptime_s":       int64(time.Since(s.started).Seconds()),
		"serve_enabled":  mc != nil && mc.serveEnabled,
		"offers":         0,
		"bridge":         s.bridge(),
		"escrow_posture": "fixture",
	}
	if mc != nil {
		resp["offers"] = len(mc.offers)
		if mc.rail != nil {
			resp["escrow_posture"] = "configured"
		}
		if len(mc.errs) > 0 {
			resp["config_error"] = strings.Join(mc.errs, "; ")
		}
	}
	if s.mgr != nil {
		resp["sessions_live"] = s.mgr.sessionCount()
	}
	s.auditAPI(r, http.StatusOK, time.Now())
	writeJSON(w, http.StatusOK, resp)
}

// sessionLookup is the request shape /v1/session and /v1/receipt share.
// The thin CLI sends {"id": ...}; the ACP-facing name is session_id —
// both resolve to the same lookup key.
type sessionLookup struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id"`
}

// id resolves either spelling to the lookup key.
func (r sessionLookup) id() string {
	if r.SessionID != "" {
		return r.SessionID
	}
	return r.ID
}

// handleSession reports the live state of a sell-side session — the
// local operator's view of what the module is serving right now.
func (s *apiServer) handleSession(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req sessionLookup
	if !s.decodeBody(w, r, &req) {
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{"code": "not_ready", "detail": "session manager not up"},
		})
		s.auditAPI(r, http.StatusServiceUnavailable, start)
		return
	}
	sess := s.mgr.lookup(req.id())
	if sess == nil && s.buyer != nil {
		// Buyer-role records: keyed by purchase_id or escrow session id.
		// Snapshots (not live pointers) — the orchestrator mutates them.
		if p := s.buyer.lookup(req.id()); p != nil {
			s.auditAPI(r, http.StatusOK, start)
			writeJSON(w, http.StatusOK, s.buyer.snapshot(p))
			return
		}
		for _, p := range s.buyer.bySession(req.id()) {
			s.auditAPI(r, http.StatusOK, start)
			writeJSON(w, http.StatusOK, s.buyer.snapshot(p))
			return
		}
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{
				"code":   "not_found",
				"detail": "no live session or purchase " + req.id(),
			},
		})
		s.auditAPI(r, http.StatusNotFound, start)
		return
	}
	sess.mu.Lock()
	resp := map[string]any{
		"session_id":  sess.ID,
		"state":       sess.State,
		"offer_id":    sess.Offer.ID,
		"peer":        sess.Peer,
		"duration_ms": sess.durationMS(),
		"opened_at":   sess.OpenedAt.UTC().Format(time.RFC3339),
	}
	if !sess.EndedAt.IsZero() {
		resp["ended_at"] = sess.EndedAt.UTC().Format(time.RFC3339)
	}
	if sess.usage != nil {
		resp["usage"] = sess.usage
	}
	if sess.receipt != nil {
		resp["result_sha256"] = sess.receipt.ResultSHA256
	}
	sess.mu.Unlock()
	s.auditAPI(r, http.StatusOK, start)
	writeJSON(w, http.StatusOK, resp)
}

// handleReceipt serves the signed receipt for a finished session — the
// artifact a buyer presents to justify escrow release, and the seller's
// own claim-path record.
func (s *apiServer) handleReceipt(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req sessionLookup
	if !s.decodeBody(w, r, &req) {
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.mgr != nil {
		if sess := s.mgr.lookup(req.id()); sess != nil {
			sess.mu.Lock()
			rc := sess.receipt
			sess.mu.Unlock()
			if rc != nil {
				s.auditAPI(r, http.StatusOK, start)
				writeJSON(w, http.StatusOK, rc)
				return
			}
		}
	}
	if s.buyer != nil {
		// Buyer-role records: resolve by purchase id or escrow session id —
		// the same dual lookup dispute/refund/release advertise.
		var matches []*purchase
		if p := s.buyer.lookup(req.id()); p != nil {
			matches = []*purchase{p}
		} else {
			matches = s.buyer.bySession(req.id())
		}
		for _, p := range matches {
			snap := s.buyer.snapshot(p)
			if snap.Receipt != nil {
				s.auditAPI(r, http.StatusOK, start)
				writeJSON(w, http.StatusOK, snap.Receipt)
				return
			}
		}
	}
	rc, err := loadReceipt(s.moduleDir, req.id())
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{
				"code":   "not_found",
				"detail": "no receipt for session " + req.id(),
			},
		})
		s.auditAPI(r, http.StatusNotFound, start)
		return
	}
	s.auditAPI(r, http.StatusOK, start)
	writeJSON(w, http.StatusOK, rc)
}

// handleFind executes `market find <query>` — index query or the
// daemon-journaled peer adverts.
func (s *apiServer) handleFind(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req struct {
		Query string `json:"query"`
	}
	if !s.decodeBody(w, r, &req) {
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.buyer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{"code": "not_ready", "detail": "buy side not up"},
		})
		s.auditAPI(r, http.StatusServiceUnavailable, start)
		return
	}
	resp, err := s.buyer.runFind(r.Context(), req.Query)
	status := http.StatusOK
	if err != nil {
		status = http.StatusBadRequest
		resp = errBody(err)
	}
	s.auditAPI(r, status, start)
	writeJSON(w, status, resp)
}

// handleBuy starts a purchase or confirms a pending review. Returns the
// purchase record — the async orchestrator drives the lifecycle.
func (s *apiServer) handleBuy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req buyRequest
	if !s.decodeBody(w, r, &req) {
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.buyer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{"code": "not_ready", "detail": "buy side not up"},
		})
		s.auditAPI(r, http.StatusServiceUnavailable, start)
		return
	}
	p, reviewID, err := s.buyer.begin(r.Context(), req)
	if err != nil {
		status := http.StatusBadRequest
		if be, ok := err.(*buyError); ok &&
			(be.code == "not_ready" || be.code == "rail_unavailable" ||
				be.code == "index_unavailable" || be.code == "peer_adverts") {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, errBody(err))
		s.auditAPI(r, status, start)
		return
	}
	snap := s.buyer.snapshot(p)
	resp := map[string]any{"purchase": snap}
	if reviewID != "" {
		resp["state"] = purchasePendingReview
		resp["review_id"] = reviewID
		resp["preview"] = map[string]any{
			"task_sent":   snap.Task, // the exact (post-redact) text the seller will see
			"provider":    snap.Provider,
			"offer_id":    snap.OfferID,
			"price":       snap.Price,
			"asset":       snap.Asset,
			"session_id":  snap.SessionID,
			"attachments": snap.Attachments,
		}
		resp["confirm"] = "rhizome market buy --confirm " + reviewID
	}
	s.auditAPI(r, http.StatusOK, start)
	writeJSON(w, http.StatusOK, resp)
}

// handleDispute locks the escrow via lock(details) — buyer-initiated.
func (s *apiServer) handleDispute(w http.ResponseWriter, r *http.Request) {
	s.buyAction(w, r, "dispute",
		func(ctx context.Context, id string, extra map[string]any) (*purchase, error) {
			reason, _ := extra["reason"].(string)
			return s.buyer.dispute(ctx, id, reason)
		})
}

// handleAttest mints a signed attestation for a terminal purchase —
// strictly opt-in. The response IS the attestation JSON the seller
// registers; delivery is out-of-band (no post-session channel exists).
func (s *apiServer) handleAttest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req struct {
		ID string `json:"id"`
	}
	if !s.decodeBody(w, r, &req) {
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.buyer == nil || strings.TrimSpace(req.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": "bad_request", "detail": "id required"},
		})
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	a, err := s.buyer.attest(req.ID)
	if err != nil {
		var be *buyError
		status := http.StatusInternalServerError
		if errors.As(err, &be) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, errBody(err))
		s.auditAPI(r, status, start)
		return
	}
	s.auditAPI(r, http.StatusOK, start)
	writeJSON(w, http.StatusOK, map[string]any{"attestation": a})
}

// handleAttestVerify validates an attestation's sig → peer → terms
// chain — buyers cross-check terms against their own record; third
// parties get the signature verdict plus terms_match=null.
func (s *apiServer) handleAttestVerify(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req struct {
		Attestation json.RawMessage `json:"attestation"`
	}
	if !s.decodeBody(w, r, &req) || len(req.Attestation) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": "bad_request", "detail": "attestation required"},
		})
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.buyer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{"code": "not_ready", "detail": "buy side not up"},
		})
		s.auditAPI(r, http.StatusServiceUnavailable, start)
		return
	}
	chk, err := s.buyer.verifyAttestation(req.Attestation)
	if err != nil {
		var be *buyError
		status := http.StatusInternalServerError
		if errors.As(err, &be) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, errBody(err))
		s.auditAPI(r, status, start)
		return
	}
	s.auditAPI(r, http.StatusOK, start)
	writeJSON(w, http.StatusOK, chk)
}

// handleAttestRegister stores a received attestation on the seller
// side — verifies the signature chain, then lands it in the bounded
// store the advert writer renders.
func (s *apiServer) handleAttestRegister(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req struct {
		Attestation attestation `json:"attestation"`
	}
	if !s.decodeBody(w, r, &req) || req.Attestation.Signature == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": "bad_request", "detail": "signed attestation required"},
		})
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.attest == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{"code": "not_ready", "detail": "attestation store not up"},
		})
		s.auditAPI(r, http.StatusServiceUnavailable, start)
		return
	}
	if err := s.attest.register(req.Attestation); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	s.auditAPI(r, http.StatusOK, start)
	s.audit.log("market.attestation.register", map[string]any{
		"session_id": req.Attestation.SessionID,
		"provider":   req.Attestation.ProviderPeerID,
		"outcome":    req.Attestation.Outcome,
	})
	writeJSON(w, http.StatusOK, map[string]any{"registered": true})
}

// handleEscalate retries the Kleros createDispute — the manual path when
// auto-escalation at dispute time failed or the fee quote moved.
func (s *apiServer) handleEscalate(w http.ResponseWriter, r *http.Request) {
	s.buyAction(w, r, "escalate",
		func(ctx context.Context, id string, _ map[string]any) (*purchase, error) {
			return s.buyer.escalate(ctx, id)
		})
}

// handleEvidence returns the purchase's ERC-1497 evidence bundle — the
// same bytes dispute() submitted on-chain, for operator inspection.
func (s *apiServer) handleEvidence(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req struct {
		ID string `json:"id"`
	}
	if !s.decodeBody(w, r, &req) {
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.buyer == nil || strings.TrimSpace(req.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": "bad_request", "detail": "id required"},
		})
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	bundle, err := s.buyer.evidence(req.ID)
	if err != nil {
		var be *buyError
		status := http.StatusInternalServerError
		if errors.As(err, &be) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, errBody(err))
		s.auditAPI(r, status, start)
		return
	}
	s.auditAPI(r, http.StatusOK, start)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(bundle)
}

// handleRefund calls escrow.withdraw() — the post-termination clawback.
func (s *apiServer) handleRefund(w http.ResponseWriter, r *http.Request) {
	s.buyAction(w, r, "refund",
		func(ctx context.Context, id string, _ map[string]any) (*purchase, error) {
			return s.buyer.refund(ctx, id)
		})
}

// handleRelease manually releases a verified purchase (auto-release off).
func (s *apiServer) handleRelease(w http.ResponseWriter, r *http.Request) {
	s.buyAction(w, r, "release",
		func(ctx context.Context, id string, _ map[string]any) (*purchase, error) {
			return s.buyer.releaseByID(ctx, id)
		})
}

// buyAction is the shared {id}+verb plumbing for dispute/refund/release —
// each submits a settlement tx (queued for approval on real chains) and
// returns the updated purchase.
func (s *apiServer) buyAction(
	w http.ResponseWriter, r *http.Request, verb string,
	fn func(context.Context, string, map[string]any) (*purchase, error),
) {
	start := time.Now()
	var req struct {
		ID     string `json:"id"`
		Reason string `json:"reason,omitempty"`
	}
	if !s.decodeBody(w, r, &req) {
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	if s.buyer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{"code": "not_ready", "detail": "buy side not up"},
		})
		s.auditAPI(r, http.StatusServiceUnavailable, start)
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": "bad_request", "detail": "id required"},
		})
		s.auditAPI(r, http.StatusBadRequest, start)
		return
	}
	p, err := fn(r.Context(), req.ID, map[string]any{"reason": req.Reason})
	if err != nil {
		var be *buyError
		status := http.StatusInternalServerError
		if errors.As(err, &be) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, errBody(err))
		s.auditAPI(r, status, start)
		return
	}
	s.auditAPI(r, http.StatusOK, start)
	writeJSON(w, http.StatusOK, map[string]any{
		"purchase": s.buyer.snapshot(p), "verb": verb,
	})
}

// errBody wraps an error into the API's {"error":{code,detail}} shape;
// buyError codes pass through, everything else gets "internal".
func errBody(err error) map[string]any {
	code := "internal"
	if be, ok := err.(*buyError); ok {
		code = be.code
	}
	return map[string]any{
		"error": map[string]any{"code": code, "detail": err.Error()},
	}
}

// decodeBody reads a bounded JSON body; false = response already written.
func (s *apiServer) decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, apiBodyMaxBytes+1))
	if err != nil || len(body) > apiBodyMaxBytes {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": "bad_request", "detail": "body unreadable or >1 MiB"},
		})
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"code": "bad_request", "detail": "body is not a JSON object"},
		})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

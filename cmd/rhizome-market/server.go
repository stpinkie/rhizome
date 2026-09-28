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
	moduleDir string, token *tokenProvider, mgr *sessionMgr,
	audit *auditLogger, version string,
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
		audit:     audit,
		started:   time.Now(),
		version:   version,
	}
	s.bridge = func() bridgeStatus { return bridgeStatus{} }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.wrap(s.handleHealth))
	mux.HandleFunc("POST /v1/find", s.wrap(s.handleNotImplemented("find", 103)))
	mux.HandleFunc("POST /v1/buy", s.wrap(s.handleNotImplemented("buy", 103)))
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
type sessionLookup struct {
	SessionID string `json:"session_id"`
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
	sess := s.mgr.lookup(req.SessionID)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{
				"code":   "not_found",
				"detail": "no live session " + req.SessionID,
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
		if sess := s.mgr.lookup(req.SessionID); sess != nil {
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
	rc, err := loadReceipt(s.moduleDir, req.SessionID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{
				"code":   "not_found",
				"detail": "no receipt for session " + req.SessionID,
			},
		})
		s.auditAPI(r, http.StatusNotFound, start)
		return
	}
	s.auditAPI(r, http.StatusOK, start)
	writeJSON(w, http.StatusOK, rc)
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

// handleNotImplemented returns the honest posture for verbs whose business
// logic lands in Tracks 102/103 — a track-tagged JSON-RPC-style error, not
// a fake empty result.
func (s *apiServer) handleNotImplemented(verb string, track int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body, err := io.ReadAll(io.LimitReader(r.Body, apiBodyMaxBytes+1))
		if err != nil || len(body) > apiBodyMaxBytes {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{"code": "bad_request", "detail": "body unreadable or >1 MiB"},
			})
			s.auditAPI(r, http.StatusBadRequest, start)
			return
		}
		var v map[string]any
		if err := json.Unmarshal(body, &v); err != nil && len(strings.TrimSpace(string(body))) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{"code": "bad_request", "detail": "body is not a JSON object"},
			})
			s.auditAPI(r, http.StatusBadRequest, start)
			return
		}
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": map[string]any{
				"code":   "not_implemented",
				"detail": fmt.Sprintf("market %s lands in Track %d — this build serves the skeleton only", verb, track),
				"track":  track,
			},
		})
		s.auditAPI(r, http.StatusNotImplemented, start)
	}
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

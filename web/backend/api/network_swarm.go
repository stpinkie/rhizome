package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/web/backend/utils"
)

// Swarm endpoints: /api/network/swarms proxies the daemon's /network/swarms
// API. Reads fall back to the persisted <RHIZOME_HOME>/swarms.json roster
// merged with swarm.memberships from config; join/leave falls back to a
// config edit. The SSE event stream requires a running daemon.
func (h *Handler) registerNetworkSwarmRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/network/swarms", h.handleNetworkSwarms)
	mux.HandleFunc("POST /api/network/swarms", h.handleNetworkSwarms)
	mux.HandleFunc("GET /api/network/swarms/", h.handleNetworkSwarms)
	mux.HandleFunc("POST /api/network/swarms/", h.handleNetworkSwarms)
	mux.HandleFunc("GET /api/network/swarms/events", h.handleNetworkSwarmEvents)
}

// swarmMember is the member shape shared with the daemon's swarm.MemberInfo.
type swarmMember struct {
	PeerID      string `json:"peer_id"`
	LastSeen    string `json:"last_seen,omitempty"`
	Source      string `json:"source,omitempty"`
	CapDigest   string `json:"cap_digest,omitempty"`
	ActiveTasks int    `json:"active_tasks,omitempty"`
}

type swarmInfo struct {
	ID       string        `json:"id"`
	Joined   bool          `json:"joined"`
	JoinedAt string        `json:"joined_at,omitempty"`
	Members  []swarmMember `json:"members,omitempty"`
}

type swarmStatusResponse struct {
	PeerID string      `json:"peer_id,omitempty"`
	Swarms []swarmInfo `json:"swarms,omitempty"`
}

func (h *Handler) handleNetworkSwarms(w http.ResponseWriter, r *http.Request) {
	timeout, err := parseNetworkTimeout(r.URL.Query().Get("timeout"))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "invalid timeout: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if h.gatewayAvailableForProxy() {
		var bodyReader io.Reader
		if r.Method == http.MethodPost && r.Body != nil {
			raw, rerr := io.ReadAll(r.Body)
			if rerr != nil {
				respondNetworkError(w, http.StatusBadRequest, "read request body: "+rerr.Error())
				return
			}
			bodyReader = bytes.NewReader(raw)
		}
		output, err := h.networkSwarmsFromGateway(ctx, r, bodyReader, timeout)
		if err == nil {
			h.invalidateNetworkCache()
			w.Header().Set("Content-Type", "application/json")
			w.Write(output)
			return
		}
		logger.Warnf("swarm gateway fetch failed, falling back to local state: %v", err)
	}

	if r.Method == http.MethodPost {
		h.swarmActionFromConfig(w, r)
		return
	}
	h.swarmsFromLocal(w, r)
}

// networkSwarmsFromGateway forwards the request to the daemon's
// /network/swarms endpoint, preserving the sub-path (/<id>/members etc.).
func (h *Handler) networkSwarmsFromGateway(
	ctx context.Context,
	r *http.Request,
	body io.Reader,
	timeout time.Duration,
) ([]byte, error) {
	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		return nil, errors.New("gateway pid data unavailable")
	}

	u := h.gatewayProxyURL()
	u.Path = strings.Replace(r.URL.Path, "/api/network/swarms", "/network/swarms", 1)
	upstream := url.Values{}
	if v := r.URL.Query().Get("timeout"); v != "" {
		upstream.Set("timeout", v)
	}
	u.RawQuery = upstream.Encode()

	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// swarmsFile mirrors the daemon's persisted swarm registry.
type swarmsFile struct {
	Swarms map[string]struct {
		JoinedAt time.Time `json:"joined_at"`
		Members  []struct {
			PeerID      string    `json:"peer_id"`
			LastSeen    time.Time `json:"last_seen"`
			Source      string    `json:"source"`
			CapDigest   string    `json:"cap_digest,omitempty"`
			ActiveTasks int       `json:"active_tasks,omitempty"`
		} `json:"members"`
	} `json:"swarms"`
}

// swarmsFromLocal builds a swarm status response from config + the persisted
// roster file, for when the daemon is not running.
func (h *Handler) swarmsFromLocal(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}

	var file swarmsFile
	if data, err := os.ReadFile(filepath.Join(utils.GetRhizomeHome(), "swarms.json")); err == nil {
		_ = json.Unmarshal(data, &file)
	}

	merged := make(map[string]*swarmInfo)
	for _, id := range cfg.Swarm.Memberships {
		merged[id] = &swarmInfo{ID: id, Joined: true}
	}
	for id, sw := range file.Swarms {
		info, ok := merged[id]
		if !ok {
			info = &swarmInfo{ID: id, Joined: true}
			merged[id] = info
		}
		if !sw.JoinedAt.IsZero() {
			info.JoinedAt = sw.JoinedAt.UTC().Format(time.RFC3339)
		}
		for _, m := range sw.Members {
			info.Members = append(info.Members, swarmMember{
				PeerID:      m.PeerID,
				LastSeen:    m.LastSeen.UTC().Format(time.RFC3339),
				Source:      m.Source,
				CapDigest:   m.CapDigest,
				ActiveTasks: m.ActiveTasks,
			})
		}
	}

	// A sub-path request (/api/network/swarms/<id>[/<sub>]) narrows the
	// response to one swarm.
	rest := strings.TrimPrefix(r.URL.Path, "/api/network/swarms")
	rest = strings.TrimPrefix(rest, "/")
	if rest != "" {
		parts := strings.SplitN(rest, "/", 2)
		info, ok := merged[parts[0]]
		if !ok {
			respondNetworkError(w, http.StatusNotFound, "unknown swarm")
			return
		}
		sub := ""
		if len(parts) == 2 {
			sub = parts[1]
		}
		switch sub {
		case "":
			writeJSON(w, http.StatusOK, info)
		case "members":
			writeJSON(w, http.StatusOK, map[string]any{"swarm_id": parts[0], "members": info.Members})
		case "offers":
			writeJSON(w, http.StatusOK, map[string]any{"swarm_id": parts[0], "offers": []any{}})
		default:
			respondNetworkError(w, http.StatusNotFound, "unknown sub-resource")
		}
		return
	}

	resp := swarmStatusResponse{Swarms: make([]swarmInfo, 0, len(merged))}
	for _, info := range merged {
		resp.Swarms = append(resp.Swarms, *info)
	}
	writeJSON(w, http.StatusOK, resp)
}

// swarmActionFromConfig applies join/leave to swarm.memberships when the
// daemon is unavailable.
func (h *Handler) swarmActionFromConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Swarm  string `json:"swarm"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondNetworkError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	body.Swarm = strings.TrimSpace(body.Swarm)
	if body.Swarm == "" {
		respondNetworkError(w, http.StatusBadRequest, "swarm id is required")
		return
	}
	add := false
	switch strings.ToLower(strings.TrimSpace(body.Action)) {
	case "join":
		add = true
	case "leave":
	default:
		respondNetworkError(w, http.StatusBadRequest, "invalid action: expected 'join' or 'leave'")
		return
	}

	h.configMu.Lock()
	defer h.configMu.Unlock()
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	var memberships []string
	seen := make(map[string]bool)
	if add {
		memberships = append(memberships, body.Swarm)
		seen[body.Swarm] = true
	}
	for _, m := range cfg.Swarm.Memberships {
		if m == body.Swarm && !add {
			continue
		}
		if !seen[m] {
			memberships = append(memberships, m)
			seen[m] = true
		}
	}
	cfg.Swarm.Memberships = memberships
	if add {
		cfg.Swarm.Enabled = true
	}
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}
	h.invalidateNetworkCache()
	writeJSON(w, http.StatusOK, map[string]any{
		"swarm":   body.Swarm,
		"action":  body.Action,
		"warning": "daemon not running; applied to config (effective on next daemon start)",
	})
}

// handleNetworkSwarmEvents proxies the daemon's swarm SSE stream. There is
// no offline fallback — events require a running daemon.
func (h *Handler) handleNetworkSwarmEvents(w http.ResponseWriter, r *http.Request) {
	if !h.gatewayAvailableForProxy() {
		respondNetworkError(w, http.StatusServiceUnavailable, "daemon required for swarm events")
		return
	}
	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		respondNetworkError(w, http.StatusServiceUnavailable, "gateway pid data unavailable")
		return
	}

	u := h.gatewayProxyURL()
	u.Path = "/network/swarms/events"
	u.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := (&http.Client{}).Do(req) // no client timeout: SSE is long-lived
	if err != nil {
		respondNetworkError(w, http.StatusBadGateway, "connect to daemon: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		respondNetworkError(w, resp.StatusCode, strings.TrimSpace(string(body)))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

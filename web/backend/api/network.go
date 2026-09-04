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
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/web/backend/utils"
)

type networkCacheEntry struct {
	body    []byte
	expires time.Time
}

const (
	defaultNetworkTimeout = 10 * time.Second
	maxNetworkTimeout     = 5 * time.Minute
	networkCacheTTL       = 5 * time.Second
)

var (
	findRhizomeBinaryForNetwork = utils.FindRhizomeBinary
	runNetworkStatus            = executeNetworkStatus
)

func (h *Handler) registerNetworkRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/network/status", h.handleNetworkStatus)
	mux.HandleFunc("GET /api/network/peers", h.handleNetworkPeers)
	mux.HandleFunc("GET /api/network/dht", h.handleNetworkDHT)
	mux.HandleFunc("GET /api/network/saved-peers", h.handleNetworkSavedPeers)
	mux.HandleFunc("POST /api/network/saved-peers", h.handleNetworkSavedPeers)
	mux.HandleFunc("DELETE /api/network/saved-peers", h.handleNetworkSavedPeers)
}

func (h *Handler) handleNetworkPeers(w http.ResponseWriter, r *http.Request) {
	h.handleNetworkStatus(w, r)
}

func (h *Handler) handleNetworkDHT(w http.ResponseWriter, r *http.Request) {
	h.handleNetworkStatus(w, r)
}

func (h *Handler) handleNetworkStatus(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	timeout, err := parseNetworkTimeout(query.Get("timeout"))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "invalid timeout: "+err.Error())
		return
	}

	for _, b := range query["bootstrap"] {
		if strings.TrimSpace(b) == "" {
			respondNetworkError(w, http.StatusBadRequest, "empty bootstrap multiaddr")
			return
		}
	}

	cacheKey := networkCacheKey("status", query)
	h.networkCacheMu.Lock()
	if entry, ok := h.networkCache[cacheKey]; ok && time.Now().Before(entry.expires) {
		h.networkCacheMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(entry.body)
		return
	}
	h.networkCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// A custom listen address cannot be applied to the running daemon's
	// already-bound listeners; in that case use the CLI fallback directly.
	// Bootstrap overrides are now honored by the daemon endpoint.
	hasListen := len(query["listen"]) > 0

	var output []byte
	if !hasListen {
		output, err = h.networkStatusFromGateway(ctx, query, timeout)
		if err != nil {
			logger.Warnf("network status gateway fetch failed, falling back to CLI: %v", err)
		}
	}
	if len(output) == 0 {
		output, err = h.networkStatusFromCLI(ctx, query, timeout)
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			respondNetworkError(w, http.StatusGatewayTimeout, "network status timed out")
			return
		}
		respondNetworkError(w, http.StatusBadGateway, err.Error())
		return
	}

	if !json.Valid(output) {
		respondNetworkError(w, http.StatusBadGateway, "network status returned invalid JSON")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	h.networkCacheMu.Lock()
	h.networkCache[cacheKey] = networkCacheEntry{body: output, expires: time.Now().Add(networkCacheTTL)}
	h.networkCacheMu.Unlock()
	w.Write(output)
}

func (h *Handler) networkStatusFromGateway(ctx context.Context, query url.Values, timeout time.Duration) ([]byte, error) {
	if !h.gatewayAvailableForProxy() {
		return nil, errors.New("gateway not available for proxy")
	}

	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		return nil, errors.New("gateway pid data unavailable")
	}

	u := h.gatewayProxyURL()
	u.Path = "/network/status"

	// Forward only parameters the daemon endpoint understands.
	upstream := url.Values{}
	for _, b := range query["bootstrap"] {
		if strings.TrimSpace(b) != "" {
			upstream.Add("bootstrap", b)
		}
	}
	if t := query.Get("timeout"); t != "" {
		upstream.Set("timeout", t)
	}
	if v := query.Get("trust"); v != "" {
		upstream.Set("trust", v)
	}
	u.RawQuery = upstream.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (h *Handler) networkStatusFromCLI(ctx context.Context, query map[string][]string, timeout time.Duration) ([]byte, error) {
	execPath := findRhizomeBinaryForNetwork()
	if execPath == "" {
		return nil, errors.New("rhizome binary not found")
	}

	args := []string{"network", "status", "--peers", "--dht", "--json", "--timeout", timeout.String()}
	for _, b := range query["bootstrap"] {
		args = append(args, "--bootstrap", b)
	}
	for _, l := range query["listen"] {
		args = append(args, "--listen", l)
	}
	for _, v := range query["trust"] {
		if strings.TrimSpace(v) == "true" {
			args = append(args, "--trust")
			break
		}
	}

	env := append(os.Environ(), config.EnvHome+"="+utils.GetRhizomeHome())
	if h.configPath != "" {
		env = append(env, config.EnvConfig+"="+h.configPath)
	}

	output, stderr, err := runNetworkStatus(ctx, execPath, args, env)
	if err != nil {
		msg := trimNetworkError(stderr)
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return output, nil
}

func parseNetworkTimeout(v string) (time.Duration, error) {
	if v == "" {
		return defaultNetworkTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("timeout must be positive")
	}
	if d > maxNetworkTimeout {
		return 0, errors.New("timeout exceeds maximum")
	}
	return d, nil
}

func networkCacheKey(kind string, query map[string][]string) string {
	parts := make([]string, 0, len(query)+1)
	for k, vs := range query {
		sort.Strings(vs)
		parts = append(parts, k+"="+strings.Join(vs, ","))
	}
	sort.Strings(parts)
	return kind + "?" + strings.Join(parts, "&")
}

func respondNetworkError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	logger.Warnf("network API error %d: %s", code, msg)
}

func trimNetworkError(output []byte) string {
	s := string(bytes.TrimSpace(output))
	if s == "" {
		return "network status command failed"
	}
	if i := strings.IndexAny(s, "\r\n"); i > 0 {
		s = s[:i]
	}
	return s
}

func executeNetworkStatus(ctx context.Context, execPath string, args, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, execPath, args...)
	cmd.Env = env
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	err := cmd.Run()
	return outb.Bytes(), errb.Bytes(), err
}

// savedPeer and savedPeersResponse are the JSON shapes returned by
// /api/network/saved-peers. They match the daemon's SavedPeer / SavedPeersResponse.
type savedPeer struct {
	PeerID         string              `json:"peer_id"`
	BootstrapAddrs []string            `json:"bootstrap_addrs,omitempty"`
	Trusted        bool                `json:"trusted"`
	Connected      bool                `json:"connected"`
	Capability     savedPeerCapability `json:"capability,omitempty"`
}

type savedPeerCapability struct {
	Models []string `json:"models,omitempty"`
	Skills []string `json:"skills,omitempty"`
	Agents []string `json:"agents,omitempty"`
}

type savedPeersResponse struct {
	PeerID     string      `json:"peer_id"`
	SavedPeers []savedPeer `json:"saved_peers"`
}

func (h *Handler) handleNetworkSavedPeers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSavedPeers(w, r)
	case http.MethodPost:
		h.untrustSavedPeer(w, r)
	case http.MethodDelete:
		h.removeSavedPeer(w, r)
	default:
		respondNetworkError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) listSavedPeers(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	timeout, err := parseNetworkTimeout(query.Get("timeout"))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "invalid timeout: "+err.Error())
		return
	}

	cacheKey := networkCacheKey("saved-peers", query)
	h.networkCacheMu.Lock()
	if entry, ok := h.networkCache[cacheKey]; ok && time.Now().Before(entry.expires) {
		h.networkCacheMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write(entry.body)
		return
	}
	h.networkCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if h.gatewayAvailableForProxy() {
		output, err := h.savedPeersFromGateway(ctx, http.MethodGet, query, timeout)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			h.networkCacheMu.Lock()
			h.networkCache[cacheKey] = networkCacheEntry{body: output, expires: time.Now().Add(networkCacheTTL)}
			h.networkCacheMu.Unlock()
			w.Write(output)
			return
		}
		logger.Warnf("saved peers gateway fetch failed, falling back to config: %v", err)
	}

	resp, err := h.savedPeersFromConfig()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}

	body, err := json.Marshal(resp)
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "encode response: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	h.networkCacheMu.Lock()
	h.networkCache[cacheKey] = networkCacheEntry{body: body, expires: time.Now().Add(networkCacheTTL)}
	h.networkCacheMu.Unlock()
	w.Write(body)
}

func (h *Handler) untrustSavedPeer(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	peerID := strings.TrimSpace(query.Get("peer"))
	if peerID == "" {
		respondNetworkError(w, http.StatusBadRequest, "peer id is required")
		return
	}
	if strings.ContainsAny(peerID, " \t\r\n") {
		respondNetworkError(w, http.StatusBadRequest, "peer id contains whitespace")
		return
	}

	timeout, err := parseNetworkTimeout(query.Get("timeout"))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "invalid timeout: "+err.Error())
		return
	}

	if h.gatewayAvailableForProxy() {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		output, err := h.savedPeersFromGateway(ctx, http.MethodPost, query, timeout)
		if err == nil {
			h.invalidateNetworkCache()
			w.Header().Set("Content-Type", "application/json")
			w.Write(output)
			return
		}
		logger.Warnf("saved peers gateway untrust failed, falling back to config: %v", err)
	}

	resp, err := h.savedPeersFromConfig()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}

	wasKnown := false
	for _, p := range resp.SavedPeers {
		if p.PeerID == peerID {
			wasKnown = true
			break
		}
	}
	if !wasKnown {
		respondNetworkError(w, http.StatusNotFound, "peer not found in saved peers")
		return
	}

	if err := h.untrustSavedPeerInConfig(peerID); err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.invalidateNetworkCache()
	resp, err = h.savedPeersFromConfig()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}

	for _, p := range resp.SavedPeers {
		if p.PeerID == peerID {
			writeJSON(w, http.StatusOK, p)
			return
		}
	}
	respondNetworkError(w, http.StatusNotFound, "peer not found in saved peers")
}

func (h *Handler) removeSavedPeer(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	peerID := strings.TrimSpace(query.Get("peer"))
	if peerID == "" {
		respondNetworkError(w, http.StatusBadRequest, "peer id is required")
		return
	}
	if strings.ContainsAny(peerID, " \t\r\n") {
		respondNetworkError(w, http.StatusBadRequest, "peer id contains whitespace")
		return
	}

	timeout, err := parseNetworkTimeout(query.Get("timeout"))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "invalid timeout: "+err.Error())
		return
	}

	if h.gatewayAvailableForProxy() {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		_, err := h.savedPeersFromGateway(ctx, http.MethodDelete, query, timeout)
		if err == nil {
			h.invalidateNetworkCache()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		logger.Warnf("saved peers gateway remove failed, falling back to config: %v", err)
	}

	resp, err := h.savedPeersFromConfig()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}

	wasKnown := false
	for _, p := range resp.SavedPeers {
		if p.PeerID == peerID {
			wasKnown = true
			break
		}
	}
	if !wasKnown {
		respondNetworkError(w, http.StatusNotFound, "peer not found in saved peers")
		return
	}

	if err := h.removeSavedPeerFromConfig(peerID); err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.invalidateNetworkCache()
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) savedPeersFromConfig() (savedPeersResponse, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return savedPeersResponse{}, fmt.Errorf("load config: %w", err)
	}

	return buildSavedPeersResponseFromConfig(cfg), nil
}

func (h *Handler) untrustSavedPeerInConfig(peerID string) error {
	h.configMu.Lock()
	defer h.configMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cfg.Mesh.TrustedPeers = filterStringSlice(cfg.Mesh.TrustedPeers, func(s string) bool { return s != peerID })

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func (h *Handler) removeSavedPeerFromConfig(peerID string) error {
	h.configMu.Lock()
	defer h.configMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	cfg.Mesh.TrustedPeers = filterStringSlice(cfg.Mesh.TrustedPeers, func(s string) bool { return s != peerID })
	cfg.Mesh.BootstrapPeers = filterStringSlice(cfg.Mesh.BootstrapPeers, func(b string) bool {
		return !isBootstrapForPeer(b, peerID)
	})

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func (h *Handler) savedPeersFromGateway(ctx context.Context, method string, query url.Values, timeout time.Duration) ([]byte, error) {
	if !h.gatewayAvailableForProxy() {
		return nil, errors.New("gateway not available for proxy")
	}

	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		return nil, errors.New("gateway pid data unavailable")
	}

	u := h.gatewayProxyURL()
	u.Path = "/network/saved-peers"
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (h *Handler) invalidateNetworkCache() {
	h.networkCacheMu.Lock()
	defer h.networkCacheMu.Unlock()
	h.networkCache = make(map[string]networkCacheEntry)
}

func buildSavedPeersResponseFromConfig(cfg *config.Config) savedPeersResponse {
	m := make(map[string]*savedPeer)

	for _, p := range cfg.Mesh.TrustedPeers {
		if p == "" {
			continue
		}
		if _, ok := m[p]; !ok {
			m[p] = &savedPeer{PeerID: p}
		}
		m[p].Trusted = true
	}

	for _, b := range cfg.Mesh.BootstrapPeers {
		if b == "" {
			continue
		}
		pid, ok := peerIDFromBootstrapMultiaddr(b)
		if !ok {
			continue
		}
		if _, ok := m[pid]; !ok {
			m[pid] = &savedPeer{PeerID: pid}
		}
		m[pid].BootstrapAddrs = appendUniqueString(m[pid].BootstrapAddrs, b)
	}

	out := make([]savedPeer, 0, len(m))
	for _, p := range m {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })

	return savedPeersResponse{SavedPeers: out}
}

func peerIDFromBootstrapMultiaddr(addr string) (string, bool) {
	pid, err := rnet.PeerIDFromMultiaddr(addr)
	if err != nil {
		return "", false
	}
	return pid.String(), true
}

func isBootstrapForPeer(bootstrap, peerID string) bool {
	got, ok := peerIDFromBootstrapMultiaddr(bootstrap)
	return ok && got == peerID
}

func filterStringSlice(items []string, keep func(string) bool) []string {
	out := items[:0]
	for _, s := range items {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

func appendUniqueString(items []string, value string) []string {
	for _, s := range items {
		if s == value {
			return items
		}
	}
	return append(items, value)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

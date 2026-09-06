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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
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
	mux.HandleFunc("GET /api/network/tasks", h.handleNetworkTasks)
	mux.HandleFunc("POST /api/network/tasks", h.handleNetworkTasks)
	mux.HandleFunc("GET /api/network/tasks/events", h.handleNetworkTaskEvents)
	mux.HandleFunc("GET /api/network/audit", h.handleNetworkAudit)
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

// --- Remote task operations ---
//
// /api/network/tasks proxies the daemon's /network/tasks endpoint. When the
// daemon is unavailable, read ops and cancel fall back to spawning
// `rhizome network task` against the peer's saved bootstrap address. Submit
// requires the daemon because a temporary node cannot share the daemon's
// live connections, so a submit whose gateway proxy fails returns 503
// instead of falling back to the CLI.
func (h *Handler) handleNetworkTasks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	isSubmit := r.Method == http.MethodPost && query.Get("action") == ""

	// Validate the peer up front. For GET/cancel it comes from the query
	// string; for submit it comes from the JSON body, which we buffer here
	// so the gateway proxy and any fallback can both read it.
	var submitBody []byte
	peerID := strings.TrimSpace(query.Get("peer"))
	if isSubmit {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			respondNetworkError(w, http.StatusBadRequest, "read request body: "+err.Error())
			return
		}
		var parsed struct {
			Peer string `json:"peer"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			respondNetworkError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}
		peerID = strings.TrimSpace(parsed.Peer)
		submitBody = raw
	}
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
	// Result long-polls need the HTTP timeout to exceed the requested wait.
	if wait, werr := time.ParseDuration(query.Get("wait")); werr == nil && wait > 0 {
		if wait+15*time.Second > timeout {
			timeout = wait + 15*time.Second
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if h.gatewayAvailableForProxy() {
		var bodyReader io.Reader
		if isSubmit {
			bodyReader = bytes.NewReader(submitBody)
		}
		output, err := h.networkTasksFromGateway(ctx, r.Method, query, bodyReader, timeout)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write(output)
			return
		}
		logger.Warnf("network tasks gateway request failed: %v", err)
		// Submit requires the daemon's live connections; a temporary node
		// cannot serve it, so do not fall back to the CLI.
		if isSubmit {
			respondNetworkError(w, http.StatusServiceUnavailable, errDaemonRequired.Error())
			return
		}
		logger.Warnf("falling back to CLI for network task request")
	} else if isSubmit {
		// No gateway detected at all — submit is impossible without it.
		respondNetworkError(w, http.StatusServiceUnavailable, errDaemonRequired.Error())
		return
	}

	output, err := h.networkTasksFromCLI(ctx, r.Method, query)
	if err != nil {
		if errors.Is(err, errDaemonRequired) {
			respondNetworkError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if ctx.Err() == context.DeadlineExceeded {
			respondNetworkError(w, http.StatusGatewayTimeout, "network task request timed out")
			return
		}
		respondNetworkError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(output)
}

var errDaemonRequired = errors.New("operation requires a running rhizome daemon")

func (h *Handler) networkTasksFromGateway(ctx context.Context, method string, query url.Values, body io.Reader, timeout time.Duration) ([]byte, error) {
	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		return nil, errors.New("gateway pid data unavailable")
	}

	u := h.gatewayProxyURL()
	u.Path = "/network/tasks"
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
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

// networkTasksFromCLI maps the request onto `rhizome network task` using the
// peer's saved bootstrap address. Returns errDaemonRequired when no address
// is known or the op cannot be served by a temporary node. Submit is never
// routed here — it requires the daemon's live connections.
func (h *Handler) networkTasksFromCLI(ctx context.Context, method string, query url.Values) ([]byte, error) {
	execPath := findRhizomeBinaryForNetwork()
	if execPath == "" {
		return nil, errors.New("rhizome binary not found")
	}

	peerParam := strings.TrimSpace(query.Get("peer"))
	if peerParam == "" {
		return nil, errors.New("peer id is required")
	}

	maddr, err := h.peerMultiaddr(peerParam)
	if err != nil {
		return nil, err
	}

	var args []string
	switch {
	case method == http.MethodGet && query.Get("task") == "":
		args = []string{"network", "task", "list", maddr, "--json"}
	case method == http.MethodGet && query.Get("wait") != "":
		args = []string{"network", "task", "result", maddr, query.Get("task"), "--json", "--wait", query.Get("wait")}
	case method == http.MethodGet:
		args = []string{"network", "task", "status", maddr, query.Get("task"), "--json"}
	case method == http.MethodPost && strings.EqualFold(query.Get("action"), "cancel"):
		taskID := strings.TrimSpace(query.Get("task"))
		if taskID == "" {
			return nil, errors.New("task id is required")
		}
		args = []string{"network", "task", "cancel", maddr, taskID, "--json"}
	default:
		return nil, errDaemonRequired
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

// peerMultiaddr returns raw unchanged when it is already a multiaddr;
// otherwise it looks the peer id up in mesh.bootstrap_peers.
func (h *Handler) peerMultiaddr(raw string) (string, error) {
	if strings.Contains(raw, "/") {
		return raw, nil
	}
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	for _, b := range cfg.Mesh.BootstrapPeers {
		if isBootstrapForPeer(b, raw) {
			return b, nil
		}
	}
	return "", errDaemonRequired
}

// --- Mesh audit trail ---

// handleNetworkAudit serves the tail of the daemon's mesh audit log. Without
// a running daemon it reads the local audit file directly — the file lives
// under RHIZOME_HOME and is shared by any daemon using that home.
func (h *Handler) handleNetworkAudit(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	tail := 50
	if v := strings.TrimSpace(query.Get("tail")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 1000 {
			respondNetworkError(w, http.StatusBadRequest, "invalid tail: must be 1-1000")
			return
		}
		tail = n
	}

	if h.gatewayAvailableForProxy() {
		ctx, cancel := context.WithTimeout(r.Context(), defaultNetworkTimeout)
		defer cancel()
		output, err := h.networkAuditFromGateway(ctx, query)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write(output)
			return
		}
		logger.Warnf("network audit gateway fetch failed, reading local file: %v", err)
	}

	entries, err := mesh.ReadAuditTail(filepath.Join(utils.GetRhizomeHome(), "mesh-audit.jsonl"), tail)
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "read audit log: "+err.Error())
		return
	}
	if entries == nil {
		entries = []json.RawMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"count":   len(entries),
	})
}

func (h *Handler) networkAuditFromGateway(ctx context.Context, query url.Values) ([]byte, error) {
	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		return nil, errors.New("gateway pid data unavailable")
	}

	u := h.gatewayProxyURL()
	u.Path = "/network/audit"
	upstream := url.Values{}
	if t := query.Get("tail"); t != "" {
		upstream.Set("tail", t)
	}
	u.RawQuery = upstream.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)

	client := &http.Client{Timeout: defaultNetworkTimeout}
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

// handleNetworkTaskEvents proxies the daemon's /network/tasks/events SSE
// stream. Without a running daemon it returns 503 because the launcher has no
// mesh event source of its own.
func (h *Handler) handleNetworkTaskEvents(w http.ResponseWriter, r *http.Request) {
	if !h.gatewayAvailableForProxy() {
		respondNetworkError(w, http.StatusServiceUnavailable, errDaemonRequired.Error())
		return
	}

	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		respondNetworkError(w, http.StatusServiceUnavailable, errDaemonRequired.Error())
		return
	}

	u := h.gatewayProxyURL()
	u.Path = "/network/tasks/events"
	u.RawQuery = r.URL.Query().Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)
	if id := r.Header.Get("Last-Event-ID"); id != "" {
		req.Header.Set("Last-Event-ID", id)
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		respondNetworkError(w, http.StatusBadGateway, err.Error())
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

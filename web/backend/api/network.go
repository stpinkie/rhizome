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

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	mux.HandleFunc("GET /api/network/peers", h.handleNetworkPeers)
	mux.HandleFunc("GET /api/network/dht", h.handleNetworkDHT)
}

func (h *Handler) handleNetworkPeers(w http.ResponseWriter, r *http.Request) {
	h.handleNetworkStatus(w, r, "peers")
}

func (h *Handler) handleNetworkDHT(w http.ResponseWriter, r *http.Request) {
	h.handleNetworkStatus(w, r, "dht")
}

func (h *Handler) handleNetworkStatus(w http.ResponseWriter, r *http.Request, kind string) {
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

	cacheKey := networkCacheKey(kind, query)
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

	execPath := findRhizomeBinaryForNetwork()
	if execPath == "" {
		respondNetworkError(w, http.StatusInternalServerError, "rhizome binary not found")
		return
	}

	args := []string{"network", "status", "--" + kind, "--json", "--timeout", timeout.String()}
	for _, b := range query["bootstrap"] {
		args = append(args, "--bootstrap", b)
	}
	for _, l := range query["listen"] {
		args = append(args, "--listen", l)
	}

	env := append(os.Environ(), config.EnvHome+"="+utils.GetRhizomeHome())
	if h.configPath != "" {
		env = append(env, config.EnvConfig+"="+h.configPath)
	}

	output, stderr, err := runNetworkStatus(ctx, execPath, args, env)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			respondNetworkError(w, http.StatusGatewayTimeout, "network status timed out")
			return
		}
		msg := trimNetworkError(stderr)
		if msg == "" {
			msg = err.Error()
		}
		respondNetworkError(w, http.StatusBadGateway, msg)
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

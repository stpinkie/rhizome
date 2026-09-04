package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
)

// networkStatusHandler serves a live, read-only snapshot of the daemon's mesh
// and DHT state. It is registered on the gateway's shared HTTP mux.
type networkStatusHandler struct {
	mesh       *mesh.Mesh
	authToken  string
	homePath   string
	configPath string
	saveMu     sync.Mutex
}

func newNetworkStatusHandler(m *mesh.Mesh, authToken, homePath, configPath string) http.Handler {
	return &networkStatusHandler{
		mesh:       m,
		authToken:  authToken,
		homePath:   homePath,
		configPath: configPath,
	}
}

func (h *networkStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use GET",
		})
		return
	}

	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	if h.mesh == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "mesh not available",
		})
		return
	}

	timeout, err := parseNetworkStatusTimeout(r.URL.Query().Get("timeout"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid timeout: " + err.Error(),
		})
		return
	}

	trust, err := parseNetworkStatusTrust(r.URL.Query().Get("trust"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid trust: " + err.Error(),
		})
		return
	}

	bootstrap := r.URL.Query()["bootstrap"]
	for _, b := range bootstrap {
		if err := validateBootstrapMultiaddr(b); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid bootstrap multiaddr: " + err.Error(),
			})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	type connectResult struct {
		addr string
		err  error
	}
	connectResults := make([]connectResult, len(bootstrap))

	var wg sync.WaitGroup
	for i, b := range bootstrap {
		wg.Add(1)
		go func(idx int, addr string) {
			defer wg.Done()
			err := h.mesh.Connect(ctx, addr)
			if err != nil {
				logger.Warnf("network status connect to %s: %v", addr, err)
			}
			connectResults[idx] = connectResult{addr: addr, err: err}
		}(i, b)
	}
	wg.Wait()

	if trust {
		for _, res := range connectResults {
			if res.err != nil {
				continue
			}
			pid, err := rnet.PeerIDFromMultiaddr(res.addr)
			if err != nil {
				logger.Warnf("network status extract peer id from %s: %v", res.addr, err)
				continue
			}
			_, tdErr := h.mesh.TrustAndDiscover(ctx, pid)
			if tdErr != nil {
				logger.Warnf("network status trust and discover %s: %v", pid, tdErr)
			}
			// Only persist if capability exchange succeeded or we still have a live,
			// verified connection to the peer. This prevents persisting an unreachable
			// or bogus peer id extracted from an address that never completed the
			// libp2p security handshake.
			if tdErr != nil && !h.mesh.IsConnected(pid) {
				continue
			}
			if err := h.saveTrustedPeer(res.addr, pid); err != nil {
				logger.Warnf("network status save trusted peer %s: %v", pid, err)
			}
		}
	}

	identityPath := filepath.Join(h.homePath, "identity")
	status := h.mesh.NetworkStatus(identityPath)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

func (h *networkStatusHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

// extractBearerToken returns the token from an "Authorization: Bearer <t>"
// header, or the empty string if the header is missing or malformed.
func extractBearerToken(header string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return header[len(prefix):]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func parseNetworkStatusTimeout(v string) (time.Duration, error) {
	if v == "" {
		return 10 * time.Second, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("timeout must be positive")
	}
	if d > 2*time.Minute {
		return 0, errors.New("timeout exceeds maximum")
	}
	return d, nil
}

func validateBootstrapMultiaddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return errors.New("empty multiaddr")
	}
	m, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		return err
	}
	if _, err := peer.AddrInfoFromP2pAddr(m); err != nil {
		return fmt.Errorf("missing /p2p/ peer id")
	}
	return nil
}

func parseNetworkStatusTrust(v string) (bool, error) {
	if v == "" {
		return false, nil
	}
	return strconv.ParseBool(v)
}

func (h *networkStatusHandler) saveTrustedPeer(addr string, pid peer.ID) error {
	if h.configPath == "" {
		return errors.New("config path not available")
	}
	return saveTrustedPeer(h.configPath, &h.saveMu, addr, pid)
}

// saveTrustedPeer loads the config at configPath, appends addr and pid to the
// mesh bootstrap and trusted peer lists, and writes the file back. The mutex
// serializes callers within this process; concurrent writers in other processes
// may still race and the atomic write in SaveConfig ensures the file is not
// corrupted, but one side's changes may be lost. Callers should hold mu if
// non-nil.
func saveTrustedPeer(configPath string, mu *sync.Mutex, addr string, pid peer.ID) error {
	if mu != nil {
		mu.Lock()
		defer mu.Unlock()
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	peerID := pid.String()
	cfg.Mesh.BootstrapPeers = rnet.AppendUnique(cfg.Mesh.BootstrapPeers, strings.TrimSpace(addr))
	cfg.Mesh.TrustedPeers = rnet.AppendUnique(cfg.Mesh.TrustedPeers, peerID)

	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// networkStatusHandler serves a live, read-only snapshot of the daemon's mesh
// and DHT state. It is registered on the gateway's shared HTTP mux.
type networkStatusHandler struct {
	mesh      *mesh.Mesh
	authToken string
	homePath  string
}

func newNetworkStatusHandler(m *mesh.Mesh, authToken, homePath string) http.Handler {
	return &networkStatusHandler{
		mesh:      m,
		authToken: authToken,
		homePath:  homePath,
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

	var wg sync.WaitGroup
	for _, b := range bootstrap {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			if err := h.mesh.Connect(ctx, addr); err != nil {
				logger.Warnf("network status connect to %s: %v", addr, err)
			}
		}(b)
	}
	wg.Wait()

	identityPath := filepath.Join(h.homePath, "identity")
	status := h.mesh.NetworkStatus(identityPath)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

func (h *networkStatusHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return true
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

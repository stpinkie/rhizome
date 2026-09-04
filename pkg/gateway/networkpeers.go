package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// networkSavedPeersHandler serves the daemon's saved-peer management endpoint.
type networkSavedPeersHandler struct {
	mesh       *mesh.Mesh
	authToken  string
	configPath string
	saveMu     sync.Mutex
}

func newNetworkSavedPeersHandler(m *mesh.Mesh, authToken, configPath string) http.Handler {
	return &networkSavedPeersHandler{
		mesh:       m,
		authToken:  authToken,
		configPath: configPath,
	}
}

func (h *networkSavedPeersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.list(w, r)
	case http.MethodPost:
		h.mutate(w, r)
	case http.MethodDelete:
		h.remove(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use GET/POST/DELETE",
		})
	}
}

func (h *networkSavedPeersHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

func (h *networkSavedPeersHandler) list(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "load config: " + err.Error(),
		})
		return
	}

	includeStatus := parseIncludeStatus(r.URL.Query().Get("include_status"))
	resp := mesh.BuildSavedPeersResponse(h.mesh, cfg, includeStatus)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *networkSavedPeersHandler) mutate(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("action")))
	if action != "untrust" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid action: expected 'untrust'",
		})
		return
	}

	peerID := strings.TrimSpace(r.URL.Query().Get("peer"))
	if peerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "peer id is required"})
		return
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		logger.Debugf("saved peers untrust: invalid peer id %q: %v", peerID, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid peer id: must be a valid libp2p peer ID",
		})
		return
	}

	h.saveMu.Lock()
	defer h.saveMu.Unlock()

	wasKnown, err := h.isPeerKnown(pid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load config: " + err.Error()})
		return
	}
	if !wasKnown {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "peer not found in saved peers"})
		return
	}

	if h.mesh != nil {
		// UntrustPeer cannot fail; it is applied before config persistence so the
		// peer is denied runtime access immediately even if the config save fails.
		h.mesh.UntrustPeer(pid)
	}

	if h.configPath != "" {
		if err := mesh.UntrustSavedPeer(h.configPath, nil, pid); err != nil {
			logger.Warnf("network saved peers untrust save: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "save config: " + err.Error(),
			})
			return
		}
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "reload config: " + err.Error(),
		})
		return
	}

	sp := mesh.BuildSavedPeerForPID(h.mesh, cfg, pid, true)
	writeJSON(w, http.StatusOK, sp)
}

func (h *networkSavedPeersHandler) remove(w http.ResponseWriter, r *http.Request) {
	peerID := strings.TrimSpace(r.URL.Query().Get("peer"))
	if peerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "peer id is required"})
		return
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		logger.Debugf("saved peers remove: invalid peer id %q: %v", peerID, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid peer id: must be a valid libp2p peer ID",
		})
		return
	}

	h.saveMu.Lock()
	defer h.saveMu.Unlock()

	wasKnown, err := h.isPeerKnown(pid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load config: " + err.Error()})
		return
	}
	if !wasKnown {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "peer not found in saved peers"})
		return
	}

	if h.mesh != nil {
		// UntrustPeer cannot fail; it is applied before config persistence so the
		// peer is denied runtime access immediately even if the config save fails.
		h.mesh.UntrustPeer(pid)
		if err := h.mesh.Disconnect(pid); err != nil {
			logger.Warnf("saved peers remove: failed to disconnect peer %s: %v", pid, err)
		}
	}

	if h.configPath != "" {
		if _, err := mesh.RemoveSavedPeer(h.configPath, nil, pid); err != nil {
			logger.Warnf("network saved peers remove save: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "save config: " + err.Error(),
			})
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *networkSavedPeersHandler) isPeerKnown(pid peer.ID) (bool, error) {
	if h.mesh != nil && h.mesh.IsTrusted(pid) {
		return true, nil
	}
	if h.configPath == "" {
		return false, nil
	}
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return false, err
	}
	sp := mesh.BuildSavedPeerForPID(h.mesh, cfg, pid, false)
	return sp.Trusted || len(sp.BootstrapAddrs) > 0, nil
}

func parseIncludeStatus(v string) bool {
	if v == "" {
		return true
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return true
	}
	return b
}

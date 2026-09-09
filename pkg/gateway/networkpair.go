package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stpinkie/rhizome/pkg/rhizome/pair"
)

// activePairManager holds the daemon's pairing manager so HTTP handlers can
// mint and redeem trust-pairing bundles.
var activePairManager atomic.Pointer[pair.Manager]

// SetPairManager registers the daemon's pairing manager (nil clears it).
func SetPairManager(pm *pair.Manager) {
	activePairManager.Store(pm)
}

func currentPairManager() *pair.Manager {
	return activePairManager.Load()
}

// networkPairHandler serves trust pairing on the daemon gateway:
//
//	POST /network/pair         {ttl}       — mint a pairing bundle
//	POST /network/pair/accept  {bundle}    — redeem a pairing bundle
type networkPairHandler struct {
	authToken string
}

func newNetworkPairHandler(authToken string) http.Handler {
	return &networkPairHandler{authToken: authToken}
}

func (h *networkPairHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authToken == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	if given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use POST",
		})
		return
	}
	pm := currentPairManager()
	if pm == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "pairing not available (mesh.enabled required)",
		})
		return
	}

	switch strings.TrimPrefix(r.URL.Path, "/network/pair") {
	case "", "/":
		h.create(w, r, pm)
	case "/accept":
		h.accept(w, r, pm)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource"})
	}
}

func (h *networkPairHandler) create(w http.ResponseWriter, r *http.Request, pm *pair.Manager) {
	var body struct {
		TTL string `json:"ttl,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body) // empty body is fine
	}
	ttl := pair.DefaultTTL
	if body.TTL != "" {
		d, err := time.ParseDuration(body.TTL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ttl: " + err.Error()})
			return
		}
		ttl = d
	}
	bundle, err := pm.Create(ttl)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bundle": bundle,
		"ttl":    ttl.String(),
	})
}

func (h *networkPairHandler) accept(w http.ResponseWriter, r *http.Request, pm *pair.Manager) {
	var body struct {
		Bundle string `json:"bundle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	if strings.TrimSpace(body.Bundle) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bundle is required"})
		return
	}
	peerID, err := pm.Accept(r.Context(), body.Bundle)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"peer_id": peerID,
		"paired":  true,
	})
}

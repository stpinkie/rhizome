package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

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

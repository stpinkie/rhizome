package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// registerEvolutionRoutes proxies the daemon's /evolution/* surface
// (status/drafts/records/skills reads + run/accept/reject mutations) to the
// dashboard. All endpoints are daemon-required — mutation and live status
// come from the running gateway; daemonless reads are served by the CLI.
func (h *Handler) registerEvolutionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/evolution/status", h.handleEvolutionProxy)
	mux.HandleFunc("GET /api/evolution/drafts", h.handleEvolutionProxy)
	mux.HandleFunc("GET /api/evolution/records", h.handleEvolutionProxy)
	mux.HandleFunc("GET /api/evolution/skills", h.handleEvolutionProxy)
	mux.HandleFunc("POST /api/evolution/run", h.handleEvolutionProxy)
	mux.HandleFunc("POST /api/evolution/drafts/{id}/accept", h.handleEvolutionProxy)
	mux.HandleFunc("POST /api/evolution/drafts/{id}/reject", h.handleEvolutionProxy)
}

// handleEvolutionProxy forwards /api/evolution/<sub> to the daemon's
// /evolution/<sub> preserving method, query, and body.
func (h *Handler) handleEvolutionProxy(w http.ResponseWriter, r *http.Request) {
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
	u.Path = strings.TrimPrefix(r.URL.Path, "/api")
	u.RawQuery = r.URL.Query().Encode()

	var bodyReader io.Reader
	if r.Body != nil {
		bodyReader = r.Body
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), bodyReader)
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)
	if r.Header.Get("Content-Type") != "" {
		req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	}

	// Cold-path runs can take minutes (LLM draft generation); accept/reject
	// and reads are fast. Bound by the request context — a client disconnect
	// cancels the proxy, not the daemon-side run.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		respondNetworkError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		respondNetworkError(w, http.StatusBadGateway, err.Error())
		return
	}
	// Forward the daemon's status + body verbatim (same convention as the
	// web3 proxy) so error payloads keep their {"error": ...} shape.
	writeModuleJSON(w, resp.StatusCode, json.RawMessage(body))
}

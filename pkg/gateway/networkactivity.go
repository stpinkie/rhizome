package gateway

import (
	"crypto/subtle"
	"net/http"
	"strconv"

	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// networkActivityHandler serves the in-memory mesh/swarm activity feed.
//
//	GET /network/activity?tail=N   recent mesh.*/swarm.* events (oldest first)
type networkActivityHandler struct {
	mesh      *mesh.Mesh
	authToken string
}

func newNetworkActivityHandler(m *mesh.Mesh, authToken string) http.Handler {
	return &networkActivityHandler{mesh: m, authToken: authToken}
}

func (h *networkActivityHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if h.mesh == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "mesh disabled"})
		return
	}
	tail := 0
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			tail = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": h.mesh.Activity(tail)})
}

func (h *networkActivityHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

// networkEventsHandler streams all mesh.*/swarm.* runtime events as SSE.
//
//	GET /network/events
type networkEventsHandler struct {
	mesh      *mesh.Mesh
	authToken string
}

func newNetworkEventsHandler(m *mesh.Mesh, authToken string) http.Handler {
	return &networkEventsHandler{mesh: m, authToken: authToken}
}

func (h *networkEventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if h.mesh == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "mesh disabled"})
		return
	}

	// Subscribe before committing to 200 so a missing event bus surfaces as a
	// normal error status rather than an SSE error event.
	events, cleanup, err := h.mesh.Events(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	defer cleanup()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			writeSSE(w, evt)
		}
	}
}

func (h *networkEventsHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

package gateway

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// networkSkillsHandler serves mesh skill distribution on the daemon
// gateway:
//
//	GET  /network/skills?peer=<id>        — list a peer's shareable skills
//	POST /network/skills/pull             — pull a skill bundle {peer, name}
type networkSkillsHandler struct {
	mesh      *mesh.Mesh
	authToken string
}

func newNetworkSkillsHandler(m *mesh.Mesh, authToken string) http.Handler {
	return &networkSkillsHandler{mesh: m, authToken: authToken}
}

func (h *networkSkillsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.authToken == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	if given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if h.mesh == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "mesh is not enabled",
		})
		return
	}

	sub := strings.TrimPrefix(r.URL.Path, "/network/skills")
	switch {
	case sub == "/pull" && r.Method == http.MethodPost:
		h.pull(w, r)
	case sub == "/pull":
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use POST",
		})
	case r.Method == http.MethodGet && (sub == "" || sub == "/"):
		h.list(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource"})
	}
}

func (h *networkSkillsHandler) list(w http.ResponseWriter, r *http.Request) {
	peerID := strings.TrimSpace(r.URL.Query().Get("peer"))
	if peerID == "" {
		// Local shareable skills when no peer is given.
		writeJSON(w, http.StatusOK, map[string]any{
			"peer_id": h.meshNodeID(),
			"skills":  h.meshLocalShareable(),
		})
		return
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid peer id"})
		return
	}
	list, err := h.mesh.ListPeerSkills(r.Context(), pid)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"peer_id": peerID,
		"skills":  list,
	})
}

func (h *networkSkillsHandler) pull(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Peer            string `json:"peer"`
		Name            string `json:"name"`
		AllowSuspicious bool   `json:"allow_suspicious,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	pid, err := peer.Decode(strings.TrimSpace(body.Peer))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid peer id"})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	ctx := r.Context()
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Minute {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	res, err := h.mesh.PullSkill(ctx, pid, body.Name, body.AllowSuspicious)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *networkSkillsHandler) meshNodeID() string {
	return h.mesh.PeerIDString()
}

func (h *networkSkillsHandler) meshLocalShareable() []string {
	return h.mesh.ShareableSkills()
}

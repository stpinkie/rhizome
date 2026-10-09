package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// networkEconomyHandler serves the paired-settlement mutation verbs on the
// daemon gateway (Track 141):
//
//	POST /network/economy/dispute  {task_id, reason}
//	POST /network/economy/resolve  {task_id, action: credit|drop}
//	POST /network/economy/settle   {peer, unit, backend, mark_only}
//
// Reads (balance, ledger, invoice) are daemonless — the CLI reads the
// ledger file directly. Mutations prefer the daemon so transitions emit
// mesh.econ.* events and land in the live ledger index; the CLI falls
// back to a direct file write when the daemon is down.
type networkEconomyHandler struct {
	mesh      *mesh.Mesh
	authToken string
}

func newNetworkEconomyHandler(m *mesh.Mesh, authToken string) http.Handler {
	return &networkEconomyHandler{mesh: m, authToken: authToken}
}

func (h *networkEconomyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use POST",
		})
		return
	}

	sub := strings.TrimPrefix(r.URL.Path, "/network/economy")
	switch sub {
	case "/dispute":
		h.dispute(w, r)
	case "/resolve":
		h.resolve(w, r)
	case "/settle":
		h.settle(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource"})
	}
}

func (h *networkEconomyHandler) dispute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID string `json:"task_id"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	if strings.TrimSpace(body.TaskID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id is required"})
		return
	}
	entries, err := h.mesh.EconomyDispute(strings.TrimSpace(body.TaskID), body.Reason)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id": body.TaskID,
		"entries": entries,
		"state":   econ.StateDisputed,
	})
}

func (h *networkEconomyHandler) resolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TaskID string `json:"task_id"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	if strings.TrimSpace(body.TaskID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id is required"})
		return
	}
	var drop bool
	switch strings.TrimSpace(body.Action) {
	case "credit":
		drop = false
	case "drop":
		drop = true
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "action must be credit or drop",
		})
		return
	}
	entries, err := h.mesh.EconomyResolve(strings.TrimSpace(body.TaskID), drop)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id": body.TaskID,
		"entries": entries,
		"action":  body.Action,
	})
}

func (h *networkEconomyHandler) settle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Peer     string `json:"peer"`
		Unit     string `json:"unit,omitempty"`
		Backend  string `json:"backend,omitempty"`
		MarkOnly bool   `json:"mark_only,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	if strings.TrimSpace(body.Peer) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "peer is required"})
		return
	}
	backend := strings.TrimSpace(body.Backend)
	if backend == "" {
		backend = "ledger"
	}
	switch backend {
	case "ledger":
		// ledger backend = bilateral book only; no external transfer rail.
	case "web3":
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "web3 close-out lands in Track 143 — use --backend ledger",
		})
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "backend must be ledger or web3",
		})
		return
	}
	if !body.MarkOnly {
		// ledger backend without mark_only runs the /rhizome/econ/1.0.0
		// handshake (Track 142): offer → peer verification → shared
		// settle_id on both ledgers.
		offer, entries, err := h.mesh.EconomySettle(
			r.Context(), strings.TrimSpace(body.Peer), strings.TrimSpace(body.Unit))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"peer":      body.Peer,
			"backend":   backend,
			"mark_only": false,
			"settle_id": offer.ID(),
			"offer":     offer,
			"entries":   entries,
		})
		return
	}
	offer, entries, err := h.mesh.EconomyMarkSettled(
		strings.TrimSpace(body.Peer), strings.TrimSpace(body.Unit))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"peer":      body.Peer,
		"backend":   backend,
		"mark_only": true,
		"settle_id": offer.ID(),
		"offer":     offer,
		"entries":   entries,
	})
}

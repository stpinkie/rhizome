package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/stpinkie/rhizome/pkg/config"
	rswarm "github.com/stpinkie/rhizome/pkg/rhizome/swarm"
)

// networkSwarmsHandler serves live swarm state and membership actions on the
// daemon's gateway HTTP mux.
//
//	GET  /network/swarms                       all swarms with rosters
//	GET  /network/swarms/<id>                  one swarm
//	GET  /network/swarms/<id>/members          member roster
//	GET  /network/swarms/<id>/offers           tracked offers for the swarm
//	POST /network/swarms  {swarm, action}      join or leave a swarm live,
//	                                           persisting swarm.memberships
//	GET  /network/swarms/events?swarm=<id>     SSE stream of swarm.* events
type networkSwarmsHandler struct {
	authToken  string
	configPath string
	saveMu     sync.Mutex
}

func newNetworkSwarmsHandler(authToken, configPath string) http.Handler {
	return &networkSwarmsHandler{authToken: authToken, configPath: configPath}
}

func (h *networkSwarmsHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

// ServeHTTP dispatches /network/swarms and /network/swarms/<id>[/<sub>].
func (h *networkSwarmsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	sw := currentSwarm()
	if sw == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "swarm not available (enable swarm.enabled with mesh.enabled)",
		})
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/network/swarms")
	rest = strings.TrimPrefix(rest, "/")

	switch r.Method {
	case http.MethodGet:
		h.read(w, r, sw, rest)
	case http.MethodPost:
		h.write(w, r, sw)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use GET/POST",
		})
	}
}

func (h *networkSwarmsHandler) read(w http.ResponseWriter, r *http.Request, sw *rswarm.Swarm, rest string) {
	// /network/swarms — full snapshot.
	if rest == "" {
		writeJSON(w, http.StatusOK, sw.Status())
		return
	}

	parts := strings.SplitN(rest, "/", 2)
	swarmID := parts[0]
	if !rswarm.ValidSwarmID(swarmID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid swarm id"})
		return
	}
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	switch sub {
	case "":
		for _, info := range sw.Swarms() {
			if info.ID == swarmID {
				writeJSON(w, http.StatusOK, info)
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown swarm"})
	case "members":
		writeJSON(w, http.StatusOK, map[string]any{
			"swarm_id": swarmID,
			"members":  sw.Members(swarmID),
		})
	case "offers":
		writeJSON(w, http.StatusOK, map[string]any{
			"swarm_id": swarmID,
			"offers":   sw.OffersFor(swarmID),
		})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource"})
	}
}

// swarmActionRequest is the JSON body accepted by POST /network/swarms.
type swarmActionRequest struct {
	Swarm  string `json:"swarm"`
	Action string `json:"action"` // join | leave
}

func (h *networkSwarmsHandler) write(w http.ResponseWriter, r *http.Request, sw *rswarm.Swarm) {
	// POST /network/swarms/<id>/offers — publish a task offer.
	// POST /network/swarms/<id>/run    — orchestrate a goal.
	rest := strings.TrimPrefix(r.URL.Path, "/network/swarms")
	rest = strings.TrimPrefix(rest, "/")
	if parts := strings.SplitN(rest, "/", 2); len(parts) == 2 {
		switch parts[1] {
		case "offers":
			h.submitOffer(w, r, sw, parts[0])
			return
		case "run":
			h.runGoal(w, r, sw, parts[0])
			return
		}
	}

	var body swarmActionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	body.Swarm = strings.TrimSpace(body.Swarm)
	if !rswarm.ValidSwarmID(body.Swarm) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid swarm id"})
		return
	}

	var err error
	switch strings.ToLower(strings.TrimSpace(body.Action)) {
	case "join":
		err = sw.Join(r.Context(), body.Swarm)
	case "leave":
		err = sw.Leave(r.Context(), body.Swarm)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid action: expected 'join' or 'leave'",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Persist membership so it survives restarts.
	if perr := h.saveMembership(body.Swarm, strings.EqualFold(body.Action, "join")); perr != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"swarm":   body.Swarm,
			"action":  body.Action,
			"warning": "applied live but not persisted: " + perr.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"swarm":  body.Swarm,
		"action": body.Action,
		"status": sw.Status(),
	})
}

// offerRequest is the JSON body accepted by POST /network/swarms/<id>/offers.
type offerRequest struct {
	AgentID string   `json:"agent_id"`
	Model   string   `json:"model,omitempty"`
	Task    string   `json:"task"`
	Tools   []string `json:"tools,omitempty"`
}

// submitOffer publishes a task offer to a swarm and returns the offer id.
// Claims resolve asynchronously; poll GET /network/swarms/<id>/offers.
func (h *networkSwarmsHandler) submitOffer(w http.ResponseWriter, r *http.Request, sw *rswarm.Swarm, swarmID string) {
	if !rswarm.ValidSwarmID(swarmID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid swarm id"})
		return
	}
	var body offerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	body.Task = strings.TrimSpace(body.Task)
	if body.Task == "" || body.AgentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id and task are required"})
		return
	}
	offerID, err := sw.Offer(r.Context(), swarmID, rswarm.OfferRequest{
		AgentID: body.AgentID,
		Model:   body.Model,
		Task:    body.Task,
		Tools:   body.Tools,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"offer_id": offerID,
		"swarm_id": swarmID,
		"status":   "open",
	})
}

// runGoalRequest is the JSON body accepted by POST /network/swarms/<id>/run.
type runGoalRequest struct {
	Goal    string `json:"goal"`
	AgentID string `json:"agent_id,omitempty"`
}

// runGoal orchestrates a goal across the swarm and returns the aggregated
// result. It blocks until subtasks finish or the request context ends.
func (h *networkSwarmsHandler) runGoal(w http.ResponseWriter, r *http.Request, sw *rswarm.Swarm, swarmID string) {
	if !rswarm.ValidSwarmID(swarmID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid swarm id"})
		return
	}
	var body runGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	if strings.TrimSpace(body.Goal) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "goal is required"})
		return
	}
	res, err := sw.RunGoal(r.Context(), swarmID, body.Goal, body.AgentID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// saveMembership updates swarm.memberships in config.json, mirroring the
// CLI's join/leave mutation.
func (h *networkSwarmsHandler) saveMembership(swarmID string, add bool) error {
	if h.configPath == "" {
		return errors.New("config path not available")
	}
	h.saveMu.Lock()
	defer h.saveMu.Unlock()

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	var memberships []string
	seen := make(map[string]bool)
	if add {
		memberships = append(memberships, swarmID)
		seen[swarmID] = true
	}
	for _, m := range cfg.Swarm.Memberships {
		if m == swarmID && !add {
			continue
		}
		if !seen[m] {
			memberships = append(memberships, m)
			seen[m] = true
		}
	}
	cfg.Swarm.Memberships = memberships
	if add {
		cfg.Swarm.Enabled = true
	}
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// networkSwarmEventsHandler streams swarm.* runtime events as SSE, with an
// optional ?swarm=<id> filter.
type networkSwarmEventsHandler struct {
	authToken string
}

func newNetworkSwarmEventsHandler(authToken string) http.Handler {
	return &networkSwarmEventsHandler{authToken: authToken}
}

func (h *networkSwarmEventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use GET",
		})
		return
	}
	if h.authToken == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	if subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	sw := currentSwarm()
	if sw == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "swarm not available (enable swarm.enabled with mesh.enabled)",
		})
		return
	}

	swarmID := strings.TrimSpace(r.URL.Query().Get("swarm"))
	if swarmID != "" && !rswarm.ValidSwarmID(swarmID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid swarm id"})
		return
	}

	events, cleanup, err := sw.Events(r.Context(), swarmID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "event bus not configured") {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
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

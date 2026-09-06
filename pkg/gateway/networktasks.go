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
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
)

// networkTasksHandler exposes the daemon's live mesh task operations
// (submit/status/result/cancel/list) over the gateway HTTP mux.
//
//	GET    /network/tasks?peer=<id>                      list this node's tasks on the peer
//	GET    /network/tasks?peer=<id>&task=<id>            task status
//	GET    /network/tasks?peer=<id>&task=<id>&wait=<dur> result (long-poll)
//	POST   /network/tasks  {peer, agent_id, model, task, tools[]}   submit
//	POST   /network/tasks?peer=<id>&task=<id>&action=cancel          cancel
type networkTasksHandler struct {
	mesh      *mesh.Mesh
	authToken string
}

func newNetworkTasksHandler(m *mesh.Mesh, authToken string) http.Handler {
	return &networkTasksHandler{mesh: m, authToken: authToken}
}

// taskSubmitRequest is the JSON body accepted by POST /network/tasks.
type taskSubmitRequest struct {
	Peer    string   `json:"peer"`
	AgentID string   `json:"agent_id"`
	Model   string   `json:"model,omitempty"`
	Task    string   `json:"task"`
	Tools   []string `json:"tools,omitempty"`
}

const (
	maxTaskWait        = 90 * time.Second
	defaultTaskTimeout = 15 * time.Second
)

func (h *networkTasksHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodPost:
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use GET/POST",
		})
		return
	}
	if h.mesh == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "mesh not available",
		})
		return
	}

	if r.Method == http.MethodGet {
		h.read(w, r)
	} else {
		h.write(w, r)
	}
}

func (h *networkTasksHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

// read handles list, status, and result requests.
func (h *networkTasksHandler) read(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	wait, err := parseTaskWait(query.Get("wait"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid wait: " + err.Error(),
		})
		return
	}
	timeout, err := parseTaskOpTimeout(query.Get("timeout"), wait)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid timeout: " + err.Error(),
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	pid, err := h.resolvePeer(ctx, query.Get("peer"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	taskID := strings.TrimSpace(query.Get("task"))
	if taskID == "" {
		tasks, err := h.mesh.ListRemoteTasks(ctx, pid)
		if err != nil {
			h.writeOpError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"peer_id": pid.String(),
			"tasks":   tasks,
		})
		return
	}

	var resp agenttask.Response
	if wait > 0 {
		resp, err = h.mesh.RemoteTaskResult(ctx, pid, taskID, wait)
	} else {
		resp, err = h.mesh.RemoteTaskStatus(ctx, pid, taskID)
	}
	if err != nil {
		h.writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// write handles submit (JSON body) and cancel (action=cancel).
func (h *networkTasksHandler) write(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	action := strings.TrimSpace(strings.ToLower(query.Get("action")))

	if action == "cancel" {
		h.cancel(w, r)
		return
	}
	if action != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid action: expected 'cancel'",
		})
		return
	}
	h.submit(w, r)
}

func (h *networkTasksHandler) submit(w http.ResponseWriter, r *http.Request) {
	var body taskSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON body: " + err.Error(),
		})
		return
	}
	body.Task = strings.TrimSpace(body.Task)
	if body.Task == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task is required"})
		return
	}
	if body.AgentID == "" {
		body.AgentID = "main"
	}

	timeout, err := parseTaskOpTimeout(r.URL.Query().Get("timeout"), 0)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid timeout: " + err.Error(),
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	pid, err := h.resolvePeer(ctx, body.Peer)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	usedPeer, taskID, err := h.mesh.SubmitRemoteTaskWithPeer(ctx, pid, mesh.RemoteCall{
		TargetAgentID: body.AgentID,
		Model:         body.Model,
		SystemPrompt:  body.Task,
		Tools:         body.Tools,
	})
	if err != nil {
		h.writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"task_id": taskID,
		"peer_id": usedPeer.String(),
	})
}

func (h *networkTasksHandler) cancel(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	taskID := strings.TrimSpace(query.Get("task"))
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task id is required"})
		return
	}

	timeout, err := parseTaskOpTimeout(query.Get("timeout"), 0)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid timeout: " + err.Error(),
		})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	pid, err := h.resolvePeer(ctx, query.Get("peer"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp, err := h.mesh.CancelRemoteTask(ctx, pid, taskID)
	if err != nil {
		h.writeOpError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolvePeer accepts either a bare peer id or a /p2p/ multiaddr. For a
// multiaddr it dials the peer first so status/result calls work right after
// a restart, before the reconnect loop has fired.
func (h *networkTasksHandler) resolvePeer(ctx context.Context, raw string) (peer.ID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("peer is required")
	}
	if strings.Contains(raw, "/") {
		if err := validateBootstrapMultiaddr(raw); err != nil {
			return "", fmt.Errorf("invalid peer multiaddr: %w", err)
		}
		pid, err := rnet.PeerIDFromMultiaddr(raw)
		if err != nil {
			return "", fmt.Errorf("invalid peer multiaddr: %w", err)
		}
		if !h.mesh.IsConnected(pid) {
			if err := h.mesh.Connect(ctx, raw); err != nil {
				return "", fmt.Errorf("connect to peer: %w", err)
			}
		}
		return pid, nil
	}
	pid, err := peer.Decode(raw)
	if err != nil {
		return "", errors.New("invalid peer id: must be a libp2p peer ID or /p2p/ multiaddr")
	}
	return pid, nil
}

// writeOpError maps mesh errors to sensible status codes.
func (h *networkTasksHandler) writeOpError(w http.ResponseWriter, err error) {
	msg := err.Error()
	code := http.StatusBadGateway
	switch {
	case strings.Contains(msg, "not trusted"):
		code = http.StatusForbidden
	case strings.HasPrefix(msg, "forbidden:"):
		code = http.StatusForbidden
	case strings.HasPrefix(msg, "rate_limited:"):
		code = http.StatusTooManyRequests
	case strings.Contains(msg, "does not support"):
		code = http.StatusBadGateway
	}
	writeJSON(w, code, map[string]string{"error": msg})
}

func parseTaskWait(v string) (time.Duration, error) {
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("wait must be positive")
	}
	if d > maxTaskWait {
		return 0, fmt.Errorf("wait exceeds maximum %s", maxTaskWait)
	}
	return d, nil
}

// parseTaskOpTimeout resolves the overall HTTP timeout for a task operation.
// For result long-polls it must exceed wait by enough headroom for the
// request to round-trip.
func parseTaskOpTimeout(v string, wait time.Duration) (time.Duration, error) {
	def := defaultTaskTimeout
	if wait > 0 && wait+15*time.Second > def {
		def = wait + 15*time.Second
	}
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, errors.New("timeout must be positive")
	}
	if d > 2*time.Minute+maxTaskWait {
		return 0, errors.New("timeout exceeds maximum")
	}
	return d, nil
}

// networkAuditHandler serves the tail of the local mesh audit trail.
type networkAuditHandler struct {
	authToken string
	auditPath string
}

func newNetworkAuditHandler(authToken, homePath string) http.Handler {
	return &networkAuditHandler{
		authToken: authToken,
		auditPath: filepath.Join(homePath, "mesh-audit.jsonl"),
	}
}

func (h *networkAuditHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	tail := 50
	if v := strings.TrimSpace(r.URL.Query().Get("tail")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 || n > 1000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid tail: must be 1-1000",
			})
			return
		}
		tail = n
	}

	entries, err := mesh.ReadAuditTail(h.auditPath, tail)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "read audit log: " + err.Error(),
		})
		return
	}
	if entries == nil {
		entries = []json.RawMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"count":   len(entries),
	})
}

// networkTaskEventsHandler streams mesh task events as a Server-Sent Event
// endpoint. Clients subscribe with ?peer=<peer-id> to filter to one peer.
type networkTaskEventsHandler struct {
	mesh      *mesh.Mesh
	authToken string
}

func newNetworkTaskEventsHandler(m *mesh.Mesh, authToken string) http.Handler {
	return &networkTaskEventsHandler{mesh: m, authToken: authToken}
}

func (h *networkTaskEventsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	if h.mesh == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "mesh not available",
		})
		return
	}

	peerID := strings.TrimSpace(r.URL.Query().Get("peer"))

	// Subscribe to the event stream before committing to a 200 response so
	// setup errors (invalid peer id, missing event bus) surface as proper
	// HTTP status codes instead of an SSE error event on an already-200 stream.
	events, cleanup, err := h.mesh.TaskEvents(r.Context(), peerID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "invalid peer id") {
			status = http.StatusBadRequest
		} else if strings.Contains(err.Error(), "event bus not configured") {
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

func writeSSE(w http.ResponseWriter, data any) {
	if f, ok := w.(http.Flusher); ok {
		defer f.Flush()
	}
	raw, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", string(raw))
}

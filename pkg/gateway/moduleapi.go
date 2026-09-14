// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/stpinkie/rhizome/pkg/modules"
)

// activeModuleManager holds the daemon's module manager so HTTP handlers can
// serve live module state without changing the RunWithMesh signature.
var activeModuleManager atomic.Pointer[modules.Manager]

// SetModuleManager registers the daemon's module manager (nil clears it).
// Called by the daemon at startup, before the HTTP handlers serve.
func SetModuleManager(m *modules.Manager) {
	activeModuleManager.Store(m)
}

// currentModuleManager returns the registered module manager, or nil.
func currentModuleManager() *modules.Manager {
	return activeModuleManager.Load()
}

// moduleHandler serves companion-module state and lifecycle on the daemon's
// gateway HTTP mux.
//
//	GET  /modules                    list catalog modules with live status
//	GET  /modules/<id>               one module
//	GET  /modules/<id>/logs?tail=N   stdout/stderr log tails
//	POST /modules/<id>               {"action": install|uninstall|enable|
//	                                 disable|start|stop|restart}
//	PUT  /modules/<id>/fields        {"key": "value"} — non-secret fields
//	PUT  /modules/<id>/secrets       {"key": "value"} — secret fields
type moduleHandler struct {
	authToken string
}

func newModuleHandler(authToken string) http.Handler {
	return &moduleHandler{authToken: authToken}
}

func (h *moduleHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

// ServeHTTP dispatches /modules and /modules/<id>[/<sub>].
func (h *moduleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	mgr := currentModuleManager()
	if mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "module manager not available",
		})
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/modules")
	rest = strings.TrimPrefix(rest, "/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	switch r.Method {
	case http.MethodGet:
		h.read(w, r, mgr, id, sub)
	case http.MethodPost:
		h.action(w, r, mgr, id)
	case http.MethodPut:
		h.put(w, r, mgr, id, sub)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed, use GET/POST/PUT",
		})
	}
}

func (h *moduleHandler) read(w http.ResponseWriter, r *http.Request, mgr *modules.Manager, id, sub string) {
	if id == "" {
		infos, err := mgr.List()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"modules": infos})
		return
	}
	if sub == "logs" {
		tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
		stdout, stderr, err := mgr.Logs(id, tail)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"stdout": stdout, "stderr": stderr})
		return
	}
	if sub != "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown sub-resource"})
		return
	}
	info, err := mgr.Info(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *moduleHandler) action(w http.ResponseWriter, r *http.Request, mgr *modules.Manager, id string) {
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "module id required"})
		return
	}
	var body struct {
		Action  string `json:"action"`
		Version string `json:"version,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	var err error
	switch body.Action {
	case "install":
		err = mgr.Install(r.Context(), id, body.Version)
	case "uninstall":
		err = mgr.Uninstall(id)
	case "enable":
		err = mgr.Enable(id, true)
	case "disable":
		err = mgr.Enable(id, false)
	case "start":
		err = mgr.Start(id)
	case "stop":
		err = mgr.Stop(id)
	case "restart":
		err = mgr.Restart(id)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "unknown action " + strconv.Quote(body.Action),
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": body.Action})
}

// put writes module config: PUT /modules/<id>/fields or /secrets with a
// {"key": "value"} body. Secrets persist to .security.yml, never config.json.
func (h *moduleHandler) put(w http.ResponseWriter, r *http.Request, mgr *modules.Manager, id, sub string) {
	if id == "" || (sub != "fields" && sub != "secrets") {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "use PUT /modules/<id>/fields or /modules/<id>/secrets",
		})
		return
	}
	var kv map[string]string
	if err := json.NewDecoder(r.Body).Decode(&kv); err != nil || len(kv) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected {key: value} JSON body"})
		return
	}
	var err error
	if sub == "secrets" {
		err = mgr.SetSecrets(id, kv)
	} else {
		err = mgr.SetFields(id, kv)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/modules"
)

// registerModuleRoutes binds companion-module admin endpoints.
//
// Read paths (list/info/logs) work without a daemon — status and logs come
// from the on-disk module state — but live status (running/unhealthy) and
// lifecycle actions (start/stop/restart) proxy to the daemon when one is
// running.
func (h *Handler) registerModuleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/modules", h.handleModules)
	mux.HandleFunc("GET /api/modules/", h.handleModule)
	mux.HandleFunc("POST /api/modules/", h.handleModule)
	mux.HandleFunc("PUT /api/modules/", h.handleModule)
}

// moduleManager builds a local (daemonless) manager for the current config.
func (h *Handler) moduleManager() (*modules.Manager, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, err
	}
	return modules.NewManager(globalConfigDir(), cfg, nil,
		func(c *config.Config) error { return config.SaveConfig(h.configPath, c) }), nil
}

// moduleDaemon proxies a request to the daemon's /modules endpoints. Returns
// (nil, err) when no daemon is running — callers decide whether a local
// fallback exists.
func (h *Handler) moduleDaemon(r *http.Request, path string, body []byte) ([]byte, int, error) {
	if !h.gatewayAvailableForProxy() {
		return nil, 0, errors.New("daemon not available")
	}
	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		return nil, 0, errors.New("gateway pid data unavailable")
	}

	u := h.gatewayProxyURL()
	u.Path = "/modules" + path

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if q := r.URL.RawQuery; q != "" {
		req.URL.RawQuery = q
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

// handleModules serves GET /api/modules — daemon view when live, else local.
func (h *Handler) handleModules(w http.ResponseWriter, r *http.Request) {
	if out, code, err := h.moduleDaemon(r, "", nil); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(out)
		return
	}
	mgr, err := h.moduleManager()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	infos, err := mgr.List()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeModuleJSON(w, http.StatusOK, map[string]any{"modules": infos})
}

// handleModule serves /api/modules/<id>[/<sub>] for GET/POST/PUT.
func (h *Handler) handleModule(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/modules/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}
	if id == "" {
		respondNetworkError(w, http.StatusBadRequest, "module id required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.moduleGet(w, r, id, sub)
	case http.MethodPost:
		h.moduleAction(w, r, id)
	case http.MethodPut:
		h.modulePut(w, r, id, sub)
	default:
		respondNetworkError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) moduleGet(w http.ResponseWriter, r *http.Request, id, sub string) {
	if out, code, err := h.moduleDaemon(r, "/"+id+subPath(sub), nil); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(out)
		return
	}
	mgr, err := h.moduleManager()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	switch sub {
	case "":
		info, err := mgr.Info(id)
		if err != nil {
			respondNetworkError(w, http.StatusNotFound, err.Error())
			return
		}
		writeModuleJSON(w, http.StatusOK, info)
	case "logs":
		tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
		stdout, stderr, err := mgr.Logs(id, tail)
		if err != nil {
			respondNetworkError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeModuleJSON(w, http.StatusOK, map[string]string{"stdout": stdout, "stderr": stderr})
	default:
		respondNetworkError(w, http.StatusNotFound, "unknown sub-resource")
	}
}

// moduleAction runs a lifecycle action. install/uninstall/enable/disable run
// locally (they only touch disk+config); start/stop/restart require the
// daemon supervisor.
func (h *Handler) moduleAction(w http.ResponseWriter, r *http.Request, id string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var req struct {
		Action  string `json:"action"`
		Version string `json:"version,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		respondNetworkError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Lifecycle actions always need the daemon.
	switch req.Action {
	case "start", "stop", "restart":
		out, code, err := h.moduleDaemon(r, "/"+id, body)
		if err != nil {
			respondNetworkError(w, http.StatusServiceUnavailable,
				fmt.Sprintf("%s requires a running daemon", req.Action))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(out)
		return
	}

	// Disk/config actions run locally — but when a daemon IS running, proxy
	// anyway so its live status view stays authoritative.
	if out, code, err := h.moduleDaemon(r, "/"+id, body); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(out)
		return
	}

	mgr, err := h.moduleManager()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	if req.Action == "verify" {
		// Disk-only: re-hash the installed binary against its install-time
		// digest record. Runs locally even with no daemon.
		res, verr := mgr.Verify(id)
		if verr != nil {
			writeModuleJSON(w, http.StatusBadRequest, map[string]any{
				"error": verr.Error(), "result": res,
			})
			return
		}
		writeModuleJSON(w, http.StatusOK, res)
		return
	}
	switch req.Action {
	case "install":
		err = mgr.Install(r.Context(), id, req.Version)
	case "uninstall":
		err = mgr.Uninstall(id)
	case "enable":
		err = mgr.Enable(id, true)
	case "disable":
		err = mgr.Enable(id, false)
	default:
		respondNetworkError(w, http.StatusBadRequest, "unknown action "+strconv.Quote(req.Action))
		return
	}
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeModuleJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": req.Action})
}

// modulePut writes module fields/secrets. When a daemon runs, proxy so it
// applies against the same config instance; otherwise write locally.
func (h *Handler) modulePut(w http.ResponseWriter, r *http.Request, id, sub string) {
	if sub != "fields" && sub != "secrets" {
		respondNetworkError(w, http.StatusBadRequest,
			"use PUT /api/modules/<id>/fields or /api/modules/<id>/secrets")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var kv map[string]string
	if err := json.Unmarshal(body, &kv); err != nil || len(kv) == 0 {
		respondNetworkError(w, http.StatusBadRequest, "expected {key: value} JSON body")
		return
	}

	if out, code, err := h.moduleDaemon(r, "/"+id+"/"+sub, body); err == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(out)
		return
	}

	mgr, err := h.moduleManager()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	if sub == "secrets" {
		err = mgr.SetSecrets(id, kv)
	} else {
		err = mgr.SetFields(id, kv)
	}
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeModuleJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func subPath(sub string) string {
	if sub == "" {
		return ""
	}
	return "/" + sub
}

func writeModuleJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

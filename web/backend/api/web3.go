package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// registerWeb3Routes binds the web3 approval-queue endpoints.
//
// Pending/wallet reads work daemonless (the stores are files under
// <RHIZOME_HOME>/web3); approvals proxy to the daemon when one runs —
// it owns execution — and resolve locally otherwise, matching
// `rhizome web3 approve` daemonless behavior.
func (h *Handler) registerWeb3Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/web3/pending", h.handleWeb3Pending)
	mux.HandleFunc("GET /api/web3/pending/{id}", h.handleWeb3PendingEntry)
	mux.HandleFunc("GET /api/web3/wallet", h.handleWeb3Wallet)
	mux.HandleFunc("POST /api/web3/approvals/{id}", h.handleWeb3Approval)
}

func (h *Handler) web3Stack() (*web3.SigningStack, *config.Config, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, nil, err
	}
	if !cfg.Tools.Web3.Enabled {
		return nil, nil, errors.New("web3 tools disabled (tools.web3.enabled)")
	}
	stack, err := web3.OpenSigningStack(globalConfigDir(), &cfg.Tools.Web3.Signing)
	if err != nil {
		return nil, nil, err
	}
	return stack, cfg, nil
}

// web3Daemon proxies to the daemon's /web3 endpoints. Returns (nil, err)
// when no daemon is running — callers fall back to local handling.
func (h *Handler) web3Daemon(r *http.Request, path string, body []byte) ([]byte, int, error) {
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
	u.Path = "/web3" + path
	u.RawQuery = r.URL.RawQuery
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
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func (h *Handler) handleWeb3Pending(w http.ResponseWriter, r *http.Request) {
	if out, code, err := h.web3Daemon(r, "/pending", nil); err == nil {
		writeModuleJSON(w, code, json.RawMessage(out))
		return
	}
	stack, _, err := h.web3Stack()
	if err != nil {
		respondNetworkError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	entries, err := stack.Pending.List()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeModuleJSON(w, http.StatusOK, map[string]any{
		"pending": entries, "count": len(entries),
	})
}

func (h *Handler) handleWeb3Wallet(w http.ResponseWriter, r *http.Request) {
	if out, code, err := h.web3Daemon(r, "/wallet", nil); err == nil {
		writeModuleJSON(w, code, json.RawMessage(out))
		return
	}
	stack, cfg, err := h.web3Stack()
	if err != nil {
		respondNetworkError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	entries, err := stack.Wallets.List()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}
	def, _ := stack.Wallets.Default()
	type addrJSON struct {
		web3.WalletEntry
		Default    bool   `json:"default,omitempty"`
		BalanceWei string `json:"balance_wei,omitempty"`
	}
	out := make([]addrJSON, 0, len(entries))
	var provider *web3.Provider
	if r.URL.Query().Get("balances") == "true" {
		provider = web3.NewProvider(cfg)
	}
	for _, e := range entries {
		row := addrJSON{WalletEntry: e, Default: e.Address == def}
		if provider != nil {
			raw, err := provider.Call(r.Context(), "eth_getBalance",
				[]any{e.Address, "latest"})
			if err == nil {
				var hexBal string
				if json.Unmarshal(raw, &hexBal) == nil {
					if wei, err := web3.ParseQuantity(hexBal); err == nil {
						row.BalanceWei = wei.String()
					}
				}
			}
		}
		out = append(out, row)
	}
	writeModuleJSON(w, http.StatusOK, map[string]any{
		"addresses": out, "default": def, "count": len(out),
	})
}

func (h *Handler) handleWeb3PendingEntry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if out, code, err := h.web3Daemon(r, "/pending/"+id, nil); err == nil {
		writeModuleJSON(w, code, json.RawMessage(out))
		return
	}
	stack, _, err := h.web3Stack()
	if err != nil {
		respondNetworkError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	e, err := stack.Pending.Get(id)
	if err != nil {
		respondNetworkError(w, http.StatusNotFound, err.Error())
		return
	}
	writeModuleJSON(w, http.StatusOK, e)
}

func (h *Handler) handleWeb3Approval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	if out, code, err := h.web3Daemon(r, "/approvals/"+id, body); err == nil {
		writeModuleJSON(w, code, json.RawMessage(out))
		return
	}
	// Daemonless: resolve + execute locally (same path as the CLI).
	stack, cfg, err := h.web3Stack()
	if err != nil {
		respondNetworkError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if !cfg.Tools.Web3.Signing.Enabled {
		respondNetworkError(w, http.StatusServiceUnavailable,
			"web3 signing disabled (tools.web3.signing.enabled)")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(body, &req); err != nil ||
		(req.Action != "approve" && req.Action != "reject") {
		respondNetworkError(w, http.StatusBadRequest, "action must be approve or reject")
		return
	}
	e, err := stack.Pending.Resolve(id, req.Action == "approve", "launcher")
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Action == "reject" {
		writeModuleJSON(w, http.StatusOK, e)
		return
	}
	provider := web3.NewProvider(cfg)
	done, execErr := stack.Pending.ExecuteApproved(
		r.Context(), id, stack.Wallets, provider, stack.Ledger)
	if done != nil {
		e = done
	}
	if execErr != nil {
		writeModuleJSON(w, http.StatusBadGateway, e)
		return
	}
	writeModuleJSON(w, http.StatusOK, e)
}

// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/web3"
)

// web3Handler serves wallet state and the signing-approval queue on the
// daemon's gateway HTTP mux. The daemon owns resolution while it runs; a
// daemonless CLI writes the same files directly (last writer wins — the
// pending store documents that trade).
//
//	GET  /web3/pending          list queued approvals
//	GET  /web3/pending/<id>     one entry
//	POST /web3/approvals/<id>   {"action": "approve"|"reject"} — approve
//	                            executes (sign + broadcast) synchronously
//	GET  /web3/wallet           wallet addresses + labels (never keys)
type web3Handler struct {
	authToken string
	cfg       *config.Config
	home      string
	bus       runtimeevents.Bus
}

func newWeb3Handler(
	authToken string, cfg *config.Config, homePath string, bus runtimeevents.Bus,
) http.Handler {
	return &web3Handler{authToken: authToken, cfg: cfg, home: homePath, bus: bus}
}

func (h *web3Handler) publish(kind string, attrs map[string]any) {
	if h.bus == nil {
		return
	}
	h.bus.PublishNonBlocking(runtimeevents.Event{
		Kind:     runtimeevents.Kind(kind),
		Source:   runtimeevents.Source{Component: "web3"},
		Severity: runtimeevents.SeverityInfo,
		Attrs:    attrs,
	})
}

func (h *web3Handler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

func (h *web3Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if h.cfg == nil || !h.cfg.Tools.Web3.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "web3 tools disabled (tools.web3.enabled)",
		})
		return
	}
	stack, err := web3.OpenSigningStack(h.home, &h.cfg.Tools.Web3.Signing)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/web3"), "/")
	parts := strings.SplitN(rest, "/", 2)

	switch {
	case parts[0] == "pending" && r.Method == http.MethodGet:
		h.pending(w, stack, parts)
	case parts[0] == "approvals" && r.Method == http.MethodPost:
		h.approve(w, r, stack, parts)
	case parts[0] == "wallet" && r.Method == http.MethodGet:
		h.wallet(w, stack)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "use GET /web3/pending, GET /web3/wallet, POST /web3/approvals/<id>",
		})
	}
}

func (h *web3Handler) pending(
	w http.ResponseWriter, stack *web3.SigningStack, parts []string,
) {
	if len(parts) == 2 && parts[1] != "" {
		e, err := stack.Pending.Get(parts[1])
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, e)
		return
	}
	entries, err := stack.Pending.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": entries, "count": len(entries)})
}

func (h *web3Handler) approve(
	w http.ResponseWriter, r *http.Request, stack *web3.SigningStack, parts []string,
) {
	if len(parts) != 2 || parts[1] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "POST /web3/approvals/<id>",
		})
		return
	}
	if !h.cfg.Tools.Web3.Signing.Enabled {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "web3 signing disabled (tools.web3.signing.enabled)",
		})
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	id := parts[1]
	switch body.Action {
	case "approve", "reject":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "action must be approve or reject",
		})
		return
	}

	e, err := stack.Pending.Resolve(id, body.Action == "approve", "daemon")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.Action == "reject" {
		h.publish("web3.rejected", map[string]any{"id": e.ID, "kind": string(e.Kind), "summary": e.Summary})
		writeJSON(w, http.StatusOK, e)
		return
	}

	h.publish("web3.approved", map[string]any{"id": e.ID, "kind": string(e.Kind), "summary": e.Summary})
	// Execute synchronously — the response carries the tx hash or the
	// failure reason; the entry reflects the final state either way.
	provider := web3.NewProvider(h.cfg)
	done, execErr := stack.Pending.ExecuteApproved(r.Context(), id, stack.Wallets, provider, stack.Ledger)
	if done != nil {
		e = done
	}
	if execErr != nil {
		h.publish("web3.failed", map[string]any{
			"id": id, "kind": string(e.Kind), "error": execErr.Error(),
		})
		writeJSON(w, http.StatusBadGateway, e)
		return
	}
	kind := "web3.sent"
	if e.Kind == web3.KindSign {
		kind = "web3.done"
	}
	h.publish(kind, map[string]any{
		"id": id, "kind": string(e.Kind), "tx_hash": e.TxHash, "from": e.From, "to": e.To,
	})
	writeJSON(w, http.StatusOK, e)
}

func (h *web3Handler) wallet(w http.ResponseWriter, stack *web3.SigningStack) {
	entries, err := stack.Wallets.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	def, _ := stack.Wallets.Default()
	writeJSON(w, http.StatusOK, map[string]any{
		"addresses": entries, "default": def, "count": len(entries),
	})
}

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/econ"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

// Track 146 — the Network page economy panel's backend. The handler
// proxies GET /network/economy to the daemon when it is up (the live view
// carries in-flight settle state); when the daemon is down it derives the
// same mesh.EconomyView shape from the ledger file + config — reads are
// daemonless by design, same posture as `rhizome mesh economy balance`.

func (h *Handler) registerNetworkEconomyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/network/economy", h.handleNetworkEconomy)
}

func (h *Handler) handleNetworkEconomy(w http.ResponseWriter, r *http.Request) {
	timeout, err := parseNetworkTimeout(r.URL.Query().Get("timeout"))
	if err != nil {
		respondNetworkError(w, http.StatusBadRequest, "invalid timeout: "+err.Error())
		return
	}
	if h.gatewayAvailableForProxy() {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		out, err := h.economyFromGateway(ctx)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Write(out)
			return
		}
		logger.Warnf("economy gateway fetch failed, falling back to ledger: %v", err)
	}
	view, err := h.economyFromFiles()
	if err != nil {
		respondNetworkError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// economyFromGateway proxies the daemon's live economy snapshot.
func (h *Handler) economyFromGateway(ctx context.Context) ([]byte, error) {
	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil {
		return nil, errors.New("gateway pid data unavailable")
	}
	u := h.gatewayProxyURL()
	u.Path = "/network/economy"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gateway returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// economyFromFiles builds the daemonless snapshot: the ledger's entry
// stream feeds the same econ.Summarize/FlagThresholds derivation the
// daemon uses; the in-flight journal contributes live settle records.
func (h *Handler) economyFromFiles() (*mesh.EconomyView, error) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	home := config.GetHome()
	view := &mesh.EconomyView{Enabled: cfg.Mesh.Economy.Enabled}
	view.Unit = cfg.Mesh.Economy.Unit
	view.SettleThreshold = cfg.Mesh.Economy.SettleThreshold
	if cfg.Mesh.Economy.SettleGracePeriod > 0 {
		view.SettleGracePeriod = cfg.Mesh.Economy.SettleGracePeriod.String()
	}

	entries, err := econ.ReadEntries(filepath.Join(home, econ.LedgerFileName))
	if err != nil {
		return nil, fmt.Errorf("read economy ledger: %w", err)
	}
	view.Peers = econ.Summarize(entries)
	for pid, s := range view.Peers {
		econ.FlagThresholds(s, pid, entries,
			cfg.Mesh.Economy.SettleThreshold, cfg.Mesh.Economy.SettleGracePeriod, time.Now())
	}
	if n := len(entries); n > 0 {
		start := n - 25
		if start < 0 {
			start = 0
		}
		view.RecentCharges = append([]econ.Entry(nil), entries[start:]...)
	}
	journal := econ.OpenInflightJournal(econ.InflightJournalPath(home))
	if live, jerr := journal.Live(); jerr == nil && len(live) > 0 {
		view.Inflight = live
	}
	return view, nil
}

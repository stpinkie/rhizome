package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/stpinkie/rhizome/pkg/config"
)

// registerSetupRoutes binds first-run onboarding endpoints to the ServeMux.
func (h *Handler) registerSetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", h.handleSetupStatus)
}

// handleSetupStatus reports whether the installation still needs initial model
// setup so the frontend can gate first-run onboarding.
//
//	GET /api/setup/status
func (h *Handler) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	normalizeStoredModelProviders(cfg)
	normalizeDefaultChainReferences(cfg)

	total := 0
	configured := 0
	for _, m := range cfg.ModelList {
		if m.IsVirtual() || !m.Enabled {
			continue
		}
		total++
		if hasModelConfiguration(m) {
			configured++
		}
	}

	defaultModel := cfg.Agents.Defaults.GetModelName()
	needsSetup := configured == 0 || defaultModel == ""

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"needs_setup":       needsSetup,
		"total_models":      total,
		"configured_models": configured,
		"default_model":     defaultModel,
	})
}

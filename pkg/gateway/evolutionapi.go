package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

// evolutionHandler exposes the agent self-evolution subsystem on the
// daemon's gateway mux:
//
//	GET  /evolution/status                       config + per-workspace counters
//	GET  /evolution/drafts?workspace=&state=     skill drafts (all workspaces)
//	GET  /evolution/records?workspace=&kind=&limit=  learning records
//	GET  /evolution/skills?workspace=            skill profiles
//	POST /evolution/run                          {workspace|agent|all, dry_run}
//	POST /evolution/drafts/<id>/accept           {workspace?, force?}
//	POST /evolution/drafts/<id>/reject           {workspace?, reason?}
//
// Reads are store-level and cheap; mutations go through the agent loop's
// evolution runtime so draft application uses the production applier path.
type evolutionHandler struct {
	agentLoop *agent.AgentLoop
	cfg       *config.Config
	authToken string
}

func newEvolutionHandler(agentLoop *agent.AgentLoop, cfg *config.Config, authToken string) http.Handler {
	return &evolutionHandler{agentLoop: agentLoop, cfg: cfg, authToken: authToken}
}

func (h *evolutionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	sub := strings.TrimPrefix(r.URL.Path, "/evolution")
	sub = strings.TrimPrefix(sub, "/")

	switch {
	case sub == "status" && r.Method == http.MethodGet:
		h.handleStatus(w, r)
	case sub == "drafts" && r.Method == http.MethodGet:
		h.handleDrafts(w, r)
	case sub == "records" && r.Method == http.MethodGet:
		h.handleRecords(w, r)
	case sub == "skills" && r.Method == http.MethodGet:
		h.handleSkills(w, r)
	case sub == "run" && r.Method == http.MethodPost:
		h.handleRun(w, r)
	case strings.HasPrefix(sub, "drafts/"):
		h.handleDraftAction(w, r, strings.TrimPrefix(sub, "drafts/"))
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown evolution endpoint"})
	}
}

func (h *evolutionHandler) handleStatus(w http.ResponseWriter, _ *http.Request) {
	evCfg := h.evolutionConfig()
	workspaces := h.workspaces()
	out := map[string]any{
		"enabled":           evCfg.Enabled,
		"mode":              evCfg.EffectiveMode(),
		"cold_path_trigger": evCfg.ColdPathTriggerMode(),
		"cold_path_times":   evCfg.EffectiveColdPathTimes(),
		"state_dir":         evCfg.StateDir,
		"min_task_count":    evCfg.EffectiveMinTaskCount(),
		"min_success_ratio": evCfg.EffectiveMinSuccessRatio(),
		"workspaces":        []any{},
	}
	statuses := make([]any, 0, len(workspaces))
	for _, ws := range workspaces {
		wsStatus, err := evolution.SummarizeWorkspace(h.storeFor(ws), ws)
		if err != nil {
			statuses = append(statuses, map[string]any{
				"workspace": ws,
				"error":     err.Error(),
			})
			continue
		}
		statuses = append(statuses, wsStatus)
	}
	out["workspaces"] = statuses
	if h.agentLoop != nil {
		if runs := h.agentLoop.EvolutionLastRuns(); len(runs) > 0 {
			out["last_runs"] = runs
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *evolutionHandler) handleDrafts(w http.ResponseWriter, r *http.Request) {
	workspace := evolution.ResolveWorkspacePath(r.URL.Query().Get("workspace"))
	stateFilter := strings.TrimSpace(r.URL.Query().Get("state"))

	var states []evolution.DraftStatus
	if stateFilter != "" {
		for _, part := range strings.Split(stateFilter, ",") {
			if s := strings.TrimSpace(part); s != "" {
				states = append(states, evolution.DraftStatus(s))
			}
		}
	}

	workspaces := h.workspaces()
	if workspace != "" {
		workspaces = []string{workspace}
	}
	drafts := make([]evolution.SkillDraft, 0)
	for _, ws := range workspaces {
		list, err := evolution.SortedDrafts(h.storeFor(ws), ws, states...)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		drafts = append(drafts, list...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"drafts": drafts})
}

func (h *evolutionHandler) handleRecords(w http.ResponseWriter, r *http.Request) {
	workspace := evolution.ResolveWorkspacePath(r.URL.Query().Get("workspace"))
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	workspaces := h.workspaces()
	if workspace != "" {
		workspaces = []string{workspace}
	}
	// A shared state_dir serves every workspace the same store: filter
	// records to the workspace being read and dedup across the loop.
	seen := make(map[string]struct{})
	records := make([]evolution.LearningRecord, 0)
	for _, ws := range workspaces {
		list, err := evolution.LoadRecordsByKind(h.storeFor(ws), kind)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.HasPrefix(err.Error(), "kind must be") {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		for _, rec := range list {
			if rec.WorkspaceID != "" && rec.WorkspaceID != ws {
				continue
			}
			key := rec.WorkspaceID + "\x00" + rec.ID
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			records = append(records, rec)
		}
	}
	if len(records) > limit {
		records = records[len(records)-limit:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": records})
}

func (h *evolutionHandler) handleSkills(w http.ResponseWriter, r *http.Request) {
	workspace := evolution.ResolveWorkspacePath(r.URL.Query().Get("workspace"))
	workspaces := h.workspaces()
	if workspace != "" {
		workspaces = []string{workspace}
	}
	profiles := make([]evolution.SkillProfile, 0)
	seen := make(map[string]struct{})
	for _, ws := range workspaces {
		list, err := h.storeFor(ws).LoadProfiles()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, p := range list {
			if p.WorkspaceID != ws && p.WorkspaceID != "" {
				continue
			}
			key := p.WorkspaceID + "\x00" + p.SkillName
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			profiles = append(profiles, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": profiles})
}

// handleRun executes (or previews) a manual cold-path run. The run itself is
// synchronous on the request context so the CLI can report a summary; draft
// generation calls the configured LLM, so this can take minutes.
func (h *evolutionHandler) handleRun(w http.ResponseWriter, r *http.Request) {
	if h.agentLoop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent loop unavailable"})
		return
	}
	var req struct {
		Workspace string `json:"workspace"`
		Agent     string `json:"agent"`
		All       bool   `json:"all"`
		DryRun    bool   `json:"dry_run"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil &&
		!errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	workspaces, err := h.resolveRunTargets(req.Workspace, req.Agent, req.All)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	results := make([]evolution.ColdPathRunSummary, 0, len(workspaces))
	for _, ws := range workspaces {
		if req.DryRun {
			preview, err := h.agentLoop.PreviewEvolutionColdPath(ws)
			if err != nil {
				results = append(results, evolution.ColdPathRunSummary{
					Workspace: ws, DryRun: true, Error: err.Error(),
				})
				continue
			}
			results = append(results, preview)
			continue
		}
		summary, err := h.agentLoop.RunEvolutionColdPath(r.Context(), ws)
		if err != nil && summary.Workspace == "" {
			summary = evolution.ColdPathRunSummary{Workspace: ws}
		}
		if err != nil && summary.Error == "" {
			summary.Error = err.Error()
		}
		results = append(results, summary)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *evolutionHandler) handleDraftAction(w http.ResponseWriter, r *http.Request, rest string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	if h.agentLoop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent loop unavailable"})
		return
	}
	id, action, found := strings.Cut(strings.TrimSuffix(rest, "/"), "/")
	if !found || id == "" || (action != "accept" && action != "reject") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown draft endpoint"})
		return
	}
	var req struct {
		Workspace string `json:"workspace"`
		Force     bool   `json:"force"`
		Reason    string `json:"reason"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	workspace, err := h.resolveDraftWorkspace(req.Workspace, id)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	var draft evolution.SkillDraft
	switch action {
	case "accept":
		draft, err = h.agentLoop.AcceptEvolutionDraft(r.Context(), workspace, id, req.Force)
	case "reject":
		draft, err = h.agentLoop.RejectEvolutionDraft(workspace, id, req.Reason)
	}
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not reviewable") {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": draft})
}

// resolveRunTargets turns the run request's targeting fields into concrete
// workspace paths.
func (h *evolutionHandler) resolveRunTargets(workspace, agentID string, all bool) ([]string, error) {
	switch {
	case all:
		return h.workspaces(), nil
	case workspace != "":
		return []string{evolution.ResolveWorkspacePath(workspace)}, nil
	case agentID != "":
		ws, ok := evolution.AgentWorkspace(h.cfg, agentID)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q", agentID)
		}
		return []string{ws}, nil
	default:
		return []string{h.defaultWorkspace()}, nil
	}
}

// resolveDraftWorkspace locates the workspace owning draftID: the explicit
// workspace when given, else the single matching store across known
// workspaces.
func (h *evolutionHandler) resolveDraftWorkspace(workspace, draftID string) (string, error) {
	if workspace != "" {
		return evolution.ResolveWorkspacePath(workspace), nil
	}
	var matched []string
	for _, ws := range h.workspaces() {
		drafts, err := evolution.SortedDrafts(h.storeFor(ws), ws)
		if err != nil {
			continue
		}
		for _, d := range drafts {
			if d.ID == draftID || strings.HasPrefix(d.ID, draftID) {
				matched = append(matched, ws)
				break
			}
		}
	}
	switch len(matched) {
	case 0:
		return "", fmt.Errorf("draft %q not found in any workspace", draftID)
	case 1:
		return matched[0], nil
	default:
		return "", fmt.Errorf("draft id %q matches multiple workspaces; pass workspace", draftID)
	}
}

func (h *evolutionHandler) workspaces() []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, ws := range evolution.ConfigWorkspaces(h.cfg) {
		if _, ok := seen[ws]; !ok && ws != "" {
			seen[ws] = struct{}{}
			out = append(out, ws)
		}
	}
	if h.agentLoop != nil {
		for _, ws := range h.agentLoop.EvolutionWorkspaces() {
			if _, ok := seen[ws]; !ok && ws != "" {
				seen[ws] = struct{}{}
				out = append(out, ws)
			}
		}
	}
	return out
}

func (h *evolutionHandler) defaultWorkspace() string {
	if h.cfg == nil {
		return ""
	}
	return h.cfg.WorkspacePath()
}

func (h *evolutionHandler) evolutionConfig() config.EvolutionConfig {
	if h.cfg == nil {
		return config.EvolutionConfig{}
	}
	return h.cfg.Evolution
}

func (h *evolutionHandler) storeFor(workspace string) *evolution.Store {
	var stateDir string
	if h.cfg != nil {
		stateDir = h.cfg.Evolution.StateDir
	}
	return evolution.NewStore(evolution.NewPaths(workspace, stateDir))
}

func (h *evolutionHandler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		return false
	}
	given := extractBearerToken(r.Header.Get("Authorization"))
	return given != "" && subtle.ConstantTimeCompare([]byte(given), []byte(h.authToken)) == 1
}

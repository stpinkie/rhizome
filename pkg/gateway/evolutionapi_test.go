package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/evolution"
)

func testEvolutionConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Evolution.Enabled = true
	cfg.Evolution.Mode = "draft"
	return cfg, workspace
}

func seedDraft(t *testing.T, cfg *config.Config, workspace, id, status string) {
	t.Helper()
	store := evolution.NewStore(evolution.NewPaths(workspace, cfg.Evolution.StateDir))
	if err := store.SaveDrafts([]evolution.SkillDraft{{
		ID:              id,
		WorkspaceID:     workspace,
		CreatedAt:       time.Unix(1700000000, 0).UTC(),
		SourceRecordID:  "rule-1",
		TargetSkillName: "weather",
		DraftType:       evolution.DraftTypeShortcut,
		ChangeKind:      evolution.ChangeKindCreate,
		HumanSummary:    "weather helper",
		BodyOrPatch:     "---\nname: weather\ndescription: x\n---\n# Weather\n",
		Status:          evolution.DraftStatus(status),
	}}); err != nil {
		t.Fatalf("SaveDrafts: %v", err)
	}
}

func TestEvolutionHandlerRequiresAuth(t *testing.T) {
	cfg, _ := testEvolutionConfig(t)
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/evolution/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestEvolutionHandlerUnknownEndpoint(t *testing.T) {
	cfg, _ := testEvolutionConfig(t)
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/evolution/bogus", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestEvolutionHandlerStatus(t *testing.T) {
	cfg, workspace := testEvolutionConfig(t)
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/evolution/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Enabled    bool             `json:"enabled"`
		Mode       string           `json:"mode"`
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Enabled || out.Mode != "draft" {
		t.Fatalf("unexpected status: %s", rec.Body.String())
	}
	if len(out.Workspaces) != 1 || out.Workspaces[0]["workspace"] != workspace {
		t.Fatalf("unexpected workspaces: %s", rec.Body.String())
	}
}

func TestEvolutionHandlerDrafts(t *testing.T) {
	cfg, workspace := testEvolutionConfig(t)
	seedDraft(t, cfg, workspace, "draft-1", "candidate")
	seedDraft(t, cfg, workspace, "draft-2", "accepted")
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/evolution/drafts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Drafts []evolution.SkillDraft `json:"drafts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Drafts) != 2 {
		t.Fatalf("expected 2 drafts, got %s", rec.Body.String())
	}

	// State filter narrows the list.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/evolution/drafts?state=candidate", nil))
	out.Drafts = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Drafts) != 1 || out.Drafts[0].ID != "draft-1" {
		t.Fatalf("expected only draft-1, got %s", rec.Body.String())
	}
}

func TestEvolutionHandlerRecords(t *testing.T) {
	cfg, workspace := testEvolutionConfig(t)
	store := evolution.NewStore(evolution.NewPaths(workspace, cfg.Evolution.StateDir))
	if err := store.AppendTaskRecord(context.Background(), evolution.LearningRecord{
		ID: "t-1", Kind: evolution.RecordKindTask, WorkspaceID: workspace,
		Status: evolution.RecordStatus("new"), Summary: "did a thing",
	}); err != nil {
		t.Fatalf("AppendTaskRecord: %v", err)
	}
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/evolution/records?kind=task", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Records []evolution.LearningRecord `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Records) != 1 || out.Records[0].ID != "t-1" {
		t.Fatalf("expected t-1, got %s", rec.Body.String())
	}

	// Unknown kinds are a bad request, not an empty list.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/evolution/records?kind=bogus", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestEvolutionHandlerSkills(t *testing.T) {
	cfg, workspace := testEvolutionConfig(t)
	store := evolution.NewStore(evolution.NewPaths(workspace, cfg.Evolution.StateDir))
	if err := store.SaveProfile(evolution.SkillProfile{
		WorkspaceID:    workspace,
		SkillName:      "weather",
		Status:         evolution.SkillStatusActive,
		CurrentVersion: "v1",
		UseCount:       3,
	}); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/evolution/skills", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Skills []evolution.SkillProfile `json:"skills"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Skills) != 1 || out.Skills[0].SkillName != "weather" {
		t.Fatalf("expected weather profile, got %s", rec.Body.String())
	}
}

// Mutations and runs require the agent loop — a handler built without one
// answers 503, not a silent store write.
func TestEvolutionHandlerRunNeedsAgentLoop(t *testing.T) {
	cfg, _ := testEvolutionConfig(t)
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/evolution/run",
		strings.NewReader(`{"all":true}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestEvolutionHandlerDraftActionNeedsAgentLoop(t *testing.T) {
	cfg, workspace := testEvolutionConfig(t)
	seedDraft(t, cfg, workspace, "draft-1", "candidate")
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	for _, action := range []string{"accept", "reject"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authedRequest(http.MethodPost, "/evolution/drafts/draft-1/"+action,
			strings.NewReader(`{}`)))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want %d", action, rec.Code, http.StatusServiceUnavailable)
		}
	}

	// No mutation happened.
	drafts, _ := evolution.NewStore(evolution.NewPaths(workspace, cfg.Evolution.StateDir)).LoadDrafts()
	if drafts[0].Status != evolution.DraftStatusCandidate {
		t.Fatalf("draft mutated despite missing agent loop: %s", drafts[0].Status)
	}
}

func TestEvolutionHandlerDraftActionMethodNotAllowed(t *testing.T) {
	cfg, _ := testEvolutionConfig(t)
	h := newEvolutionHandler(nil, cfg, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/evolution/drafts/draft-1/accept", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestEvolutionResolveRunTargets(t *testing.T) {
	cfg, workspace := testEvolutionConfig(t)
	agentWS := t.TempDir()
	cfg.Agents.List = []config.AgentConfig{{ID: "writer", Workspace: agentWS}}
	h := &evolutionHandler{cfg: cfg}

	got, err := h.resolveRunTargets("", "", false)
	if err != nil || len(got) != 1 || got[0] != workspace {
		t.Fatalf("default target = %v, %v", got, err)
	}
	got, err = h.resolveRunTargets("", "writer", false)
	if err != nil || len(got) != 1 || got[0] != agentWS {
		t.Fatalf("agent target = %v, %v", got, err)
	}
	if _, err = h.resolveRunTargets("", "ghost", false); err == nil {
		t.Fatal("expected unknown agent error")
	}
	got, err = h.resolveRunTargets("", "", true)
	if err != nil || len(got) != 2 {
		t.Fatalf("all target = %v, %v", got, err)
	}
}

func TestEvolutionResolveDraftWorkspace(t *testing.T) {
	cfg, workspace := testEvolutionConfig(t)
	seedDraft(t, cfg, workspace, "draft-1", "candidate")
	h := &evolutionHandler{cfg: cfg}

	ws, err := h.resolveDraftWorkspace("", "draft-1")
	if err != nil || ws != workspace {
		t.Fatalf("resolve = %q, %v", ws, err)
	}
	// Explicit workspace wins over search.
	ws, err = h.resolveDraftWorkspace("other", "draft-1")
	if err != nil || ws != "other" {
		t.Fatalf("explicit resolve = %q, %v", ws, err)
	}
	if _, err = h.resolveDraftWorkspace("", "nope"); err == nil {
		t.Fatal("expected not-found error")
	}
}

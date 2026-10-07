package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

func TestNetworkActivityHandlerFilters(t *testing.T) {
	bus := runtimeevents.NewBus()
	m := &mesh.Mesh{}
	m.SetEventBus(bus)
	h := newNetworkActivityHandler(m, testTasksToken)

	events := []struct {
		kind  string
		attrs map[string]any
	}{
		{"mesh.task.update", map[string]any{"task_id": "t-1", "peer_id": "p-aaa"}},
		{"swarm.offer.open", map[string]any{"swarm_id": "ops", "offer_id": "o-1"}},
		{"mesh.peer.online", map[string]any{"peer_id": "p-bbb"}},
	}
	for _, e := range events {
		bus.PublishNonBlocking(runtimeevents.Event{
			Kind:  runtimeevents.Kind(e.kind),
			Attrs: e.attrs,
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(m.Activity(0)) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	get := func(path string) []mesh.ActivityEntry {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, authedRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body=%s", path, rec.Code, rec.Body.String())
		}
		var resp struct {
			Events []mesh.ActivityEntry `json:"events"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
		return resp.Events
	}

	if got := get("/network/activity?kind=mesh.*"); len(got) != 2 {
		t.Fatalf("kind=mesh.* matched %d, want 2", len(got))
	}
	if got := get("/network/activity?peer=p-aaa"); len(got) != 1 || got[0].Kind != "mesh.task.update" {
		t.Fatalf("peer=p-aaa → %+v", got)
	}
	if got := get("/network/activity?swarm=ops"); len(got) != 1 || got[0].Kind != "swarm.offer.open" {
		t.Fatalf("swarm=ops → %+v", got)
	}
	if got := get("/network/activity?since=1h"); len(got) != 3 {
		t.Fatalf("since=1h matched %d, want 3", len(got))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/activity?since=bogus", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("since=bogus: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestNetworkTraceHandlerRequiresAuth(t *testing.T) {
	h := newNetworkTraceHandler(&mesh.Mesh{}, "secret-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/network/trace?id=x", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNetworkTraceHandlerCorrelates(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "mesh-audit.jsonl")
	auditLine := `{"ts":"2026-10-04T00:00:00Z","peer_id":"p-aaa","op":"submit","ref":"corr-77","status":"ok"}` + "\n"
	if err := os.WriteFile(auditPath, []byte(auditLine), 0o600); err != nil {
		t.Fatal(err)
	}

	bus := runtimeevents.NewBus()
	m := &mesh.Mesh{}
	m.SetAuditPath(auditPath)
	m.SetEventBus(bus)
	bus.PublishNonBlocking(runtimeevents.Event{
		Kind:  runtimeevents.Kind("mesh.remote.delegate"),
		Attrs: map[string]any{"correlation_id": "corr-77", "peer_id": "p-aaa"},
	})
	deadline := time.Now().Add(5 * time.Second)
	for len(m.Activity(0)) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	h := newNetworkTraceHandler(m, testTasksToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/trace?id=corr-77", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var rep struct {
		Events []mesh.ActivityEntry `json:"events"`
		Audit  []json.RawMessage    `json:"audit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rep.Events) != 1 || rep.Events[0].Kind != "mesh.remote.delegate" {
		t.Fatalf("events = %+v", rep.Events)
	}
	if len(rep.Audit) != 1 {
		t.Fatalf("audit = %+v", rep.Audit)
	}
}

func TestNetworkTraceHandlerNoMatch(t *testing.T) {
	m := &mesh.Mesh{}
	m.SetEventBus(runtimeevents.NewBus())
	h := newNetworkTraceHandler(m, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/trace?id=nothing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/trace", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

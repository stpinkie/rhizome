package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
)

func TestNetworkActivityHandlerRequiresAuth(t *testing.T) {
	h := newNetworkActivityHandler(&mesh.Mesh{}, "secret-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/network/activity", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNetworkActivityHandlerMethodNotAllowed(t *testing.T) {
	h := newNetworkActivityHandler(&mesh.Mesh{}, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/activity", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestNetworkActivityHandlerMeshDisabled(t *testing.T) {
	h := newNetworkActivityHandler(nil, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/activity", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestNetworkActivityHandlerTail(t *testing.T) {
	bus := runtimeevents.NewBus()
	m := &mesh.Mesh{}
	m.SetEventBus(bus)
	h := newNetworkActivityHandler(m, testTasksToken)

	for _, kind := range []string{"mesh.a", "swarm.b", "mesh.c"} {
		bus.PublishNonBlocking(runtimeevents.Event{
			Kind:  runtimeevents.Kind(kind),
			Attrs: map[string]any{"k": kind},
		})
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(m.Activity(0)) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/activity?tail=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Events []mesh.ActivityEntry `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d (%s)", len(resp.Events), rec.Body.String())
	}
	if resp.Events[0].Kind != "swarm.b" || resp.Events[1].Kind != "mesh.c" {
		t.Fatalf("tail order wrong: %+v", resp.Events)
	}
}

func TestNetworkEventsHandlerStreams(t *testing.T) {
	bus := runtimeevents.NewBus()
	m := &mesh.Mesh{}
	m.SetEventBus(bus)
	h := newNetworkEventsHandler(m, testTasksToken)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/network/events", nil)
	req.Header.Set("Authorization", "Bearer "+testTasksToken)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	// Give the subscription a moment to register before publishing.
	time.Sleep(50 * time.Millisecond)
	bus.PublishNonBlocking(runtimeevents.Event{
		Kind:  runtimeevents.Kind("mesh.test.sse"),
		Attrs: map[string]any{"marker": "xyz"},
	})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "data: {") {
		t.Fatalf("not SSE format: %s", body)
	}
	if !strings.Contains(body, "mesh.test.sse") || !strings.Contains(body, "xyz") {
		t.Fatalf("event payload missing: %s", body)
	}
}

func TestNetworkEventsHandlerNoBus(t *testing.T) {
	// A mesh without SetEventBus returns 503 rather than a broken SSE stream.
	h := newNetworkEventsHandler(&mesh.Mesh{}, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/events", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

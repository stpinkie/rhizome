package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	runtimeevents "github.com/stpinkie/rhizome/pkg/events"
	"github.com/stpinkie/rhizome/pkg/rhizome/agentrpc"
	"github.com/stpinkie/rhizome/pkg/rhizome/agenttask"
	"github.com/stpinkie/rhizome/pkg/rhizome/identity"
	"github.com/stpinkie/rhizome/pkg/rhizome/mesh"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	toolshared "github.com/stpinkie/rhizome/pkg/tools/shared"
)

const testTasksToken = "secret-token"

func authedRequest(method, target string, body *strings.Reader) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, target, body)
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Header.Set("Authorization", "Bearer "+testTasksToken)
	return req
}

func TestNetworkTasksHandlerRequiresAuth(t *testing.T) {
	h := newNetworkTasksHandler(nil, testTasksToken)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/tasks?peer=x", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestNetworkTasksHandlerMethodNotAllowed(t *testing.T) {
	h := newNetworkTasksHandler(nil, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPut, "/network/tasks", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

func TestNetworkTasksHandlerNoMesh(t *testing.T) {
	h := newNetworkTasksHandler(nil, testTasksToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/tasks?peer=x", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}

func TestNetworkTasksHandlerBadRequests(t *testing.T) {
	m, cleanup := newTestNetworkStatusMesh(t)
	defer cleanup()
	h := newNetworkTasksHandler(m, testTasksToken)

	cases := []struct {
		name   string
		method string
		target string
		body   *strings.Reader
		want   int
	}{
		{"missing peer", http.MethodGet, "/network/tasks", nil, http.StatusBadRequest},
		{"invalid peer", http.MethodGet, "/network/tasks?peer=not-a-peer", nil, http.StatusBadRequest},
		{"bad wait", http.MethodGet, "/network/tasks?peer=12D3KooWGRcjvRUBXU3bJvCKkQvR5ME7zByZNddT5d5nhCFoHVDx&task=t&wait=nope", nil, http.StatusBadRequest},
		{"bad action", http.MethodPost, "/network/tasks?action=explode", nil, http.StatusBadRequest},
		{"empty submit", http.MethodPost, "/network/tasks", strings.NewReader(`{"peer":"x","task":""}`), http.StatusBadRequest},
		{"bad submit json", http.MethodPost, "/network/tasks", strings.NewReader(`{`), http.StatusBadRequest},
		{"cancel missing task", http.MethodPost, "/network/tasks?action=cancel&peer=12D3KooWGRcjvRUBXU3bJvCKkQvR5ME7zByZNddT5d5nhCFoHVDx", nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, authedRequest(tc.method, tc.target, tc.body))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d, body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// newTaskPeerFixture builds two started meshes: a daemon mesh (no runFunc) and
// a peer mesh that accepts spawn and runs runFunc.
func newTaskPeerFixture(t *testing.T, runFunc func(context.Context, agentrpc.Request) (*toolshared.ToolResult, error)) (*mesh.Mesh, *rnet.Node, func()) {
	t.Helper()
	ctx := context.Background()

	idA, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 20)
	if err != nil {
		t.Fatalf("derive identity A: %v", err)
	}
	idB, _, err := identity.FromMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", 21)
	if err != nil {
		t.Fatalf("derive identity B: %v", err)
	}

	nodeA, err := rnet.NewNode(ctx, idA.Libp2pPrivKey, rnet.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	if err != nil {
		t.Fatalf("node A: %v", err)
	}
	nodeB, err := rnet.NewNode(ctx, idB.Libp2pPrivKey, rnet.Config{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}})
	if err != nil {
		_ = nodeA.Close()
		t.Fatalf("node B: %v", err)
	}

	meshA := mesh.NewMesh(nodeA, nil, idA, config.MeshConfig{
		Enabled: true, RemoteTimeout: 30 * time.Second,
	}, nil)
	if err := meshA.Start(ctx); err != nil {
		t.Fatalf("mesh A start: %v", err)
	}
	meshB := mesh.NewMesh(nodeB, nil, idB, config.MeshConfig{
		Enabled: true, AllowRemoteSpawn: true, RemoteTimeout: 30 * time.Second,
	}, runFunc)
	if err := meshB.Start(ctx); err != nil {
		t.Fatalf("mesh B start: %v", err)
	}

	addrsB := nodeB.BootstrapAddrs()
	if len(addrsB) == 0 {
		t.Fatal("node B has no listen addresses")
	}
	if err := meshA.Connect(ctx, addrsB[0]); err != nil {
		t.Fatalf("connect A->B: %v", err)
	}
	meshA.TrustPeer(nodeB.ID())
	meshB.TrustPeer(nodeA.ID())

	cleanup := func() {
		_ = meshA.Stop()
		_ = meshB.Stop()
		_ = nodeA.Close()
		_ = nodeB.Close()
	}
	return meshA, nodeB, cleanup
}

func TestNetworkTasksHandlerLifecycle(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	started := make(chan struct{})
	runFunc := func(ctx context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return toolshared.NewToolResult("remote result"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	daemonMesh, peerNode, cleanup := newTaskPeerFixture(t, runFunc)
	defer cleanup()
	defer close(release)

	h := newNetworkTasksHandler(daemonMesh, testTasksToken)
	peerID := peerNode.PeerID()

	// Submit.
	rec := httptest.NewRecorder()
	body := strings.NewReader(fmt.Sprintf(
		`{"peer":%q,"agent_id":"main","task":"do remote work"}`, peerID))
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/tasks", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var submitResp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitResp); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if submitResp.TaskID == "" {
		t.Fatal("submit returned empty task_id")
	}

	// Wait until the task is actually running on the peer.
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("task did not start on peer")
	}

	// List.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/tasks?peer="+peerID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Tasks []agenttask.TaskInfo `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var listed bool
	for _, task := range listResp.Tasks {
		if task.TaskID == submitResp.TaskID {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("task %s not in list response: %s", submitResp.TaskID, rec.Body.String())
	}

	// Status.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet,
		fmt.Sprintf("/network/tasks?peer=%s&task=%s", peerID, submitResp.TaskID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var statusResp agenttask.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &statusResp); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if statusResp.Status != agenttask.StatusRunning && statusResp.Status != agenttask.StatusAccepted {
		t.Fatalf("unexpected task status %q", statusResp.Status)
	}

	// Cancel.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost,
		fmt.Sprintf("/network/tasks?peer=%s&task=%s&action=cancel", peerID, submitResp.TaskID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var cancelResp agenttask.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &cancelResp); err != nil {
		t.Fatalf("decode cancel: %v", err)
	}
	if cancelResp.Status != agenttask.StatusCancelled {
		t.Fatalf("cancel returned status %q", cancelResp.Status)
	}
}

func TestNetworkTasksHandlerResult(t *testing.T) {
	runFunc := func(_ context.Context, _ agentrpc.Request) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("done fast"), nil
	}
	daemonMesh, peerNode, cleanup := newTaskPeerFixture(t, runFunc)
	defer cleanup()

	h := newNetworkTasksHandler(daemonMesh, testTasksToken)
	peerID := peerNode.PeerID()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/tasks",
		strings.NewReader(fmt.Sprintf(`{"peer":%q,"task":"quick task"}`, peerID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var submitResp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &submitResp); err != nil {
		t.Fatalf("decode submit: %v", err)
	}

	// Result long-polls until the (already finished) task completes.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet,
		fmt.Sprintf("/network/tasks?peer=%s&task=%s&wait=10s", peerID, submitResp.TaskID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("result status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp agenttask.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if resp.Status != agenttask.StatusDone {
		t.Fatalf("result status = %q, want done", resp.Status)
	}
	if resp.Result == nil || resp.Result.ForLLM != "done fast" {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
}

func TestNetworkTasksHandlerUntrustedPeer(t *testing.T) {
	daemonMesh, peerNode, cleanup := newTaskPeerFixture(t, func(
		_ context.Context, _ agentrpc.Request,
	) (*toolshared.ToolResult, error) {
		return toolshared.NewToolResult("nope"), nil
	})
	defer cleanup()

	// Break the caller-side trust so the request is refused before dialing.
	daemonMesh.UntrustPeer(peerNode.ID())

	h := newNetworkTasksHandler(daemonMesh, testTasksToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/tasks?peer="+peerNode.PeerID(), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not trusted") {
		t.Fatalf("body missing 'not trusted': %s", rec.Body.String())
	}
}

// --- audit handler ---

func writeAuditFile(t *testing.T, dir string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, "mesh-audit.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write audit file: %v", err)
	}
	return path
}

func TestNetworkAuditHandlerRequiresAuth(t *testing.T) {
	h := newNetworkAuditHandler(testTasksToken, t.TempDir())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/network/audit", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNetworkAuditHandlerMethodNotAllowed(t *testing.T) {
	h := newNetworkAuditHandler(testTasksToken, t.TempDir())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodPost, "/network/audit", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestNetworkAuditHandlerTail(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"ts":"2026-09-06T00:00:01Z","op":"submit","status":"ok","peer_id":"p1"}`,
		`{"ts":"2026-09-06T00:00:02Z","op":"status","status":"ok","peer_id":"p1"}`,
		`not-json`,
		`{"ts":"2026-09-06T00:00:03Z","op":"result","status":"ok","peer_id":"p2"}`,
	}
	writeAuditFile(t, dir, lines)

	h := newNetworkAuditHandler(testTasksToken, dir)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/audit?tail=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Entries []json.RawMessage `json:"entries"`
		Count   int               `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 2 || len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d (%s)", resp.Count, rec.Body.String())
	}
	if !strings.Contains(string(resp.Entries[1]), `"op":"result"`) {
		t.Fatalf("newest entry missing: %s", rec.Body.String())
	}
}

func TestNetworkAuditHandlerMissingFile(t *testing.T) {
	h := newNetworkAuditHandler(testTasksToken, t.TempDir())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Entries []json.RawMessage `json:"entries"`
		Count   int               `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 0 {
		t.Fatalf("expected 0 entries, got %d", resp.Count)
	}
}

func TestNetworkTaskEventsHandler(t *testing.T) {
	bus := runtimeevents.NewBus()
	m, cleanup := newTestNetworkStatusMesh(t)
	defer cleanup()
	m.SetEventBus(bus)
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start mesh: %v", err)
	}
	defer m.Stop()

	h := newNetworkTaskEventsHandler(m, testTasksToken)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/network/tasks/events", nil)
	req.Header.Set("Authorization", "Bearer "+testTasksToken)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	// Give the subscription time to register, then publish a task event.
	time.Sleep(50 * time.Millisecond)
	bus.PublishNonBlocking(runtimeevents.Event{
		Kind: runtimeevents.KindMeshTaskUpdate,
		Attrs: map[string]any{
			"peer_id":  "12D3KooWH3umosfqFuBeS5PVJFvSsQkuxFWcbv13tDEfwYa9XUvv",
			"task_id":  "task-1",
			"agent_id": "main",
			"status":   "done",
		},
	})

	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "task-1") {
		t.Fatalf("SSE body missing task id: %s", body)
	}
	if !strings.HasPrefix(body, "data: {") {
		t.Fatalf("SSE body not in event-stream format: %s", body)
	}
	if !strings.Contains(body, "\"status\":\"done\"") {
		t.Fatalf("SSE body missing status: %s", body)
	}
}

func TestNetworkAuditHandlerBadTail(t *testing.T) {
	h := newNetworkAuditHandler(testTasksToken, t.TempDir())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authedRequest(http.MethodGet, "/network/audit?tail=99999", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

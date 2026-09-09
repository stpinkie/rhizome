package cdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCDPServer is an in-process WebSocket server emulating a browser-level
// CDP endpoint. It answers the commands the driver issues and records them.
type fakeCDPServer struct {
	t        *testing.T
	srv      *httptest.Server
	calls    chan string
	authSeen chan string
}

func newFakeCDPServer(t *testing.T) *fakeCDPServer {
	t.Helper()
	f := &fakeCDPServer{
		t:        t,
		calls:    make(chan string, 256),
		authSeen: make(chan string, 4),
	}
	up := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			w.Header().Set("Content-Type", "application/json")
			wsURL := "ws://" + r.Host + "/devtools/browser/fake"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"webSocketDebuggerUrl": wsURL,
			})
			return
		}
		f.authSeen <- r.Header.Get("Authorization")
		conn, err := up.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		for {
			var msg message
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			f.calls <- msg.Method
			resp := message{ID: msg.ID, SessionID: msg.SessionID}
			resp.Result = f.resultFor(msg.Method, msg.Params)
			if err := conn.WriteJSON(resp); err != nil {
				return
			}
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCDPServer) resultFor(method string, params json.RawMessage) json.RawMessage {
	switch method {
	case "Target.getTargets":
		raw, _ := json.Marshal(map[string]any{"targetInfos": []any{}})
		return raw
	case "Target.createTarget":
		raw, _ := json.Marshal(map[string]any{"targetId": "T1"})
		return raw
	case "Target.attachToTarget":
		raw, _ := json.Marshal(map[string]any{"sessionId": "S1"})
		return raw
	case "Page.navigate":
		raw, _ := json.Marshal(map[string]any{"frameId": "F1"})
		return raw
	case "Runtime.evaluate":
		// The driver embeds the expression in params.expression; the fake
		// evaluates only the patterns it knows.
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		var value any
		switch {
		case strings.Contains(p.Expression, "document.readyState"):
			value = "complete"
		case strings.Contains(p.Expression, "getBoundingClientRect"):
			value = map[string]any{"x": 10.0, "y": 20.0, "tag": "BUTTON"}
		case strings.Contains(p.Expression, "data-rh-ref") && strings.Contains(p.Expression, "focus"):
			value = "INPUT"
		case strings.Contains(p.Expression, "document.title"):
			value = `{"title":"T","url":"u","refs":0,"lines":["hi"]}`
		default:
			value = "eval-result"
		}
		raw, _ := json.Marshal(map[string]any{
			"result": map[string]any{"type": "string", "value": value},
		})
		return raw
	case "Page.captureScreenshot":
		raw, _ := json.Marshal(map[string]any{
			"data": base64.StdEncoding.EncodeToString([]byte("PNGDATA")),
		})
		return raw
	default:
		raw, _ := json.Marshal(map[string]any{})
		return raw
	}
}

func (f *fakeCDPServer) wsURL() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/devtools/browser/fake"
}

func (f *fakeCDPServer) httpURL() string { return f.srv.URL }

func drainCalls(f *fakeCDPServer) []string {
	var out []string
	for {
		select {
		case c := <-f.calls:
			out = append(out, c)
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

func TestCDPDriverFullFlow(t *testing.T) {
	f := newFakeCDPServer(t)
	d := &Driver{}
	ctx := context.Background()

	out, err := d.Open(ctx, f.wsURL(), nil, "s1", "https://example.com")
	require.NoError(t, err)
	assert.Contains(t, out, "Opened")

	snap, err := d.Snapshot(ctx, f.wsURL(), nil, "s1", false)
	require.NoError(t, err)
	assert.Contains(t, snap, "Page: T")

	click, err := d.Click(ctx, f.wsURL(), nil, "s1", "@e1")
	require.NoError(t, err)
	assert.Contains(t, click, "Clicked")

	fill, err := d.Fill(ctx, f.wsURL(), nil, "s1", "@e2", "hello")
	require.NoError(t, err)
	assert.Contains(t, fill, "Filled")

	png := filepath.Join(t.TempDir(), "shot.png")
	shot, err := d.Screenshot(ctx, f.wsURL(), nil, "s1", png)
	require.NoError(t, err)
	assert.Contains(t, shot, "shot.png")
	data, err := os.ReadFile(png)
	require.NoError(t, err)
	assert.Equal(t, []byte("PNGDATA"), data)

	eval, err := d.Eval(ctx, f.wsURL(), nil, "s1", "1+1")
	require.NoError(t, err)
	assert.Contains(t, eval, "eval-result")

	waited, err := d.Wait(ctx, f.wsURL(), nil, "s1", "10ms")
	require.NoError(t, err)
	assert.Contains(t, waited, "Waited")

	require.NoError(t, d.Close(ctx, f.wsURL(), "s1"))

	calls := drainCalls(f)
	assert.Contains(t, calls, "Target.createTarget")
	assert.Contains(t, calls, "Target.attachToTarget")
	assert.Contains(t, calls, "Page.enable")
	assert.Contains(t, calls, "Page.navigate")
	assert.Contains(t, calls, "Input.dispatchMouseEvent")
	assert.Contains(t, calls, "Input.insertText")
	assert.Contains(t, calls, "Page.captureScreenshot")
	assert.Contains(t, calls, "Target.closeTarget")
}

func TestCDPDialHTTPBase(t *testing.T) {
	f := newFakeCDPServer(t)
	d := &Driver{}
	out, err := d.Open(context.Background(), f.httpURL(), nil, "s2", "https://x")
	require.NoError(t, err)
	assert.Contains(t, out, "Opened")
	require.NoError(t, d.Close(context.Background(), f.httpURL(), "s2"))
}

func TestCDPDialAuthHeader(t *testing.T) {
	f := newFakeCDPServer(t)
	d := &Driver{}
	h := http.Header{"Authorization": {"Bearer tok-123"}}
	_, err := d.Open(context.Background(), f.wsURL(), h, "s3", "https://x")
	require.NoError(t, err)
	select {
	case got := <-f.authSeen:
		assert.Equal(t, "Bearer tok-123", got)
	case <-time.After(2 * time.Second):
		t.Fatal("no ws handshake seen")
	}
	require.NoError(t, d.Close(context.Background(), f.wsURL(), "s3"))
}

// Package cdp is a minimal native Go Chrome DevTools Protocol client. It
// speaks JSON-RPC over WebSocket to a browser-level endpoint, manages page
// targets as named sessions, and implements the subset of commands the
// browser driver needs: Target.*, Page.navigate/captureScreenshot,
// Runtime.evaluate, DOM.getBoxModel, and Input.dispatch*/insertText.
//
// The wire format is CDP's flat multiplexed protocol: every command carries
// an id and an optional sessionId; responses arrive interleaved with events.
package cdp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// message is the CDP wire envelope for commands, responses, and events.
type message struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Conn is a multiplexed CDP connection over one WebSocket.
type Conn struct {
	ws     *websocket.Conn
	write  sync.Mutex
	seq    atomic.Int64
	pendMu sync.Mutex
	pend   map[int64]chan message
	closed atomic.Bool
	done   chan struct{}
}

// Dial connects to a browser-level CDP endpoint. endpoint may be a ws:// or
// wss:// URL, or an http(s):// debug base whose /json/version yields the
// webSocketDebuggerUrl. headers are sent on the WebSocket handshake
// (authenticated endpoints such as Cloudflare use Authorization: Bearer).
func Dial(ctx context.Context, endpoint string, headers http.Header) (*Conn, error) {
	wsURL := strings.TrimSpace(endpoint)
	if strings.HasPrefix(wsURL, "http://") || strings.HasPrefix(wsURL, "https://") {
		var err error
		wsURL, err = browserWSURL(ctx, wsURL)
		if err != nil {
			return nil, err
		}
	}
	u, err := url.Parse(wsURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return nil, fmt.Errorf("cdp endpoint %q is not a ws/wss URL", endpoint)
	}

	dialer := websocket.Dialer{
		HandshakeTimeout:  15 * time.Second,
		ReadBufferSize:    1 << 20,
		WriteBufferSize:   1 << 16,
		Subprotocols:      []string{},
		EnableCompression: false,
	}
	ws, resp, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		status := ""
		if resp != nil {
			status = fmt.Sprintf(" (HTTP %d)", resp.StatusCode)
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
		}
		return nil, fmt.Errorf("cdp dial %s: %w%s", u.Host, err, status)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	// CDP can emit large snapshots (DOM trees, base64 screenshots).
	ws.SetReadLimit(64 << 20)

	c := &Conn{ws: ws, pend: make(map[int64]chan message), done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

// browserWSURL resolves an http(s) debug base to the browser-level
// webSocketDebuggerUrl via /json/version.
func browserWSURL(ctx context.Context, base string) (string, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, strings.TrimRight(base, "/")+"/json/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("cdp version probe: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return "", err
	}
	var v struct {
		WebSocket string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(data, &v); err != nil || v.WebSocket == "" {
		return "", fmt.Errorf("cdp endpoint %s has no webSocketDebuggerUrl", base)
	}
	return v.WebSocket, nil
}

// readLoop dispatches responses to pending callers and drops events.
func (c *Conn) readLoop() {
	defer close(c.done)
	for {
		var msg message
		if err := c.ws.ReadJSON(&msg); err != nil {
			c.failAll(fmt.Errorf("cdp read: %w", err))
			return
		}
		if msg.ID == 0 {
			continue // event — no waiter
		}
		c.pendMu.Lock()
		ch := c.pend[msg.ID]
		if ch != nil {
			delete(c.pend, msg.ID)
		}
		c.pendMu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

// failAll unblocks every pending call when the socket dies.
func (c *Conn) failAll(err error) {
	c.closed.Store(true)
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	for id, ch := range c.pend {
		delete(c.pend, id)
		ch <- message{Error: &struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}{Code: -32000, Message: err.Error()}}
	}
}

// Call sends one command; sessionID targets a flattened session, "" is the
// browser target. The decoded result is unmarshalled into out (may be nil).
func (c *Conn) Call(
	ctx context.Context,
	sessionID, method string,
	params any,
	out any,
) error {
	if c.closed.Load() {
		return fmt.Errorf("cdp connection closed")
	}
	id := c.seq.Add(1)
	req := message{ID: id, Method: method, SessionID: sessionID}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("cdp encode %s: %w", method, err)
		}
		req.Params = raw
	}

	ch := make(chan message, 1)
	c.pendMu.Lock()
	c.pend[id] = ch
	c.pendMu.Unlock()
	defer func() {
		c.pendMu.Lock()
		delete(c.pend, id)
		c.pendMu.Unlock()
	}()

	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.write.Lock()
	err = c.ws.WriteMessage(websocket.TextMessage, raw)
	c.write.Unlock()
	if err != nil {
		return fmt.Errorf("cdp write %s: %w", method, err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return fmt.Errorf("cdp %s: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("cdp decode %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close terminates the connection.
func (c *Conn) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	err := c.ws.Close()
	c.failAll(fmt.Errorf("cdp closed"))
	return err
}

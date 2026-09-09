package cdp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Driver drives CDP sessions natively — one WebSocket per endpoint, one page
// target per named session. Sessions persist across calls so a page opened
// under a name stays live until Close.
type Driver struct {
	// Timeout bounds a single CDP call (default 2m).
	Timeout time.Duration

	mu       sync.Mutex
	sessions map[string]*tabSession
	// creating guards per-session creation so concurrent tabFor calls for
	// the same name don't race to build duplicate connections. The main
	// mu is only held for map reads/writes, not for network I/O.
	creating map[string]*sync.WaitGroup
}

type tabSession struct {
	conn      *Conn
	sessionID string
	targetID  string
	refSeq    int
	refs      map[string]string // "@eN" -> element selector
}

func (d *Driver) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return 2 * time.Minute
}

// callCtx returns a context bounded by the driver's per-call timeout. If
// the parent context already has a deadline, it is returned unchanged so
// shorter caller deadlines still win. This ensures every CDP command is
// bounded even when the caller passes a long-lived context (e.g. the
// agent loop's background context) — without it, a hung CDP command
// such as Runtime.evaluate with awaitPromise:true on a never-resolving
// promise would block the goroutine indefinitely.
func (d *Driver) callCtx(parent context.Context) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, d.timeout())
}

// tabFor returns the live tab for a session, attaching on first use:
// connect to the endpoint, create (or adopt the first) page target, attach
// flattened, and enable the domains the driver needs. The main mutex is
// only held for map access, not for network I/O, so concurrent sessions
// don't serialize on each other's connection setup.
func (d *Driver) tabFor(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	name string,
) (*tabSession, error) {
	d.mu.Lock()
	if d.sessions == nil {
		d.sessions = make(map[string]*tabSession)
	}
	if d.creating == nil {
		d.creating = make(map[string]*sync.WaitGroup)
	}
	if ts := d.sessions[name]; ts != nil {
		d.mu.Unlock()
		return ts, nil
	}
	// If another goroutine is already creating this session, wait for it.
	if wg := d.creating[name]; wg != nil {
		d.mu.Unlock()
		wg.Wait()
		d.mu.Lock()
		if ts := d.sessions[name]; ts != nil {
			d.mu.Unlock()
			return ts, nil
		}
		// Creator failed; fall through to retry creation ourselves.
		d.mu.Unlock()
		return d.tabFor(ctx, endpoint, headers, name)
	}
	wg := &sync.WaitGroup{}
	wg.Add(1)
	d.creating[name] = wg
	d.mu.Unlock()

	ts, err := d.buildSession(ctx, endpoint, headers, name)

	d.mu.Lock()
	delete(d.creating, name)
	if err == nil {
		d.sessions[name] = ts
	}
	d.mu.Unlock()
	wg.Done()

	if err != nil {
		return nil, err
	}
	return ts, nil
}

// buildSession performs the network I/O to create a CDP session: dial,
// create/adopt a page target, attach flattened, and enable domains.
func (d *Driver) buildSession(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	name string,
) (*tabSession, error) {
	cctx, cancel := d.callCtx(ctx)
	defer cancel()

	conn, err := Dial(cctx, endpoint, headers)
	if err != nil {
		return nil, err
	}
	ts := &tabSession{conn: conn, refs: map[string]string{}}

	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := conn.Call(cctx, "", "Target.getTargets", nil, &targets); err != nil {
		conn.Close()
		return nil, err
	}
	targetID := ""
	for _, ti := range targets.TargetInfos {
		if ti.Type == "page" {
			targetID = ti.TargetID
			break
		}
	}
	if targetID == "" {
		var created struct {
			TargetID string `json:"targetId"`
		}
		if err := conn.Call(cctx, "", "Target.createTarget",
			map[string]any{"url": "about:blank"}, &created); err != nil {
			conn.Close()
			return nil, fmt.Errorf("cdp create target: %w", err)
		}
		targetID = created.TargetID
	}
	ts.targetID = targetID

	var attach struct {
		SessionID string `json:"sessionId"`
	}
	if err := conn.Call(cctx, "", "Target.attachToTarget",
		map[string]any{"targetId": targetID, "flatten": true}, &attach); err != nil {
		conn.Close()
		return nil, fmt.Errorf("cdp attach: %w", err)
	}
	ts.sessionID = attach.SessionID

	for _, domain := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if err := conn.Call(cctx, ts.sessionID, domain, nil, nil); err != nil {
			conn.Close()
			return nil, fmt.Errorf("cdp %s: %w", domain, err)
		}
	}
	return ts, nil
}

// evaluate runs JS in the page and returns the result decoded to JSON text.
func (d *Driver) evaluate(ctx context.Context, ts *tabSession, expr string) (any, error) {
	cctx, cancel := d.callCtx(ctx)
	defer cancel()
	var res struct {
		Result struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
		} `json:"result"`
		ExDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := ts.conn.Call(cctx, ts.sessionID, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	}, &res); err != nil {
		return nil, err
	}
	if res.ExDetails != nil {
		return nil, fmt.Errorf("page eval failed: %s", res.ExDetails.Text)
	}
	return res.Result.Value, nil
}

// Open navigates the session's tab to url and waits for load.
func (d *Driver) Open(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	session, url string,
) (string, error) {
	ts, err := d.tabFor(ctx, endpoint, headers, session)
	if err != nil {
		return "", err
	}
	var nav struct {
		FrameID   string `json:"frameId"`
		ErrorText string `json:"errorText"`
	}
	cctx, cancel := d.callCtx(ctx)
	defer cancel()
	if err := ts.conn.Call(cctx, ts.sessionID, "Page.navigate",
		map[string]any{"url": url}, &nav); err != nil {
		return "", err
	}
	if nav.ErrorText != "" {
		return "", fmt.Errorf("navigation failed: %s", nav.ErrorText)
	}
	// Wait for readiness: poll document.readyState.
	deadline := time.Now().Add(d.timeout())
	for {
		v, err := d.evaluate(ctx, ts, "document.readyState")
		if err == nil && v == "complete" {
			break
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("page did not finish loading within %s", d.timeout())
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	ts.refs = map[string]string{} // stale refs cleared on navigation
	return fmt.Sprintf("Opened %s", url), nil
}

// snapshotJS assigns data-rh-ref markers to interactive elements and emits a
// compact outline: title/url plus @eN refs for actionable elements and
// visible text lines.
const snapshotJS = `(() => {
  const interactive = document.querySelectorAll(
    'a,button,input,select,textarea,[role="button"],[role="link"],summary,[onclick],[tabindex]');
  const lines = [];
  let n = 0;
  const walk = (el) => {
    if (el.nodeType === Node.TEXT_NODE) {
      const t = el.textContent.trim();
      if (t.length > 1) lines.push(t.slice(0, 120));
      return;
    }
    if (el.nodeType !== Node.ELEMENT_NODE) return;
    const tag = el.tagName.toLowerCase();
    if (['script','style','noscript','svg','path'].includes(tag)) return;
    if (interactive && Array.prototype.includes.call(interactive, el)) {
      if (!el.hasAttribute('data-rh-ref')) el.setAttribute('data-rh-ref', String(++n));
      const label = (el.innerText || el.getAttribute('aria-label') ||
        el.getAttribute('placeholder') || el.getAttribute('value') || '').trim().slice(0, 80);
      lines.push('@e' + el.getAttribute('data-rh-ref') + ' <' + tag + '> ' + label);
      return;
    }
    for (const c of el.childNodes) walk(c);
  };
  walk(document.body);
  return JSON.stringify({
    title: document.title,
    url: location.href,
    refs: n,
    lines: lines.slice(0, 500),
  });
})()`

// Snapshot returns a text outline of the page. interactive=true lists only
// actionable elements with @eN refs.
func (d *Driver) Snapshot(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	session string,
	interactive bool,
) (string, error) {
	ts, err := d.tabFor(ctx, endpoint, headers, session)
	if err != nil {
		return "", err
	}
	v, err := d.evaluate(ctx, ts, snapshotJS)
	if err != nil {
		return "", err
	}
	raw, _ := v.(string)
	var doc struct {
		Title string   `json:"title"`
		URL   string   `json:"url"`
		Refs  int      `json:"refs"`
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", fmt.Errorf("decode snapshot: %w", err)
	}
	ts.refs = map[string]string{}
	ts.refSeq = doc.Refs
	var b strings.Builder
	fmt.Fprintf(&b, "Page: %s\nURL: %s\n\n", doc.Title, doc.URL)
	for _, line := range doc.Lines {
		if interactive && !strings.HasPrefix(line, "@e") {
			continue
		}
		b.WriteString(line + "\n")
	}
	return b.String(), nil
}

// resolveRefJS returns a JS expression locating the element for a ref
// (@eN → [data-rh-ref="N"]) or a CSS selector fallback.
func resolveRefJS(ref string) (string, error) {
	if m := regexp.MustCompile(`^@e(\d+)$`).FindStringSubmatch(ref); m != nil {
		return fmt.Sprintf(`document.querySelector('[data-rh-ref="%s"]')`, m[1]), nil
	}
	if strings.HasPrefix(ref, "css=") {
		return fmt.Sprintf("document.querySelector(%s)", strconv.Quote(ref[4:])), nil
	}
	return fmt.Sprintf("document.querySelector(%s)", strconv.Quote(ref)), nil
}

// Click clicks the element identified by ref via Input.dispatchMouseEvent at
// the element's center.
func (d *Driver) Click(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	session, ref string,
) (string, error) {
	ts, err := d.tabFor(ctx, endpoint, headers, session)
	if err != nil {
		return "", err
	}
	expr, err := resolveRefJS(ref)
	if err != nil {
		return "", err
	}
	v, err := d.evaluate(ctx, ts, `(() => {
	  const el = `+expr+`;
	  if (!el) return null;
	  el.scrollIntoView({block:'center'});
	  const r = el.getBoundingClientRect();
	  return {x: r.left + r.width/2, y: r.top + r.height/2, tag: el.tagName};
	})()`)
	if err != nil {
		return "", err
	}
	box, _ := v.(map[string]any)
	if box == nil {
		return "", fmt.Errorf("element %q not found", ref)
	}
	x, _ := box["x"].(float64)
	y, _ := box["y"].(float64)

	cctx, cancel := d.callCtx(ctx)
	defer cancel()
	for _, ev := range []map[string]any{
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1},
	} {
		if err := ts.conn.Call(cctx, ts.sessionID, "Input.dispatchMouseEvent", ev, nil); err != nil {
			return "", fmt.Errorf("dispatch %v: %w", ev["type"], err)
		}
	}
	return fmt.Sprintf("Clicked %s", ref), nil
}

// Fill focuses the element and inserts text via Input.insertText.
func (d *Driver) Fill(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	session, ref, text string,
) (string, error) {
	ts, err := d.tabFor(ctx, endpoint, headers, session)
	if err != nil {
		return "", err
	}
	expr, err := resolveRefJS(ref)
	if err != nil {
		return "", err
	}
	v, err := d.evaluate(ctx, ts, `(() => {
	  const el = `+expr+`;
	  if (!el) return null;
	  el.scrollIntoView({block:'center'});
	  el.focus();
	  if ('value' in el) { el.value = ''; el.dispatchEvent(new Event('input',{bubbles:true})); }
	  return el.tagName;
	})()`)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("element %q not found", ref)
	}
	cctx, cancel := d.callCtx(ctx)
	defer cancel()
	if err := ts.conn.Call(cctx, ts.sessionID, "Input.insertText",
		map[string]any{"text": text}, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("Filled %s", ref), nil
}

// Screenshot captures the page to outPath (PNG).
func (d *Driver) Screenshot(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	session, outPath string,
) (string, error) {
	ts, err := d.tabFor(ctx, endpoint, headers, session)
	if err != nil {
		return "", err
	}
	cctx, cancel := d.callCtx(ctx)
	defer cancel()
	var shot struct {
		Data string `json:"data"`
	}
	if err := ts.conn.Call(cctx, ts.sessionID, "Page.captureScreenshot",
		map[string]any{"format": "png"}, &shot); err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return "", fmt.Errorf("decode screenshot: %w", err)
	}
	if err := os.WriteFile(outPath, raw, 0o600); err != nil {
		return "", err
	}
	return fmt.Sprintf("Screenshot saved to %s", outPath), nil
}

// Eval evaluates a JS expression and returns the JSON-encoded result.
func (d *Driver) Eval(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	session, js string,
) (string, error) {
	ts, err := d.tabFor(ctx, endpoint, headers, session)
	if err != nil {
		return "", err
	}
	v, err := d.evaluate(ctx, ts, js)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

var waitDurationRe = regexp.MustCompile(`^(\d+)(ms|s)?$`)

// Wait waits for a CSS selector/@eN ref to appear, or sleeps for a duration
// like "2s"/"500ms".
func (d *Driver) Wait(
	ctx context.Context,
	endpoint string,
	headers http.Header,
	session, target string,
) (string, error) {
	target = strings.TrimSpace(target)
	if m := waitDurationRe.FindStringSubmatch(target); m != nil {
		n, _ := strconv.Atoi(m[1])
		dur := time.Duration(n) * time.Second
		if m[2] == "ms" {
			dur = time.Duration(n) * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(dur):
		}
		return fmt.Sprintf("Waited %s", dur), nil
	}

	ts, err := d.tabFor(ctx, endpoint, headers, session)
	if err != nil {
		return "", err
	}
	expr, err := resolveRefJS(target)
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(d.timeout())
	for {
		v, err := d.evaluate(ctx, ts, `!!(`+expr+`)`)
		if err == nil && v == true {
			return fmt.Sprintf("Found %s", target), nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for %q", target)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Close detaches the session tab (leaving the page open on custom endpoints
// is the caller's choice — here we close the target we own) and drops the
// connection.
func (d *Driver) Close(
	ctx context.Context,
	endpoint string,
	session string,
) error {
	d.mu.Lock()
	ts := d.sessions[session]
	delete(d.sessions, session)
	d.mu.Unlock()
	if ts == nil {
		return nil
	}
	if ts.targetID != "" {
		cctx, cancel := d.callCtx(ctx)
		_ = ts.conn.Call(cctx, "", "Target.closeTarget",
			map[string]any{"targetId": ts.targetID}, nil)
		cancel()
	}
	return ts.conn.Close()
}

// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// Session is one browser session bound to an agent conversation.
type Session struct {
	Key       string
	BackendID string
	Spec      BackendSpec
	Endpoint  *Endpoint
	REST      *CloudflareClient // non-nil for cloud-rest backends
	LastURL   string            // current page (REST backends navigate per call)
	CreatedAt time.Time
	LastUsed  time.Time
	closed    bool
}

// Manager owns browser sessions for an agent registry entry. Sessions are
// resolved lazily on first use and released on Close, timeout, or CloseAll.
type Manager struct {
	cfg       *config.BrowserToolsConfig
	driver    Driver
	runner    Runner
	workspace string
	now       func() time.Time

	// mu guards only the sessions/locks maps and the closed flag — session
	// resolution and teardown do blocking I/O (cloud session APIs, driver
	// close) and run under the per-key lock instead so they never stall
	// other sessions.
	mu       sync.Mutex
	sessions map[string]*Session
	locks    map[string]*sync.Mutex
	closed   bool

	// done stops the idle-session reaper; closeOnce guards closing it.
	done      chan struct{}
	closeOnce sync.Once
}

// NewManager creates a session manager. workspace is the agent workspace used
// for artifact output (screenshots). A background reaper tears down sessions
// whose LastUsed exceeds the configured session_timeout so idle cloud
// sessions are released even when no further tool calls arrive.
func NewManager(cfg *config.BrowserToolsConfig, workspace string) *Manager {
	m := &Manager{
		cfg:       cfg,
		driver:    &AgentBrowserDriver{},
		workspace: workspace,
		now:       time.Now,
		sessions:  map[string]*Session{},
		locks:     map[string]*sync.Mutex{},
		done:      make(chan struct{}),
	}
	go m.reapLoop()
	return m
}

// SetDriver overrides the driver (tests).
func (m *Manager) SetDriver(d Driver) { m.driver = d }

// SetRunner overrides the exec runner used by spawn/CLI resolvers (tests).
func (m *Manager) SetRunner(r Runner) { m.runner = r }

// Workspace returns the workspace used for browser artifacts.
func (m *Manager) Workspace() string { return m.workspace }

func (m *Manager) backendID() string {
	if m.cfg != nil {
		if id := m.cfg.DefaultBackend; id != "" {
			return id
		}
	}
	return "agent-browser"
}

func (m *Manager) backendConfig(id string) config.BrowserBackendConfig {
	if m.cfg != nil && m.cfg.Backends != nil {
		if bc, ok := m.cfg.Backends[id]; ok {
			return bc
		}
	}
	return config.BrowserBackendConfig{}
}

func (m *Manager) sessionTimeout() time.Duration {
	if m.cfg != nil {
		return m.cfg.GetSessionTimeout()
	}
	return 10 * time.Minute
}

// lockFor returns the per-session-key mutex, creating it on demand. Callers
// hold it around session create/use/close so a session is resolved or closed
// at most once even when Do calls race.
func (m *Manager) lockFor(key string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.locks[key]
	if l == nil {
		l = &sync.Mutex{}
		m.locks[key] = l
	}
	return l
}

// session returns (or lazily creates) the session for a key. Expired sessions
// are closed and re-created so stale cloud sessions are not reused. Must be
// called with the per-key lock held (see Do/Close).
func (m *Manager) session(ctx context.Context, key string) (*Session, error) {
	var expired *Session

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("browser session manager is closed")
	}
	if sess, ok := m.sessions[key]; ok && !sess.closed {
		if m.now().Sub(sess.LastUsed) <= m.sessionTimeout() {
			sess.LastUsed = m.now()
			m.mu.Unlock()
			return sess, nil
		}
		delete(m.sessions, key)
		sess.closed = true
		expired = sess
	}
	m.mu.Unlock()

	// Tear the expired session down outside the map lock — close may block on
	// driver or provider API calls.
	if expired != nil {
		_ = m.closeSession(ctx, expired)
	}

	spec, ok := Lookup(m.backendID())
	if !ok {
		return nil, fmt.Errorf("unknown browser backend %q (see tools.browser.backends)", m.backendID())
	}
	backendCfg := m.backendConfig(spec.ID)

	sess := &Session{
		Key:       key,
		BackendID: spec.ID,
		Spec:      spec,
		CreatedAt: m.now(),
		LastUsed:  m.now(),
	}

	switch spec.Kind {
	case KindCloudREST:
		client, err := NewCloudflareClient(backendCfg)
		if err != nil {
			return nil, err
		}
		sess.REST = client
	default:
		resolver, err := ResolverFor(spec, m.runner)
		if err != nil {
			return nil, err
		}
		ep, err := resolver.Resolve(ctx, backendCfg)
		if err != nil {
			return nil, err
		}
		sess.Endpoint = ep
	}

	m.mu.Lock()
	if m.closed {
		// CloseAll ran while the endpoint was being resolved — tear the new
		// session down instead of leaking it into a cleared map.
		m.mu.Unlock()
		_ = m.closeSession(ctx, sess)
		return nil, fmt.Errorf("browser session manager is closed")
	}
	m.sessions[key] = sess
	m.mu.Unlock()
	return sess, nil
}

// requireCap fails fast when the backend does not support an action.
func requireCap(spec BackendSpec, capability Capability, tool string) error {
	if spec.HasCapability(capability) {
		return nil
	}
	return fmt.Errorf("backend %q does not support %s (status: %s)", spec.ID, tool, spec.Status)
}

// Do executes a browser action for the session key. action is the tool
// capability; args are action-specific (url, ref, text, outPath, js, target).
func (m *Manager) Do(
	ctx context.Context,
	key string,
	capability Capability,
	args map[string]string,
) (string, error) {
	lock := m.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	sess, err := m.session(ctx, key)
	if err != nil {
		return "", err
	}

	// REST-only backend dispatch (Cloudflare).
	if sess.REST != nil {
		return m.doREST(ctx, sess, capability, args)
	}

	if err := requireCap(sess.Spec, capability, string(capability)); err != nil {
		return "", err
	}

	ep := sess.Endpoint
	switch capability {
	case CapOpen:
		out, err := m.driver.Open(ctx, ep, sess.Key, args["url"])
		if err == nil {
			sess.LastURL = args["url"]
		}
		return out, err
	case CapSnapshot:
		return m.driver.Snapshot(ctx, ep, sess.Key, args["interactive"] == "true")
	case CapClick:
		return m.driver.Click(ctx, ep, sess.Key, args["ref"])
	case CapFill:
		return m.driver.Fill(ctx, ep, sess.Key, args["ref"], args["text"])
	case CapScreenshot:
		return m.driver.Screenshot(ctx, ep, sess.Key, args["path"])
	case CapEval:
		return m.driver.Eval(ctx, ep, sess.Key, args["js"])
	case CapWait:
		return m.driver.Wait(ctx, ep, sess.Key, args["target"])
	default:
		return "", fmt.Errorf("unknown browser action %q", capability)
	}
}

// doREST handles stateless Cloudflare Browser Rendering calls. Each call
// navigates fresh; the "current page" is tracked via sess.LastURL.
func (m *Manager) doREST(
	ctx context.Context,
	sess *Session,
	capability Capability,
	args map[string]string,
) (string, error) {
	if err := requireCap(sess.Spec, capability, string(capability)); err != nil {
		return "", err
	}
	if capability == CapOpen {
		sess.LastURL = args["url"]
		return fmt.Sprintf("URL registered for stateless backend %q: %s", sess.BackendID, args["url"]), nil
	}
	target := args["url"]
	if target == "" {
		target = sess.LastURL
	}
	if target == "" {
		return "", fmt.Errorf("no page open — call browser_open first")
	}
	switch capability {
	case CapSnapshot:
		return sess.REST.Snapshot(ctx, target)
	case CapScreenshot:
		return sess.REST.Screenshot(ctx, target, args["path"])
	default:
		return "", fmt.Errorf("backend %q does not support %s", sess.BackendID, capability)
	}
}

// Close releases the session for a key (idempotent).
func (m *Manager) Close(ctx context.Context, key string) error {
	lock := m.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	m.mu.Lock()
	sess := m.sessions[key]
	if sess != nil {
		delete(m.sessions, key)
		sess.closed = true
	}
	m.mu.Unlock()

	if sess == nil {
		return nil
	}
	return m.closeSession(ctx, sess)
}

// closeSession performs the actual teardown. Callers must remove the session
// from m.sessions and mark it closed under m.mu first; the driver close and
// endpoint cleanup run here outside the lock since they may block on I/O.
func (m *Manager) closeSession(ctx context.Context, sess *Session) error {
	if sess.Spec.Kind == KindCloudREST {
		return nil // stateless — nothing to release
	}
	if sess.Endpoint == nil {
		return nil
	}
	if sess.Spec.Kind != KindCustom {
		if err := m.driver.Close(ctx, sess.Endpoint, sess.Key); err != nil {
			logger.WarnCF("browser", "browser close failed",
				map[string]any{"session": sess.Key, "error": err.Error()})
		}
	}
	if sess.Endpoint.Cleanup != nil {
		if err := sess.Endpoint.Cleanup(ctx); err != nil {
			logger.WarnCF("browser", "browser endpoint cleanup failed",
				map[string]any{"session": sess.Key, "backend": sess.BackendID, "error": err.Error()})
			return err
		}
	}
	return nil
}

// reapLoop periodically releases sessions that have been idle longer than
// session_timeout, so cloud provider sessions are stopped (and stop billing)
// even when no further tool calls arrive.
func (m *Manager) reapLoop() {
	interval := m.sessionTimeout()
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.reapExpired()
		}
	}
}

// reapExpired closes every session past its idle timeout. Each key is
// processed under its per-key lock so an in-flight Do is never torn down
// mid-call.
func (m *Manager) reapExpired() {
	m.mu.Lock()
	expired := make([]string, 0, len(m.sessions))
	for key, sess := range m.sessions {
		if !sess.closed && m.now().Sub(sess.LastUsed) > m.sessionTimeout() {
			expired = append(expired, key)
		}
	}
	m.mu.Unlock()

	for _, key := range expired {
		lock := m.lockFor(key)
		lock.Lock()
		m.mu.Lock()
		sess, ok := m.sessions[key]
		if ok && !sess.closed && m.now().Sub(sess.LastUsed) > m.sessionTimeout() {
			delete(m.sessions, key)
			sess.closed = true
		} else {
			sess = nil
		}
		m.mu.Unlock()
		if sess != nil {
			// Background reaping has no caller context; use a bounded one.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = m.closeSession(ctx, sess)
			cancel()
		}
		lock.Unlock()
	}
}

// CloseAll releases every session and stops the reaper — called on agent
// teardown. Idempotent.
func (m *Manager) CloseAll(ctx context.Context) {
	m.closeOnce.Do(func() { close(m.done) })

	m.mu.Lock()
	m.closed = true
	all := m.sessions
	m.sessions = map[string]*Session{}
	for _, sess := range all {
		sess.closed = true
	}
	m.mu.Unlock()

	// Close each session under its per-key lock so a concurrent Do holding
	// that lock finishes before teardown begins. If a Do is in-flight, the
	// teardown continues in the background instead of blocking shutdown on a
	// driver call that may take up to the driver timeout.
	for key, sess := range all {
		lock := m.lockFor(key)
		if !lock.TryLock() {
			go func(l *sync.Mutex, s *Session) {
				l.Lock()
				defer l.Unlock()
				cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				_ = m.closeSession(cctx, s)
				cancel()
			}(lock, sess)
			continue
		}
		_ = m.closeSession(ctx, sess)
		lock.Unlock()
	}
}

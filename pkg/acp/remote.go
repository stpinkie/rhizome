package acp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
)

// RemoteMux serves ACP over many concurrent remote connections — the
// acp.server.remote path. Each accepted stream gets its own
// connection-scoped Server (its own session map, event pump, and client
// conn) sharing the daemon's agent runner; a single shared permission
// hook resolves each approval request to the owning connection, and the
// mux itself is an additive bus stream delegate so remote sessions stream
// on their own connection.
type RemoteMux struct {
	runner AgentRunner
	opts   Options
	log    *slog.Logger

	mu      sync.Mutex
	servers map[*Server]io.Closer // server → its transport (closed on Close)
	started bool
	closed  bool
	wg      sync.WaitGroup
}

var _ bus.StreamDelegate = (*RemoteMux)(nil)

// NewRemoteMux builds a remote ACP multiplexer over the shared agent
// runner. opts applies to every connection-scoped server; the mux forces
// SkipPermissionHook on them because it mounts one shared approver.
func NewRemoteMux(runner AgentRunner, opts Options) *RemoteMux {
	opts.SkipPermissionHook = true
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &RemoteMux{
		runner:  runner,
		opts:    opts,
		log:     log,
		servers: map[*Server]io.Closer{},
	}
}

// Start mounts the shared permission hook (name distinct from the stdio
// server's so a process hosting both does not collide — hook registration
// replaces same-name hooks).
func (m *RemoteMux) Start() error {
	m.mu.Lock()
	if m.started || m.closed {
		m.mu.Unlock()
		return fmt.Errorf("acp remote mux already started or closed")
	}
	m.started = true
	m.mu.Unlock()
	if err := m.runner.MountHook(
		agent.NamedHook("acp-permission-remote", &toolApprover{resolve: m.resolveSessionConn}),
	); err != nil {
		return fmt.Errorf("mounting remote acp permission hook: %w", err)
	}
	return nil
}

// Serve binds one remote stream to a fresh connection-scoped Server and
// runs the ACP agent side until the connection dies. It is the body of the
// /rhizome/acp/1.0.0 stream handler; callers perform the trust gate before
// invoking it. Blocks until the transport closes.
func (m *RemoteMux) Serve(rw io.ReadWriteCloser) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = rw.Close()
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() { _ = rw.Close() }()

	srv := NewServer(m.runner, m.opts)
	conn := acpsdk.NewAgentSideConnection(srv, rw, rw)
	srv.Bind(conn)
	if err := srv.Start(); err != nil {
		m.log.Warn("acp remote: connection-scoped server failed to start", "error", err)
		return
	}

	m.mu.Lock()
	m.servers[srv] = rw
	m.mu.Unlock()
	m.log.Info("acp remote: connection accepted")
	defer func() {
		m.mu.Lock()
		delete(m.servers, srv)
		m.mu.Unlock()
		srv.Close()
		m.log.Info("acp remote: connection closed")
	}()

	<-conn.Done()
}

// Close stops accepting work and tears down every live connection-scoped
// server, closing each transport so its Serve goroutine exits. The caller
// owns the underlying stream handler registration.
func (m *RemoteMux) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	type conn struct {
		srv *Server
		rw  io.Closer
	}
	conns := make([]conn, 0, len(m.servers))
	for srv, rw := range m.servers {
		conns = append(conns, conn{srv: srv, rw: rw})
	}
	m.servers = map[*Server]io.Closer{}
	m.mu.Unlock()
	for _, c := range conns {
		_ = c.rw.Close()
		c.srv.Close()
	}
	m.wg.Wait()
}

// resolveSessionConn is the shared toolApprover resolver: it finds the
// owning server for a chat id across every live remote connection and
// returns that connection so permission prompts land on the right peer.
func (m *RemoteMux) resolveSessionConn(chatID string) (*acpSession, ClientConn, PermissionPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for srv := range m.servers {
		if sess := srv.sessionByChatID(chatID); sess != nil {
			conn, _ := srv.connOrErr()
			return sess, conn, m.policy()
		}
	}
	return nil, nil, ""
}

func (m *RemoteMux) policy() PermissionPolicy {
	if m.opts.Policy == "" {
		return PermissionPrompt
	}
	return m.opts.Policy
}

// GetStreamer implements bus.StreamDelegate: the connection owning the
// session supplies the streamer. Installed via bus.AddStreamDelegate so
// remote sessions multiplex with any existing channel delegate.
func (m *RemoteMux) GetStreamer(
	ctx context.Context,
	channel, chatID, sessionKey string,
) (bus.Streamer, bool) {
	if channel != ChannelName {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for srv := range m.servers {
		if st, ok := srv.GetStreamer(ctx, channel, chatID, sessionKey); ok {
			return st, true
		}
	}
	return nil, false
}

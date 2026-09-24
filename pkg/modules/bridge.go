// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/rhizome/p2putil"
)

// Module stream bridge. A module that declares `protocols` receives
// RHIZOME_BRIDGE_ADDR + RHIZOME_BRIDGE_TOKEN in its process environment.
// The daemon listens on a dedicated loopback address — independent of the
// gateway mux so bridging works under `--no-gateway` — and splices
// authenticated loopback connections onto libp2p streams byte-for-byte.
//
// Two directions share the same JSON-hello handshake on the loopback wire:
//
//	outbound (module → peer): the module connects to the bridge, sends
//	  {"token","action":"dial","peer":<peer-id-or-multiaddr>,"protocol":<id>},
//	  and the daemon opens a fresh peer stream and splices.
//	inbound (peer → module): a peer stream arrives on a registered protocol
//	  handler; the daemon dials the module's own listener (published in
//	  <module_dir>/bridge.addr), sends
//	  {"token","action":"accept","peer":<source peer id>,"protocol":<id>},
//	  and splices.
const (
	// bridgeTokenFile is the per-module bearer token file (0600) minted at
	// install/bridge-start. The token gates the loopback listener — a token
	// resolves to exactly one module and its declared protocol allowlist.
	bridgeTokenFile = "bridge-token"
	// bridgeAddrFile is where a serving module publishes its own loopback
	// address so inbound peer streams can be spliced to it.
	bridgeAddrFile = "bridge.addr"

	bridgeHelloMaxBytes = 4 << 10
	bridgeHelloTimeout  = 5 * time.Second
	bridgeDialTimeout   = 15 * time.Second
)

// bridgeHello is the single JSON line exchanged on the loopback wire before
// raw byte splicing begins. Action is "dial" (module-initiated outbound) or
// "accept" (daemon-initiated inbound). Peer is a peer ID or full multiaddr
// for "dial", and the inbound source peer ID for "accept".
type bridgeHello struct {
	Token    string `json:"token"`
	Action   string `json:"action"`
	Peer     string `json:"peer"`
	Protocol string `json:"protocol"`
}

// Bridge owns the module stream bridge: a loopback listener, the per-module
// token map, and the libp2p stream handlers claimed for declared protocols.
type Bridge struct {
	host      host.Host
	mgr       *Manager
	ln        net.Listener
	isTrusted func(peer.ID) bool // inbound peer gate; nil refuses all inbound

	mu       sync.Mutex
	claims   map[protocol.ID]string // protocol → owning module ID
	tokens   map[string]string      // module ID → bridge token
	done     chan struct{}
	closeErr error
	closed   bool
}

// NewBridge binds the bridge's loopback listener (127.0.0.1:0). Handlers are
// not registered until Start. isTrusted gates inbound peer streams (the mesh
// trust set); pass nil to refuse all inbound splicing — outbound module
// dials remain token-gated and module-initiated either way.
func NewBridge(h host.Host, mgr *Manager, isTrusted func(peer.ID) bool) (*Bridge, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("module bridge listen: %w", err)
	}
	return &Bridge{
		host:      h,
		mgr:       mgr,
		ln:        ln,
		isTrusted: isTrusted,
		claims:    map[protocol.ID]string{},
		tokens:    map[string]string{},
		done:      make(chan struct{}),
	}, nil
}

// Addr returns the bridge's loopback address for RHIZOME_BRIDGE_ADDR.
func (b *Bridge) Addr() string {
	return b.ln.Addr().String()
}

// Start mints/refreshes per-module bridge tokens for every enabled
// protocol-declaring module, claims their declared protocols as libp2p
// stream handlers (skipping protocols already owned by core or another
// registrant), and begins serving module dial requests.
func (b *Bridge) Start() {
	for _, spec := range b.mgr.specs() {
		if len(spec.Protocols) == 0 || !b.mgr.moduleConfig(spec.ID).Enabled {
			continue
		}
		id := spec.ID
		tok, err := b.mgr.ensureBridgeToken(id)
		if err != nil {
			logger.WarnCF("modules", "bridge token mint failed", map[string]any{
				"module": id, "error": err.Error(),
			})
			continue
		}
		b.mu.Lock()
		b.tokens[id] = tok
		b.mu.Unlock()
		for _, p := range spec.Protocols {
			pid := protocol.ID(p)
			if owner := b.host.Mux().Protocols(); containsProtocol(owner, pid) {
				logger.WarnCF("modules", "bridge protocol already registered; skipping", map[string]any{
					"module": id, "protocol": p,
				})
				continue
			}
			b.mu.Lock()
			if prior, dup := b.claims[pid]; dup {
				b.mu.Unlock()
				logger.WarnCF("modules", "bridge protocol claimed by another module; skipping", map[string]any{
					"module": id, "protocol": p, "owner": prior,
				})
				continue
			}
			b.claims[pid] = id
			b.mu.Unlock()
			b.host.SetStreamHandler(pid, b.serveInbound)
			logger.InfoCF("modules", "module protocol bridged", map[string]any{
				"module": id, "protocol": p,
			})
		}
	}
	go b.acceptLoop()
}

func containsProtocol(ids []protocol.ID, want protocol.ID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// Close stops the accept loop, closes the listener, and releases all claimed
// protocol handlers.
func (b *Bridge) Close() error {
	b.mu.Lock()
	if b.closed {
		err := b.closeErr
		b.mu.Unlock()
		return err
	}
	b.closed = true
	for pid := range b.claims {
		b.host.RemoveStreamHandler(pid)
	}
	b.mu.Unlock()
	close(b.done)
	err := b.ln.Close()
	b.mu.Lock()
	b.closeErr = err
	b.mu.Unlock()
	return err
}

// moduleForToken resolves a presented bearer token to its module ID using a
// constant-time comparison over every registered token.
func (b *Bridge) moduleForToken(token string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var found string
	ok := false
	for id, tok := range b.tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(tok)) == 1 {
			found, ok = id, true
		}
	}
	return found, ok
}

// serveInbound is the libp2p stream handler for every claimed protocol. It
// reads the owning module's published loopback address, sends an "accept"
// hello, and splices the peer stream to the module.
func (b *Bridge) serveInbound(s network.Stream) {
	pid := s.Protocol()
	b.mu.Lock()
	moduleID, ok := b.claims[pid]
	b.mu.Unlock()
	if !ok {
		_ = s.Reset()
		return
	}
	remoteID := s.Conn().RemotePeer()
	if b.isTrusted == nil || !b.isTrusted(remoteID) {
		logger.WarnCF("modules", "inbound module stream refused: untrusted peer", map[string]any{
			"module": moduleID, "protocol": string(pid), "peer": remoteID.String(),
		})
		_ = s.Reset()
		return
	}
	remote := remoteID.String()
	fields := map[string]any{"module": moduleID, "protocol": string(pid), "peer": remote}

	addr, err := b.mgr.readBridgeAddr(moduleID)
	if err != nil {
		logger.WarnCF("modules", "inbound stream refused: module bridge.addr unreadable", fields)
		_ = s.Reset()
		return
	}
	conn, err := net.DialTimeout("tcp", addr, bridgeDialTimeout)
	if err != nil {
		fields["error"] = err.Error()
		logger.WarnCF("modules", "inbound stream refused: module dial failed", fields)
		_ = s.Reset()
		return
	}
	b.mu.Lock()
	tok := b.tokens[moduleID]
	b.mu.Unlock()
	hello, err := json.Marshal(bridgeHello{
		Token:    tok,
		Action:   "accept",
		Peer:     remote,
		Protocol: string(pid),
	})
	if err != nil {
		_ = conn.Close()
		_ = s.Reset()
		return
	}
	_ = conn.SetDeadline(time.Now().Add(bridgeHelloTimeout))
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		_ = conn.Close()
		_ = s.Reset()
		return
	}
	_ = conn.SetDeadline(time.Time{})
	logger.DebugCF("modules", "inbound module stream bridged", fields)
	splice(conn, s)
}

// acceptLoop serves module-initiated outbound dial requests on the loopback
// listener.
func (b *Bridge) acceptLoop() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			select {
			case <-b.done:
				return
			default:
			}
			logger.WarnCF("modules", "bridge accept failed", map[string]any{"error": err.Error()})
			return
		}
		go b.serveOutbound(conn)
	}
}

// serveOutbound authenticates a module's hello, enforces its declared
// protocol allowlist, opens the requested peer stream, and splices.
func (b *Bridge) serveOutbound(conn net.Conn) {
	// The hello reader may over-read into its buffer (a module that writes
	// hello+payload in one segment) — the buffered reader must stay the
	// splice source so no payload bytes are lost.
	br := bufio.NewReaderSize(conn, bridgeHelloMaxBytes+1)
	_ = conn.SetDeadline(time.Now().Add(bridgeHelloTimeout))
	hello, err := readBridgeHello(br)
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		logger.WarnCF("modules", "bridge hello refused", map[string]any{"error": err.Error()})
		_ = conn.Close()
		return
	}
	if hello.Action != "dial" {
		logger.WarnCF("modules", "bridge hello refused: unsupported action", map[string]any{
			"action": hello.Action,
		})
		_ = conn.Close()
		return
	}
	moduleID, ok := b.moduleForToken(hello.Token)
	if !ok {
		logger.WarnCF("modules", "bridge dial refused: bad token", nil)
		_ = conn.Close()
		return
	}
	fields := map[string]any{"module": moduleID, "protocol": hello.Protocol, "peer": hello.Peer}
	spec, _, ok := b.mgr.lookupSpec(moduleID)
	if !ok || !declaresProtocol(spec, hello.Protocol) {
		logger.WarnCF("modules", "bridge dial refused: undeclared protocol", fields)
		_ = conn.Close()
		return
	}
	target, err := b.resolvePeer(hello.Peer)
	if err != nil {
		fields["error"] = err.Error()
		logger.WarnCF("modules", "bridge dial refused: bad peer", fields)
		_ = conn.Close()
		return
	}
	stream, err := p2putil.OpenProtocolStream(
		context.Background(), b.host, target, protocol.ID(hello.Protocol), bridgeDialTimeout,
	)
	if err != nil {
		fields["error"] = err.Error()
		logger.WarnCF("modules", "bridge dial failed: peer stream open failed", fields)
		_ = conn.Close()
		return
	}
	logger.DebugCF("modules", "outbound module stream bridged", fields)
	splice(bufferedConn{Conn: conn, r: br}, stream)
}

// resolvePeer accepts a bare peer ID or a full peer multiaddr; a multiaddr's
// addresses are added to the peerstore so dialing works for freshly
// discovered peers.
func (b *Bridge) resolvePeer(s string) (peer.ID, error) {
	return p2putil.ResolvePeerAddr(b.host, s)
}

func declaresProtocol(spec ModuleSpec, p string) bool {
	for _, declared := range spec.Protocols {
		if declared == p {
			return true
		}
	}
	return false
}

// bufferedConn reads through a bufio.Reader that may already hold
// post-hello payload bytes; writes and lifecycle go to the raw conn.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// readBridgeHello reads one newline-terminated JSON hello bounded by
// bridgeHelloMaxBytes. Bytes past the newline stay in r for the splice.
func readBridgeHello(r *bufio.Reader) (bridgeHello, error) {
	var hello bridgeHello
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		line = append(line, frag...)
		if err == bufio.ErrBufferFull {
			if len(line) > bridgeHelloMaxBytes {
				return hello, fmt.Errorf("hello exceeds %d bytes", bridgeHelloMaxBytes)
			}
			continue
		}
		if err != nil {
			return hello, fmt.Errorf("hello read: %w", err)
		}
		break // ReadSlice returned a line terminated by '\n'
	}
	if len(line) > bridgeHelloMaxBytes {
		return hello, fmt.Errorf("hello exceeds %d bytes", bridgeHelloMaxBytes)
	}
	line = line[:len(line)-1] // drop the trailing '\n'
	if err := json.Unmarshal(line, &hello); err != nil {
		return hello, fmt.Errorf("hello parse: %w", err)
	}
	return hello, nil
}

// splice copies bytes in both directions until either side closes, then
// closes both. Half-close is used when supported so a peer that finishes
// writing still receives the rest of the other direction's bytes.
func splice(a io.ReadWriteCloser, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); closeWrite(a); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); closeWrite(b); done <- struct{}{} }()
	<-done
	<-done
	_ = a.Close()
	_ = b.Close()
}

type closeWriter interface{ CloseWrite() error }

func closeWrite(c io.Closer) {
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// --- token / addr files -------------------------------------------------

// bridgeTokenPath is <module_dir>/bridge-token.
func (m *Manager) bridgeTokenPath(id string) string {
	return filepath.Join(m.Dir(id), bridgeTokenFile)
}

// ensureBridgeToken returns the module's bridge token, minting a fresh
// 256-bit token (0600) when absent.
func (m *Manager) ensureBridgeToken(id string) (string, error) {
	if tok, err := m.bridgeToken(id); err == nil && tok != "" {
		return tok, nil
	}
	return m.mintBridgeToken(id)
}

// mintBridgeToken rotates the module's bridge token — called on install and
// lazily by the bridge when no token exists.
func (m *Manager) mintBridgeToken(id string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("bridge token mint: %w", err)
	}
	tok := hex.EncodeToString(buf)
	if err := os.MkdirAll(m.Dir(id), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(m.bridgeTokenPath(id), []byte(tok+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("bridge token write: %w", err)
	}
	return tok, nil
}

// bridgeToken reads the module's current bridge token ("" when absent).
func (m *Manager) bridgeToken(id string) (string, error) {
	data, err := os.ReadFile(m.bridgeTokenPath(id))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// readBridgeAddr reads the module's published loopback listener address
// from <module_dir>/bridge.addr.
func (m *Manager) readBridgeAddr(id string) (string, error) {
	// G304: path is under the managed module dir.
	data, err := os.ReadFile(filepath.Join(m.Dir(id), bridgeAddrFile))
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(string(data))
	if addr == "" {
		return "", fmt.Errorf("module %q bridge.addr is empty", id)
	}
	return addr, nil
}

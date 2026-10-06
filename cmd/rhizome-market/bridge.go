// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stpinkie/rhizome/pkg/acp"
	"github.com/stpinkie/rhizome/pkg/logger"
)

// Module side of the stream bridge — mirrors pkg/modules/bridge.go. The
// module listens on its own loopback address, publishes it in
// <module_dir>/bridge.addr, and the daemon splices inbound peer streams
// onto it after a JSON hello. Outbound dials go the other way: connect to
// RHIZOME_BRIDGE_ADDR, send a "dial" hello, get a spliced peer stream.
const (
	bridgeHelloMaxBytes = 4 << 10
	bridgeHelloTimeout  = 5 * time.Second
	bridgeDialTimeout   = 15 * time.Second
)

type bridgeHello struct {
	Token     string `json:"token"`
	Action    string `json:"action"`
	Peer      string `json:"peer"`
	Protocol  string `json:"protocol,omitempty"`
	Op        string `json:"op,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Ref       string `json:"ref,omitempty"`
	ValueHash string `json:"value_hash,omitempty"`
	NS        string `json:"ns,omitempty"` // dht_provide/dht_find namespace
}

// bridgeResponse mirrors pkg/modules' non-splice answer line; Peers carries
// dht_find results.
type bridgeResponse struct {
	OK    bool         `json:"ok"`
	Error string       `json:"error,omitempty"`
	Peers []bridgePeer `json:"peers,omitempty"`
}

// bridgePeer is one DHT-discovered provider — peer id + observed addrs.
type bridgePeer struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs,omitempty"`
}

// bridgeServer owns the inbound accept listener plus the conn cap; each
// verified connection gets its own marketAgent bound to the authenticated
// peer id and a conn sequence id the session manager finalizes on drop.
type bridgeServer struct {
	ln        net.Listener
	moduleDir string
	token     *tokenProvider
	audit     *auditLogger
	mgr       *sessionMgr
	sem       chan struct{} // concurrent-connection cap
	wg        sync.WaitGroup
	closed    chan struct{}
	once      sync.Once
}

// startBridge binds the module's accept listener and publishes bridge.addr.
// connCap bounds simultaneous bridged streams (hello-gated, peer-verified
// — the session manager's per-peer and global caps bound the work
// underneath each connection).
func startBridge(
	moduleDir string, token *tokenProvider, mgr *sessionMgr,
	connCap int, audit *auditLogger,
) (*bridgeServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bridge listen: %w", err)
	}
	b := &bridgeServer{
		ln:        ln,
		moduleDir: moduleDir,
		token:     token,
		audit:     audit,
		mgr:       mgr,
		sem:       make(chan struct{}, connCap),
		closed:    make(chan struct{}),
	}
	if err := writeFileAtomic(
		filepath.Join(moduleDir, bridgeAddrFile),
		[]byte(ln.Addr().String()+"\n"),
	); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("bridge.addr write: %w", err)
	}
	go b.acceptLoop()
	return b, nil
}

// outboundReady reports whether the daemon bridge address is configured —
// the outbound dial path needs RHIZOME_BRIDGE_ADDR.
func outboundReady() bool { return os.Getenv("RHIZOME_BRIDGE_ADDR") != "" }

func (b *bridgeServer) acceptLoop() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			select {
			case <-b.closed:
				return
			default:
			}
			logger.WarnCF("market", "bridge accept failed", map[string]any{"error": err.Error()})
			return
		}
		select {
		case b.sem <- struct{}{}:
		default:
			// Cap reached — refuse rather than queue.
			b.audit.log("market.stream.rejected", map[string]any{"reason": "conn cap"})
			_ = conn.Close()
			continue
		}
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			defer func() { <-b.sem }()
			b.serveConn(conn)
		}()
	}
}

// serveConn reads the daemon's hello, verifies the module's token and the
// declared protocol, then splices the rest of the conn onto the ACP agent.
func (b *bridgeServer) serveConn(conn net.Conn) {
	br := bufio.NewReaderSize(conn, bridgeHelloMaxBytes+1)
	_ = conn.SetDeadline(time.Now().Add(bridgeHelloTimeout))
	hello, err := readBridgeHello(br)
	_ = conn.SetDeadline(time.Time{})
	if err != nil {
		b.audit.log(
			"market.stream.rejected",
			map[string]any{"reason": "hello read: " + err.Error()},
		)
		_ = conn.Close()
		return
	}
	if hello.Action != "accept" {
		b.audit.log("market.stream.rejected", map[string]any{"reason": "action " + hello.Action})
		_ = conn.Close()
		return
	}
	tok := b.token.token()
	if tok == "" || subtle.ConstantTimeCompare([]byte(hello.Token), []byte(tok)) != 1 {
		b.audit.log(
			"market.stream.rejected",
			map[string]any{"peer": hello.Peer, "reason": "bad token"},
		)
		_ = conn.Close()
		return
	}
	if hello.Protocol != acp.RemoteProtocolID {
		b.audit.log("market.stream.rejected", map[string]any{
			"peer": hello.Peer, "protocol": hello.Protocol, "reason": "undeclared protocol",
		})
		_ = conn.Close()
		return
	}
	b.audit.log("market.stream.accept", map[string]any{
		"peer": hello.Peer, "protocol": hello.Protocol,
	})
	logger.InfoCF("market", "bridged stream accepted", map[string]any{
		"peer": hello.Peer, "protocol": hello.Protocol,
	})
	// bufferedConn keeps any post-hello bytes the reader already consumed.
	agent := newConnAgent(hello.Peer, b.mgr.nextConnID(), b.mgr, b.audit)
	b.serveACP(bufferedConn{Conn: conn, r: br}, agent)
}

// serveACP runs one verified peer connection through the ACP agent side —
// transport-agnostic by construction (any io.ReadWriteCloser), so the
// Track 110 HTTPS path can drive the same gate/session machinery without
// touching it. Blocks until the transport closes, then finalizes the
// conn's sessions (a dropped peer can never leave an agent running).
func (b *bridgeServer) serveACP(rwc io.ReadWriteCloser, a *marketAgent) {
	serveAgentConn(rwc, a, b.mgr)
}

// Close stops accepting, closes the listener, and removes bridge.addr.
// In-flight connections are closed by their own Done() teardown once the
// listener drops; the WaitGroup bounds shutdown.
func (b *bridgeServer) Close() {
	b.once.Do(func() {
		close(b.closed)
		_ = b.ln.Close()
		_ = os.Remove(filepath.Join(b.moduleDir, bridgeAddrFile))
	})
	// Give in-flight ACP connections a brief drain window.
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

// bufferedConn reads through a bufio.Reader that may already hold
// post-hello payload bytes (the daemon splices byte-for-byte and a peer
// may write immediately); writes/lifecycle go to the raw conn.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// readBridgeHello reads one newline-terminated JSON hello bounded by
// bridgeHelloMaxBytes; bytes past the newline stay in r for the splice.
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
		break
	}
	if len(line) > bridgeHelloMaxBytes {
		return hello, fmt.Errorf("hello exceeds %d bytes", bridgeHelloMaxBytes)
	}
	line = line[:len(line)-1]
	if err := json.Unmarshal(line, &hello); err != nil {
		return hello, fmt.Errorf("hello parse: %w", err)
	}
	return hello, nil
}

// dialPeer dials the daemon bridge and requests an outbound peer stream —
// the Track 103 buy path. Exported-for-tests shape: same hello contract as
// pkg/modules' serveOutbound expects ({token, action:"dial", peer,
// protocol}).
func dialPeer(peer, protocol string) (net.Conn, error) {
	addr := os.Getenv("RHIZOME_BRIDGE_ADDR")
	if addr == "" {
		return nil, fmt.Errorf(
			"RHIZOME_BRIDGE_ADDR unset — module not running under the daemon bridge",
		)
	}
	return dialBridge(addr, newTokenProvider("").token(), peer, protocol)
}

// dialBridge is the testable core of dialPeer: connect, send the hello,
// return the spliced conn. The daemon validates token + declared protocol.
func dialBridge(addr, token, peer, protocol string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", addr, bridgeDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("bridge dial %s: %w", addr, err)
	}
	hello, err := json.Marshal(bridgeHello{
		Token: token, Action: "dial", Peer: peer, Protocol: protocol,
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(bridgeHelloTimeout))
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("bridge hello write: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// reportOutcome sends a peer_score report to the daemon over the bridge —
// the module's write path into the mesh's peer-score store. Best-effort:
// settlement reporting must never stall a session state transition, so the
// caller logs-and-drops errors (audit line carries the attempt).
func reportOutcome(peerID, op, outcome, sessionID, valueHash string) error {
	addr := os.Getenv("RHIZOME_BRIDGE_ADDR")
	if addr == "" {
		return fmt.Errorf("RHIZOME_BRIDGE_ADDR unset")
	}
	conn, err := net.DialTimeout("tcp", addr, bridgeDialTimeout)
	if err != nil {
		return fmt.Errorf("bridge dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	hello, err := json.Marshal(bridgeHello{
		Token: newTokenProvider("").token(), Action: "peer_score",
		Peer: peerID, Op: op, Outcome: outcome, Ref: sessionID, ValueHash: valueHash,
	})
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(bridgeHelloTimeout))
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		return fmt.Errorf("bridge hello write: %w", err)
	}
	line, err := bufio.NewReaderSize(conn, bridgeHelloMaxBytes).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("bridge response read: %w", err)
	}
	var resp bridgeResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("bridge response parse: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("peer_score refused: %s", resp.Error)
	}
	return nil
}

// bridgeAction runs one non-splice request/response action against the
// daemon bridge: send the hello, read one JSON line, surface the refusal.
func bridgeAction(hello bridgeHello) (*bridgeResponse, error) {
	addr := os.Getenv("RHIZOME_BRIDGE_ADDR")
	if addr == "" {
		return nil, fmt.Errorf("RHIZOME_BRIDGE_ADDR unset")
	}
	conn, err := net.DialTimeout("tcp", addr, bridgeDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("bridge dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	hello.Token = newTokenProvider("").token()
	line, err := json.Marshal(hello)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(bridgeHelloTimeout))
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return nil, fmt.Errorf("bridge hello write: %w", err)
	}
	rl, err := bufio.NewReaderSize(conn, bridgeHelloMaxBytes).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("bridge response read: %w", err)
	}
	var resp bridgeResponse
	if err := json.Unmarshal(rl, &resp); err != nil {
		return nil, fmt.Errorf("bridge response parse: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s refused: %s", hello.Action, resp.Error)
	}
	return &resp, nil
}

// dhtProvide advertises this host on a market rendezvous namespace — the
// sell side's reachability tier (rhizome-market-v1). Vars so tests can
// stub the bridge boundary without a daemon.
var dhtProvide = func(ns string) error {
	_, err := bridgeAction(bridgeHello{Action: "dht_provide", NS: ns})
	return err
}

// dhtFind returns the providers the daemon's DHT sees for a namespace —
// the buy side's unvetted discovery tier.
var dhtFind = func(ns string) ([]bridgePeer, error) {
	resp, err := bridgeAction(bridgeHello{Action: "dht_find", NS: ns})
	if err != nil {
		return nil, err
	}
	return resp.Peers, nil
}

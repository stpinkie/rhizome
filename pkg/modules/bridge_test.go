// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package modules

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	rnet "github.com/stpinkie/rhizome/pkg/rhizome/network"
	"github.com/stpinkie/rhizome/pkg/rhizome/testutil"
)

// --- validation ---

func TestValidateProtocolsRules(t *testing.T) {
	cases := []struct {
		name    string
		protos  []string
		wantErr string
	}{
		{"empty", nil, ""},
		{"valid", []string{"/acme/market/1.0.0"}, ""},
		{"malformed", []string{"acme/market"}, "malformed protocol id"},
		{"double slash", []string{"/acme//market"}, "malformed protocol id"},
		{"core collision", []string{"/rhizome/agent/1.0.0"}, "core-registered"},
		{"acp remote allowed", []string{"/rhizome/acp/1.0.0"}, ""},
		{"dupe in module", []string{"/a/b/1", "/a/b/1"}, "declared twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProtocols("m", tc.protos)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateConfigCrossModuleProtocol(t *testing.T) {
	a := testSpec("mod-a")
	a.Protocols = []string{"/acme/proto/1.0.0"}
	b := testSpec("mod-b")
	b.Protocols = []string{"/acme/proto/1.0.0"}
	lookup := func(id string) (ModuleSpec, bool) {
		switch id {
		case "mod-a":
			return a, true
		case "mod-b":
			return b, true
		}
		return ModuleSpec{}, false
	}
	cfg := &config.Config{Modules: map[string]config.ModuleConfig{
		"mod-a": {Enabled: true, Fields: map[string]string{"endpoint": "x"}},
		"mod-b": {Enabled: true, Fields: map[string]string{"endpoint": "x"}},
	}}
	err := ValidateConfig(cfg, lookup)
	if err == nil || !strings.Contains(err.Error(), "also claimed by") {
		t.Fatalf("expected cross-module collision, got: %v", err)
	}

	// Disabling one clears the collision.
	cfg.Modules["mod-b"] = config.ModuleConfig{Enabled: false}
	if err := ValidateConfig(cfg, lookup); err != nil {
		t.Fatalf("disabled module should not collide: %v", err)
	}
}

// --- tokens ---

func TestBridgeTokenLifecycle(t *testing.T) {
	mgr, _, _ := newTestManager(t, &config.Config{})

	tok, err := mgr.mintBridgeToken("mod-x")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(tok) != 64 {
		t.Fatalf("token len = %d, want 64 hex chars", len(tok))
	}
	info, err := os.Stat(mgr.bridgeTokenPath("mod-x"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// POSIX mode bits only; Windows stores ACLs and reports 0666 here.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("token file perm = %o, want 600", perm)
		}
	}

	// ensure is idempotent while a token exists.
	again, err := mgr.ensureBridgeToken("mod-x")
	if err != nil || again != tok {
		t.Fatalf("ensure = %q %v, want %q", again, err, tok)
	}

	// mint rotates.
	rotated, err := mgr.mintBridgeToken("mod-x")
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated == tok {
		t.Fatal("rotation produced the same token")
	}
}

func TestBridgeEnvInjection(t *testing.T) {
	mgr, _, _ := newTestManager(t, &config.Config{})
	mgr.SetBridgeAddr("127.0.0.1:9999")
	spec := testSpec("mod-env")
	spec.Protocols = []string{"/acme/proto/1.0.0"}
	if _, err := mgr.mintBridgeToken(spec.ID); err != nil {
		t.Fatalf("mint: %v", err)
	}

	cmd, err := mgr.commandBase(context.Background(), spec, "bin", nil, map[string]string{})
	if err != nil {
		t.Fatalf("commandBase: %v", err)
	}
	var addr, tok, dir string
	for _, e := range cmd.Env {
		if v, ok := strings.CutPrefix(e, "RHIZOME_BRIDGE_ADDR="); ok {
			addr = v
		}
		if v, ok := strings.CutPrefix(e, "RHIZOME_BRIDGE_TOKEN="); ok {
			tok = v
		}
		if v, ok := strings.CutPrefix(e, "RHIZOME_MODULE_DIR="); ok {
			dir = v
		}
	}
	if addr != "127.0.0.1:9999" {
		t.Fatalf("BRIDGE_ADDR = %q", addr)
	}
	if len(tok) != 64 {
		t.Fatalf("BRIDGE_TOKEN = %q", tok)
	}
	if dir != mgr.Dir(spec.ID) {
		t.Fatalf("MODULE_DIR = %q", dir)
	}

	// A spec without protocols gets none of it.
	plain := testSpec("mod-plain")
	cmd2, err := mgr.commandBase(context.Background(), plain, "bin", nil, map[string]string{})
	if err != nil {
		t.Fatalf("commandBase: %v", err)
	}
	for _, e := range cmd2.Env {
		if strings.HasPrefix(e, "RHIZOME_BRIDGE_") || strings.HasPrefix(e, "RHIZOME_MODULE_DIR=") {
			t.Fatalf("plain module env leaked bridge var: %s", e)
		}
	}
}

// --- networked bridge ---

func bridgeTestNode(t *testing.T, ctx context.Context, bootstrap []string) *rnet.Node {
	t.Helper()
	id := testutil.NewIdentity(t)
	node, err := rnet.NewNode(ctx, id.Libp2pPrivKey, rnet.Config{
		ListenAddrs:       []string{"/ip4/127.0.0.1/tcp/0"},
		BootstrapPeers:    bootstrap,
		DisableNATPortMap: true,
		DisableMDNS:       true,
	})
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	t.Cleanup(func() { node.Close() })
	return node
}

func waitBridgeConnected(t *testing.T, a, b *rnet.Node) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if rnet.IsConnectednessUp(a.Connectedness(b.ID())) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("nodes never connected")
}

const echoProto = "/acme/echo/1.0.0"

// bridgeFixture stands up a Bridge on nodeA whose module "mod-b" declares
// echoProto, plus a raw echo handler on nodeB.
type bridgeFixture struct {
	mgr    *Manager
	bridge *Bridge
	nodeA  *rnet.Node
	nodeB  *rnet.Node
	token  string
	trust  map[peer.ID]bool
}

func newBridgeFixture(t *testing.T, ctx context.Context) *bridgeFixture {
	t.Helper()
	f := &bridgeFixture{trust: map[peer.ID]bool{}}

	spec := testSpec("mod-b")
	spec.Protocols = []string{echoProto}
	registerTestSpec(t, spec)
	f.mgr, _, _ = newTestManager(t, &config.Config{Modules: map[string]config.ModuleConfig{
		"mod-b": {Enabled: true},
	}})

	f.nodeA = bridgeTestNode(t, ctx, nil)
	f.nodeB = bridgeTestNode(t, ctx, []string{f.nodeA.BootstrapAddrs()[0]})
	f.nodeB.Host().SetStreamHandler(protocol.ID(echoProto), func(s network.Stream) {
		_, _ = io.Copy(s, s)
		_ = s.Close()
	})
	waitBridgeConnected(t, f.nodeA, f.nodeB)

	var err error
	f.bridge, err = NewBridge(f.nodeA.Host(), f.mgr, func(pid peer.ID) bool {
		return f.trust[pid]
	})
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	t.Cleanup(func() { _ = f.bridge.Close() })
	f.bridge.Start()

	f.token, err = f.mgr.bridgeToken("mod-b")
	if err != nil || f.token == "" {
		t.Fatalf("token not minted: %q %v", f.token, err)
	}
	return f
}

// dialBridge connects to the loopback bridge and sends a hello.
func dialBridge(t *testing.T, addr string, hello bridgeHello) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	line, _ := json.Marshal(hello)
	if _, err := conn.Write(append(line, '\n')); err != nil {
		t.Fatalf("hello write: %v", err)
	}
	return conn
}

// expectClosed asserts the bridge hung up on the connection within a bound.
func expectClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection should be closed")
	}
}

func TestBridgeOutboundRefusals(t *testing.T) {
	ctx := context.Background()
	f := newBridgeFixture(t, ctx)
	addr := f.bridge.Addr()
	peerID := f.nodeB.ID().String()

	// Wrong token.
	c := dialBridge(t, addr, bridgeHello{
		Token: "bogus", Action: "dial", Peer: peerID, Protocol: echoProto,
	})
	expectClosed(t, c)

	// Right token, undeclared protocol.
	c = dialBridge(t, addr, bridgeHello{
		Token: f.token, Action: "dial", Peer: peerID, Protocol: "/acme/other/1.0.0",
	})
	expectClosed(t, c)

	// Right token, unparseable peer.
	c = dialBridge(t, addr, bridgeHello{
		Token: f.token, Action: "dial", Peer: "not-a-peer", Protocol: echoProto,
	})
	expectClosed(t, c)

	// Unsupported action.
	c = dialBridge(t, addr, bridgeHello{
		Token: f.token, Action: "accept", Peer: peerID, Protocol: echoProto,
	})
	expectClosed(t, c)
}

func TestBridgeOutboundSplice(t *testing.T) {
	ctx := context.Background()
	f := newBridgeFixture(t, ctx)

	// hello+payload in a single write — the hello reader may over-read
	// into its buffer; the splice must still forward every payload byte.
	conn, err := net.DialTimeout("tcp", f.bridge.Addr(), 3*time.Second)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer conn.Close()
	line, _ := json.Marshal(bridgeHello{
		Token:    f.token,
		Action:   "dial",
		Peer:     f.nodeB.ID().String(),
		Protocol: echoProto,
	})
	if _, err := conn.Write(append(append(line, '\n'), []byte("ping-payload")...)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, len("ping-payload"))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("echo read: %v", err)
	}
	if string(buf) != "ping-payload" {
		t.Fatalf("echo = %q", buf)
	}
}

func TestBridgeInboundTrustGate(t *testing.T) {
	prev := logger.GetLevel()
	logger.SetLevel(logger.DEBUG)
	t.Cleanup(func() { logger.SetLevel(prev) })
	ctx := context.Background()
	f := newBridgeFixture(t, ctx)

	// Module "mod-b" runs its own loopback listener and publishes its addr.
	modLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("module listen: %v", err)
	}
	defer modLn.Close()
	if err := os.MkdirAll(f.mgr.Dir("mod-b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(f.mgr.Dir("mod-b"), bridgeAddrFile),
		[]byte(modLn.Addr().String()), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	helloCh := make(chan bridgeHello, 4)
	go func() {
		for {
			conn, err := modLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hello, err := readBridgeHello(bufio.NewReader(c))
				if err != nil {
					return
				}
				helloCh <- hello
				// Echo whatever the peer sends back over the splice.
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	// The module listener must be dialable before the trusted phase runs.
	probe, err := net.DialTimeout("tcp", modLn.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("module listener not accepting: %v", err)
	}
	_ = probe.Close()

	// Untrusted: nodeC is not in the trust set → stream reset, no module
	// dial. A separate node keeps this on its own connection — a stream
	// reset on an established libp2p connection delays subsequent stream
	// dispatch on that conn by seconds in some transports.
	nodeC := bridgeTestNode(t, ctx, []string{f.nodeA.BootstrapAddrs()[0]})
	waitBridgeConnected(t, nodeC, f.nodeA)
	s, err := nodeC.Host().NewStream(ctx, f.nodeA.ID(), protocol.ID(echoProto))
	if err == nil {
		// A write flushes lazy negotiation so the trust gate runs.
		_ = s.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = s.Write([]byte("x"))
		if _, err := s.Read(make([]byte, 1)); err == nil {
			t.Fatal("untrusted stream should be reset")
		}
		_ = s.Close()
	}
	select {
	case h := <-helloCh:
		t.Fatalf("module received hello for untrusted peer: %+v", h)
	case <-time.After(500 * time.Millisecond):
	}

	// The module listener must still be dialable before the trusted phase.
	probe2, err := net.DialTimeout("tcp", modLn.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("module listener died before trusted phase: %v", err)
	}
	_ = probe2.Close()

	// Trusted: splice runs end to end. The first client write flushes the
	// lazy multistream negotiation — until then nodeA's handler cannot
	// run, so write before waiting on the module hello.
	f.trust[f.nodeB.ID()] = true
	s, err = f.nodeB.Host().NewStream(ctx, f.nodeA.ID(), protocol.ID(echoProto))
	if err != nil {
		t.Fatalf("trusted stream: %v", err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := s.Write([]byte("from-peer")); err != nil {
		t.Fatalf("peer write: %v", err)
	}
	select {
	case h := <-helloCh:
		if h.Action != "accept" || h.Token != f.token ||
			h.Protocol != echoProto || h.Peer != f.nodeB.ID().String() {
			t.Fatalf("hello = %+v", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("module never received accept hello")
	}
	buf := make([]byte, len("from-peer"))
	if _, err := io.ReadFull(s, buf); err != nil {
		t.Fatalf("peer echo read: %v", err)
	}
	if string(buf) != "from-peer" {
		t.Fatalf("echo = %q", buf)
	}
}

func TestBridgeSkipsMuxOwnedProtocols(t *testing.T) {
	ctx := context.Background()
	f := &bridgeFixture{trust: map[peer.ID]bool{}}

	spec := testSpec("mod-c")
	spec.Protocols = []string{echoProto, "/claimed/1.0.0"}
	registerTestSpec(t, spec)
	f.mgr, _, _ = newTestManager(t, &config.Config{Modules: map[string]config.ModuleConfig{
		"mod-c": {Enabled: true},
	}})

	f.nodeA = bridgeTestNode(t, ctx, nil)
	// Pre-claim /claimed/1.0.0 as if it were a core/other registrant.
	f.nodeA.Host().SetStreamHandler(protocol.ID("/claimed/1.0.0"), func(s network.Stream) {
		_ = s.Reset()
	})

	var err error
	f.bridge, err = NewBridge(f.nodeA.Host(), f.mgr, func(peer.ID) bool { return true })
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	t.Cleanup(func() { _ = f.bridge.Close() })
	f.bridge.Start()

	f.bridge.mu.Lock()
	claimed := map[protocol.ID]string{}
	for p, id := range f.bridge.claims {
		claimed[p] = id
	}
	f.bridge.mu.Unlock()
	if _, ok := claimed[protocol.ID("/claimed/1.0.0")]; ok {
		t.Fatal("bridge claimed a protocol already owned by the host mux")
	}
	if claimed[protocol.ID(echoProto)] != "mod-c" {
		t.Fatalf("echo protocol claim = %v", claimed)
	}
}

// --- catalog ---

func TestCatalogV4EmittedOnlyWithProtocols(t *testing.T) {
	var env struct {
		CatalogVersion int `json:"catalog_version"`
	}

	// No protocols → schema stays below v4.
	registerTestSpec(t, testSpec("plain"))
	out, err := MarshalCatalog()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.CatalogVersion >= 4 {
		t.Fatalf("v%d emitted without protocol declarations", env.CatalogVersion)
	}

	proto := testSpec("proto-mod")
	proto.Protocols = []string{"/acme/x/1.0.0"}
	registerTestSpec(t, proto)
	out, err = MarshalCatalog()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.CatalogVersion != 4 {
		t.Fatalf("catalog_version = %d, want 4", env.CatalogVersion)
	}
}

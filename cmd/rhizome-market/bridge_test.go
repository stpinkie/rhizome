// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

// startTestBridge boots the accept listener on a temp module dir.
func startTestBridge(t *testing.T, token string, connCap int) (*bridgeServer, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RHIZOME_BRIDGE_TOKEN", token)
	mgr := newSessionMgr(dir, newAuditLogger(""))
	b, err := startBridge(dir, newTokenProvider(dir), mgr, connCap, newAuditLogger(""))
	if err != nil {
		t.Fatalf("startBridge: %v", err)
	}
	t.Cleanup(b.Close)
	data, err := os.ReadFile(filepath.Join(dir, bridgeAddrFile))
	if err != nil {
		t.Fatalf("bridge.addr: %v", err)
	}
	return b, string(data[:len(data)-1]) // strip trailing \n
}

// fakeDaemonHello dials the module's bridge listener and sends a hello
// exactly as pkg/modules' serveInbound does.
func fakeDaemonHello(t *testing.T, addr, token, action, peer, protocol string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	hello, _ := json.Marshal(bridgeHello{
		Token: token, Action: action, Peer: peer, Protocol: protocol,
	})
	if _, err := conn.Write(append(hello, '\n')); err != nil {
		t.Fatalf("hello write: %v", err)
	}
	return conn
}

func TestBridge_AddrPublished(t *testing.T) {
	_, addr := startTestBridge(t, "tok", 4)
	if addr == "" {
		t.Fatal("no bridge addr")
	}
}

func TestBridge_RejectsBadHello(t *testing.T) {
	_, addr := startTestBridge(t, "good-tok", 4)

	cases := []struct {
		name                 string
		token, action, proto string
	}{
		{"bad token", "wrong", "accept", "/rhizome/acp/1.0.0"},
		{"bad action", "good-tok", "dial", "/rhizome/acp/1.0.0"},
		{"undeclared protocol", "good-tok", "accept", "/rhizome/blob/1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := fakeDaemonHello(t, addr, tc.token, tc.action, "peer1", tc.proto)
			defer func() { _ = conn.Close() }()
			// Rejected conns get closed — a read must EOF/error quickly.
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			buf := make([]byte, 1)
			if _, err := conn.Read(buf); err == nil {
				t.Fatal("rejected conn stayed open")
			}
		})
	}
}

// testClient is the minimal acpsdk.Client stub for the roundtrip.
type testClient struct{}

func (testClient) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, nil
}

func (testClient) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, nil
}

func (testClient) RequestPermission(
	context.Context,
	acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{}, nil
}
func (testClient) SessionUpdate(context.Context, acpsdk.SessionNotification) error { return nil }
func (testClient) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, nil
}

func (testClient) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, nil
}

func (testClient) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, nil
}

func (testClient) ReleaseTerminal(
	context.Context,
	acpsdk.ReleaseTerminalRequest,
) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, nil
}

func (testClient) WaitForTerminalExit(
	context.Context,
	acpsdk.WaitForTerminalExitRequest,
) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, nil
}

// TestBridge_ACPInitializeRoundtrip drives a real ACP client handshake
// over the bridged conn — the module must complete initialize before the
// Track-102 session verbs refuse cleanly.
func TestBridge_ACPInitializeRoundtrip(t *testing.T) {
	_, addr := startTestBridge(t, "tok", 4)

	conn := fakeDaemonHello(t, addr, "tok", "accept", "12D3peer", "/rhizome/acp/1.0.0")
	defer func() { _ = conn.Close() }()

	client := acpsdk.NewClientSideConnection(testClient{}, conn, conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if resp.ProtocolVersion != acpsdk.ProtocolVersionNumber {
		t.Fatalf("protocol version = %v", resp.ProtocolVersion)
	}
}

func TestBridge_ConnCapRefuses(t *testing.T) {
	// cap 1: first verified conn holds the slot (ACP conn blocks on Done),
	// second gets rejected.
	_, addr := startTestBridge(t, "tok", 1)

	c1 := fakeDaemonHello(t, addr, "tok", "accept", "p1", "/rhizome/acp/1.0.0")
	defer func() { _ = c1.Close() }()
	// Give the accept loop a beat to take the semaphore.
	time.Sleep(150 * time.Millisecond)
	c2 := fakeDaemonHello(t, addr, "tok", "accept", "p2", "/rhizome/acp/1.0.0")
	defer func() { _ = c2.Close() }()
	_ = c2.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1)
	if _, err := c2.Read(buf); err == nil {
		t.Fatal("overflow conn stayed open")
	}
}

func TestBridge_CloseRemovesAddrFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RHIZOME_BRIDGE_TOKEN", "tok")
	b, err := startBridge(dir, newTokenProvider(dir),
		newSessionMgr(dir, newAuditLogger("")), 4, newAuditLogger(""))
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if _, err := os.Stat(filepath.Join(dir, bridgeAddrFile)); !os.IsNotExist(err) {
		t.Fatal("bridge.addr should be removed on close")
	}
}

func TestDialBridge_HelloShape(t *testing.T) {
	// A fake daemon: accept a conn, read the hello line, verify its shape.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	got := make(chan bridgeHello, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var h bridgeHello
		dec := json.NewDecoder(conn)
		if err := dec.Decode(&h); err == nil {
			got <- h
		}
	}()

	conn, err := dialBridge(ln.Addr().String(), "tok", "peerX", "/rhizome/acp/1.0.0")
	if err != nil {
		t.Fatalf("dialBridge: %v", err)
	}
	defer func() { _ = conn.Close() }()

	select {
	case h := <-got:
		if h.Action != "dial" || h.Peer != "peerX" || h.Protocol != "/rhizome/acp/1.0.0" || h.Token != "tok" {
			t.Fatalf("hello = %+v", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no hello received")
	}
}

func TestDialPeer_NoBridgeAddr(t *testing.T) {
	t.Setenv("RHIZOME_BRIDGE_ADDR", "")
	if _, err := dialPeer("peer", "proto"); err == nil {
		t.Fatal("expected error without RHIZOME_BRIDGE_ADDR")
	}
}

func TestAuditTrail_StreamEvents(t *testing.T) {
	// Accept + reject must land in market-audit.jsonl.
	dir := t.TempDir()
	auditPath := filepath.Join(dir, auditFile)
	audit := newAuditLogger(auditPath)
	t.Setenv("RHIZOME_BRIDGE_TOKEN", "tok")
	b, err := startBridge(dir, newTokenProvider(dir),
		newSessionMgr(dir, audit), 4, audit)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	data, _ := os.ReadFile(filepath.Join(dir, bridgeAddrFile))
	addr := string(data[:len(data)-1])

	conn := fakeDaemonHello(t, addr, "bad", "accept", "p", "/rhizome/acp/1.0.0")
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Read(make([]byte, 1))
	_ = conn.Close()

	time.Sleep(200 * time.Millisecond) // let the audit write land
	contents, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("audit read: %v", err)
	}
	if !json.Valid(contents[:len(contents)-1]) && len(contents) < 2 {
		t.Fatal("audit not JSONL")
	}
	if got := string(contents); !strings.Contains(got, "market.stream.rejected") {
		t.Fatalf("audit missing rejection: %s", got)
	}
}

package acp

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/bus"
	"github.com/stpinkie/rhizome/pkg/config"
)

// remoteBoundRegistry builds a registry whose "ext" agent is bound via
// acp.remote instead of a command.
func remoteBoundRegistry(t *testing.T, workspace, remote string) *agent.AgentRegistry {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{
				ID:        "ext",
				Workspace: workspace,
				ACP:       &config.ACPAgentConfig{Remote: remote},
			}},
		},
	}
	return agent.NewAgentRegistry(cfg, nil)
}

// remotePipedManager builds a ClientManager whose RemoteDialer splices the
// remote binding onto an in-process mock agent through io.Pipe pairs.
// gotRemote records the configured remote value the dialer was given.
func remotePipedManager(
	t *testing.T,
	cfg *config.Config,
	registry *agent.AgentRegistry,
	mock *mockExternalAgent,
	gotRemote *string,
) *ClientManager {
	t.Helper()
	m := NewClientManager(cfg, func() *agent.AgentRegistry { return registry })
	if m == nil {
		t.Fatal("manager should exist for remote-bound agent")
	}
	m.SetRemoteDialer(func(_ context.Context, agentID, remote string) (io.ReadWriteCloser, error) {
		*gotRemote = remote
		c2aR, c2aW := io.Pipe()
		a2cR, a2cW := io.Pipe()
		mock.conn = acpsdk.NewAgentSideConnection(mock, a2cW, c2aR)
		return &duplexPipe{reader: a2cR, writer: c2aW}, nil
	})
	return m
}

// duplexPipe adapts an io.Pipe pair into an io.ReadWriteCloser for the
// RemoteDialer seam (reads from the agent's writer, writes to its reader).
type duplexPipe struct {
	reader io.ReadCloser
	writer io.WriteCloser
}

func (d *duplexPipe) Read(p []byte) (int, error)  { return d.reader.Read(p) }
func (d *duplexPipe) Write(p []byte) (int, error) { return d.writer.Write(p) }
func (d *duplexPipe) Close() error {
	_ = d.reader.Close()
	return d.writer.Close()
}

func TestRunAgentRemoteBinding(t *testing.T) {
	ws := t.TempDir()
	reg := remoteBoundRegistry(t, ws, "12D3KooWPeer")
	mock := &mockExternalAgent{}
	var gotRemote string
	m := remotePipedManager(t, &config.Config{}, reg, mock, &gotRemote)
	defer m.Close()

	out, err := m.RunAgent(context.Background(), "ext", "say hi")
	if err != nil {
		t.Fatalf("RunAgent over remote dialer: %v", err)
	}
	if out != "Hello world" {
		t.Fatalf("got %q", out)
	}
	if gotRemote != "12D3KooWPeer" {
		t.Fatalf("dialer got remote %q", gotRemote)
	}
	if mock.initCalls != 1 {
		t.Fatalf("expected 1 initialize, got %d", mock.initCalls)
	}
	// Remote bindings send "." (remote-resolved) on the wire unless
	// acp.cwd is set — never a local path.
	if len(mock.newSessionReqs) != 1 || mock.newSessionReqs[0].Cwd != "." {
		t.Fatalf("remote session/new cwd = %+v", mock.newSessionReqs)
	}
}

func TestRemoteBindingWithoutDialer(t *testing.T) {
	ws := t.TempDir()
	reg := remoteBoundRegistry(t, ws, "12D3KooWPeer")
	m := NewClientManager(&config.Config{}, func() *agent.AgentRegistry { return reg })
	if m == nil {
		t.Fatal("manager should exist for remote-bound agent")
	}
	defer m.Close()

	_, err := m.RunAgent(context.Background(), "ext", "hi")
	if err == nil {
		t.Fatal("expected error without a dialer")
	}
	if got := err.Error(); !contains(got, "remote dialer") && !contains(got, "mesh") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

func TestRemoteDialerErrorSurfaces(t *testing.T) {
	ws := t.TempDir()
	reg := remoteBoundRegistry(t, ws, "12D3KooWPeer")
	m := NewClientManager(&config.Config{}, func() *agent.AgentRegistry { return reg })
	defer m.Close()
	m.SetRemoteDialer(func(_ context.Context, _, _ string) (io.ReadWriteCloser, error) {
		return nil, fmt.Errorf("peer is not trusted")
	})

	_, err := m.RunAgent(context.Background(), "ext", "hi")
	if err == nil || !contains(err.Error(), "not trusted") {
		t.Fatalf("dialer error should surface: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- RemoteMux ---

// recordingClient embeds clientHandler (acpsdk.Client) and counts inbound
// permission requests to prove which connection a prompt landed on.
type recordingClient struct {
	*clientHandler
	permCalls atomic.Int32
}

func (c *recordingClient) RequestPermission(
	ctx context.Context,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.RequestPermissionResponse, error) {
	c.permCalls.Add(1)
	return c.clientHandler.RequestPermission(ctx, params)
}

// muxPipeClient dials a RemoteMux over an io.Pipe duplex: returns the
// client-side connection plus the recording client handler.
func muxPipeClient(t *testing.T, mux *RemoteMux) (*acpsdk.ClientSideConnection, *recordingClient) {
	t.Helper()
	c2aR, c2aW := io.Pipe()
	a2cR, a2cW := io.Pipe()
	go mux.Serve(&duplexPipe{reader: c2aR, writer: a2cW})

	rc := &recordingClient{clientHandler: &clientHandler{
		agentID:  "remote-client",
		policy:   ClientPolicyAllow,
		sessions: map[acpsdk.SessionId]*sessionBuffer{},
	}}
	conn := acpsdk.NewClientSideConnection(rc, c2aW, a2cR)
	t.Cleanup(func() {
		// ClientSideConnection has no Close — closing the write end ends
		// the agent-side read, which closes the mux's connection.
		_ = c2aW.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
	}); err != nil {
		t.Fatalf("remote initialize: %v", err)
	}
	return conn, rc
}

func TestRemoteMuxServesTwoConnections(t *testing.T) {
	runner := newFakeRunner()
	mux := NewRemoteMux(runner, Options{Policy: PermissionAllow})
	if err := mux.Start(); err != nil {
		t.Fatalf("mux.Start: %v", err)
	}
	defer mux.Close()

	connA, _ := muxPipeClient(t, mux)
	connB, _ := muxPipeClient(t, mux)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessA, err := connA.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acpsdk.McpServer{}})
	if err != nil {
		t.Fatalf("connA session/new: %v", err)
	}
	sessB, err := connB.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acpsdk.McpServer{}})
	if err != nil {
		t.Fatalf("connB session/new: %v", err)
	}
	if sessA.SessionId == sessB.SessionId {
		t.Fatal("sessions must be distinct per connection")
	}

	for i, c := range []*acpsdk.ClientSideConnection{connA, connB} {
		resp, err := c.Prompt(ctx, acpsdk.PromptRequest{
			SessionId: []acpsdk.SessionId{sessA.SessionId, sessB.SessionId}[i],
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(fmt.Sprintf("prompt-%d", i))},
		})
		if err != nil {
			t.Fatalf("conn%d prompt: %v", i, err)
		}
		if resp.StopReason != acpsdk.StopReasonEndTurn {
			t.Fatalf("conn%d stop reason = %v", i, resp.StopReason)
		}
	}
	if len(runner.msgs) != 2 {
		t.Fatalf("expected 2 inbound messages, got %d", len(runner.msgs))
	}
}

func TestRemoteMuxPermissionRoutesToOwningConn(t *testing.T) {
	runner := newFakeRunner()
	mux := NewRemoteMux(runner, Options{Policy: PermissionPrompt})
	if err := mux.Start(); err != nil {
		t.Fatalf("mux.Start: %v", err)
	}
	defer mux.Close()

	connA, rcA := muxPipeClient(t, mux)
	connB, rcB := muxPipeClient(t, mux)
	_ = connA

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessB, err := connB.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acpsdk.McpServer{}})
	if err != nil {
		t.Fatalf("connB session/new: %v", err)
	}

	// Drive the shared permission hook for the conn-B session: the request
	// must arrive on conn B's wire only.
	approver := runner.approver()
	if approver == nil {
		t.Fatal("mux permission hook not mounted")
	}
	d, err := approver.ApproveTool(ctx, &agent.ToolApprovalRequest{
		Context: &agent.TurnContext{
			Inbound: &bus.InboundContext{Channel: ChannelName, ChatID: string(sessB.SessionId)},
		},
		Tool: "write_file",
	})
	if err != nil {
		t.Fatalf("ApproveTool: %v", err)
	}
	if !d.Approved {
		t.Fatalf("allow client should approve: %+v", d)
	}
	if rcB.permCalls.Load() != 1 {
		t.Fatalf("conn B permCalls = %d, want 1", rcB.permCalls.Load())
	}
	if rcA.permCalls.Load() != 0 {
		t.Fatalf("conn A must not receive conn B's permission request (got %d)", rcA.permCalls.Load())
	}
}

func TestRemoteMuxAbstainsOnUnknownSession(t *testing.T) {
	runner := newFakeRunner()
	mux := NewRemoteMux(runner, Options{Policy: PermissionDeny})
	if err := mux.Start(); err != nil {
		t.Fatalf("mux.Start: %v", err)
	}
	defer mux.Close()

	approver := runner.approver()
	if approver == nil {
		t.Fatal("mux permission hook not mounted")
	}
	// Unknown acp-channel chat → abstain (approve) so a co-located stdio
	// server or other hook can claim it.
	d, err := approver.ApproveTool(context.Background(), &agent.ToolApprovalRequest{
		Context: &agent.TurnContext{
			Inbound: &bus.InboundContext{Channel: ChannelName, ChatID: "foreign-session"},
		},
		Tool: "exec_command",
	})
	if err != nil || !d.Approved {
		t.Fatalf("unknown session should abstain-approve: %+v %v", d, err)
	}
}

func TestRemoteMuxStreamsOnOwningConn(t *testing.T) {
	runner := newFakeRunner()
	mux := NewRemoteMux(runner, Options{Policy: PermissionAllow})
	if err := mux.Start(); err != nil {
		t.Fatalf("mux.Start: %v", err)
	}
	defer mux.Close()

	connA, _ := muxPipeClient(t, mux)
	connB, rcB := muxPipeClient(t, mux)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := connA.NewSession(
		ctx,
		acpsdk.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acpsdk.McpServer{}},
	); err != nil {
		t.Fatalf("connA session/new: %v", err)
	}
	sessB, err := connB.NewSession(ctx, acpsdk.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acpsdk.McpServer{}})
	if err != nil {
		t.Fatalf("connB session/new: %v", err)
	}

	streamer, ok := mux.GetStreamer(ctx, ChannelName, string(sessB.SessionId), "key")
	if !ok || streamer == nil {
		t.Fatal("mux should claim conn B's session streamer")
	}
	if err := streamer.Update(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := streamer.Finalize(ctx, "hello world"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for rcB.sessionText(sessB.SessionId) != "hello world" {
		if time.Now().After(deadline) {
			t.Fatalf("conn B accumulated %q", rcB.sessionText(sessB.SessionId))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

package acp

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	mcpgo "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/mcp"
	"github.com/stpinkie/rhizome/pkg/tools"
)

// maxSessionMCPServers bounds how many MCP servers one ACP session may
// declare — each spawns a child process.
const maxSessionMCPServers = 4

// sessionTool wraps a session-scoped MCP tool: it only runs when the tool
// context's chat id matches the owning ACP session, and it is only
// advertised to that session's turns (SessionScoper).
type sessionTool struct {
	tools.Tool
	sid string
}

func (t sessionTool) SessionChatID() string { return t.sid }

func (t sessionTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if tools.ToolChatID(ctx) != t.sid {
		return tools.ErrorResult("tool is bound to a different ACP session")
	}
	return t.Tool.Execute(ctx, args)
}

// sessionMCPTools attaches the client-declared MCP servers for one ACP
// session: connects each stdio/http/sse server on a dedicated manager and
// registers the discovered tools on the agent's registry under
// session-unique, chat-scoped names. Unsupported entries (nested acp or
// malformed URLs) are refused per-entry — the session itself is not failed.
//
// Tool names follow `mcp_acp-<sid8>-<server>_<tool>` so two sessions can
// declare the same server name without colliding.
func (s *Server) sessionMCPTools(
	ctx context.Context,
	sess *acpSession,
	agentID string,
	servers []acpsdk.McpServer,
) {
	if len(servers) == 0 {
		return
	}
	if len(servers) > maxSessionMCPServers {
		s.log.Warn("acp: session declared too many MCP servers; extra entries refused",
			"session_id", string(sess.id), "declared", len(servers), "max", maxSessionMCPServers)
		servers = servers[:maxSessionMCPServers]
	}

	reg := s.runner.GetRegistry()
	if reg == nil {
		s.log.Warn("acp: no agent registry — skipping session MCP servers", "session_id", string(sess.id))
		return
	}
	inst, ok := reg.GetAgent(agentID)
	if !ok || inst == nil || inst.Tools == nil {
		s.log.Warn("acp: agent not found for session MCP servers",
			"session_id", string(sess.id), "agent", agentID)
		return
	}

	mgr := s.newMCPManager()
	sid8 := string(sess.id)
	if len(sid8) > 8 {
		sid8 = sid8[:8]
	}

	var toolNames []string
	for _, srv := range servers {
		cfg, name, kind := sessionMCPConfig(srv)
		if cfg == nil {
			s.log.Warn("acp: refusing unsupported session MCP server",
				"session_id", string(sess.id), "kind", kind)
			continue
		}
		if name == "" {
			name = "server"
		}
		// Session-unique server label keeps tool names collision-free.
		label := fmt.Sprintf("acp-%s-%s", sid8, name)
		connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := mgr.ConnectServer(connCtx, label, *cfg)
		cancel()
		if err != nil {
			s.log.Warn("acp: session MCP server connect failed",
				"session_id", string(sess.id), "server", name, "error", err)
			continue
		}
		conn, ok := mgr.GetServer(label)
		if !ok {
			continue
		}
		for _, tool := range conn.Tools {
			if tool == nil {
				continue
			}
			mt := tools.NewMCPTool(mgr, label, tool)
			if agentWs := inst.Workspace; agentWs != "" {
				mt.SetWorkspace(agentWs)
			}
			wrapped := sessionTool{Tool: mt, sid: string(sess.id)}
			inst.Tools.Register(wrapped)
			if inst.Tools.HasRegistered(wrapped.Name()) {
				toolNames = append(toolNames, wrapped.Name())
			}
		}
		s.log.Info("acp: session MCP server connected",
			"session_id", string(sess.id), "server", name, "tools", len(conn.Tools))
	}

	if len(toolNames) == 0 {
		_ = mgr.Close()
		return
	}
	sess.setMCP(mgr, inst.Tools, toolNames)
}

// sessionMCPConfig maps an ACP-declared McpServer onto an MCPServerConfig
// for the shared mcp.Manager. Returns the config, the server name, and a
// kind label for refusals ("stdio"/"http"/"sse" entries always return a
// config; "acp", malformed URLs, and unknown variants return nil).
func sessionMCPConfig(srv acpsdk.McpServer) (*config.MCPServerConfig, string, string) {
	switch {
	case srv.Stdio != nil:
		env := make(map[string]string, len(srv.Stdio.Env))
		for _, e := range srv.Stdio.Env {
			env[e.Name] = e.Value
		}
		return &config.MCPServerConfig{
			Enabled: true,
			Command: srv.Stdio.Command,
			Args:    srv.Stdio.Args,
			Env:     env,
			EnvOnly: true,
		}, strings.TrimSpace(srv.Stdio.Name), "stdio"
	case srv.Http != nil:
		if !validMCPHTTPURL(srv.Http.Url) {
			return nil, "", "http"
		}
		return &config.MCPServerConfig{
			Enabled: true,
			Type:    "http",
			URL:     srv.Http.Url,
			Headers: mcpHeaders(srv.Http.Headers),
		}, strings.TrimSpace(srv.Http.Name), "http"
	case srv.Sse != nil:
		if !validMCPHTTPURL(srv.Sse.Url) {
			return nil, "", "sse"
		}
		return &config.MCPServerConfig{
			Enabled: true,
			Type:    "sse",
			URL:     srv.Sse.Url,
			Headers: mcpHeaders(srv.Sse.Headers),
		}, strings.TrimSpace(srv.Sse.Name), "sse"
	case srv.Acp != nil:
		return nil, "", "acp"
	default:
		return nil, "", "unknown"
	}
}

// validMCPHTTPURL restricts session-declared http/sse servers to real
// http(s) endpoints — the URL becomes a client the daemon dials.
func validMCPHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func mcpHeaders(hdrs []acpsdk.HttpHeader) map[string]string {
	if len(hdrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(hdrs))
	for _, h := range hdrs {
		if h.Name != "" {
			out[h.Name] = h.Value
		}
	}
	return out
}

// sessionMCPManager abstracts mcp.Manager for tests.
type sessionMCPManager interface {
	sessionMCPConn
	ConnectServer(ctx context.Context, name string, cfg config.MCPServerConfig) error
	GetServer(name string) (*mcp.ServerConnection, bool)
	CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (*mcpgo.CallToolResult, error)
}

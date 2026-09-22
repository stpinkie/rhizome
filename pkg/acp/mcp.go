package acp

import (
	"context"
	"fmt"
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
// session: connects each stdio server on a dedicated manager and registers
// the discovered tools on the agent's registry under session-unique,
// chat-scoped names. Non-stdio entries (http/sse/acp) are refused per-entry —
// the session itself is not failed.
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
		stdio := srv.Stdio
		if stdio == nil {
			kind := "unknown"
			switch {
			case srv.Http != nil:
				kind = "http"
			case srv.Sse != nil:
				kind = "sse"
			case srv.Acp != nil:
				kind = "acp"
			}
			s.log.Warn("acp: refusing non-stdio MCP server",
				"session_id", string(sess.id), "kind", kind)
			continue
		}

		env := make(map[string]string, len(stdio.Env))
		for _, e := range stdio.Env {
			env[e.Name] = e.Value
		}
		cfg := config.MCPServerConfig{
			Enabled: true,
			Command: stdio.Command,
			Args:    stdio.Args,
			Env:     env,
			EnvOnly: true,
		}

		name := strings.TrimSpace(stdio.Name)
		if name == "" {
			name = "server"
		}
		// Session-unique server label keeps tool names collision-free.
		label := fmt.Sprintf("acp-%s-%s", sid8, name)
		connCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := mgr.ConnectServer(connCtx, label, cfg)
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

// sessionMCPManager abstracts mcp.Manager for tests.
type sessionMCPManager interface {
	sessionMCPConn
	ConnectServer(ctx context.Context, name string, cfg config.MCPServerConfig) error
	GetServer(name string) (*mcp.ServerConnection, bool)
	CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (*mcpgo.CallToolResult, error)
}

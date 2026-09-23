// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/tools"
)

// ServeTools exposes allowlisted registry tools as an MCP server over the
// stdio transport until ctx is cancelled or the client disconnects.
//
// stdout is the protocol channel: any logging must stay on stderr — the
// same rule the ACP stdio binding follows. allow names registry tools; a
// name absent from the registry is skipped with a warning (it may be an
// agent-loop tool, which is never registered for serve). An empty allow
// list serves zero tools — deny-all by construction.
func ServeTools(ctx context.Context, reg *tools.ToolRegistry, allow []string, version string) error {
	err := newServeServer(reg, allow, version).Run(ctx, &sdkmcp.StdioTransport{})
	if errors.Is(err, io.EOF) {
		return nil // client closed stdin — normal shutdown, not a failure
	}
	return err
}

// newServeServer builds the MCP server with allowlisted tools registered.
// Split from ServeTools so tests can connect in-memory transports.
func newServeServer(reg *tools.ToolRegistry, allow []string, version string) *sdkmcp.Server {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "rhizome",
		Version: version,
	}, nil)

	registered := 0
	for _, name := range allow {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		tool, ok := reg.Get(name)
		if !ok {
			logger.WarnCF("mcp-serve", "mcp_server.allow names an unregistered tool; skipping",
				map[string]any{"tool": name})
			continue
		}
		server.AddTool(&sdkmcp.Tool{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.Parameters(),
		}, serveHandler(reg, tool.Name()))
		registered++
	}
	logger.InfoCF("mcp-serve", "MCP server listening on stdio",
		map[string]any{"tools": registered, "allowed": sortedAllow(allow)})

	return server
}

// serveHandler bridges one registry tool into an MCP tool handler. Tool
// errors report in-band (IsError + text) so the client's model can
// self-correct; protocol-level errors are reserved for malformed requests.
func serveHandler(reg *tools.ToolRegistry, name string) sdkmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		var args map[string]any
		if raw := req.Params.Arguments; len(raw) > 0 {
			if err := json.Unmarshal(raw, &args); err != nil {
				return &sdkmcp.CallToolResult{
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: fmt.Sprintf("invalid arguments: %v", err)},
					},
					IsError: true,
				}, nil
			}
		}

		res := reg.Execute(ctx, name, args)
		text := ""
		if res != nil {
			text = res.ContentForLLM()
			if text == "" && res.Err != nil {
				text = res.Err.Error()
			}
		}
		result := &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}},
		}
		if res != nil && res.IsError {
			result.IsError = true
		}
		return result, nil
	}
}

func sortedAllow(allow []string) []string {
	out := make([]string, 0, len(allow))
	for _, name := range allow {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	sort.Strings(out)
	return out
}

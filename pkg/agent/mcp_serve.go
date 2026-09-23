// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package agent

import (
	"fmt"
	"os"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/isolation"
	"github.com/stpinkie/rhizome/pkg/logger"
	"github.com/stpinkie/rhizome/pkg/tools"
)

// BuildMCPServeToolRegistry constructs the standalone tool registry used
// by `rhizome mcp serve`. It mirrors the agent-instance registrations for
// tools that need no agent loop (files, exec, web, hardware) against the
// default agent workspace; agent-loop-dependent tools (delegate, acp_run,
// swarm verbs, message/send_*, spawn/subagent, skills, media loaders) are
// simply never registered here — the operator's tools.mcp_server.allow
// list then cannot name them.
func BuildMCPServeToolRegistry(cfg *config.Config) (*tools.ToolRegistry, error) {
	isolation.Configure(cfg)

	workspace := cfg.WorkspacePath()
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, fmt.Errorf("create agent workspace: %w", err)
	}

	restrict := cfg.Agents.Defaults.RestrictToWorkspace
	readRestrict := restrict && !cfg.Agents.Defaults.AllowReadOutsideWorkspace
	allowReadPaths := buildAllowReadPatterns(cfg)
	allowWritePaths := compilePatterns(cfg.Tools.AllowWritePaths)

	registry := tools.NewToolRegistry()

	if cfg.Tools.IsToolEnabled("read_file") {
		maxReadFileSize := cfg.Tools.ReadFile.MaxReadFileSize
		switch cfg.Tools.ReadFile.EffectiveMode() {
		case config.ReadFileModeLines:
			registry.Register(tools.NewReadFileLinesTool(workspace, readRestrict, maxReadFileSize, allowReadPaths))
		default:
			registry.Register(tools.NewReadFileBytesTool(workspace, readRestrict, maxReadFileSize, allowReadPaths))
		}
	}
	if cfg.Tools.IsToolEnabled("edit_file") {
		registry.Register(tools.NewEditFileTool(workspace, restrict, allowWritePaths))
	}
	if cfg.Tools.IsToolEnabled("append_file") {
		registry.Register(tools.NewAppendFileTool(workspace, restrict, allowWritePaths))
	}
	if cfg.Tools.IsToolEnabled("write_file") {
		writeTool := tools.NewWriteFileTool(workspace, restrict, allowWritePaths)
		var altTools []string
		if registry.HasRegistered("append_file") {
			altTools = append(altTools, "append_file")
		}
		if registry.HasRegistered("edit_file") {
			altTools = append(altTools, "edit_file")
		}
		writeTool.SetAlternativeTools(altTools)
		registry.Register(writeTool)
	}
	if cfg.Tools.IsToolEnabled("list_dir") {
		registry.Register(tools.NewListDirTool(workspace, readRestrict, allowReadPaths))
	}
	if cfg.Tools.IsToolEnabled("exec") {
		execTool, err := tools.NewExecToolWithConfig(workspace, restrict, cfg, allowReadPaths)
		if err != nil {
			logger.ErrorCF("agent", "Failed to initialize exec tool; continuing without exec",
				map[string]any{"error": err.Error()})
		} else {
			registry.Register(execTool)
		}
	}
	if cfg.Tools.IsToolEnabled("web") {
		searchTool, err := tools.NewWebSearchTool(tools.WebSearchToolOptionsFromConfig(cfg))
		if err != nil {
			logger.ErrorCF("agent", "Failed to create web search tool", map[string]any{"error": err.Error()})
		} else if searchTool != nil {
			registry.Register(searchTool)
		}
	}
	if cfg.Tools.IsToolEnabled("web_fetch") {
		fetchTool, err := tools.NewWebFetchToolWithProxy(
			50000,
			cfg.Tools.Web.Proxy,
			cfg.Tools.Web.Format,
			cfg.Tools.Web.FetchLimitBytes,
			cfg.Tools.Web.PrivateHostWhitelist)
		if err != nil {
			logger.ErrorCF("agent", "Failed to create web fetch tool", map[string]any{"error": err.Error()})
		} else {
			registry.Register(fetchTool)
		}
	}
	if cfg.Tools.IsToolEnabled("i2c") {
		registry.Register(tools.NewI2CTool())
	}
	if cfg.Tools.IsToolEnabled("spi") {
		registry.Register(tools.NewSPITool())
	}
	if cfg.Tools.IsToolEnabled("serial") {
		registry.Register(tools.NewSerialTool())
	}

	return registry, nil
}

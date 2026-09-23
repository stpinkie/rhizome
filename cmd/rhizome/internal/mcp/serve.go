package mcp

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/pkg/agent"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/logger"
	picomcp "github.com/stpinkie/rhizome/pkg/mcp"
)

// newServeCommand exposes allowlisted local tools as an MCP server over
// stdio. stdout is the protocol channel — nothing may print to it, so all
// diagnostics go through stderr (the ACP stderr-drain rule applies here
// too). The command is daemonless and refuses to run unless
// tools.mcp_server.enabled is set in config.
func newServeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve allowlisted Rhizome tools over MCP stdio",
		Long: "Run Rhizome as an MCP server on stdio. Any MCP client (Claude Desktop, " +
			"Cursor, another agent) can then list and call the local tools named in " +
			"tools.mcp_server.allow. Disabled by default; set tools.mcp_server.enabled " +
			"in config.json to turn it on.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// stdout carries the MCP protocol stream — no log line may
			// reach it. Same drain as `rhizome acp`: must happen before
			// loadConfig, which logs through the stdout-bound console
			// writer. RHIZOME_LOG_FILE keeps working.
			logger.ConfigureFromEnv()
			logger.DisableConsole()
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if !cfg.Tools.MCPServer.Enabled {
				return fmt.Errorf(
					"mcp serve is disabled — set tools.mcp_server.enabled (and tools.mcp_server.allow) in config.json")
			}

			registry, err := agent.BuildMCPServeToolRegistry(cfg)
			if err != nil {
				return fmt.Errorf("failed to build tool registry: %w", err)
			}
			if len(cfg.Tools.MCPServer.Allow) == 0 {
				fmt.Fprintln(os.Stderr,
					"warning: tools.mcp_server.allow is empty — serving zero tools")
			}

			return picomcp.ServeTools(cmd.Context(), registry, cfg.Tools.MCPServer.Allow, config.FormatVersion())
		},
	}
	return cmd
}

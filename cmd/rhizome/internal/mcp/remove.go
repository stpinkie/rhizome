package mcp

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an MCP server from config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			name := args[0]
			_, inServers := cfg.Tools.MCP.Servers[name]
			_, inPresets := cfg.Tools.MCP.Presets[name]
			if !inServers && !inPresets {
				return fmt.Errorf("MCP server %q not found", name)
			}

			if inPresets {
				delete(cfg.Tools.MCP.Presets, name)
				if len(cfg.Tools.MCP.Presets) == 0 {
					cfg.Tools.MCP.Presets = nil
				}
			}
			if inServers {
				delete(cfg.Tools.MCP.Servers, name)
			}
			if len(cfg.Tools.MCP.Servers) == 0 && len(cfg.Tools.MCP.Presets) == 0 {
				cfg.Tools.MCP.Servers = nil
				cfg.Tools.MCP.Enabled = false
			}

			if err := saveValidatedConfig(cfg); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ MCP server %q removed.\n", name)
			return nil
		},
	}
}

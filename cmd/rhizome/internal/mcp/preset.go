package mcp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/pkg/config"
)

// newPresetCommand implements `rhizome mcp preset` — enable/configure
// first-class hosted MCP presets (currently "context7"). The api key is
// stored as a SecureString and never reprinted.
func newPresetCommand() *cobra.Command {
	var key string
	var enable, disable bool

	cmd := &cobra.Command{
		Use:   "preset <name>",
		Short: "Configure a first-class MCP preset (context7)",
		Long: "First-class presets wire well-known hosted MCP servers. " +
			"`context7` expands to the hosted Context7 server " +
			"(https://mcp.context7.com/mcp) with the CONTEXT7_API_KEY header. " +
			"The key is stored as a SecureString (config.security.yml, " +
			"or a file:// / enc:// reference) and falls back to the " +
			"CONTEXT7_API_KEY environment variable.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.ToLower(strings.TrimSpace(args[0]))
			spec, ok := config.MCPPresetSpecs[name]
			if !ok {
				names := make([]string, 0, len(config.MCPPresetSpecs))
				for n := range config.MCPPresetSpecs {
					names = append(names, n)
				}
				sort.Strings(names)
				return fmt.Errorf("unknown preset %q (known: %s)", name, strings.Join(names, ", "))
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.Tools.MCP.Presets == nil {
				cfg.Tools.MCP.Presets = make(map[string]config.MCPPresetConfig)
			}
			preset := cfg.Tools.MCP.Presets[name]

			if key != "" {
				preset.APIKey = *config.NewSecureString(key)
			}
			switch {
			case enable:
				preset.Enabled = true
			case disable:
				preset.Enabled = false
			case key == "":
				return fmt.Errorf("nothing to do — pass --key and/or --enable/--disable")
			}
			if preset.Enabled {
				cfg.Tools.MCP.Enabled = true
			}
			cfg.Tools.MCP.Presets[name] = preset

			if err := saveValidatedConfig(cfg); err != nil {
				return err
			}

			state := "disabled"
			if preset.Enabled {
				state = "enabled"
			}
			keyNote := " (no key stored — falls back to env " + spec.EnvVar + ")"
			if preset.APIKey.String() != "" {
				keyNote = " (api key stored — not shown)"
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"✓ MCP preset %q %s → %s via %s header%s\n",
				name, state, spec.URL, spec.HeaderName, keyNote)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "API key (stored as a SecureString; CONTEXT7_API_KEY env is the fallback)")
	cmd.Flags().BoolVar(&enable, "enable", false, "Enable the preset")
	cmd.Flags().BoolVar(&disable, "disable", false, "Disable the preset")
	return cmd
}

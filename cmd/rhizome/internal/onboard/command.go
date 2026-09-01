package onboard

import (
	"github.com/spf13/cobra"
)

type onboardConfig struct {
	encrypt        bool
	workspace      string
	nonInteractive bool
	yes            bool
}

// NewOnboardCommand returns the top-level `rhizome onboard` command that
// initializes the Rhizome configuration and workspace.
func NewOnboardCommand() *cobra.Command {
	var cfg onboardConfig

	cmd := &cobra.Command{
		Use:     "onboard",
		Aliases: []string{"o"},
		Short:   "Initialize Rhizome configuration and workspace",
		Long: "Create the default config.json, prepare the agent workspace, " +
			"and optionally set up an SSH key for credential encryption.",
		Args: cobra.NoArgs,
		Example: `  rhizome onboard
  rhizome onboard --enc
  rhizome onboard --workspace /path/to/existing/workspace
  rhizome onboard --non-interactive`,
		Run: func(_ *cobra.Command, _ []string) {
			runOnboard(cfg)
		},
	}

	cmd.Flags().BoolVarP(&cfg.encrypt, "enc", "e", false,
		"Enable credential encryption (generates SSH key and prompts for a passphrase)")
	cmd.Flags().StringVarP(&cfg.workspace, "workspace", "w", "",
		"Use an existing workspace directory instead of creating a new one")
	cmd.Flags().BoolVarP(&cfg.nonInteractive, "non-interactive", "n", false,
		"Fail if any required value is missing instead of prompting")
	cmd.Flags().BoolVarP(&cfg.yes, "yes", "y", false,
		"Answer yes to non-destructive confirmation prompts")

	return cmd
}

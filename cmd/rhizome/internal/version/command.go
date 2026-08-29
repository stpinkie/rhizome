package version

import (
	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/cliui"
	"github.com/stpinkie/rhizome/pkg/config"
)

func NewVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Show version information",
		Run: func(_ *cobra.Command, _ []string) {
			printVersion()
		},
	}

	return cmd
}

func printVersion() {
	build, goVer := config.FormatBuildInfo()
	cliui.PrintVersion(internal.Logo, "rhizome "+config.FormatVersion(), build, goVer)
}

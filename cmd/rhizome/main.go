// Rhizome - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	acpcmd "github.com/stpinkie/rhizome/cmd/rhizome/internal/acp"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/agent"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/auth"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/cliui"
	configcmd "github.com/stpinkie/rhizome/cmd/rhizome/internal/config"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/cron"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/daemon"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/gateway"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/mcp"
	meshcmd "github.com/stpinkie/rhizome/cmd/rhizome/internal/mesh"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/migrate"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/model"
	modulecmd "github.com/stpinkie/rhizome/cmd/rhizome/internal/module"
	networkcmd "github.com/stpinkie/rhizome/cmd/rhizome/internal/network"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/onboard"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/skills"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/status"
	swarmcmd "github.com/stpinkie/rhizome/cmd/rhizome/internal/swarm"
	synccmd "github.com/stpinkie/rhizome/cmd/rhizome/internal/sync"
	"github.com/stpinkie/rhizome/cmd/rhizome/internal/version"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/updater"
)

var rootNoColor bool

// initTermuxSSL detects Termux environment and sets SSL_CERT_FILE if not already set.
// This fixes X509 certificate errors when running Rhizome inside Termux or termux-chroot.
// See: https://github.com/stpinkie/rhizome/issues/2944
func initTermuxSSL() {
	// Only applicable on Linux/Android
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		return
	}

	// Skip if already set
	if os.Getenv("SSL_CERT_FILE") != "" {
		return
	}

	// Check for Termux prefix in PATH or HOME
	home := os.Getenv("HOME")
	path := os.Getenv("PATH")

	isTermux := strings.Contains(home, "com.termux") ||
		strings.Contains(path, "com.termux") ||
		strings.Contains(home, "/data/data/com.termux")

	if !isTermux {
		return
	}

	// Check common CA bundle locations in Termux
	caPaths := []string{
		"$PREFIX/etc/tls/cert.pem",
		os.Getenv("PREFIX") + "/etc/tls/cert.pem",
		"/data/data/com.termux/files/usr/etc/tls/cert.pem",
		"/usr/etc/tls/cert.pem",
	}

	for _, caPath := range caPaths {
		expanded := os.ExpandEnv(caPath)
		if _, err := os.Stat(expanded); err == nil {
			os.Setenv("SSL_CERT_FILE", expanded)
			return
		}
	}
}

func syncCliUIColor(root *cobra.Command) {
	no, _ := root.PersistentFlags().GetBool("no-color")
	cliui.Init(no || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb")
}

// earlyColorDisabled matches lipgloss/banner behavior from env and argv before Cobra parses flags.
func earlyColorDisabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return true
	}
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--no-color" || arg == "--no-color=true" || arg == "--no-color=1" {
			return true
		}
	}
	return false
}

func NewRhizomeCommand() *cobra.Command {
	short := fmt.Sprintf("%s Rhizome — personal AI assistant", internal.Logo)
	long := fmt.Sprintf(`%s Rhizome is a lightweight personal AI assistant.

Version: %s`, internal.Logo, config.FormatVersion())

	cmd := &cobra.Command{
		Use:   "rhizome",
		Short: short,
		Long:  long,
		Example: `rhizome version
rhizome onboard
rhizome --no-color status`,
		SilenceErrors: true,
		// Avoid plain UsageString() on stderr/stdout when a command fails; cliui
		// renders matching panels on stderr instead.
		SilenceUsage: true,
		PersistentPreRun: func(c *cobra.Command, _ []string) {
			syncCliUIColor(c.Root())
		},
	}

	cmd.PersistentFlags().BoolVar(&rootNoColor, "no-color", false,
		"Disable colors (boxed layout unchanged)")

	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		syncCliUIColor(c.Root())
		fmt.Fprint(c.OutOrStdout(), cliui.RenderCommandHelp(c))
	})

	cmd.AddCommand(
		configcmd.NewConfigCommand(),
		onboard.NewOnboardCommand(),
		acpcmd.NewACPCommand(),
		agent.NewAgentCommand(),
		auth.NewAuthCommand(),
		daemon.NewDaemonCommand(),
		gateway.NewGatewayCommand(),
		status.NewStatusCommand(),
		synccmd.NewSyncCommand(),
		cron.NewCronCommand(),
		mcp.NewMCPCommand(),
		migrate.NewMigrateCommand(),
		networkcmd.NewNetworkCommand(),
		meshcmd.NewMeshCommand(),
		swarmcmd.NewSwarmCommand(),
		modulecmd.NewModuleCommand(),
		skills.NewSkillsCommand(),
		model.NewModelCommand(),
		updater.NewUpdateCommand("rhizome"),
		version.NewVersionCommand(),
	)

	return cmd
}

const (
	colorBlue = "\033[1;38;2;62;93;185m"
	colorRed  = "\033[1;38;2;213;70;70m"
	banner    = "\r\n" +
		colorBlue + "██████╗ ██╗  ██╗██╗███████╗" + colorRed + " ██████╗ ███╗   ███╗███████╗\n" +
		colorBlue + "██╔══██╗██║  ██║██║╚══███╔╝" + colorRed + "██╔═══██╗████╗ ████║██╔════╝\n" +
		colorBlue + "██████╔╝███████║██║  ███╔╝ " + colorRed + "██║   ██║██╔████╔██║█████╗  \n" +
		colorBlue + "██╔══██╗██╔══██║██║ ███╔╝  " + colorRed + "██║   ██║██║╚██╔╝██║██╔══╝  \n" +
		colorBlue + "██║  ██║██║  ██║██║███████╗" + colorRed + "╚██████╔╝██║ ╚═╝ ██║███████╗\n" +
		colorBlue + "╚═╝  ╚═╝╚═╝  ╚═╝╚═╝╚══════╝" + colorRed + " ╚═════╝ ╚═╝     ╚═╝╚══════╝\n " +
		"\033[0m\r\n"
	plainBanner = "\r\n" +
		"██████╗ ██╗  ██╗██╗███████╗ ██████╗ ███╗   ███╗███████╗\n" +
		"██╔══██╗██║  ██║██║╚══███╔╝██╔═══██╗████╗ ████║██╔════╝\n" +
		"██████╔╝███████║██║  ███╔╝ ██║   ██║██╔████╔██║█████╗  \n" +
		"██╔══██╗██╔══██║██║ ███╔╝  ██║   ██║██║╚██╔╝██║██╔══╝  \n" +
		"██║  ██║██║  ██║██║███████╗╚██████╔╝██║ ╚═╝ ██║███████╗\n" +
		"╚═╝  ╚═╝╚═╝  ╚═╝╚═╝╚══════╝ ╚═════╝ ╚═╝     ╚═╝╚══════╝\n " +
		"\r\n"
)

// stdioProtocolCommand reports whether argv selects a command that speaks a
// machine protocol on stdout (currently only `rhizome acp`). For those
// commands nothing may reach stdout outside the protocol stream — no banner,
// no env diagnostics.
func stdioProtocolCommand() bool {
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-") {
			continue // flags (and "--") never name the subcommand
		}
		return arg == "acp"
	}
	return false
}

func main() {
	// Initialize Termux SSL certificate detection before anything else
	initTermuxSSL()

	cliui.Init(earlyColorDisabled())
	stdio := stdioProtocolCommand()

	if !stdio {
		if earlyColorDisabled() {
			fmt.Print(plainBanner)
		} else {
			fmt.Printf("%s", banner)
		}
	}

	tzEnv := os.Getenv("TZ")
	if tzEnv != "" && !stdio {
		fmt.Println("TZ environment:", tzEnv)
		zoneinfoEnv := os.Getenv("ZONEINFO")
		fmt.Println("ZONEINFO environment:", zoneinfoEnv)
		loc, err := time.LoadLocation(tzEnv)
		if err != nil {
			fmt.Println("Error loading time zone:", err)
		} else {
			fmt.Println("Time zone loaded successfully:", loc)
			time.Local = loc //nolint:gosmopolitan // We intentionally set local timezone from TZ env
		}
	}

	cmd := NewRhizomeCommand()
	last, err := cmd.ExecuteC()
	if err != nil {
		syncCliUIColor(cmd)
		fmt.Fprint(os.Stderr, cliui.FormatCLIError(err.Error(), last))
		os.Exit(1)
	}
}

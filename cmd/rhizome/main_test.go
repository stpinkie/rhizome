package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stpinkie/rhizome/cmd/rhizome/internal"
	"github.com/stpinkie/rhizome/pkg/config"
)

func TestNewRhizomeCommand(t *testing.T) {
	cmd := NewRhizomeCommand()

	require.NotNil(t, cmd)

	short := fmt.Sprintf("%s Rhizome — personal AI assistant", internal.Logo)
	longHas := strings.Contains(cmd.Long, config.FormatVersion())

	assert.Equal(t, "rhizome", cmd.Use)
	assert.Equal(t, short, cmd.Short)
	assert.True(t, longHas)

	assert.True(t, cmd.HasSubCommands())
	assert.True(t, cmd.HasAvailableSubCommands())

	assert.True(t, cmd.PersistentFlags().Lookup("no-color") != nil)

	assert.Nil(t, cmd.Run)
	assert.Nil(t, cmd.RunE)

	assert.NotNil(t, cmd.PersistentPreRun)
	assert.Nil(t, cmd.PersistentPostRun)

	allowedCommands := []string{
		"acp",
		"agent",
		"auth",
		"config",
		"cron",
		"daemon",
		"gateway",
		"mesh",
		"mcp",
		"migrate",
		"model",
		"module",
		"network",
		"onboard",
		"skills",
		"status",
		"swarm",
		"sync",
		"update",
		"version",
		"wallet",
		"web3",
	}

	subcommands := cmd.Commands()
	assert.Len(t, subcommands, len(allowedCommands))

	for _, subcmd := range subcommands {
		found := slices.Contains(allowedCommands, subcmd.Name())
		assert.True(t, found, "unexpected subcommand %q", subcmd.Name())

		assert.False(t, subcmd.Hidden)
	}
}

func TestStdioProtocolCommand(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"rhizome"}, false},
		{[]string{"rhizome", "acp"}, true},
		{[]string{"rhizome", "--no-color", "acp"}, true},
		{[]string{"rhizome", "mcp", "serve"}, true},
		{[]string{"rhizome", "mcp"}, false},
		{[]string{"rhizome", "mcp", "list"}, false},
		{[]string{"rhizome", "serve"}, false},
		{[]string{"rhizome", "agent"}, false},
	}
	for _, tc := range cases {
		os.Args = tc.args
		assert.Equal(t, tc.want, stdioProtocolCommand(), "args=%v", tc.args)
	}
}

package onboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOnboardCommand(t *testing.T) {
	cmd := NewOnboardCommand()

	require.NotNil(t, cmd)
	assert.Equal(t, "onboard", cmd.Use)
	assert.Equal(t, "Initialize Rhizome configuration and workspace", cmd.Short)
	assert.ElementsMatch(t, []string{"o"}, cmd.Aliases)
	assert.NotNil(t, cmd.Run)
	assert.Nil(t, cmd.RunE)

	flags := []string{"enc", "workspace", "non-interactive", "yes"}
	for _, name := range flags {
		assert.NotNil(t, cmd.Flags().Lookup(name), "expected --%s flag to be registered", name)
	}

	encFlag := cmd.Flags().Lookup("enc")
	require.NotNil(t, encFlag)
	assert.Equal(t, "false", encFlag.DefValue)

	workspaceFlag := cmd.Flags().Lookup("workspace")
	require.NotNil(t, workspaceFlag)
	assert.Equal(t, "", workspaceFlag.DefValue)

	nonInteractiveFlag := cmd.Flags().Lookup("non-interactive")
	require.NotNil(t, nonInteractiveFlag)
	assert.Equal(t, "false", nonInteractiveFlag.DefValue)
}

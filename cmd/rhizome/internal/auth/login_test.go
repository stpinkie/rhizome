package auth

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLoginSubCommand(t *testing.T) {
	cmd := newLoginCommand()

	require.NotNil(t, cmd)

	assert.Equal(t, "Login via OAuth or paste token", cmd.Short)

	assert.True(t, cmd.HasFlags())

	assert.NotNil(t, cmd.Flags().Lookup("device-code"))
	assert.NotNil(t, cmd.Flags().Lookup("no-browser"))

	providerFlag := cmd.Flags().Lookup("provider")
	require.NotNil(t, providerFlag)

	val, found := providerFlag.Annotations[cobra.BashCompOneRequiredFlag]
	require.True(t, found)
	require.NotEmpty(t, val)
	assert.Equal(t, "true", val[0])
}

// TestAuthLoginCmd_FlagMatrix verifies unsupported flag/provider
// combinations fail fast instead of being silently ignored.
func TestAuthLoginCmd_FlagMatrix(t *testing.T) {
	cases := []struct {
		name          string
		provider      string
		deviceCode    bool
		setupToken    bool
		noBrowser     bool
		wantErrSubstr string
	}{
		{"device-code on anthropic", "anthropic", true, false, false, "--device-code is only supported for openai"},
		{"device-code on antigravity", "antigravity", true, false, false, "--device-code is only supported"},
		{"setup-token on openai", "openai", false, true, false, "--setup-token is only supported for anthropic"},
		{"setup-token on antigravity", "google-antigravity", false, true, false, "--setup-token is only supported"},
		{"no-browser on anthropic", "anthropic", false, false, true, "--no-browser has no effect"},
		{"unknown provider", "bogus", false, false, false, "unsupported provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := authLoginCmd(tc.provider, tc.deviceCode, tc.setupToken, tc.noBrowser)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErrSubstr)
		})
	}
}

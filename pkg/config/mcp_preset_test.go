package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPPresetContext7Expands(t *testing.T) {
	mcp := MCPConfig{
		Presets: map[string]MCPPresetConfig{
			"context7": {
				Enabled: true,
				APIKey:  *NewSecureString("ctx7sk-test"),
			},
		},
	}
	servers := mcp.EffectiveServers()
	require.Contains(t, servers, "context7")
	srv := servers["context7"]
	assert.True(t, srv.Enabled)
	assert.Equal(t, "http", srv.Type)
	assert.Equal(t, "https://mcp.context7.com/mcp", srv.URL)
	assert.Equal(t, "ctx7sk-test", srv.Headers["CONTEXT7_API_KEY"])
}

func TestMCPPresetEnvFallback(t *testing.T) {
	t.Setenv("CONTEXT7_API_KEY", "env-key-1")
	mcp := MCPConfig{
		Presets: map[string]MCPPresetConfig{"context7": {Enabled: true}},
	}
	srv := mcp.EffectiveServers()["context7"]
	assert.Equal(t, "env-key-1", srv.Headers["CONTEXT7_API_KEY"])
}

func TestMCPPresetExplicitServerWins(t *testing.T) {
	mcp := MCPConfig{
		Presets: map[string]MCPPresetConfig{"context7": {Enabled: true}},
		Servers: map[string]MCPServerConfig{
			"context7": {Enabled: true, Type: "http", URL: "http://localhost:9999/mcp"},
		},
	}
	assert.Equal(t, "http://localhost:9999/mcp", mcp.EffectiveServers()["context7"].URL)
}

func TestMCPPresetUnknownIgnored(t *testing.T) {
	mcp := MCPConfig{
		Presets: map[string]MCPPresetConfig{"nonexistent": {Enabled: true}},
	}
	assert.Empty(t, mcp.EffectiveServers())
}

func TestMCPPresetKeyMaskedInJSON(t *testing.T) {
	mcp := MCPConfig{
		Presets: map[string]MCPPresetConfig{
			"context7": {Enabled: true, APIKey: *NewSecureString("ctx7sk-secret")},
		},
	}
	data, err := mcp.Presets["context7"].APIKey.MarshalJSON()
	require.NoError(t, err)
	assert.NotContains(t, string(data), "ctx7sk-secret")
}

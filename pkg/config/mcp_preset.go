package config

import (
	"os"
	"strings"
)

// MCPPresetConfig configures a first-class named MCP preset. Presets wire
// well-known hosted MCP servers without hand-editing mcp.servers.
//
//	api_key marshals as "[NOT_HERE]" in config.json; the resolved value lives
//	in config.security.yml (or an enc:// / file:// reference, or the preset's
//	fallback environment variable).
type MCPPresetConfig struct {
	Enabled bool         `json:"enabled"          yaml:"enabled"`
	APIKey  SecureString `json:"api_key,omitzero" yaml:"api_key,omitempty" env:"-"`
}

// MCPPresetSpec describes one built-in preset.
type MCPPresetSpec struct {
	// URL is the hosted server endpoint.
	URL string
	// HeaderName is the HTTP header carrying the API key.
	HeaderName string
	// EnvVar is the environment fallback when api_key is unset.
	EnvVar string
}

// MCPPresetSpecs is the registry of known MCP presets. Adding a preset is a
// table entry — the EffectiveServers merge handles the rest.
var MCPPresetSpecs = map[string]MCPPresetSpec{
	"context7": {
		URL:        "https://mcp.context7.com/mcp",
		HeaderName: "CONTEXT7_API_KEY",
		EnvVar:     "CONTEXT7_API_KEY",
	},
}

// PresetServer expands a preset entry into an MCPServerConfig. Returns false
// for unknown preset names (ignored, not an error — forward compatibility).
func PresetServer(name string, p MCPPresetConfig) (MCPServerConfig, bool) {
	spec, ok := MCPPresetSpecs[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return MCPServerConfig{}, false
	}
	key := p.APIKey.String()
	if key == "" {
		key = strings.TrimSpace(os.Getenv(spec.EnvVar))
	}
	srv := MCPServerConfig{
		Enabled: p.Enabled,
		Type:    "http",
		URL:     spec.URL,
	}
	if key != "" {
		srv.Headers = map[string]string{spec.HeaderName: key}
	}
	return srv, true
}

// EffectiveServers returns mcp.servers merged with expanded presets.
// An explicit mcp.servers entry with the same name always wins over the
// preset expansion. Names are compared case-insensitively so an explicit
// "Context7" entry overrides the built-in "context7" preset rather than
// coexisting as a separate server.
func (c *MCPConfig) EffectiveServers() map[string]MCPServerConfig {
	out := make(map[string]MCPServerConfig, len(c.Servers)+len(c.Presets))
	// Build a case-insensitive index of explicit servers so we can detect
	// overrides regardless of case differences.
	explicitLower := make(map[string]string, len(c.Servers)) // lowercase name -> original key
	for name := range c.Servers {
		explicitLower[strings.ToLower(name)] = name
	}
	for name, p := range c.Presets {
		lower := strings.ToLower(name)
		// Skip preset if an explicit server overrides it (case-insensitive).
		if _, overridden := explicitLower[lower]; overridden {
			continue
		}
		if srv, ok := PresetServer(name, p); ok {
			out[lower] = srv
		}
	}
	for name, s := range c.Servers {
		out[name] = s
	}
	return out
}

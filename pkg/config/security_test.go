// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSecurityConfig(t *testing.T) {
	t.Run("LoadNonExistent", func(t *testing.T) {
		sec := &Config{Channels: make(ChannelsConfig)}
		err := loadSecurityConfig(sec, "/nonexistent/.security.yml")
		require.NoError(t, err)
		assert.NotNil(t, sec)
		assert.Empty(t, sec.ModelList)
		assert.NotNil(t, sec.Channels)
		assert.NotNil(t, sec.Tools.Web)
		assert.NotNil(t, sec.Tools.Skills)
	})
}

func TestSecurityPath(t *testing.T) {
	tests := []struct {
		name      string
		configDir string
		want      string
	}{
		{
			name:      "standard path",
			configDir: "/home/user/.rhizome/config.json",
			want:      "/home/user/.rhizome/.security.yml",
		},
		{
			name:      "nested path",
			configDir: "/path/to/config/myconfig.json",
			want:      "/path/to/config/.security.yml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := securityPath(tt.configDir)
			assert.Equal(t, filepath.FromSlash(tt.want), got)
		})
	}
}

func TestSaveAndLoadSecurityConfig(t *testing.T) {
	t.Run("test for securestring", func(t *testing.T) {
		type testStruct struct {
			Secret SecureString `json:"secret,omitzero" yaml:"secret,omitempty" env:"TEST_SECURE_STRING"`
		}
		s := testStruct{Secret: *NewSecureString("test")}
		out, err := yaml.Marshal(s) // 直接对 SecureString 进行序列化
		require.NoError(t, err)
		t.Logf("output: %v", string(out))
		assert.Equal(t, "secret: test\n", string(out))
		out, err = json.Marshal(s)
		require.NoError(t, err)
		t.Logf("output: %v", string(out))
		assert.Equal(t, "{}", string(out))
	})
	tmpDir := t.TempDir()
	secPath := filepath.Join(tmpDir, SecurityConfigFile)

	original := &Config{
		Version: CurrentVersion,
		ModelList: SecureModelList{
			{
				ModelName: "model1",
				Model:     "test/model",
				APIBase:   "api.example.com",
				APIKeys:   SecureStrings{NewSecureString("key1"), NewSecureString("key2")},
			},
			{
				ModelName: "model2",
				Model:     "test/model2",
				APIBase:   "api2.example.com",
				APIKeys:   SecureStrings{NewSecureString("model2_key")},
			},
		},
		Tools: ToolsConfig{
			Web: WebToolsConfig{
				Brave: BraveConfig{
					Enabled: true,
					APIKeys: SecureStrings{NewSecureString("brave_key")},
				},
			},
			Skills: SkillsToolsConfig{
				Github: SkillsGithubConfig{
					Token: *NewSecureString("github_token"),
					Proxy: "test proxy",
				},
			},
		},
		Channels: func() ChannelsConfig {
			chs := make(ChannelsConfig)
			type def struct {
				name string
				raw  string // raw JSON with actual secure values (bypasses SecureString.MarshalJSON)
			}
			for _, d := range []def{
				{"telegram", `{"enabled":true,"settings":{"token":"telegram_token"}}`},
				{"feishu", `{"enabled":true,"settings":{"app_id":"feishu_app_id","app_secret":"feishu_app_secret"}}`},
				{"discord", `{"enabled":true,"settings":{"token":"discord_token"}}`},
				{"qq", `{"enabled":true,"settings":{"app_secret":"qq_app_secret"}}`},
				{"pico_client", `{"enabled":true,"settings":{"token":"pico_client_token"}}`},
			} {
				bc := &Channel{}
				json.Unmarshal([]byte(d.raw), bc)
				bc.Type = d.name
				switch bc.Type {
				case "qq":
					bc.Decode(&QQSettings{})
				case "telegram":
					bc.Decode(&TelegramSettings{})
				case "discord":
					bc.Decode(&DiscordSettings{})
				case "feishu":
					bc.Decode(&FeishuSettings{})
				case "pico_client":
					bc.Decode(&PicoClientSettings{})
				}
				chs[d.name] = bc
			}
			return chs
		}(),
	}

	t.Run("test for original", func(t *testing.T) {
		assert.Equal(t, 2, len(original.ModelList[0].APIKeys))
		assert.Equal(t, "key1", original.ModelList[0].APIKeys[0].String())
	})

	cfg2 := &Config{}
	t.Run("test for json", func(t *testing.T) {
		marshal, err := json.Marshal(original)
		require.NoError(t, err)
		t.Logf("json: %s", string(marshal))
		assert.NotContains(t, string(marshal), "\"api_keys\"")
		assert.NotContains(t, string(marshal), notHere)

		err = json.Unmarshal(marshal, cfg2)
		require.NoError(t, err)
		require.Equal(t, 2, len(cfg2.ModelList))
		assert.Empty(t, cfg2.ModelList[0].APIKeys)
		assert.Empty(t, cfg2.ModelList[1].APIKeys)
	})

	t.Run("test for save yaml", func(t *testing.T) {
		// Save
		err := saveSecurityConfig(secPath, original)
		require.NoError(t, err)

		// Verify file was created with correct permissions (Unix only).
		info, err := os.Stat(secPath)
		require.NoError(t, err)
		if runtime.GOOS != "windows" {
			assert.Equal(t, os.FileMode(0o600), info.Mode())
		}

		file, err := os.ReadFile(secPath)
		assert.NoError(t, err)
		t.Logf("%s", string(file))

		// Parse saved YAML and verify channelTestSaveConfig_EncryptsPlaintextAPIKey secure fields are present
		var saved struct {
			ChannelList map[string]map[string]any `yaml:"channel_list"`
		}
		require.NoError(t, yaml.Unmarshal(file, &saved))
		channels := saved.ChannelList
		getSetting := func(name string) map[string]any {
			return channels[name]["settings"].(map[string]any)
		}
		assert.Contains(t, getSetting("telegram")["token"], "telegram_token")
		assert.Contains(t, getSetting("feishu")["app_secret"], "feishu_app_secret")
		assert.Contains(t, getSetting("discord")["token"], "discord_token")
		assert.Contains(t, getSetting("qq")["app_secret"], "qq_app_secret")
		assert.Contains(t, getSetting("pico_client")["token"], "pico_client_token")

		// Rewrite file with deterministic content for load test (use channel_list)
		yamlOutput := `channel_list:
  telegram:
    token: telegram_token
  feishu:
    app_secret: feishu_app_secret
  discord:
    token: discord_token
  qq:
    app_secret: qq_app_secret
  pico_client:
    token: pico_client_token
model_list:
  model1:0:
    api_keys:
      - key1
      - key2
  model2:0:
    api_keys:
      - model2_key
web:
  brave:
    api_keys:
      - brave_key
skills:
  github:
    token: github_token
`
		err = os.WriteFile(secPath, []byte(yamlOutput), 0o600)
		require.NoError(t, err)
	})

	t.Run("test for load yaml", func(t *testing.T) {
		// Load
		cfg := cfg2
		err := loadSecurityConfig(cfg, secPath)
		require.NoError(t, err)

		t.Logf("%+v", cfg)
		t.Logf("%+v", cfg.Tools.Web.Brave.APIKeys)
		t.Logf("%+v", cfg.Tools.Skills.Github.Token)
		require.EqualValues(t, 2, len(cfg.ModelList))
		assert.Equal(t, "key1", cfg.ModelList[0].APIKeys[0].String())
		assert.Equal(t, "key2", cfg.ModelList[0].APIKeys[1].String())
		assert.Equal(t, "model2_key", cfg.ModelList[1].APIKeys[0].String())
		assert.EqualValues(t, original.Tools.Web.Brave.APIKeys, cfg.Tools.Web.Brave.APIKeys)
	})

	t.Run("test for env overwrite", func(t *testing.T) {
		// This will throw a COMPILER ERROR if SecureString doesn't
		// correctly implement the yaml.Marshaler interface.
		var _ yaml.Marshaler = (*SecureString)(nil)
		// If you are using Value types in your config, also check:
		var _ yaml.Marshaler = SecureString{}

		// Set up a fresh config with a qq channel
		envCfg := &Config{
			Channels: ChannelsConfig{
				"qq": {
					Enabled:  true,
					Type:     "qq",
					Settings: RawNode(`{"enabled":true,"app_secret":"qq_app_secret"}`),
				},
			},
			Tools: original.Tools,
		}

		t.Setenv("RHIZOME_CHANNELS_QQ_APP_SECRET", "qq_app_secret_env")
		t.Setenv("RHIZOME_TOOLS_WEB_BRAVE_API_KEYS", "brave_key_env,abc")

		require.NoError(t, env.Parse(envCfg))
		// Channel env overrides need explicit handling since ChannelsConfig is map-based
		require.NoError(t, InitChannelList(envCfg.Channels))

		bc := envCfg.Channels.Get("qq")
		decoded, err := bc.GetDecoded()
		require.NoError(t, err)
		qqCfg := decoded.(*QQSettings)
		assert.Equal(t, "qq_app_secret_env", qqCfg.AppSecret.raw)
		assert.Equal(t, "brave_key_env", envCfg.Tools.Web.Brave.APIKeys[0].raw)
		assert.Equal(t, "abc", envCfg.Tools.Web.Brave.APIKeys[1].raw)
	})
}

// TestBrowserBackendsSecurityRoundTrip verifies that browser backend API keys
// persist to .security.yml (masked as [NOT_HERE] in config.json) and merge
// back on load without clobbering the non-secret fields that live only in
// config.json.
func TestBrowserBackendsSecurityRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	secPath := securityPath(configPath)

	original := &Config{}
	original.Tools.Browser.Backends = BrowserBackendsConfig{
		"steel": {
			APIKey:  *NewSecureString("steel_secret"),
			BaseURL: "https://self.example.com",
		},
		"custom-cdp": {EndpointURL: "ws://127.0.0.1:9222"},
	}

	require.NoError(t, SaveConfig(configPath, original))

	// config.json must not contain the plaintext secret.
	rawJSON, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(rawJSON), "steel_secret")
	assert.Contains(t, string(rawJSON), "self.example.com")

	// .security.yml must carry the api_key under browser.backends.steel.
	rawYAML, err := os.ReadFile(secPath)
	require.NoError(t, err)
	assert.Contains(t, string(rawYAML), "steel_secret")
	var saved struct {
		Browser struct {
			Backends map[string]map[string]any `yaml:"backends"`
		} `yaml:"browser"`
	}
	require.NoError(t, yaml.Unmarshal(rawYAML, &saved))
	require.Contains(t, saved.Browser.Backends, "steel")
	assert.Equal(t, "steel_secret", saved.Browser.Backends["steel"]["api_key"])
	// Secret-less backends are omitted from .security.yml.
	assert.NotContains(t, saved.Browser.Backends, "custom-cdp")

	// Simulate LoadConfig: JSON first (api_key masked away), then
	// .security.yml merges secrets back — without wiping non-secret fields.
	loaded := &Config{}
	require.NoError(t, json.Unmarshal(rawJSON, loaded))
	steelJSON := loaded.Tools.Browser.Backends["steel"]
	assert.Equal(t, "", steelJSON.APIKey.String())
	assert.Equal(t, "https://self.example.com", steelJSON.BaseURL)

	// A stale backend id present only in .security.yml must not resurrect.
	require.NoError(t, os.WriteFile(secPath, []byte(`browser:
  backends:
    steel:
      api_key: steel_secret
    ghost:
      api_key: ghost_key
`), 0o600))
	require.NoError(t, loadSecurityConfig(loaded, secPath))

	steel := loaded.Tools.Browser.Backends["steel"]
	assert.Equal(t, "steel_secret", steel.APIKey.String())
	assert.Equal(t, "https://self.example.com", steel.BaseURL)
	_, ghostPresent := loaded.Tools.Browser.Backends["ghost"]
	assert.False(t, ghostPresent)

	// When config.json defines no browser backends at all, .security.yml
	// entries must still not resurrect them — every non-secret field lives
	// only in config.json, so a secret-only entry is unconfigurable anyway.
	empty := &Config{}
	require.NoError(t, loadSecurityConfig(empty, secPath))
	assert.Empty(t, empty.Tools.Browser.Backends)
}

// TestBrowserSectionOmittedWhenSecretless verifies .security.yml does not
// gain a stray `browser:` key when no backend api_key is configured —
// Backends is the only yaml-visible field, so the section should drop out.
func TestBrowserSectionOmittedWhenSecretless(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	secPath := securityPath(configPath)

	cfg := &Config{}
	cfg.Tools.Browser.DefaultBackend = "agent-browser"
	cfg.Tools.Browser.Backends = BrowserBackendsConfig{
		"custom-cdp": {EndpointURL: "ws://127.0.0.1:9222"},
	}
	require.NoError(t, SaveConfig(configPath, cfg))

	rawYAML, err := os.ReadFile(secPath)
	require.NoError(t, err)
	assert.NotContains(t, string(rawYAML), "browser:")
}

// TestBrowserBackendEnvSecretCollection verifies that env values stored under
// secret-looking keys are picked up by the sensitive-data replacer so they
// are masked in logs and tool output.
func TestBrowserBackendEnvSecretCollection(t *testing.T) {
	cfg := &Config{}
	cfg.Tools.Browser.Backends = BrowserBackendsConfig{
		"provider": {
			Env: map[string]string{
				"BROWSERUSE_API_KEY": "provider-secret-123",
				"KERNEL_STEALTH":     "true",
			},
		},
	}
	replacer := cfg.SensitiveDataReplacer()
	got := replacer.Replace("leaked: provider-secret-123 flag: true")
	assert.NotContains(t, got, "provider-secret-123")
	// Non-secret env values are not collected.
	assert.Contains(t, got, "flag: true")
}

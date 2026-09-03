package config

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDuration_MarshalUnmarshal(t *testing.T) {
	d := Duration(2*time.Minute + 30*time.Second)
	b, err := json.Marshal(d)
	require.NoError(t, err)
	assert.Equal(t, `"2m30s"`, string(b))

	var parsed Duration
	require.NoError(t, json.Unmarshal(b, &parsed))
	assert.Equal(t, d, parsed)
}

func TestDuration_UnmarshalNanoseconds(t *testing.T) {
	var d Duration
	require.NoError(t, json.Unmarshal([]byte("150000000000"), &d))
	assert.Equal(t, Duration(150*time.Second), d)
}

func TestDuration_UnmarshalInvalid(t *testing.T) {
	var d Duration
	assert.Error(t, json.Unmarshal([]byte(`"not-a-duration"`), &d))
	assert.Error(t, json.Unmarshal([]byte(`{}`), &d))
}

func TestDefaultTimeouts_Populated(t *testing.T) {
	cfg := DefaultConfig()
	assert.True(t, cfg.Timeouts.LLM.RequestSeconds > 0)
	assert.Equal(t, 120*time.Second, cfg.Timeouts.LLM.RequestSeconds.Duration())
	assert.Equal(t, 60*time.Second, cfg.Timeouts.Tools.ExecSeconds.Duration())
	assert.Equal(t, 5*time.Minute, cfg.Timeouts.Mesh.RemoteCall.Duration())
	assert.Equal(t, 3, cfg.Timeouts.Network.BootstrapAttempts)
}

func TestResolveLLMRequest_PerModelOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Timeouts.LLM.RequestSeconds = Duration(300 * time.Second)
	m := &ModelConfig{RequestTimeout: 45}
	assert.Equal(t, 45*time.Second, cfg.LLMRequestSeconds(m))

	m.RequestTimeout = 0
	assert.Equal(t, 300*time.Second, cfg.LLMRequestSeconds(m))
}

func TestResolveToolExecTimeout_PerComponentOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Timeouts.Tools.ExecSeconds = Duration(90 * time.Second)
	cfg.Tools.Exec.TimeoutSeconds = 0
	assert.Equal(t, 90*time.Second, cfg.ToolExecTimeout())

	cfg.Tools.Exec.TimeoutSeconds = 30
	assert.Equal(t, 30*time.Second, cfg.ToolExecTimeout())
}

func TestEnvOverride_Timeouts(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("RHIZOME_TIMEOUTS_LLM_REQUEST_SECONDS", "33s")
	t.Setenv("RHIZOME_TIMEOUTS_NETWORK_BOOTSTRAP_ATTEMPTS", "7")
	require.NoError(t, envParseForTest(cfg))
	assert.Equal(t, 33*time.Second, cfg.Timeouts.LLM.RequestSeconds.Duration())
	assert.Equal(t, 7, cfg.Timeouts.Network.BootstrapAttempts)
}

func envParseForTest(cfg *Config) error {
	return env.Parse(cfg)
}

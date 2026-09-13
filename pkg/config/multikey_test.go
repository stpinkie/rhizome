package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandMultiKeyModels_SingleKey(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "gpt-4",
			Model:     "openai/gpt-4o",
			APIKeys:   SimpleSecureStrings("single-key"),
		},
	}

	result := expandMultiKeyModels(models)

	if len(result) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result))
	}

	if result[0].ModelName != "gpt-4" {
		t.Errorf("expected model_name 'gpt-4', got %q", result[0].ModelName)
	}

	if result[0].APIKey() != "single-key" {
		t.Errorf("expected api_key 'single-key', got %q", result[0].APIKey())
	}

	if len(result[0].Fallbacks) != 0 {
		t.Errorf("expected no fallbacks, got %v", result[0].Fallbacks)
	}
}

func TestExpandMultiKeyModels_APIKeysOnly(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "glm-4.7",
			Model:     "zhipu/glm-4.7",
			APIBase:   "https://api.example.com",
			APIKeys:   SimpleSecureStrings("key1", "key2", "key3"),
		},
	}

	result := expandMultiKeyModels(models)

	// Should expand to 3 models
	if len(result) != 3 {
		t.Fatalf("expected 3 models, got %d", len(result))
	}

	// First entry should be the primary with key1 and fallbacks
	primary := result[2] // Primary is added last
	if primary.ModelName != "glm-4.7" {
		t.Errorf("expected primary model_name 'glm-4.7', got %q", primary.ModelName)
	}
	if primary.APIKey() != "key1" {
		t.Errorf("expected primary api_key 'key1', got %q", primary.APIKey())
	}
	if len(primary.Fallbacks) != 2 {
		t.Errorf("expected 2 fallbacks, got %d", len(primary.Fallbacks))
	}
	if primary.Fallbacks[0] != "glm-4.7__key_1" {
		t.Errorf("expected first fallback 'glm-4.7__key_1', got %q", primary.Fallbacks[0])
	}
	if primary.Fallbacks[1] != "glm-4.7__key_2" {
		t.Errorf("expected second fallback 'glm-4.7__key_2', got %q", primary.Fallbacks[1])
	}

	// Second entry should be key2
	second := result[0]
	if second.ModelName != "glm-4.7__key_1" {
		t.Errorf("expected second model_name 'glm-4.7__key_1', got %q", second.ModelName)
	}
	if second.APIKey() != "key2" {
		t.Errorf("expected second api_key 'key2', got %q", second.APIKey())
	}

	// Third entry should be key3
	third := result[1]
	if third.ModelName != "glm-4.7__key_2" {
		t.Errorf("expected third model_name 'glm-4.7__key_2', got %q", third.ModelName)
	}
	if third.APIKey() != "key3" {
		t.Errorf("expected third api_key 'key3', got %q", third.APIKey())
	}
}

func TestExpandMultiKeyModels_APIKeyAndAPIKeys(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "gpt-4",
			Model:     "openai/gpt-4o",
			APIKeys:   SimpleSecureStrings("key0", "key1", "key2"),
		},
	}

	result := expandMultiKeyModels(models)

	// Should expand to 3 models (key0 from APIKey + key1, key2 from APIKeys)
	if len(result) != 3 {
		t.Fatalf("expected 3 models, got %d", len(result))
	}

	// Primary should use key0
	primary := result[2]
	if primary.APIKey() != "key0" {
		t.Errorf("expected primary api_key 'key0', got %q", primary.APIKey())
	}
	if len(primary.Fallbacks) != 2 {
		t.Errorf("expected 2 fallbacks, got %d", len(primary.Fallbacks))
	}
}

func TestExpandMultiKeyModels_WithExistingFallbacks(t *testing.T) {
	modelCfg := &ModelConfig{
		ModelName: "gpt-4",
		Model:     "openai/gpt-4o",
	}
	modelCfg.APIKeys = SimpleSecureStrings("key0", "key1") // Use internal field for multi-key testing
	modelCfg.Fallbacks = []string{"claude-3"}
	models := []*ModelConfig{modelCfg}

	result := expandMultiKeyModels(models)

	primary := result[1]
	// With 2 keys, we get 1 key fallback + 1 existing fallback = 2 total
	if len(primary.Fallbacks) != 2 {
		t.Fatalf("expected 2 fallbacks, got %d: %v", len(primary.Fallbacks), primary.Fallbacks)
	}

	// Key fallbacks should come first, then existing fallbacks
	if primary.Fallbacks[0] != "gpt-4__key_1" {
		t.Errorf("expected first fallback 'gpt-4__key_1', got %q", primary.Fallbacks[0])
	}
	if primary.Fallbacks[1] != "claude-3" {
		t.Errorf("expected second fallback 'claude-3', got %q", primary.Fallbacks[1])
	}
}

func TestExpandMultiKeyModels_EmptyAPIKeys(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "gpt-4",
			Model:     "openai/gpt-4o",
			APIKeys:   SimpleSecureStrings(),
		},
	}

	result := expandMultiKeyModels(models)

	// Should keep as-is with no changes
	if len(result) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result))
	}

	if result[0].ModelName != "gpt-4" {
		t.Errorf("expected model_name 'gpt-4', got %q", result[0].ModelName)
	}
}

func TestExpandMultiKeyModels_Deduplication(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "gpt-4",
			Model:     "openai/gpt-4o",
			APIKeys:   SimpleSecureStrings("key1", "key2", "key1"), // Duplicate key1
		},
	}

	result := expandMultiKeyModels(models)

	t.Logf("result: %#v", result)
	// Should only create 2 models (deduplicated keys)
	if len(result) != 2 {
		t.Fatalf("expected 2 models (deduplicated), got %d", len(result))
	}

	primary := result[1]
	if primary.APIKey() != "key1" {
		t.Errorf("expected primary api_key 'key1', got %q", primary.APIKey())
	}
	if len(primary.Fallbacks) != 1 {
		t.Errorf("expected 1 fallback, got %d", len(primary.Fallbacks))
	}
}

func TestExpandMultiKeyModels_PreservesOtherFields(t *testing.T) {
	modelCfg := &ModelConfig{
		ModelName:           "gpt-4",
		Provider:            "openrouter",
		Model:               "openai/gpt-4o",
		APIBase:             "https://api.example.com",
		Proxy:               "http://proxy:8080",
		RPM:                 60,
		MaxTokensField:      "max_completion_tokens",
		RequestTimeout:      30,
		ThinkingLevel:       "high",
		ToolSchemaTransform: "simple",
		Streaming:           ModelStreamingConfig{Enabled: true},
	}
	modelCfg.APIKeys = SimpleSecureStrings("key0", "key1") // Use internal field for multi-key testing
	models := []*ModelConfig{modelCfg}

	result := expandMultiKeyModels(models)

	// Check primary entry preserves all fields
	primary := result[1]
	if primary.APIBase != "https://api.example.com" {
		t.Errorf("expected api_base preserved, got %q", primary.APIBase)
	}
	if primary.Provider != "openrouter" {
		t.Errorf("expected provider preserved, got %q", primary.Provider)
	}
	if primary.Proxy != "http://proxy:8080" {
		t.Errorf("expected proxy preserved, got %q", primary.Proxy)
	}
	if primary.RPM != 60 {
		t.Errorf("expected rpm preserved, got %d", primary.RPM)
	}
	if primary.MaxTokensField != "max_completion_tokens" {
		t.Errorf("expected max_tokens_field preserved, got %q", primary.MaxTokensField)
	}
	if primary.RequestTimeout != 30 {
		t.Errorf("expected request_timeout preserved, got %d", primary.RequestTimeout)
	}
	if primary.ThinkingLevel != "high" {
		t.Errorf("expected thinking_level preserved, got %q", primary.ThinkingLevel)
	}
	if primary.ToolSchemaTransform != "simple" {
		t.Errorf("expected tool_schema_transform preserved, got %q", primary.ToolSchemaTransform)
	}
	if !primary.Streaming.Enabled {
		t.Error("expected streaming config preserved on primary")
	}

	// Check additional entry also preserves fields
	additional := result[0]
	if additional.Provider != "openrouter" {
		t.Errorf("expected additional provider preserved, got %q", additional.Provider)
	}
	if additional.APIBase != "https://api.example.com" {
		t.Errorf("expected additional api_base preserved, got %q", additional.APIBase)
	}
	if additional.RPM != 60 {
		t.Errorf("expected additional rpm preserved, got %d", additional.RPM)
	}
	if additional.ToolSchemaTransform != "simple" {
		t.Errorf("expected additional tool_schema_transform preserved, got %q", additional.ToolSchemaTransform)
	}
	if !additional.Streaming.Enabled {
		t.Error("expected streaming config preserved on additional")
	}
}

func TestExpandMultiKeyModels_IsVirtualFlag(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "gpt-4",
			Model:     "openai/gpt-4o",
			APIKeys:   SimpleSecureStrings("key1", "key2", "key3"),
		},
	}

	result := expandMultiKeyModels(models)

	// Should expand to 3 models
	if len(result) != 3 {
		t.Fatalf("expected 3 models, got %d", len(result))
	}

	// Primary model should NOT be virtual
	primary := result[2]
	if primary.isVirtual {
		t.Errorf("primary model should not be virtual")
	}
	if primary.ModelName != "gpt-4" {
		t.Errorf("expected primary model_name 'gpt-4', got %q", primary.ModelName)
	}

	// Virtual models should have isVirtual = true
	virtual1 := result[0]
	if !virtual1.isVirtual {
		t.Errorf("gpt-4__key_1 should be virtual")
	}
	if virtual1.ModelName != "gpt-4__key_1" {
		t.Errorf("expected virtual model_name 'gpt-4__key_1', got %q", virtual1.ModelName)
	}

	virtual2 := result[1]
	if !virtual2.isVirtual {
		t.Errorf("gpt-4__key_2 should be virtual")
	}
	if virtual2.ModelName != "gpt-4__key_2" {
		t.Errorf("expected virtual model_name 'gpt-4__key_2', got %q", virtual2.ModelName)
	}

	// IsVirtual() method should work
	if !virtual1.IsVirtual() {
		t.Errorf("IsVirtual() should return true for virtual model")
	}
	if primary.IsVirtual() {
		t.Errorf("IsVirtual() should return false for primary model")
	}
}

func TestExpandMultiKeyModels_SingleKey_NotVirtual(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "gpt-4",
			Model:     "openai/gpt-4o",
			APIKeys:   SimpleSecureStrings("single-key"),
		},
	}

	result := expandMultiKeyModels(models)

	if len(result) != 1 {
		t.Fatalf("expected 1 model, got %d", len(result))
	}

	// Single key model should NOT be virtual
	if result[0].isVirtual {
		t.Errorf("single key model should not be virtual")
	}
}

func TestCollapseMultiKeyModels_RestoresPrimary(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "gpt-4",
			Model:     "openai/gpt-4o",
			APIKeys:   SimpleSecureStrings("key1", "key2", "key3"),
			Fallbacks: []string{"claude-3"},
		},
	}

	expanded := expandMultiKeyModels(models)
	require.Len(t, expanded, 3)

	collapsed := collapseMultiKeyModels(expanded)
	require.Len(t, collapsed, 1)
	assert.Equal(t, "gpt-4", collapsed[0].ModelName)
	assert.Equal(t, []string{"key1", "key2", "key3"}, collapsed[0].APIKeys.Values())
	// The user's own fallbacks survive; only "__key_i" names are stripped.
	assert.Equal(t, []string{"claude-3"}, collapsed[0].Fallbacks)

	// The in-memory expansion must be untouched — collapse produces copies.
	require.Len(t, expanded, 3)
	assert.Equal(t, []string{"key1"}, expanded[2].APIKeys.Values())
	assert.Len(t, expanded[2].Fallbacks, 3)
}

func TestCollapseMultiKeyModels_OrphanVirtualDemoted(t *testing.T) {
	// A virtual entry whose primary was removed must not lose its key —
	// it is demoted to a regular model instead.
	orphan := &ModelConfig{
		ModelName: "gone__key_1",
		Model:     "openai/gpt-4o",
		APIKeys:   SimpleSecureStrings("orphan-key"),
		isVirtual: true,
	}
	other := &ModelConfig{
		ModelName: "other",
		Model:     "openai/gpt-4o-mini",
		APIKeys:   SimpleSecureStrings("other-key"),
	}

	collapsed := collapseMultiKeyModels([]*ModelConfig{orphan, other})
	require.Len(t, collapsed, 2)
	assert.Equal(t, "gone__key_1", collapsed[0].ModelName)
	assert.Equal(t, "orphan-key", collapsed[0].APIKey())
	assert.False(t, collapsed[0].isVirtual)
}

func TestCollapseMultiKeyModels_NoExpansion(t *testing.T) {
	models := []*ModelConfig{
		{
			ModelName: "single",
			Model:     "openai/gpt-4o",
			APIKeys:   SimpleSecureStrings("only-key"),
		},
	}
	collapsed := collapseMultiKeyModels(models)
	require.Len(t, collapsed, 1)
	assert.Same(t, models[0], collapsed[0])
	assert.Equal(t, "only-key", collapsed[0].APIKey())
}

func TestSaveConfig_MultiKeyRoundTripPreservesAllKeys(t *testing.T) {
	// Regression test for upstream sipeed/picoclaw#3373: a model_list entry
	// with multiple api_keys lost every key after the first on save, and the
	// surviving entry kept dangling "__key_i" fallback references.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 3,
		"model_list": [
			{
				"model_name": "my-model",
				"provider": "openai",
				"model": "gpt-4o",
				"api_keys": ["sk-primary", "sk-secondary"]
			}
		]
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.NoError(t, SaveConfig(configPath, cfg))

	cfg2, err := LoadConfig(configPath)
	require.NoError(t, err)

	var keys []string
	for _, m := range cfg2.ModelList {
		if m.ModelName == "my-model" || strings.HasPrefix(m.ModelName, "my-model__key_") {
			keys = append(keys, m.APIKeys.Values()...)
		}
	}
	assert.ElementsMatch(t, []string{"sk-primary", "sk-secondary"}, keys)

	// The persisted config must not reference virtual "__key_i" entries.
	saved, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.NotContains(t, string(saved), "__key_")
}

func TestMergeAPIKeys(t *testing.T) {
	tests := []struct {
		name     string
		apiKey   string
		apiKeys  []string
		expected []string
	}{
		{
			name:     "both empty",
			apiKey:   "",
			apiKeys:  nil,
			expected: nil,
		},
		{
			name:     "only ApiKey",
			apiKey:   "key1",
			apiKeys:  nil,
			expected: []string{"key1"},
		},
		{
			name:     "only ApiKeys",
			apiKey:   "",
			apiKeys:  []string{"key1", "key2"},
			expected: []string{"key1", "key2"},
		},
		{
			name:     "both with overlap",
			apiKey:   "key1",
			apiKeys:  []string{"key1", "key2", "key3"},
			expected: []string{"key1", "key2", "key3"},
		},
		{
			name:     "with whitespace",
			apiKey:   "  key1  ",
			apiKeys:  []string{"  key2  ", "  key1  "},
			expected: []string{"key1", "key2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeAPIKeys(tt.apiKey, tt.apiKeys)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d keys, got %d", len(tt.expected), len(result))
			}
			for i, k := range result {
				if k != tt.expected[i] {
					t.Errorf("expected key[%d] = %q, got %q", i, tt.expected[i], k)
				}
			}
		})
	}
}

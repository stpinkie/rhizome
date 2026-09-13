package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
)

type setupStatusResponse struct {
	NeedsSetup       bool   `json:"needs_setup"`
	TotalModels      int    `json:"total_models"`
	ConfiguredModels int    `json:"configured_models"`
	DefaultModel     string `json:"default_model"`
}

func getSetupStatus(t *testing.T, configPath string) setupStatusResponse {
	t.Helper()

	h := NewHandler(configPath)
	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	rec := httptest.NewRecorder()
	h.handleSetupStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var status setupStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return status
}

func TestSetupStatus_NoModels(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.ModelList = nil
	cfg.Agents.Defaults.ModelName = ""
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	status := getSetupStatus(t, configPath)
	if !status.NeedsSetup {
		t.Fatal("expected needs_setup with no models")
	}
	if status.ConfiguredModels != 0 {
		t.Fatalf("expected 0 configured models, got %d", status.ConfiguredModels)
	}
}

func TestSetupStatus_ConfiguredModelWithDefault(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "gpt-4o",
		Provider:  "openai",
		Model:     "gpt-4o",
		APIKeys:   config.SimpleSecureStrings("sk-test"),
		Enabled:   true,
	}}
	cfg.Agents.Defaults.ModelName = "gpt-4o"
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	status := getSetupStatus(t, configPath)
	if status.NeedsSetup {
		t.Fatal("expected needs_setup=false with configured default model")
	}
	if status.ConfiguredModels != 1 {
		t.Fatalf("expected 1 configured model, got %d", status.ConfiguredModels)
	}
	if status.DefaultModel != "gpt-4o" {
		t.Fatalf("expected default_model gpt-4o, got %q", status.DefaultModel)
	}
}

func TestSetupStatus_ConfiguredModelWithoutDefault(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "gpt-4o",
		Provider:  "openai",
		Model:     "gpt-4o",
		APIKeys:   config.SimpleSecureStrings("sk-test"),
		Enabled:   true,
	}}
	cfg.Agents.Defaults.ModelName = ""
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	status := getSetupStatus(t, configPath)
	if !status.NeedsSetup {
		t.Fatal("expected needs_setup when no default model is set")
	}
	if status.ConfiguredModels != 1 {
		t.Fatalf("expected 1 configured model, got %d", status.ConfiguredModels)
	}
}

func TestSetupStatus_UnconfiguredModelOnly(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "gpt-4o",
		Provider:  "openai",
		Model:     "gpt-4o",
		Enabled:   true,
	}}
	cfg.Agents.Defaults.ModelName = "gpt-4o"
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	status := getSetupStatus(t, configPath)
	if !status.NeedsSetup {
		t.Fatal("expected needs_setup when the only model has no credentials")
	}
	if status.ConfiguredModels != 0 {
		t.Fatalf("expected 0 configured models, got %d", status.ConfiguredModels)
	}
}

package onboard

import (
	"bufio"
	"fmt"
	"strings"
	"testing"

	"github.com/stpinkie/rhizome/pkg/config"
)

// clearProviderEnv removes every detectable API-key env var so the pick list
// order is deterministic.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, pk := range providerEnvKeys {
		t.Setenv(pk.envVar, "")
	}
}

func wizardReader(script ...string) *bufio.Reader {
	return bufio.NewReader(strings.NewReader(strings.Join(script, "\n") + "\n"))
}

func pickIndex(t *testing.T, picks []wizardPick, match func(wizardPick) bool) int {
	t.Helper()
	for i, p := range picks {
		if match(p) {
			return i + 1
		}
	}
	t.Fatal("pick not found")
	return -1
}

func findModel(c *config.Config, name string) *config.ModelConfig {
	for _, m := range c.ModelList {
		if m != nil && m.ModelName == name {
			return m
		}
	}
	return nil
}

func TestRunModelWizard_Decline(t *testing.T) {
	clearProviderEnv(t)
	c := config.DefaultConfig()
	before := len(c.ModelList)
	if runModelWizard(c, wizardReader("n")) {
		t.Fatal("expected wizard to report false when declined")
	}
	if len(c.ModelList) != before {
		t.Fatalf("expected model list unchanged, got %d (was %d)", len(c.ModelList), before)
	}
}

func TestRunModelWizard_SkipProviderMenu(t *testing.T) {
	clearProviderEnv(t)
	c := config.DefaultConfig()
	before := len(c.ModelList)
	if runModelWizard(c, wizardReader("y", "0")) {
		t.Fatal("expected wizard to report false when provider menu skipped")
	}
	if len(c.ModelList) != before {
		t.Fatalf("expected model list unchanged, got %d (was %d)", len(c.ModelList), before)
	}
}

func TestRunModelWizard_CustomOpenAIEndpoint(t *testing.T) {
	clearProviderEnv(t)
	c := config.DefaultConfig()

	picks := detectProviderPicks()
	custom := pickIndex(t, picks, func(p wizardPick) bool { return p.custom })

	ok := runModelWizard(c, wizardReader(
		"y",
		fmt.Sprintf("%d", custom),
		"http://localhost:9/v1", // custom apiBase (unreachable -> saved anyway)
		"sk-test-key",
		"my-custom-model",
	))
	if !ok {
		t.Fatal("expected wizard to configure a model")
	}

	entry := findModel(c, "my-custom-model")
	if entry == nil {
		t.Fatal("configured model entry not found")
	}
	if entry.Provider != "openai-compatible" {
		t.Fatalf("custom endpoint must use the openai-compatible preset, got %q", entry.Provider)
	}
	if entry.APIBase != "http://localhost:9/v1" {
		t.Fatalf("api_base = %q", entry.APIBase)
	}
	if entry.Model != "my-custom-model" {
		t.Fatalf("model = %q", entry.Model)
	}
	if entry.APIKey() != "sk-test-key" {
		t.Fatalf("api key not stored: %q", entry.APIKey())
	}
	if !entry.Enabled {
		t.Fatal("entry should be enabled")
	}
	if c.Agents.Defaults.ModelName != entry.ModelName {
		t.Fatalf("default model = %q, want %q", c.Agents.Defaults.ModelName, entry.ModelName)
	}
}

func TestRunModelWizard_UsesDetectedEnvKey(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-env-detected")
	c := config.DefaultConfig()

	picks := detectProviderPicks()
	if len(picks) == 0 || picks[0].envVar != "DEEPSEEK_API_KEY" {
		t.Fatal("expected detected deepseek env pick first")
	}

	ok := runModelWizard(c, wizardReader(
		"y",
		"1", // the detected deepseek pick
		"",  // accept default API base
		"y", // use detected key
		"0", // enter model manually
		"deepseek-v3",
	))
	if !ok {
		t.Fatal("expected wizard to configure a model")
	}

	entry := findModel(c, "deepseek-v3")
	if entry == nil {
		t.Fatal("configured model entry not found")
	}
	if entry.Provider != "deepseek" {
		t.Fatalf("provider = %q", entry.Provider)
	}
	if entry.APIKey() != "sk-env-detected" {
		t.Fatalf("expected detected env key, got %q", entry.APIKey())
	}
	if entry.Model != "deepseek-v3" {
		t.Fatalf("model = %q", entry.Model)
	}
}

func TestRunModelWizard_ReplacesExistingAlias(t *testing.T) {
	clearProviderEnv(t)
	c := config.DefaultConfig()
	c.ModelList = append(c.ModelList, &config.ModelConfig{
		ModelName: "my-custom-model",
		Provider:  "openai",
		Model:     "old",
		Enabled:   false,
	})

	picks := detectProviderPicks()
	custom := pickIndex(t, picks, func(p wizardPick) bool { return p.custom })

	before := len(c.ModelList)
	if !runModelWizard(c, wizardReader(
		"y",
		fmt.Sprintf("%d", custom),
		"http://localhost:9/v1",
		"sk-new",
		"my-custom-model",
	)) {
		t.Fatal("expected wizard to configure a model")
	}

	if len(c.ModelList) != before {
		t.Fatalf("expected existing alias replaced, got %d entries (was %d)", len(c.ModelList), before)
	}
	entry := findModel(c, "my-custom-model")
	if entry == nil || entry.APIKey() != "sk-new" {
		t.Fatal("expected replaced entry to carry the new key")
	}
}

func TestDetectProviderPicks_CustomIsLast(t *testing.T) {
	clearProviderEnv(t)
	picks := detectProviderPicks()
	if len(picks) == 0 {
		t.Fatal("expected non-empty pick list")
	}
	last := picks[len(picks)-1]
	if !last.custom || last.provider != "openai-compatible" {
		t.Fatalf("last pick should be the custom OpenAI-compatible entry, got %+v", last)
	}
}

func TestDefaultModelAlias(t *testing.T) {
	if got := defaultModelAlias("openrouter", "openai/gpt-5.4"); got != "gpt-5.4" {
		t.Fatalf("alias = %q", got)
	}
	if got := defaultModelAlias("openai", "gpt-5.4"); got != "gpt-5.4" {
		t.Fatalf("alias = %q", got)
	}
}

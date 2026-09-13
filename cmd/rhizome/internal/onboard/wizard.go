package onboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/providers"
)

// providerEnvKey maps a catalog provider ID to the conventional upstream
// environment variable that may already hold a usable API key.
type providerEnvKey struct {
	provider string
	envVar   string
}

var providerEnvKeys = []providerEnvKey{
	{"openai", "OPENAI_API_KEY"},
	{"anthropic", "ANTHROPIC_API_KEY"},
	{"openrouter", "OPENROUTER_API_KEY"},
	{"gemini", "GEMINI_API_KEY"},
	{"gemini", "GOOGLE_API_KEY"},
	{"deepseek", "DEEPSEEK_API_KEY"},
	{"groq", "GROQ_API_KEY"},
	{"mistral", "MISTRAL_API_KEY"},
	{"moonshot", "MOONSHOT_API_KEY"},
	{"xai", "XAI_API_KEY"},
	{"cerebras", "CEREBRAS_API_KEY"},
	{"modelscope", "MODELSCOPE_API_KEY"},
	{"novita", "NOVITA_API_KEY"},
	{"zai", "ZAI_API_KEY"},
	{"zhipu", "ZHIPUAI_API_KEY"},
	{"siliconflow", "SILICONFLOW_API_KEY"},
	{"nvidia", "NVIDIA_API_KEY"},
	{"venice", "VENICE_API_KEY"},
}

// wizardPick is a selectable provider entry in the wizard menu.
type wizardPick struct {
	provider string
	label    string
	envVar   string // non-empty when an API key was detected in the environment
	apiBase  string // non-empty when the base URL must be prompted for
	custom   bool   // custom OpenAI-compatible endpoint: provider is "openai-compatible"
}

// runModelWizard guides the user through configuring a model provider.
// It mutates c and reports whether a model entry was configured.
func runModelWizard(c *config.Config, reader *bufio.Reader) bool {
	fmt.Println()
	fmt.Println("Set up a model provider now? (y/n)")
	if !promptYesNo(reader, true) {
		fmt.Println("Skipping provider setup — add one later in config.json (model_list).")
		return false
	}

	picks := detectProviderPicks()
	pick := promptProviderChoice(reader, picks)
	if pick == nil {
		return false
	}

	opt := findProviderOption(pick.provider)

	apiBase := pick.apiBase
	if apiBase == "" {
		apiBase = opt.DefaultAPIBase
	}
	if pick.custom || apiBase == "" {
		apiBase = promptRequired(reader, "API base URL (e.g. https://host/v1): ", "")
	} else {
		apiBase = promptRequired(reader,
			fmt.Sprintf("API base URL [%s]: ", apiBase), apiBase)
	}

	apiKey := ""
	// Custom endpoints may still need a key even though the preset allows
	// empty (unauthenticated local servers), so always offer the prompt.
	if !opt.EmptyAPIKeyAllowed || pick.envVar != "" || pick.custom {
		apiKey = promptAPIKey(reader, pick, opt)
		if apiKey == "" && !opt.EmptyAPIKeyAllowed {
			fmt.Println("No API key provided — skipping provider setup.")
			return false
		}
	}

	model := promptModelChoice(reader, pick, opt, apiBase, apiKey)
	if model == "" {
		fmt.Println("No model selected — skipping provider setup.")
		return false
	}

	modelName := defaultModelAlias(pick.provider, model)
	entry := &config.ModelConfig{
		ModelName: modelName,
		Provider:  pick.provider,
		Model:     model,
		APIBase:   apiBase,
		Enabled:   true,
	}
	if apiKey != "" {
		entry.APIKeys = config.SimpleSecureStrings(apiKey)
	}

	checkProviderConnectivity(apiBase, apiKey, opt.SupportsFetch)

	// Replace an existing entry with the same alias, else append.
	replaced := false
	for i, m := range c.ModelList {
		if m != nil && m.ModelName == modelName {
			c.ModelList[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		c.ModelList = append(c.ModelList, entry)
	}
	c.Agents.Defaults.ModelName = modelName

	fmt.Printf("\nConfigured %s (%s) as the default model.\n", modelName, model)
	return true
}

// detectProviderPicks builds the menu: providers with a detected API key env
// var first, then a reachable local Ollama, then the common providers.
func detectProviderPicks() []wizardPick {
	var picks []wizardPick
	seen := make(map[string]bool)

	for _, pk := range providerEnvKeys {
		if seen[pk.provider] || os.Getenv(pk.envVar) == "" {
			continue
		}
		seen[pk.provider] = true
		opt := findProviderOption(pk.provider)
		name := opt.DisplayName
		if name == "" {
			name = pk.provider
		}
		picks = append(picks, wizardPick{
			provider: pk.provider,
			label:    fmt.Sprintf("%s (detected $%s)", name, pk.envVar),
			envVar:   pk.envVar,
		})
	}

	if detectOllama() {
		seen["ollama"] = true
		picks = append(picks, wizardPick{
			provider: "ollama",
			label:    "Ollama (local server detected)",
		})
	}

	for _, id := range []string{
		"openrouter", "openai", "anthropic", "gemini", "deepseek",
		"groq", "mistral", "zai", "ollama", "vllm",
	} {
		if seen[id] {
			continue
		}
		opt := findProviderOption(id)
		label := opt.DisplayName
		if label == "" {
			label = id
		}
		picks = append(picks, wizardPick{provider: id, label: label})
	}
	picks = append(picks, wizardPick{
		provider: "openai-compatible",
		label:    "Other OpenAI-compatible endpoint (custom base URL)",
		custom:   true,
	})
	return picks
}

func promptProviderChoice(reader *bufio.Reader, picks []wizardPick) *wizardPick {
	fmt.Println("\nChoose a provider:")
	for i, p := range picks {
		fmt.Printf("  %2d) %s\n", i+1, p.label)
	}
	fmt.Printf("   0) Skip\n")

	for {
		fmt.Print("Select [0]: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" || line == "0" {
			return nil
		}
		var n int
		if _, err := fmt.Sscanf(line, "%d", &n); err == nil && n >= 1 && n <= len(picks) {
			return &picks[n-1]
		}
		fmt.Printf("Enter a number between 0 and %d.\n", len(picks))
	}
}

func promptAPIKey(reader *bufio.Reader, pick *wizardPick, opt providers.ModelProviderOption) string {
	if pick.envVar != "" {
		fmt.Printf("Detected $%s.\n", pick.envVar)
		fmt.Print("Use it for this provider? (y/n) [y]: ")
		line, _ := reader.ReadString('\n')
		if strings.ToLower(strings.TrimSpace(line)) != "n" {
			return os.Getenv(pick.envVar)
		}
	}
	fmt.Print("API key: ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

func promptModelChoice(
	reader *bufio.Reader,
	pick *wizardPick,
	opt providers.ModelProviderOption,
	apiBase, apiKey string,
) string {
	providerID := pick.provider
	models := opt.CommonModels
	if pick.custom {
		models = nil
	}
	if providerID == "ollama" || providerID == "vllm" {
		if fetched := fetchLocalModels(apiBase); len(fetched) > 0 {
			models = fetched
		}
	}

	if len(models) > 0 {
		fmt.Println("\nChoose a model:")
		for i, m := range models {
			fmt.Printf("  %2d) %s\n", i+1, m)
		}
		fmt.Println("   0) Enter manually")
		for {
			fmt.Printf("Select [1]: ")
			line, err := reader.ReadString('\n')
			if err != nil {
				return ""
			}
			line = strings.TrimSpace(line)
			if line == "" {
				return models[0]
			}
			var n int
			if _, err := fmt.Sscanf(line, "%d", &n); err == nil {
				if n == 0 {
					break
				}
				if n >= 1 && n <= len(models) {
					return models[n-1]
				}
			}
			fmt.Printf("Enter a number between 0 and %d.\n", len(models))
		}
	}
	return promptRequired(reader, "Model ID (e.g. gpt-5.4): ", "")
}

// checkProviderConnectivity performs a best-effort GET /models probe for
// fetchable providers and reports the result without blocking onboarding.
func checkProviderConnectivity(apiBase, apiKey string, fetchable bool) {
	if apiBase == "" || !fetchable {
		return
	}
	fmt.Print("Checking connectivity... ")
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(apiBase, "/")+"/models", nil)
	if err != nil {
		fmt.Println("skipped")
		return
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("failed (%v) — the entry was saved anyway; verify the base URL.\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Printf("endpoint returned HTTP %d — the entry was saved anyway.\n", resp.StatusCode)
		return
	}
	fmt.Println("ok")
}

// detectOllama returns true when an Ollama server answers /api/tags.
func detectOllama() bool {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}
	if !strings.HasPrefix(host, "http") {
		host = "http://" + host
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(strings.TrimSuffix(host, "/") + "/api/tags")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// fetchLocalModels lists model names from a local server's OpenAI-compatible
// /models endpoint (works for Ollama's /v1 and vLLM).
func fetchLocalModels(apiBase string) []string {
	base := strings.TrimSuffix(apiBase, "/")
	url := base + "/models"
	if strings.HasSuffix(base, "/v1") {
		// Ollama's native listing carries the authoritative names.
		url = strings.TrimSuffix(base, "/v1") + "/api/tags"
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return nil
	}
	var out []string
	for _, m := range listing.Data {
		out = append(out, m.ID)
	}
	for _, m := range listing.Models {
		out = append(out, m.Name)
	}
	return out
}

func defaultModelAlias(providerID, model string) string {
	short := model
	if i := strings.LastIndex(short, "/"); i >= 0 {
		short = short[i+1:]
	}
	return short
}

func promptYesNo(reader *bufio.Reader, defaultYes bool) bool {
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

func promptRequired(reader *bufio.Reader, prompt, fallback string) string {
	for {
		fmt.Print(prompt)
		line, err := reader.ReadString('\n')
		if err != nil {
			return fallback
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if fallback != "" {
				return fallback
			}
			fmt.Println("Value cannot be empty.")
			continue
		}
		return line
	}
}

func findProviderOption(id string) providers.ModelProviderOption {
	for _, o := range providers.ModelProviderOptions() {
		if o.ID == id {
			return o
		}
	}
	return providers.ModelProviderOption{ID: id}
}

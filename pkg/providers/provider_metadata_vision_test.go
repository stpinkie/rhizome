package providers

import "testing"

func TestModelSupportsVision(t *testing.T) {
	vision := []string{
		"gpt-4o", "openai/gpt-5.4", "gpt-4-vision-preview",
		"anthropic/claude-opus-4.7", "claude-3-haiku-20240307",
		"gemini-3.1-pro-preview", "google/gemini-3-flash-preview",
		"qwen/qwen3-vl-plus", "qwen-vl-max", "qwen2.5-vl-72b",
		"glm-4v", "pixtral-large-latest", "llava-13b",
		"meta-llama/llama-3.2-11b-vision-instruct", "llama-4-maverick",
		"grok-4", "minicpm-v-2_6", "internvl2-8b",
	}
	for _, m := range vision {
		if !ModelSupportsVision(m) {
			t.Errorf("expected %q to be vision-capable", m)
		}
	}

	textOnly := []string{
		"", "gpt-3.5-turbo", "deepseek-v4-flash", "deepseek-v4-pro",
		"llama-3.2-3b-instruct", "llama-3.1-70b",
		"whisper-1", "text-embedding-3-large", "dall-e-3",
		"tts-1-hd", "bge-large-en", "text-moderation-latest",
		"kimi-k2-thinking", "glm-4.7", "mistral-large-latest",
	}
	for _, m := range textOnly {
		if ModelSupportsVision(m) {
			t.Errorf("expected %q to be text-only", m)
		}
	}
}

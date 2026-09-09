package fstools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/stpinkie/rhizome/pkg/audio/asr"
	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/utils"
)

// TranscribeAudioTool transcribes a local audio file with the configured
// voice transcriber (voice.model or an auto-detected ASR-capable model).
type TranscribeAudioTool struct {
	workspace   string
	restrict    bool
	maxFileSize int
	allowPaths  []*regexp.Regexp

	transcriber asr.Transcriber
}

func NewTranscribeAudioTool(
	workspace string,
	restrict bool,
	maxFileSize int,
	allowPaths ...[]*regexp.Regexp,
) *TranscribeAudioTool {
	if maxFileSize <= 0 {
		maxFileSize = config.DefaultMaxMediaSize
	}
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &TranscribeAudioTool{
		workspace:   workspace,
		restrict:    restrict,
		maxFileSize: maxFileSize,
		allowPaths:  patterns,
	}
}

// SetTranscriber injects the voice transcriber detected at startup.
func (t *TranscribeAudioTool) SetTranscriber(tr asr.Transcriber) {
	t.transcriber = tr
}

func (t *TranscribeAudioTool) Name() string { return "transcribe_audio" }

func (t *TranscribeAudioTool) Description() string {
	return "Transcribe a local audio file to text using the configured speech " +
		"transcription provider. Use for voice memos, call recordings, or audio " +
		"attachments saved to the workspace."
}

func (t *TranscribeAudioTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the local audio file. Relative paths are resolved from workspace.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *TranscribeAudioTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return ErrorResult("path is required")
	}
	if t.transcriber == nil {
		return ErrorResult(
			"no transcription provider configured (set voice.model or a model_list ASR model)")
	}

	resolved, err := validatePathWithAllowPaths(path, t.workspace, t.restrict, t.allowPaths)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid path: %v", err))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return ErrorResult(fmt.Sprintf("file not found: %v", err))
	}
	if info.IsDir() {
		return ErrorResult("path is a directory, expected an audio file")
	}
	if info.Size() > int64(t.maxFileSize) {
		return ErrorResult(fmt.Sprintf(
			"file too large: %d bytes (max %d bytes)", info.Size(), t.maxFileSize,
		))
	}

	mediaType := detectMediaType(resolved)
	if !utils.IsAudioFile(resolved, mediaType) {
		return ErrorResult(fmt.Sprintf(
			"file does not appear to be audio (detected type: %s)", mediaType,
		))
	}

	resp, err := t.transcriber.Transcribe(ctx, resolved)
	if err != nil {
		return ErrorResult(fmt.Sprintf("transcription failed: %v", err))
	}
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return ErrorResult("no speech detected in the audio")
	}
	return &ToolResult{
		ForLLM:  fmt.Sprintf("Transcription of %s:\n%s", resolved, text),
		ForUser: fmt.Sprintf("Transcription:\n%s", text),
	}
}

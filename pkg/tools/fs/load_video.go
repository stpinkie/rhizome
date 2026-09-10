package fstools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stpinkie/rhizome/pkg/config"
	"github.com/stpinkie/rhizome/pkg/media"
)

// LoadVideoTool extracts keyframes from a local video file via ffmpeg and
// returns them as media:// refs so a vision-capable model can analyze the
// content — the video analog of LoadImageTool.
type LoadVideoTool struct {
	workspace   string
	restrict    bool
	maxFileSize int
	maxFrames   int
	ffmpegPath  string
	mediaStore  media.MediaStore
	allowPaths  []*regexp.Regexp

	// extractFrames is the frame extraction backend. It is wired to
	// media.ExtractVideoFrames by default, but can be swapped in tests or
	// replaced with an out-of-tree video processor.
	extractFrames func(context.Context, string, string, string, int) ([]string, error)

	defaultChannel string
	defaultChatID  string
}

func NewLoadVideoTool(
	workspace string,
	restrict bool,
	maxFileSize, maxFrames int,
	ffmpegPath string,
	store media.MediaStore,
	allowPaths ...[]*regexp.Regexp,
) *LoadVideoTool {
	if maxFileSize <= 0 {
		maxFileSize = config.DefaultMaxMediaSize
	}
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &LoadVideoTool{
		workspace:     workspace,
		restrict:      restrict,
		maxFileSize:   maxFileSize,
		maxFrames:     maxFrames,
		ffmpegPath:    ffmpegPath,
		mediaStore:    store,
		allowPaths:    patterns,
		extractFrames: media.ExtractVideoFrames,
	}
}

func (t *LoadVideoTool) Name() string { return "load_video" }

func (t *LoadVideoTool) Description() string {
	return "Extract keyframes from a local video file so you can analyze its " +
		"contents with vision. Requires ffmpeg installed on this node. " +
		"After calling this tool, describe or analyze the video in your next response."
}

func (t *LoadVideoTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the local video file. Relative paths are resolved from workspace.",
			},
			"frames": map[string]any{
				"type":        "integer",
				"description": "Maximum number of keyframes to extract (capped by tools.media.max_video_frames).",
			},
		},
		"required": []string{"path"},
	}
}

func (t *LoadVideoTool) SetContext(channel, chatID string) {
	t.defaultChannel = channel
	t.defaultChatID = chatID
}

func (t *LoadVideoTool) SetMediaStore(store media.MediaStore) {
	t.mediaStore = store
}

func (t *LoadVideoTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, _ := args["path"].(string)
	if strings.TrimSpace(path) == "" {
		return ErrorResult("path is required")
	}

	channel := ToolChannel(ctx)
	if channel == "" {
		channel = t.defaultChannel
	}
	chatID := ToolChatID(ctx)
	if chatID == "" {
		chatID = t.defaultChatID
	}
	if channel == "" || chatID == "" {
		return ErrorResult("no target channel/chat available")
	}

	if t.mediaStore == nil {
		return ErrorResult("media store not configured")
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
		return ErrorResult("path is a directory, expected a video file")
	}
	if info.Size() > int64(t.maxFileSize) {
		return ErrorResult(fmt.Sprintf(
			"file too large: %d bytes (max %d bytes)", info.Size(), t.maxFileSize,
		))
	}

	mediaType := detectMediaType(resolved)
	if !strings.HasPrefix(mediaType, "video/") {
		return ErrorResult(fmt.Sprintf(
			"file does not appear to be a video (detected type: %s)", mediaType,
		))
	}

	maxFrames := t.maxFrames
	if n, ok := args["frames"].(float64); ok && n > 0 {
		if maxFrames <= 0 || int(n) < maxFrames {
			maxFrames = int(n)
		}
	}
	if maxFrames <= 0 {
		maxFrames = 8
	}

	if err := os.MkdirAll(media.TempDir(), 0o700); err != nil {
		return ErrorResult(fmt.Sprintf("create media temp dir: %v", err))
	}
	tmpDir, err := os.MkdirTemp(media.TempDir(), "rhizome-video-frames-*")
	if err != nil {
		return ErrorResult(fmt.Sprintf("create temp dir: %v", err))
	}
	// Frames are registered with the media store under CleanupPolicyDeleteOnCleanup.
	// The store deletes each frame file when the scope is released and also removes
	// the now-empty per-call subdirectory, so no temp artifacts leak.
	frames, err := t.extractFrames(ctx, t.ffmpegPath, resolved, tmpDir, maxFrames)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return ErrorResult(fmt.Sprintf("extract frames: %v", err))
	}
	if len(frames) == 0 {
		_ = os.RemoveAll(tmpDir)
		return ErrorResult("no frames extracted from video")
	}

	filename := filepath.Base(resolved)
	scope := fmt.Sprintf("tool:load_video:%s:%s", channel, chatID)
	refs := make([]string, 0, len(frames))
	stored := make(map[string]struct{}, len(frames))
	for i, frame := range frames {
		ref, err := t.mediaStore.Store(frame, media.MediaMeta{
			Filename:      fmt.Sprintf("%s.frame%02d.jpg", filename, i+1),
			ContentType:   "image/jpeg",
			Source:        "tool:load_video",
			CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
		}, scope)
		if err != nil {
			// Do not leave unregistered frame files in the temp directory.
			_ = os.Remove(frame)
			continue
		}
		refs = append(refs, ref)
		stored[frame] = struct{}{}
	}

	// If every store failed, the temp directory has no registered files and
	// the media store will never clean it up. Remove it to avoid a leak.
	if len(refs) == 0 {
		_ = os.RemoveAll(tmpDir)
		return ErrorResult("failed to register extracted frames in media store")
	}

	// Remove any unregistered leftovers so they do not outlive the call.
	for _, frame := range frames {
		if _, ok := stored[frame]; !ok {
			_ = os.Remove(frame)
		}
	}

	// If the extractor did not actually place frames in tmpDir, the directory
	// is now empty and should be removed. When frames are registered there,
	// the media store will clean the files and the directory on scope release.
	_ = os.Remove(tmpDir)

	return &ToolResult{
		ForLLM: fmt.Sprintf(
			"Extracted %d keyframe(s) from video %s — the frames are attached as images below.\n[video: %s]",
			len(refs), filename, resolved,
		),
		ForUser: fmt.Sprintf("Extracted %d keyframes from %s", len(refs), filename),
		Media:   refs,
	}
}

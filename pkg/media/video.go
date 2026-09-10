package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FFmpegAvailable reports whether an ffmpeg binary is resolvable.
// ffmpegPath overrides the lookup name; empty means "ffmpeg" on PATH.
func FFmpegAvailable(ffmpegPath string) bool {
	bin := strings.TrimSpace(ffmpegPath)
	if bin == "" {
		bin = "ffmpeg"
	}
	if filepath.IsAbs(bin) || strings.ContainsAny(bin, `/\`) {
		st, err := os.Stat(bin)
		return err == nil && !st.IsDir()
	}
	_, err := exec.LookPath(bin)
	return err == nil
}

// ffprobePath returns the ffprobe binary path corresponding to the given
// ffmpeg path. If ffmpegPath is empty or just "ffmpeg", ffprobe is resolved
// from PATH. If ffmpegPath is an absolute path or contains a separator,
// the ffprobe binary is assumed to live alongside it (same directory,
// "ffprobe" basename); if that does not exist, "ffprobe" on PATH is tried.
func ffprobePath(ffmpegPath string) string {
	bin := strings.TrimSpace(ffmpegPath)
	if bin == "" || bin == "ffmpeg" {
		return "ffprobe"
	}
	if filepath.IsAbs(bin) || strings.ContainsAny(bin, `/\`) {
		dir := filepath.Dir(bin)
		candidate := filepath.Join(dir, "ffprobe")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		return "ffprobe"
	}
	return "ffprobe"
}

// probeDuration returns the video duration, preferring ffprobe and falling
// back to ffmpeg -i stderr parsing. Returns 0 if the duration cannot be
// determined.
func probeDuration(ctx context.Context, ffmpegPath, videoPath string) time.Duration {
	// Try ffprobe first — it is the canonical tool for this.
	probe := ffprobePath(ffmpegPath)
	if _, err := exec.LookPath(probe); err == nil {
		//nolint:gosec // G204: videoPath is a local file path, not untrusted input.
		cmd := exec.CommandContext(ctx, probe,
			"-v", "error",
			"-show_entries", "format=duration",
			"-of", "default=noprint_wrappers=1:nokey=1",
			videoPath,
		)
		out, err := cmd.Output()
		if err == nil {
			secs, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
			if perr == nil && secs > 0 {
				return time.Duration(secs * float64(time.Second))
			}
		}
	}

	// Fall back to ffmpeg -i stderr parsing ("Duration: 00:01:23.45").
	bin := strings.TrimSpace(ffmpegPath)
	if bin == "" {
		bin = "ffmpeg"
	}
	//nolint:gosec // G204: videoPath is a local file path, not untrusted input.
	cmd := exec.CommandContext(ctx, bin, "-i", videoPath)
	_, stderr, err := runWithSeparateOutput(cmd)
	if err != nil && stderr != "" {
		if d := parseFFmpegDuration(stderr); d > 0 {
			return d
		}
	}
	return 0
}

var ffmpegDurationRe = regexp.MustCompile(`Duration:\s*(\d+):(\d{2}):(\d{2}(?:\.\d+)?)`)

// parseFFmpegDuration extracts the duration from ffmpeg -i stderr output.
func parseFFmpegDuration(stderr string) time.Duration {
	m := ffmpegDurationRe.FindStringSubmatch(stderr)
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	mn, _ := strconv.Atoi(m[2])
	secs, _ := strconv.ParseFloat(m[3], 64)
	total := float64(h)*3600 + float64(mn)*60 + secs
	if total <= 0 {
		return 0
	}
	return time.Duration(total * float64(time.Second))
}

// runWithSeparateOutput runs a command and returns stdout and stderr as
// strings. ffmpeg -i without output args exits non-zero, so we ignore the
// error and only care about stderr.
func runWithSeparateOutput(cmd *exec.Cmd) (string, string, error) {
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// ExtractVideoFrames extracts up to maxFrames keyframes from a video file
// into outDir using ffmpeg, returning the produced frame paths (sorted).
// It prefers scene-representative keyframes (I-frames); if none are found
// it falls back to evenly-spaced frames. ffmpeg must be installed — it is
// an optional external dependency, like agent-browser.
func ExtractVideoFrames(
	ctx context.Context,
	ffmpegPath, videoPath, outDir string,
	maxFrames int,
) ([]string, error) {
	bin := strings.TrimSpace(ffmpegPath)
	if bin == "" {
		bin = "ffmpeg"
	}
	if !FFmpegAvailable(bin) {
		return nil, fmt.Errorf(
			"ffmpeg not found; install ffmpeg to analyze video files")
	}
	if maxFrames <= 0 {
		maxFrames = 8
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, fmt.Errorf("create frame dir: %w", err)
	}

	// Prefer keyframes (I-frames) — they are scene-representative.
	frames, err := runFFmpegFrames(ctx, bin, videoPath, outDir, []string{
		"-vf", "select='eq(pict_type,I)'", "-vsync", "vfr",
	}, maxFrames)
	if err != nil {
		return nil, err
	}
	if len(frames) > 0 {
		return frames, nil
	}

	// No keyframes (unusual codec) — fall back to evenly spaced frames.
	// Probe the actual duration so the fps filter spreads frames across
	// the whole video, not just the first 60 seconds.
	fpsFilter := "fps=1"
	if dur := probeDuration(ctx, ffmpegPath, videoPath); dur > 0 {
		secs := dur.Seconds()
		if secs > 0 && maxFrames > 0 {
			fpsFilter = fmt.Sprintf("fps=%.4f", float64(maxFrames)/secs)
		}
	}
	return runFFmpegFrames(ctx, bin, videoPath, outDir, []string{
		"-vf", fpsFilter,
	}, maxFrames)
}

func runFFmpegFrames(
	ctx context.Context,
	bin, videoPath, outDir string,
	filters []string,
	maxFrames int,
) ([]string, error) {
	outPattern := filepath.Join(outDir, "frame-%03d.jpg")
	args := make([]string, 0, 12+len(filters))
	args = append(args, "-hide_banner", "-loglevel", "error", "-i", videoPath)
	args = append(args, filters...)
	args = append(args, "-frames:v", fmt.Sprintf("%d", maxFrames), "-q:v", "3", "-y", outPattern)
	//nolint:gosec // G204: videoPath is a local file path; bin is looked-up ffmpeg.
	cmd := exec.CommandContext(ctx, bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	entries, err := filepath.Glob(filepath.Join(outDir, "frame-*.jpg"))
	if err != nil {
		return nil, err
	}
	return entries, nil
}

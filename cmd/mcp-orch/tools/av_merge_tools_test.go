package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAVMergeRejectsAbsoluteOutputOutsideWorkspace(t *testing.T) {
	t.Setenv("FFMPEG_PATH", fakeFFmpeg(t))

	outside := filepath.Join(t.TempDir(), "outside.mp4")
	input := map[string]string{
		"video_path":  "/tmp/legacy-video.mp4",
		"audio_path":  "/tmp/legacy-audio.mp3",
		"video_ref":   "media/video.mp4",
		"audio_ref":   "media/audio.mp3",
		"output_path": outside,
	}
	_, err := handleAVMerge()(context.Background(), mustJSON(t, input))
	if err == nil {
		t.Fatalf("handleAVMerge() accepted absolute output_path %q; want rejection", outside)
	}
	if !strings.Contains(err.Error(), "output_path") {
		t.Fatalf("handleAVMerge() error = %v, want output_path policy rejection", err)
	}
}

func TestAVMergeRequiresSharedFileOrWorkspaceRelativeInputs(t *testing.T) {
	t.Setenv("FFMPEG_PATH", fakeFFmpeg(t))

	input := map[string]string{
		"video_path":  "/tmp/legacy-video.mp4",
		"audio_path":  "/tmp/legacy-audio.mp3",
		"video_ref":   "/tmp/uncontrolled-video.mp4",
		"audio_ref":   "/tmp/uncontrolled-audio.mp3",
		"output_path": "reports/media/merged.mp4",
	}
	_, err := handleAVMerge()(context.Background(), mustJSON(t, input))
	if err == nil {
		t.Fatal("handleAVMerge() accepted absolute media input refs; want rejection")
	}
	if !strings.Contains(err.Error(), "video_ref") && !strings.Contains(err.Error(), "audio_ref") {
		t.Fatalf("handleAVMerge() error = %v, want media ref policy rejection", err)
	}
}

func fakeFFmpeg(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test input: %v", err)
	}
	return raw
}

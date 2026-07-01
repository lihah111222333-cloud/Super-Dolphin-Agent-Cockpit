package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoWithAudioUsesControlledOutputPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := mediaOutputPath("merged", "mp4")
	if err != nil {
		return
	}
	if filepath.IsAbs(got) || strings.Contains(got, home) {
		t.Fatalf("mediaOutputPath() = %q, want controlled workspace/sharedfile output instead of home fallback", got)
	}
}

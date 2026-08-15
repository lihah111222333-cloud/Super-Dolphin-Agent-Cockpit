//go:build linux

package tools

import "testing"

func TestNormalizePlatformWorkDirConvertsWindowsAbsolutePathForWSL(t *testing.T) {
	got := normalizePlatformWorkDir(`C:\Users\ai06\Desktop\Super-Dolphin`)
	want := "/mnt/c/Users/ai06/Desktop/Super-Dolphin"
	if got != want {
		t.Fatalf("normalizePlatformWorkDir() = %q, want %q", got, want)
	}
}

func TestNormalizePlatformWorkDirPreservesLinuxAndRelativePaths(t *testing.T) {
	for _, path := range []string{"/workspace/project", "relative/project", `not:a\path`} {
		if got := normalizePlatformWorkDir(path); got != path {
			t.Fatalf("normalizePlatformWorkDir(%q) = %q, want unchanged", path, got)
		}
	}
}

package installer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledBinaryCandidatesFindsDotnetGlobalToolOutsidePATH(t *testing.T) {
	dotnetHome := filepath.Join(t.TempDir(), "dotnet-cli")
	toolsDir := filepath.Join(dotnetHome, ".dotnet", "tools")
	if err := os.MkdirAll(toolsDir, 0o700); err != nil {
		t.Fatalf("create dotnet tools directory: %v", err)
	}
	launcher := filepath.Join(toolsDir, "csharp-ls")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write csharp-ls launcher: %v", err)
	}
	t.Setenv("DOTNET_CLI_HOME", dotnetHome)
	t.Setenv("PATH", t.TempDir())

	candidates, _ := installedBinaryCandidates(context.Background(), InstallerConfig{
		BinaryName: "csharp-ls",
		InstallCmd: "dotnet",
	})
	for _, candidate := range candidates {
		if filepath.Clean(candidate.path) == filepath.Clean(launcher) {
			return
		}
	}
	t.Fatalf("dotnet global launcher %s not found in candidates: %#v", launcher, candidates)
}

func TestDotnetGlobalToolBinDirFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DOTNET_CLI_HOME", "")
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".dotnet", "tools")
	if got := dotnetGlobalToolBinDir(); filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("dotnet global tool directory = %q, want %q", got, want)
	}
}

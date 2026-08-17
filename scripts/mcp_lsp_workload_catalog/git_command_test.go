package catalog

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBoundedGitOutputIgnoresAmbientGitAndRepositoryRedirectors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixed POSIX trusted Git owner")
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet"}, {"config", "user.name", "catalog-test"}, {"config", "user.email", "catalog-test@example.invalid"}} {
		if output, err := exec.Command("/usr/bin/git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "source.go"), []byte("package source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "source.go"}, {"commit", "--quiet", "-m", "fixture"}} {
		if output, err := exec.Command("/usr/bin/git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "git"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake)
	t.Setenv("GIT_DIR", t.TempDir())
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	output, err := boundedGitOutput(repo, 2*time.Second, "rev-parse", "HEAD^{tree}")
	if err != nil || len(strings.TrimSpace(string(output))) != 40 {
		t.Fatalf("boundedGitOutput() = %q, %v, want trusted tree", output, err)
	}
}

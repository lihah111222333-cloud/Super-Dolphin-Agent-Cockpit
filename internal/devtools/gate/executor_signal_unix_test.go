//go:build !windows

package gate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunConfiguredCommandTerminatesWritingGrandchildBeforeReturn(t *testing.T) {
	root := t.TempDir()
	outputPath := filepath.Join(root, "cache", "writes")
	script := strings.Join([]string{
		"set -eu",
		"mkdir -p " + shellQuote(filepath.Dir(outputPath)),
		"sh -c 'trap \"\" TERM; while :; do printf x >> \"$1\"; done' sh " + shellQuote(outputPath) + " &",
		"wait $!",
	}, "\n")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	configureCommandCancellation(command)
	err := runConfiguredCommand(command)
	if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("run configured command error = %v context = %v, want cancelled failure", err, ctx.Err())
	}
	if strings.Contains(err.Error(), "did not terminate") {
		t.Fatalf("run configured command retained an executable process group: %v", err)
	}
	before := mustFileSize(t, outputPath)
	time.Sleep(50 * time.Millisecond)
	if after := mustFileSize(t, outputPath); after != before {
		t.Fatalf("grandchild continued writing after return: before=%d after=%d", before, after)
	}
	if err := os.RemoveAll(filepath.Join(root, "cache")); err != nil {
		t.Fatalf("remove cancelled command cache: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "cache"), 0o700); err != nil {
		t.Fatalf("reuse isolated cache path: %v", err)
	}
}

func TestRunConfiguredCommandPreservesExitErrorIdentity(t *testing.T) {
	command := exec.CommandContext(context.Background(), "/bin/sh", "-c", "exit 1")
	configureCommandCancellation(command)
	err := runConfiguredCommand(command)
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run configured command error = %T %v, want direct exit code 1", err, err)
	}
	var matched *exec.ExitError
	if !errors.As(err, &matched) || matched.ExitCode() != 1 {
		t.Fatalf("errors.As exit error = %v, want exit code 1", err)
	}
}

func mustFileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

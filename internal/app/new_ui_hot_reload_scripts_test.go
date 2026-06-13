package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewUIDesktopScriptRejectsInvalidHotPollIntervals(t *testing.T) {
	for _, value := range []string{"abc", "0", "-1"} {
		t.Run(value, func(t *testing.T) {
			projectDir := t.TempDir()
			output, exitCode := runDesktopScriptHarness(t, projectDir, `
SUPER_DOLPHIN_HOT_MIN_POLL_INTERVAL=1
validate_backend_hot_poll_interval "`+value+`"
`)
			if exitCode == 0 {
				t.Fatalf("validate_backend_hot_poll_interval(%q) succeeded; output:\n%s", value, output)
			}
			if !strings.Contains(output, "SUPER_DOLPHIN_HOT_POLL_INTERVAL") {
				t.Fatalf("expected interval validation failure, got:\n%s", output)
			}
		})
	}
}

func TestNewUIDesktopScriptRejectsTooSmallHotPollInterval(t *testing.T) {
	projectDir := t.TempDir()
	output, exitCode := runDesktopScriptHarness(t, projectDir, `
SUPER_DOLPHIN_HOT_MIN_POLL_INTERVAL=1
validate_backend_hot_poll_interval "0.5"
`)
	if exitCode == 0 {
		t.Fatalf("validate_backend_hot_poll_interval accepted a sub-minimum interval; output:\n%s", output)
	}
	if !strings.Contains(output, "at least 1s") {
		t.Fatalf("expected minimum interval failure, got:\n%s", output)
	}
}

func TestNewUIDesktopScriptRejectsEscapingHotWatchPath(t *testing.T) {
	for _, value := range []string{"/tmp", "../outside"} {
		t.Run(value, func(t *testing.T) {
			projectDir := t.TempDir()
			output, exitCode := runDesktopScriptHarness(t, projectDir, `
SUPER_DOLPHIN_HOT_MIN_POLL_INTERVAL=1
SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS=16
SUPER_DOLPHIN_HOT_MAX_WATCH_FILES=5000
SUPER_DOLPHIN_HOT_POLL_INTERVAL=1
SUPER_DOLPHIN_HOT_WATCH_PATHS="`+value+`"
validate_backend_hot_reload_config
`)
			if exitCode == 0 {
				t.Fatalf("validate_backend_hot_reload_config accepted an escaping path; output:\n%s", output)
			}
			if !strings.Contains(output, "repository-relative") {
				t.Fatalf("expected repository-relative path failure, got:\n%s", output)
			}
		})
	}
}

func TestNewUIDesktopScriptRejectsTooManyHotWatchPaths(t *testing.T) {
	projectDir := t.TempDir()
	for _, rel := range []string{"p1", "p2", "p3"} {
		if err := os.Mkdir(filepath.Join(projectDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	output, exitCode := runDesktopScriptHarness(t, projectDir, `
SUPER_DOLPHIN_HOT_MIN_POLL_INTERVAL=1
SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS=2
SUPER_DOLPHIN_HOT_MAX_WATCH_FILES=5000
SUPER_DOLPHIN_HOT_POLL_INTERVAL=1
SUPER_DOLPHIN_HOT_WATCH_PATHS="p1 p2 p3"
validate_backend_hot_reload_config
`)
	if exitCode == 0 {
		t.Fatalf("validate_backend_hot_reload_config accepted too many watch paths; output:\n%s", output)
	}
	if !strings.Contains(output, "watch path limit exceeded") {
		t.Fatalf("expected watch path limit failure, got:\n%s", output)
	}
}

func TestNewUIDesktopScriptRejectsTooManyHotWatchFiles(t *testing.T) {
	projectDir := t.TempDir()
	internalDir := filepath.Join(projectDir, "internal")
	if err := os.Mkdir(internalDir, 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	for _, name := range []string{"one.go", "two.go"} {
		if err := os.WriteFile(filepath.Join(internalDir, name), []byte("package internal\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	output, exitCode := runDesktopScriptHarness(t, projectDir, `
SUPER_DOLPHIN_HOT_MIN_POLL_INTERVAL=1
SUPER_DOLPHIN_HOT_MAX_WATCH_PATHS=16
SUPER_DOLPHIN_HOT_MAX_WATCH_FILES=1
SUPER_DOLPHIN_HOT_POLL_INTERVAL=1
SUPER_DOLPHIN_HOT_WATCH_PATHS="internal"
validate_backend_hot_reload_config
snapshot_backend_watch_state
`)
	if exitCode == 0 {
		t.Fatalf("snapshot_backend_watch_state accepted too many files; output:\n%s", output)
	}
	if !strings.Contains(output, "file limit exceeded") {
		t.Fatalf("expected file limit failure, got:\n%s", output)
	}
}

func runDesktopScriptHarness(t *testing.T, projectDir, body string) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash harness uses WSL on Windows and cannot reliably access native temp paths")
	}
	text := readRootScript(t, "../../run-new-ui-desktop.sh")
	marker := "\nSUPER_DOLPHIN_HTTP_ADDR="
	mainIndex := strings.Index(text, marker)
	if mainIndex < 0 {
		t.Fatalf("run-new-ui-desktop.sh missing main configuration marker %q", marker)
	}
	cmd := exec.Command("bash", "-s")
	cmd.Dir = projectDir
	cmd.Stdin = strings.NewReader(text[:mainIndex] + "\n" + body)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run desktop script harness: %v\n%s", err, output)
	}
	return string(output), exitErr.ExitCode()
}

package app

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func readRootScript(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func runFrontendWatchModeSnippet(t *testing.T, env map[string]string) (string, string, int) {
	t.Helper()
	text := readRootScript(t, "../../run-new-ui-desktop.sh")
	snippet := extractScriptSection(t, text, "parse_frontend_watch_bool() {", "\nstart_desktop_backend() {")
	script := `set -euo pipefail
` + snippet + `
configure_frontend_watch_mode
printf '%s|%s|%s\n' "$SUPER_DOLPHIN_VITE_USE_POLLING" "$CHOKIDAR_USEPOLLING" "$FRONTEND_WATCH_MODE"
`
	path := t.TempDir() + "/watch-mode.sh"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write watch-mode snippet: %v", err)
	}

	cmd := exec.Command("bash", path)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	status := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run watch-mode snippet: %v", err)
		}
		status = exitErr.ExitCode()
	}
	return strings.TrimSpace(stdout.String()), stderr.String(), status
}

func extractScriptSection(t *testing.T, text, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(text, startMarker)
	if start < 0 {
		t.Fatalf("missing script section start %q", startMarker)
	}
	end := strings.Index(text[start:], endMarker)
	if end < 0 {
		t.Fatalf("missing script section end %q after %q", endMarker, startMarker)
	}
	return text[start : start+end]
}

func assertTextOrder(t *testing.T, text, first, second string) {
	t.Helper()
	firstIndex := strings.Index(text, first)
	if firstIndex < 0 {
		t.Fatalf("missing first text %q", first)
	}
	secondIndex := strings.Index(text, second)
	if secondIndex < 0 {
		t.Fatalf("missing second text %q", second)
	}
	if firstIndex >= secondIndex {
		t.Fatalf("expected %q before %q", first, second)
	}
}

func assertTextOrderAfter(t *testing.T, text, anchor, first, second string) {
	t.Helper()
	anchorIndex := strings.Index(text, anchor)
	if anchorIndex < 0 {
		t.Fatalf("missing anchor text %q", anchor)
	}
	assertTextOrder(t, text[anchorIndex:], first, second)
}

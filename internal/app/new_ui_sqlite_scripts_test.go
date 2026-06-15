package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewUIDesktopScriptSQLitePathWinsWhenPostgresEnvExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash snippet tests use WSL on Windows and cannot reliably preserve native paths")
	}
	home := filepath.ToSlash(filepath.Join(t.TempDir(), "home"))
	explicitSQLite := filepath.ToSlash(filepath.Join(t.TempDir(), "state", "explicit.db"))
	harness := newUIDesktopSQLiteHarness(t)
	script := harness + `
set -euo pipefail
SUPER_DOLPHIN_HOME="` + home + `"
SUPER_DOLPHIN_SQLITE_PATH="` + explicitSQLite + `"
ensure_sqlite_runtime
printf '%s\n%s\n%s\n' "$SUPER_DOLPHIN_SQLITE_PATH" "$DATABASE_URL" "$POSTGRES_CONNECTION_STRING"
`

	stdout, stderr, exitCode := runBashSnippet(t, script, []string{
		"DATABASE_URL=postgres://tester@127.0.0.1:5432/ignored?sslmode=disable",
		"POSTGRES_CONNECTION_STRING=postgres://compat@127.0.0.1:5432/ignored?sslmode=disable",
	})
	if exitCode != 0 {
		t.Fatalf("expected success, exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	want := explicitSQLite + "\n" +
		"postgres://tester@127.0.0.1:5432/ignored?sslmode=disable\n" +
		"postgres://compat@127.0.0.1:5432/ignored?sslmode=disable\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestNewUIDesktopScriptDefaultsSQLiteUnderHomeAndIgnoresOldPostgresDataDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash snippet tests use WSL on Windows and cannot reliably preserve native paths")
	}
	temp := t.TempDir()
	projectRoot := filepath.ToSlash(filepath.Join(temp, "project"))
	home := filepath.ToSlash(filepath.Join(temp, "home"))
	oldPGDataDir := filepath.Join(temp, "project", ".tmp", "pgdata")
	if err := os.MkdirAll(oldPGDataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pgVersion := filepath.Join(oldPGDataDir, "PG_VERSION")
	if err := os.WriteFile(pgVersion, []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(pgVersion)
	if err != nil {
		t.Fatal(err)
	}

	harness := newUIDesktopSQLiteHarness(t)
	script := harness + `
set -euo pipefail
PROJECT_DIR="` + projectRoot + `"
SUPER_DOLPHIN_HOME="` + home + `"
ensure_sqlite_runtime
printf '%s\n' "$SUPER_DOLPHIN_SQLITE_PATH"
`

	stdout, stderr, exitCode := runBashSnippet(t, script, []string{
		"DATABASE_URL=postgres://tester@127.0.0.1:5432/ignored?sslmode=disable",
	})
	if exitCode != 0 {
		t.Fatalf("expected success, exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
	wantSQLite := home + "/super-dolphin.db\n"
	if stdout != wantSQLite {
		t.Fatalf("stdout = %q, want %q", stdout, wantSQLite)
	}
	after, err := os.Stat(pgVersion)
	if err != nil {
		t.Fatalf("old PG data dir was touched or removed: %v", err)
	}
	if after.ModTime() != before.ModTime() || after.Size() != before.Size() {
		t.Fatalf("old PG data file changed: before=%v/%d after=%v/%d", before.ModTime(), before.Size(), after.ModTime(), after.Size())
	}
}

func newUIDesktopSQLiteHarness(t *testing.T) string {
	t.Helper()
	text := readRootScript(t, "../../run-new-ui-desktop.sh")
	return extractBashFunction(t, text, "ensure_sqlite_runtime") + "\n"
}

func extractBashFunction(t *testing.T, text, name string) string {
	t.Helper()
	marker := "\n" + name + "() {"
	start := strings.Index(text, marker)
	if start >= 0 {
		start++
	} else if strings.HasPrefix(text, name+"() {") {
		start = 0
	} else {
		t.Fatalf("missing bash function %s", name)
	}
	rest := text[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("missing end of bash function %s", name)
	}
	return rest[:end+3]
}

func runBashSnippet(t *testing.T, script string, extraEnv []string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = newUIDesktopScriptTestEnv(extraEnv)
	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	t.Fatalf("run bash snippet: %v", err)
	return "", "", 1
}

func newUIDesktopScriptTestEnv(extraEnv []string) []string {
	blocked := map[string]struct{}{
		"DATABASE_URL":               {},
		"POSTGRES_CONNECTION_STRING": {},
		"SUPER_DOLPHIN_SQLITE_PATH":  {},
		"SUPER_DOLPHIN_PROCESS_ROLE": {},
	}
	env := make([]string, 0, len(os.Environ())+len(extraEnv))
	for _, entry := range os.Environ() {
		key := strings.SplitN(entry, "=", 2)[0]
		if _, skip := blocked[key]; skip {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extraEnv...)
}

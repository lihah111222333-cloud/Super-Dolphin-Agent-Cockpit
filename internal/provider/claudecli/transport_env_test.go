package claudecli

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewTransportScrubsDatabaseEnvFromParentAndLaunchEnv(t *testing.T) {
	if os.Getenv("CLAUDECLI_TRANSPORT_ENV_HELPER") == "1" {
		writeClaudeEnvHelperFile()
		select {}
	}

	t.Setenv("DATABASE_URL", "postgres://parent@localhost/super_dolphin")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@localhost/super_dolphin")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", filepath.Join(t.TempDir(), "parent.db"))
	t.Setenv("SUPER_DOLPHIN_INTERNAL_SQLITE_PATH", filepath.Join(t.TempDir(), "parent-internal.db"))
	t.Setenv("CLAUDECLI_SAFE_PARENT", "keep-parent")

	envPath := filepath.Join(t.TempDir(), "transport-env.txt")
	tr, err := newTransport(os.Args[0], []string{"-test.run=^TestNewTransportScrubsDatabaseEnvFromParentAndLaunchEnv$"}, "", []string{
		"CLAUDECLI_TRANSPORT_ENV_HELPER=1",
		"CLAUDECLI_ENV_FILE=" + envPath,
		"CLAUDECLI_SAFE_LAUNCH=keep-launch",
		"DATABASE_URL=postgres://launch@localhost/super_dolphin",
		"POSTGRES_CONNECTION_STRING=postgres://launch-compat@localhost/super_dolphin",
		"SUPER_DOLPHIN_SQLITE_PATH=" + filepath.Join(t.TempDir(), "launch.db"),
		"SUPER_DOLPHIN_INTERNAL_SQLITE_PATH=" + filepath.Join(t.TempDir(), "launch-internal.db"),
	})
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	t.Cleanup(func() { _ = tr.Kill() })

	env := waitForClaudeEnvFile(t, envPath)
	for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", "SUPER_DOLPHIN_SQLITE_PATH", "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"} {
		requireClaudeEnvKeyAbsent(t, env, key)
	}
	requireClaudeEnvValue(t, env, "CLAUDECLI_SAFE_PARENT", "keep-parent")
	requireClaudeEnvValue(t, env, "CLAUDECLI_SAFE_LAUNCH", "keep-launch")
	if noProxy := requireClaudeEnvValue(t, env, "NO_PROXY", ""); !strings.Contains(noProxy, "127.0.0.1") {
		t.Fatalf("NO_PROXY = %q, want loopback entries", noProxy)
	}
}

func TestRunClaudeAuthStatusScrubsDatabaseEnvFromParent(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://parent@localhost/super_dolphin")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@localhost/super_dolphin")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", filepath.Join(t.TempDir(), "parent.db"))
	t.Setenv("SUPER_DOLPHIN_INTERNAL_SQLITE_PATH", filepath.Join(t.TempDir(), "parent-internal.db"))
	t.Setenv("CLAUDECLI_SAFE_PARENT", "keep-parent")

	envPath := filepath.Join(t.TempDir(), "auth-env.txt")
	claudeHome := filepath.Join(t.TempDir(), "claude-home")
	t.Setenv("CLAUDECLI_ENV_FILE", envPath)
	status, raw, err := runClaudeAuthStatus(context.Background(), writeClaudeAuthHelper(t), "", cliLaunchConfig{
		ClaudeHome: claudeHome,
	})
	if err != nil {
		t.Fatalf("runClaudeAuthStatus() error = %v raw=%q", err, raw)
	}
	if !status.LoggedIn {
		t.Fatalf("runClaudeAuthStatus() loggedIn = false raw=%q", raw)
	}

	env := waitForClaudeEnvFile(t, envPath)
	for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", "SUPER_DOLPHIN_SQLITE_PATH", "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"} {
		requireClaudeEnvKeyAbsent(t, env, key)
	}
	requireClaudeEnvValue(t, env, "CLAUDECLI_SAFE_PARENT", "keep-parent")
	requireClaudeEnvValue(t, env, "CLAUDE_CONFIG_DIR", claudeHome)
	if noProxy := requireClaudeEnvValue(t, env, "NO_PROXY", ""); !strings.Contains(noProxy, "localhost") {
		t.Fatalf("NO_PROXY = %q, want loopback entries", noProxy)
	}
}

func writeClaudeEnvHelperFile() {
	envFile := os.Getenv("CLAUDECLI_ENV_FILE")
	if envFile == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(envFile, []byte(strings.Join(os.Environ(), "\n")), 0o600); err != nil {
		os.Exit(2)
	}
}

func writeClaudeAuthHelper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "claude-auth-helper.cmd")
		body := "@echo off\r\nset > \"%CLAUDECLI_ENV_FILE%\"\r\necho {\"loggedIn\":true}\r\n"
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write auth helper: %v", err)
		}
		return path
	}
	path := filepath.Join(dir, "claude-auth-helper.sh")
	body := "#!/bin/sh\nenv > \"$CLAUDECLI_ENV_FILE\"\nprintf '%s\\n' '{\"loggedIn\":true}'\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write auth helper: %v", err)
	}
	return path
}

func waitForClaudeEnvFile(t *testing.T, path string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			text := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
			if text == "" {
				return nil
			}
			return strings.Split(text, "\n")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for env file %s", path)
	return nil
}

func requireClaudeEnvKeyAbsent(t *testing.T, env []string, key string) {
	t.Helper()
	if value, ok := claudeEnvValue(env, key); ok {
		t.Fatalf("%s leaked with value %q in env %#v", key, value, env)
	}
}

func requireClaudeEnvValue(t *testing.T, env []string, key, want string) string {
	t.Helper()
	got, ok := claudeEnvValue(env, key)
	if !ok {
		t.Fatalf("%s missing from env %#v", key, env)
	}
	if want != "" && got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
	return got
}

func claudeEnvValue(env []string, key string) (string, bool) {
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, key) {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

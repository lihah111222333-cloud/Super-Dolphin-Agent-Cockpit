package codexapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestValidCodexCLIScrubsDatabaseEnvFromProbe(t *testing.T) {
	setCodexDatabaseEnvForTest(t)
	t.Setenv("CODEX_SAFE_PARENT_ENV", "keep-parent")

	helperPath := filepath.Join(t.TempDir(), codexExecutableFileName())
	writeFakeCodexExecutableHelper(t, helperPath, true)
	envPath := filepath.Join(t.TempDir(), "probe-env.txt")
	t.Setenv(codexFakeProbeEnvFileEnv, envPath)

	if !validCodexCLI(context.Background(), helperPath) {
		t.Fatal("validCodexCLI() = false, want true")
	}

	env := readCodexEnvFile(t, envPath)
	requireCodexDatabaseEnvAbsent(t, env)
	requireEnvValue(t, env, "CODEX_SAFE_PARENT_ENV", "keep-parent")
}

func TestSpawnLocalScrubsDatabaseEnvFromAppServer(t *testing.T) {
	setCodexDatabaseEnvForTest(t)
	t.Setenv("CODEX_SAFE_PARENT_ENV", "keep-parent")

	binDir := t.TempDir()
	writeFakeCodexExecutableHelper(t, filepath.Join(binDir, codexExecutableFileName()), true)
	t.Setenv("PATH", binDir)
	envPath := filepath.Join(t.TempDir(), "app-server-env.txt")
	t.Setenv(codexFakeAppServerEnvFileEnv, envPath)

	tr := &transport{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tr.spawnLocal(ctx); err != nil {
		t.Fatalf("spawnLocal() error = %v", err)
	}
	t.Cleanup(func() { _ = tr.stopProcess(false) })

	env := readCodexEnvFile(t, envPath)
	requireCodexDatabaseEnvAbsent(t, env)
	requireEnvValue(t, env, "CODEX_SAFE_PARENT_ENV", "keep-parent")
}

func setCodexDatabaseEnvForTest(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://parent@localhost/super_dolphin")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@localhost/super_dolphin")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", filepath.Join(t.TempDir(), "parent.db"))
	t.Setenv("SUPER_DOLPHIN_INTERNAL_SQLITE_PATH", filepath.Join(t.TempDir(), "parent-internal.db"))
}

func readCodexEnvFile(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read codex env file %s: %v", path, err)
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func requireCodexDatabaseEnvAbsent(t *testing.T, env []string) {
	t.Helper()
	for _, key := range contract.ForbiddenDatabaseEnvKeyNames() {
		requireEnvKeyAbsent(t, env, key)
	}
}

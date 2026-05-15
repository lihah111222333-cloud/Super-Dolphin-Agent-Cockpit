package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestNew_PrefersCanonicalRPCAddr(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "127.0.0.1:9200")
	t.Setenv("RPC_ADDR", "127.0.0.1:9300")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := New()
	if cfg.RPCAddr != "127.0.0.1:9200" {
		t.Fatalf("RPCAddr = %q", cfg.RPCAddr)
	}
	if logs := buf.String(); logs != "" {
		t.Fatalf("logs = %q, want empty", logs)
	}
}

func TestNew_UsesLegacyRPCAddrWithDeprecationWarning(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("RPC_ADDR", "127.0.0.1:9100")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := New()
	if cfg.RPCAddr != "127.0.0.1:9100" {
		t.Fatalf("RPCAddr = %q", cfg.RPCAddr)
	}
	logs := buf.String()
	if !strings.Contains(logs, "config env deprecated") ||
		!strings.Contains(logs, "legacy=RPC_ADDR") ||
		!strings.Contains(logs, "canonical=GO_AGENT_CTL_RPC_ADDR") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestNew_ExportsResolvedDatabaseURLWhenEnvMissing(t *testing.T) {
	isolateConfigTestEnv(t)

	cfg := New()
	if got := strings.TrimSpace(cfg.DatabaseURL); got == "" {
		t.Fatal("DatabaseURL is empty")
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
}

func TestNew_PreservesDatabaseURLFromEnv(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("DATABASE_URL", "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable")

	cfg := New()
	if cfg.DatabaseURL != "postgres://tester@127.0.0.1:54320/custom_db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
}

func TestNew_UsesPostgresConnectionStringCompat(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@127.0.0.1:54320/compat_db?sslmode=disable")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := New()
	if cfg.DatabaseURL != "postgres://compat@127.0.0.1:54320/compat_db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
	if logs := buf.String(); !strings.Contains(logs, "POSTGRES_CONNECTION_STRING is deprecated; use DATABASE_URL instead") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestNew_LoadsDotEnvFromProjectRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PROJECT_ROOT", root)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	t.Setenv("LOG_LEVEL", "")

	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("POSTGRES_CONNECTION_STRING=postgres://dotenv@127.0.0.1:54320/dotenv_db?sslmode=disable\nLOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := New()
	if cfg.ProjectRoot != root {
		t.Fatalf("ProjectRoot = %q, want %q", cfg.ProjectRoot, root)
	}
	if cfg.DatabaseURL != "postgres://dotenv@127.0.0.1:54320/dotenv_db?sslmode=disable" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
}

func TestNew_DefaultsPersistentSubagentDefaultOff(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "")
	cfg := New()
	if cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = true, want false")
	}
}

func TestNew_AllowsEnablingPersistentSubagentDefault(t *testing.T) {
	isolateConfigTestEnv(t)
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "true")
	cfg := New()
	if !cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = false, want true")
	}
}

func isolateConfigTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PROJECT_ROOT", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	t.Setenv("POSTGRES_CONNECTION_STRING", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("RPC_ADDR", "")
}

func restoreConfigLogger(t *testing.T, dst *bytes.Buffer) {
	t.Helper()
	original := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewTextHandler(dst, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { pkglogger.SetForTest(original) })
}

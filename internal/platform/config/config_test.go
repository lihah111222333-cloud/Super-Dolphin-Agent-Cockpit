package config

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew_PrefersCanonicalRPCAddr(t *testing.T) {
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
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("RPC_ADDR", "127.0.0.1:9100")

	var buf bytes.Buffer
	restoreConfigLogger(t, &buf)

	cfg := New()
	if cfg.RPCAddr != "127.0.0.1:9100" {
		t.Fatalf("RPCAddr = %q", cfg.RPCAddr)
	}
	if logs := buf.String(); !strings.Contains(logs, "config env RPC_ADDR is deprecated; use GO_AGENT_CTL_RPC_ADDR instead before 2026-06-30") {
		t.Fatalf("logs = %q", logs)
	}
}

func TestNew_ExportsResolvedDatabaseURLWhenEnvMissing(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("DATABASE_URL", "")

	cfg := New()
	if got := strings.TrimSpace(cfg.DatabaseURL); got == "" {
		t.Fatal("DatabaseURL is empty")
	}
	if got := os.Getenv("DATABASE_URL"); got != cfg.DatabaseURL {
		t.Fatalf("DATABASE_URL = %q, want %q", got, cfg.DatabaseURL)
	}
}

func TestNew_PreservesDatabaseURLFromEnv(t *testing.T) {
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
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
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("DATABASE_URL", "")
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
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "")
	cfg := New()
	if cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = true, want false")
	}
}

func TestNew_AllowsEnablingPersistentSubagentDefault(t *testing.T) {
	t.Setenv("PERSISTENT_SUBAGENT_DEFAULT", "true")
	cfg := New()
	if !cfg.Agent.PersistentSubagentDefault {
		t.Fatalf("Agent.PersistentSubagentDefault = false, want true")
	}
}

func restoreConfigLogger(t *testing.T, dst *bytes.Buffer) {
	t.Helper()
	origWriter := log.Writer()
	origFlags := log.Flags()
	origPrefix := log.Prefix()
	log.SetOutput(dst)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(origWriter)
		log.SetFlags(origFlags)
		log.SetPrefix(origPrefix)
	})
}

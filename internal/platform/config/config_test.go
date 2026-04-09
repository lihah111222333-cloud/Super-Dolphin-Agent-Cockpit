package config

import (
	"bytes"
	"log"
	"os"
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

func TestNew_EnablesDynamicToolsFromEnv(t *testing.T) {
	t.Setenv("GO_AGENT_DYNAMIC_TOOLS", "true")

	cfg := New()
	if !cfg.Provider.DynamicToolsEnabled {
		t.Fatal("Provider.DynamicToolsEnabled = false, want true")
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

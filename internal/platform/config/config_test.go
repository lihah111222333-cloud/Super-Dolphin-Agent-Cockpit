package config

import (
	"bytes"
	"log"
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

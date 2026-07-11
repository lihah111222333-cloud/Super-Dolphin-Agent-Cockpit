package bootstrap

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestReadBootConfig_UsesLegacyEnvWithDeprecationWarning(t *testing.T) {
	resetBootEnvVars(t)

	t.Setenv("RPC_ADDR", "127.0.0.1:9100")
	t.Setenv("GO_AGENT_MCP_INSTANCE_ID", "instance-old")
	t.Setenv("GO_AGENT_MCP_THREAD_ID", "thread-old")
	t.Setenv("GO_AGENT_MCP_BINARY_NAME", "mcp-lsp")
	t.Setenv("GO_AGENT_MCP_CLIENT_KIND", "lsp")
	t.Setenv("GO_AGENT_MCP_SESSION_TOKEN", "session-old")
	t.Setenv("GO_AGENT_MCP_BOOT_CONTEXT", `{"instance_id":"snap-old"}`)

	var buf bytes.Buffer
	origLogger := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { pkglogger.SetForTest(origLogger) })

	cfg := ReadBootConfig()
	if cfg.RPCAddr != "127.0.0.1:9100" {
		t.Fatalf("RPCAddr = %q", cfg.RPCAddr)
	}
	if cfg.InstanceID != "instance-old" {
		t.Fatalf("InstanceID = %q", cfg.InstanceID)
	}
	if cfg.ThreadID != "thread-old" {
		t.Fatalf("ThreadID = %q", cfg.ThreadID)
	}
	if cfg.BinaryName != "mcp-lsp" {
		t.Fatalf("BinaryName = %q", cfg.BinaryName)
	}
	if cfg.ClientKind != "lsp" {
		t.Fatalf("ClientKind = %q", cfg.ClientKind)
	}
	if cfg.SessionToken != "session-old" {
		t.Fatalf("SessionToken = %q", cfg.SessionToken)
	}
	if got := string(cfg.BootSnapshot); got != `{"instance_id":"snap-old"}` {
		t.Fatalf("BootSnapshot = %q", got)
	}

	logs := buf.String()
	for _, want := range []string{
		"bootstrap env RPC_ADDR is deprecated; use GO_AGENT_CTL_RPC_ADDR instead before 2026-06-30",
		"bootstrap env GO_AGENT_MCP_INSTANCE_ID is deprecated; use GO_AGENT_CTL_INSTANCE_ID instead before 2026-06-30",
		"bootstrap env GO_AGENT_MCP_THREAD_ID is deprecated; use GO_AGENT_CTL_THREAD_ID instead before 2026-06-30",
		"bootstrap env GO_AGENT_MCP_BINARY_NAME is deprecated; use GO_AGENT_CTL_BINARY_NAME instead before 2026-06-30",
		"bootstrap env GO_AGENT_MCP_CLIENT_KIND is deprecated; use GO_AGENT_CTL_CLIENT_KIND instead before 2026-06-30",
		"bootstrap env GO_AGENT_MCP_SESSION_TOKEN is deprecated; use GO_AGENT_CTL_SESSION_TOKEN instead before 2026-06-30",
		"bootstrap env GO_AGENT_MCP_BOOT_CONTEXT is deprecated; use GO_AGENT_CTL_BOOTSTRAP_JSON instead before 2026-06-30",
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("logs = %q, want substring %q", logs, want)
		}
	}
}

func TestReadBootConfig_PrefersCanonicalEnvWithoutDeprecationWarning(t *testing.T) {
	resetBootEnvVars(t)

	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "127.0.0.1:9200")
	t.Setenv("RPC_ADDR", "127.0.0.1:9300")
	t.Setenv("GO_AGENT_CTL_BOOTSTRAP_JSON", `{"instance_id":"snap-new"}`)
	t.Setenv("GO_AGENT_MCP_BOOT_CONTEXT", `{"instance_id":"snap-old"}`)

	var buf bytes.Buffer
	origLogger := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { pkglogger.SetForTest(origLogger) })

	cfg := ReadBootConfig()
	if cfg.RPCAddr != "127.0.0.1:9200" {
		t.Fatalf("RPCAddr = %q", cfg.RPCAddr)
	}
	if got := string(cfg.BootSnapshot); got != `{"instance_id":"snap-new"}` {
		t.Fatalf("BootSnapshot = %q", got)
	}
	if logs := buf.String(); logs != "" {
		t.Fatalf("logs = %q, want empty", logs)
	}
}

func resetBootEnvVars(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"GO_AGENT_CTL_RPC_ADDR",
		"RPC_ADDR",
		"GO_AGENT_CTL_INSTANCE_ID",
		"GO_AGENT_MCP_INSTANCE_ID",
		"GO_AGENT_CTL_BOOT_ID",
		"GO_AGENT_MCP_BOOT_ID",
		"GO_AGENT_CTL_BINARY_NAME",
		"GO_AGENT_MCP_BINARY_NAME",
		"GO_AGENT_CTL_CLIENT_KIND",
		"GO_AGENT_MCP_CLIENT_KIND",
		"GO_AGENT_CTL_AGENT_ID",
		"GO_AGENT_MCP_AGENT_ID",
		"GO_AGENT_CTL_THREAD_ID",
		"GO_AGENT_MCP_THREAD_ID",
		"GO_AGENT_CTL_SESSION_TOKEN",
		"GO_AGENT_MCP_SESSION_TOKEN",
		"GO_AGENT_CTL_BOOTSTRAP_JSON",
		"GO_AGENT_MCP_BOOT_CONTEXT",
	} {
		t.Setenv(key, "")
	}
}

package codexapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestWriteCodexMCPConfig_InjectsManagedServers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	initial := strings.TrimSpace(`
[model_providers.openai]
name = "OpenAI"

[mcp_servers.custom]
command = "/usr/local/bin/custom"
`) + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	manifest := dto.BuildManifest(dto.ManifestContext{
		BinaryDir: "/tmp/bin",
		Env:       map[string]string{"GO_AGENT_CTL_RPC_ADDR": "127.0.0.1:9123"},
	})
	if err := writeCodexMCPConfig(path, manifest, "/tmp/work"); err != nil {
		t.Fatalf("writeCodexMCPConfig() error = %v", err)
	}

	text := readCodexMCPTestFile(t, path)
	if !strings.Contains(text, `[mcp_servers.lsp.env]`) {
		t.Fatalf("config = %s, want env subtable for lsp", text)
	}

	doc := readCodexMCPTestDoc(t, path)
	servers := mustCodexMCPServers(t, doc)
	custom, _ := servers["custom"].(map[string]any)
	if custom["command"] != "/usr/local/bin/custom" {
		t.Fatalf("custom server = %#v, want preserved command", custom)
	}
	lsp := mustCodexMCPServer(t, servers, "lsp")
	if lsp["command"] != "/tmp/bin/mcp-lsp" {
		t.Fatalf("lsp.command = %#v, want /tmp/bin/mcp-lsp", lsp["command"])
	}
	if lsp["type"] != "stdio" {
		t.Fatalf("lsp.type = %#v, want stdio", lsp["type"])
	}
	if lsp["cwd"] != "/tmp/work" {
		t.Fatalf("lsp.cwd = %#v, want /tmp/work", lsp["cwd"])
	}
	env, _ := lsp["env"].(map[string]any)
	if env["GO_AGENT_CTL_RPC_ADDR"] != "127.0.0.1:9123" {
		t.Fatalf("lsp.env = %#v, want GO_AGENT_CTL_RPC_ADDR", env)
	}
}

func TestWriteCodexMCPConfig_CreatesFileIfNotExists(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".codex", "config.toml")
	manifest := dto.BuildManifest(dto.ManifestContext{BinaryDir: "/tmp/bin"})
	if err := writeCodexMCPConfig(path, manifest, "/tmp/work"); err != nil {
		t.Fatalf("writeCodexMCPConfig() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
}

func TestWriteCodexMCPConfig_SkipsNonManagedBinaries(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	manifest := dto.MCPManifest{Binaries: []dto.MCPBinary{{
		Name:    "third-party",
		Command: []string{"/tmp/third-party"},
	}}}
	if err := writeCodexMCPConfig(path, manifest, "/tmp/work"); err != nil {
		t.Fatalf("writeCodexMCPConfig() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want not exists", path, err)
	}
}

func TestWriteCodexMCPConfig_IdempotentUpdate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	manifest := dto.BuildManifest(dto.ManifestContext{
		BinaryDir: "/tmp/bin",
		Env:       map[string]string{"GO_AGENT_CTL_RPC_ADDR": "127.0.0.1:9123"},
	})
	if err := writeCodexMCPConfig(path, manifest, "/tmp/work"); err != nil {
		t.Fatalf("first writeCodexMCPConfig() error = %v", err)
	}
	first := readCodexMCPTestFile(t, path)
	if err := writeCodexMCPConfig(path, manifest, "/tmp/work"); err != nil {
		t.Fatalf("second writeCodexMCPConfig() error = %v", err)
	}
	second := readCodexMCPTestFile(t, path)
	if first != second {
		t.Fatalf("config changed on idempotent rewrite\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestWriteCodexMCPConfig_PreservesUserKeysAndUsesEnvTable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	initial := strings.TrimSpace(`
[mcp_servers.lsp]
command = "/custom/bin/mcp-lsp"
startup_timeout_sec = 30
`) + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	manifest := dto.BuildManifest(dto.ManifestContext{
		BinaryDir: "/tmp/bin",
		Env:       map[string]string{"GO_AGENT_CTL_RPC_ADDR": "127.0.0.1:9123"},
	})
	if err := writeCodexMCPConfig(path, manifest, "/tmp/work"); err != nil {
		t.Fatalf("writeCodexMCPConfig() error = %v", err)
	}

	text := readCodexMCPTestFile(t, path)
	if !strings.Contains(text, `type = "stdio"`) {
		t.Fatalf("config = %s, want explicit stdio type", text)
	}
	if !strings.Contains(text, `[mcp_servers.lsp.env]`) {
		t.Fatalf("config = %s, want env subtable", text)
	}

	doc := readCodexMCPTestDoc(t, path)
	lsp := mustCodexMCPServer(t, mustCodexMCPServers(t, doc), "lsp")
	if lsp["startup_timeout_sec"] != int64(30) {
		t.Fatalf("lsp.startup_timeout_sec = %#v, want 30", lsp["startup_timeout_sec"])
	}
	if lsp["type"] != "stdio" {
		t.Fatalf("lsp.type = %#v, want stdio", lsp["type"])
	}
	env, _ := lsp["env"].(map[string]any)
	if env["GO_AGENT_CTL_RPC_ADDR"] != "127.0.0.1:9123" {
		t.Fatalf("lsp.env = %#v, want GO_AGENT_CTL_RPC_ADDR", env)
	}
}

func TestMCPReadyWatcher_FailedStatus(t *testing.T) {
	t.Parallel()

	w := newMCPReadyWatcher([]string{"lsp"})
	w.OnStartupStatus("lsp", "failed")
	err := w.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lsp") || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("Wait() error = %v, want failed status for tracked server", err)
	}
}

func TestMCPReadyWatcher_Timeout(t *testing.T) {
	t.Parallel()

	w := newMCPReadyWatcher([]string{"lsp", "orch"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := w.Wait(ctx)
	if err == nil || !strings.Contains(err.Error(), "mcp ready timeout after waiting for servers [lsp orch]") {
		t.Fatalf("Wait() error = %v, want timeout with pending server list", err)
	}
}

func TestInjectCodexMCPServers_SkipsExternalServer(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)

	d := newDriver(nil, nil, nil, nil, nil, nil).(*driver)
	s := &session{transport: &transport{serverURL: "ws://example.invalid/ws"}}
	req := dto.StartSessionRequest{
		AgentID: "agent-1",
		CWD:     "/tmp/work",
		Config:  map[string]any{"binary_dir": t.TempDir()},
	}
	if err := d.injectCodexMCPServers(context.Background(), s, req); err != nil {
		t.Fatalf("injectCodexMCPServers() error = %v, want nil", err)
	}
	if watcher := s.getMCPWatcher(); watcher != nil {
		t.Fatalf("session watcher = %#v, want nil when external server is skipped", watcher)
	}
	if _, err := os.Stat(filepath.Join(root, "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config.toml stat error = %v, want not exists", err)
	}
}

func TestInjectCodexMCPServers_SkipsEmptyManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)

	orig := buildCodexMCPManifest
	buildCodexMCPManifest = func(dto.ManifestContext) dto.MCPManifest { return dto.MCPManifest{} }
	defer func() { buildCodexMCPManifest = orig }()

	d := newDriver(nil, nil, nil, nil, nil, nil).(*driver)
	if err := d.injectCodexMCPServers(context.Background(), nil, dto.StartSessionRequest{}); err != nil {
		t.Fatalf("injectCodexMCPServers() error = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(root, "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config.toml stat error = %v, want not exists", err)
	}
}

func TestMCPReadyWatcher_CancelledStatus(t *testing.T) {
	t.Parallel()

	w := newMCPReadyWatcher([]string{"lsp"})
	w.OnStartupStatus("lsp", "cancelled")
	err := w.Wait(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lsp") || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("Wait() error = %v, want cancelled status for tracked server", err)
	}
}

func TestMCPReadyWatcher_MultiServerAllReady(t *testing.T) {
	t.Parallel()

	w := newMCPReadyWatcher([]string{"lsp", "orch"})
	w.OnStartupStatus("orch", "ready")
	if pending := w.pendingNames(); !reflect.DeepEqual(pending, []string{"lsp"}) {
		t.Fatalf("pendingNames() after first ready = %#v, want remaining tracked server", pending)
	}
	w.OnStartupStatus("lsp", "ready")
	if pending := w.pendingNames(); len(pending) != 0 {
		t.Fatalf("pendingNames() after all ready = %#v, want none", pending)
	}
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v, want nil when all servers are ready", err)
	}
}

func TestMCPReadyWatcher_DuplicateReady(t *testing.T) {
	t.Parallel()

	w := newMCPReadyWatcher([]string{"lsp"})
	w.OnStartupStatus("lsp", "ready")
	w.OnStartupStatus("lsp", "ready")
	if pending := w.pendingNames(); len(pending) != 0 {
		t.Fatalf("pendingNames() = %#v, want none after duplicate ready", pending)
	}
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v, want nil", err)
	}
}

func TestMCPReadyWatcher_UnknownServer(t *testing.T) {
	t.Parallel()

	w := newMCPReadyWatcher([]string{"lsp"})
	w.OnStartupStatus("orch", "ready")
	if pending := w.pendingNames(); !reflect.DeepEqual(pending, []string{"lsp"}) {
		t.Fatalf("pendingNames() = %#v, want tracked server unchanged", pending)
	}
}

func readCodexMCPTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(raw)
}

func readCodexMCPTestDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	var doc map[string]any
	if _, err := toml.Decode(readCodexMCPTestFile(t, path), &doc); err != nil {
		t.Fatalf("toml.Decode(%q) error = %v", path, err)
	}
	return doc
}

func mustCodexMCPServers(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	servers, ok := doc["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers = %#v, want table", doc["mcp_servers"])
	}
	return servers
}

func mustCodexMCPServer(t *testing.T, servers map[string]any, name string) map[string]any {
	t.Helper()
	server, ok := servers[name].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers[%q] = %#v, want table", name, servers[name])
	}
	return server
}

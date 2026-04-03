package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	_ "unsafe"

	"github.com/BurntSushi/toml"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	_ "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
)

//go:linkname writeCodexMCPConfig github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp.writeCodexMCPConfig
func writeCodexMCPConfig(path string, manifest dto.MCPManifest, cwd string) error

func TestCodexMCPInjection_E2E(t *testing.T) {
	requireCodexCLI(t)

	path := tempCodexConfigPath(t)
	manifest := dto.BuildManifest(dto.ManifestContext{
		BinaryDir: "/tmp/codex-e2e/bin",
		Env: map[string]string{
			"GO_AGENT_CTL_RPC_ADDR":      "127.0.0.1:9123",
			"GO_AGENT_CTL_SESSION_TOKEN": "session-token",
		},
	})
	if err := writeCodexMCPConfig(path, manifest, "/tmp/codex-e2e/work"); err != nil {
		t.Fatalf("writeCodexMCPConfig() error = %v", err)
	}

	raw := readCodexFile(t, path)
	if !strings.Contains(raw, `[mcp_servers.mcp-lsp.env]`) {
		t.Fatalf("config = %s, want env subtable for mcp-lsp", raw)
	}
	if !strings.Contains(raw, `[mcp_servers.mcp-orch.env]`) {
		t.Fatalf("config = %s, want env subtable for mcp-orch", raw)
	}

	doc := readCodexDoc(t, path)
	assertManagedCodexServer(t, codexServer(t, doc, "mcp-lsp"), "mcp-lsp")
	assertManagedCodexServer(t, codexServer(t, doc, "mcp-orch"), "mcp-orch")
}

func TestCodexMCPConfig_PreservesUserKeys_E2E(t *testing.T) {
	requireCodexCLI(t)

	path := tempCodexConfigPath(t)
	initial := strings.TrimSpace(`
[mcp_servers.exa]
command = "/Users/test/.codex/bin/mcp-exa.sh"
args = ["serve"]
env_vars = ["EXA_API_KEY", "NPM_CONFIG_CACHE", "MCP_REMOTE_CONFIG_DIR"]

[mcp_servers.postgres]
command = "/Users/test/.codex/bin/mcp-postgres.sh"
env_vars = ["POSTGRES_CONNECTION_STRING"]
startup_timeout_sec = 45
`) + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	manifest := dto.BuildManifest(dto.ManifestContext{
		BinaryDir: "/tmp/codex-e2e/bin",
		Env: map[string]string{
			"GO_AGENT_CTL_RPC_ADDR": "127.0.0.1:9123",
		},
	})
	if err := writeCodexMCPConfig(path, manifest, "/tmp/codex-e2e/work"); err != nil {
		t.Fatalf("writeCodexMCPConfig() error = %v", err)
	}

	doc := readCodexDoc(t, path)
	exa := codexServer(t, doc, "exa")
	postgres := codexServer(t, doc, "postgres")
	if exa["command"] != "/Users/test/.codex/bin/mcp-exa.sh" {
		t.Fatalf("exa.command = %#v, want preserved command", exa["command"])
	}
	if postgres["command"] != "/Users/test/.codex/bin/mcp-postgres.sh" {
		t.Fatalf("postgres.command = %#v, want preserved command", postgres["command"])
	}
	if !reflect.DeepEqual(readStringSlice(t, exa["env_vars"]), []string{"EXA_API_KEY", "NPM_CONFIG_CACHE", "MCP_REMOTE_CONFIG_DIR"}) {
		t.Fatalf("exa.env_vars = %#v, want preserved env_vars", exa["env_vars"])
	}
	if !reflect.DeepEqual(readStringSlice(t, postgres["env_vars"]), []string{"POSTGRES_CONNECTION_STRING"}) {
		t.Fatalf("postgres.env_vars = %#v, want preserved env_vars", postgres["env_vars"])
	}
	if _, ok := exa["env"]; ok {
		t.Fatalf("exa.env = %#v, want no env subtable mutation", exa["env"])
	}
	if _, ok := postgres["env"]; ok {
		t.Fatalf("postgres.env = %#v, want no env subtable mutation", postgres["env"])
	}
	if postgres["startup_timeout_sec"] != int64(45) {
		t.Fatalf("postgres.startup_timeout_sec = %#v, want 45", postgres["startup_timeout_sec"])
	}

	assertManagedCodexServer(t, codexServer(t, doc, "mcp-lsp"), "mcp-lsp")
	assertManagedCodexServer(t, codexServer(t, doc, "mcp-orch"), "mcp-orch")
}

func requireCodexCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex not installed")
	}
}

func tempCodexConfigPath(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	return filepath.Join(root, "config.toml")
}

func readCodexFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(raw)
}

func readCodexDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	var doc map[string]any
	if _, err := toml.Decode(readCodexFile(t, path), &doc); err != nil {
		t.Fatalf("toml.Decode(%q) error = %v", path, err)
	}
	return doc
}

func codexServer(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	servers, ok := doc["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers = %#v, want table", doc["mcp_servers"])
	}
	server, ok := servers[name].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers[%q] = %#v, want table", name, servers[name])
	}
	return server
}

func assertManagedCodexServer(t *testing.T, server map[string]any, name string) {
	t.Helper()
	command := "/tmp/codex-e2e/bin/" + name
	if server["type"] != "stdio" {
		t.Fatalf("%s.type = %#v, want stdio", name, server["type"])
	}
	if server["command"] != command {
		t.Fatalf("%s.command = %#v, want %s", name, server["command"], command)
	}
	if server["cwd"] != "/tmp/codex-e2e/work" {
		t.Fatalf("%s.cwd = %#v, want /tmp/codex-e2e/work", name, server["cwd"])
	}
	env, ok := server["env"].(map[string]any)
	if !ok {
		t.Fatalf("%s.env = %#v, want table", name, server["env"])
	}
	if env["GO_AGENT_CTL_RPC_ADDR"] != "127.0.0.1:9123" {
		t.Fatalf("%s.env = %#v, want GO_AGENT_CTL_RPC_ADDR", name, env)
	}
}

func readStringSlice(t *testing.T, value any) []string {
	t.Helper()
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				t.Fatalf("slice item = %#v, want string", item)
			}
			out = append(out, text)
		}
		return out
	default:
		t.Fatalf("value = %#v, want string slice", value)
		return nil
	}
}

package thread

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestRenderMCPServerConfigMapCarriesGlobalPostgresServer(t *testing.T) {
	got := renderMCPServerConfigMap(map[string]contract.MCPServerConfig{
		"postgres": {
			Transport: "stdio",
			Command:   "mcp-server-postgres",
			Args: []string{
				"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
			},
		},
	})

	server, ok := got["postgres"].(map[string]any)
	if !ok {
		t.Fatalf("postgres server = %#v, want object", got["postgres"])
	}
	if server["transport"] != "stdio" || server["command"] != "mcp-server-postgres" {
		t.Fatalf("server = %#v, want stdio mcp-server-postgres config", server)
	}
	args, ok := server["args"].([]string)
	if !ok || len(args) != 1 || args[0] != "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable" {
		t.Fatalf("server args = %#v, want postgres database url", server["args"])
	}
}

func TestMergeConfiguredMCPServersSkipsDisabledSQLiteServer(t *testing.T) {
	disabled := false
	got, err := mergeConfiguredMCPServers(t.Context(), contract.MCPSnapshot{}, staticMCPServerConfigProvider{
		servers: map[string]contract.MCPServerConfig{
			"sqlite": {
				Transport: "stdio",
				Command:   "npx",
				Enabled:   &disabled,
			},
		},
	}, "/repo")
	if err != nil {
		t.Fatalf("mergeConfiguredMCPServers() error = %v", err)
	}
	if len(got.Servers) != 0 || len(got.ServerConfigs) != 0 {
		t.Fatalf("mergeConfiguredMCPServers() = %#v, want no disabled sqlite config", got)
	}
}

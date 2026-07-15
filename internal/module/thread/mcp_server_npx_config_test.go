package thread

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestRenderMCPServerConfigMapCarriesPlaywrightServer(t *testing.T) {
	got := renderMCPServerConfigMap(map[string]contract.MCPServerConfig{
		"playwright": {
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"@playwright/mcp@latest"},
		},
	})

	server, ok := got["playwright"].(map[string]any)
	if !ok {
		t.Fatalf("playwright server = %#v, want object", got["playwright"])
	}
	if server["transport"] != "stdio" || server["command"] != "npx" {
		t.Fatalf("server = %#v, want stdio npx config", server)
	}
	args, ok := server["args"].([]string)
	if !ok || len(args) != 1 || args[0] != "@playwright/mcp@latest" {
		t.Fatalf("server args = %#v, want playwright package", server["args"])
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

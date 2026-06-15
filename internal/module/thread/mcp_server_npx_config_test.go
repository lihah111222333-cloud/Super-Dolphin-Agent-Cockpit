package thread

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestRenderMCPServerConfigMapCarriesStdioNPXServer(t *testing.T) {
	got := renderMCPServerConfigMap(map[string]contract.MCPServerConfig{
		"postgres": {
			Transport: "stdio",
			Command:   "npx",
			Args: []string{
				"-y",
				"@modelcontextprotocol/server-postgres",
				"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
			},
		},
	})

	server, ok := got["postgres"].(map[string]any)
	if !ok {
		t.Fatalf("postgres server = %#v, want object", got["postgres"])
	}
	if server["transport"] != "stdio" || server["command"] != "npx" {
		t.Fatalf("server = %#v, want stdio npx config", server)
	}
	args, ok := server["args"].([]string)
	if !ok || len(args) != 3 || args[1] != "@modelcontextprotocol/server-postgres" {
		t.Fatalf("server args = %#v, want postgres npx args", server["args"])
	}
}

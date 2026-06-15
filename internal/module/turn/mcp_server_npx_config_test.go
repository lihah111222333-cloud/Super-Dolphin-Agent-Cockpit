package turn

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestMCPServerConfigBinariesCarriesStdioNPXServer(t *testing.T) {
	got := mcpServerConfigBinaries(map[string]contract.MCPServerConfig{
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

	if len(got) != 1 {
		t.Fatalf("binaries = %#v, want one postgres binary", got)
	}
	want := []string{
		"npx",
		"-y",
		"@modelcontextprotocol/server-postgres",
		"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
	}
	if got[0].Name != "postgres" || got[0].Type != "" || !equalStrings(got[0].Command, want) {
		t.Fatalf("binary = %#v, want stdio command %#v", got[0], want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

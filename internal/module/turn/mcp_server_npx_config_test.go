package turn

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestMCPServerConfigBinariesCarriesPlaywrightServer(t *testing.T) {
	got := mcpServerConfigBinaries(map[string]contract.MCPServerConfig{
		"playwright": {
			Transport: "stdio",
			Command:   "npx",
			Args:      []string{"@playwright/mcp@latest"},
		},
	})

	if len(got) != 1 {
		t.Fatalf("binaries = %#v, want one playwright binary", got)
	}
	want := []string{"npx", "@playwright/mcp@latest"}
	if got[0].Name != "playwright" || got[0].Type != "" || !equalStrings(got[0].Command, want) {
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

package shared

import "testing"

func TestConfigMCPBinariesAcceptsStdioNPXPostgresServer(t *testing.T) {
	got, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"postgres": map[string]any{
					"transport": "stdio",
					"command":   "npx",
					"args": []any{
						"-y",
						"@modelcontextprotocol/server-postgres",
						"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
					},
				},
			},
		},
	}, "mcpConfig")
	if err != nil {
		t.Fatalf("ConfigMCPBinaries() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "postgres" || got[0].Command[0] != "npx" {
		t.Fatalf("binaries = %#v, want postgres npx stdio binary", got)
	}
	if len(got[0].Command) != 4 || got[0].Command[2] != "@modelcontextprotocol/server-postgres" {
		t.Fatalf("postgres command = %#v, want npx postgres package command", got[0].Command)
	}
}

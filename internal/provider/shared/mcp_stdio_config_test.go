package shared

import "testing"

func TestConfigMCPBinariesAcceptsGlobalPostgresStdioServer(t *testing.T) {
	got, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"postgres": map[string]any{
					"transport": "stdio",
					"command":   "mcp-server-postgres",
					"args": []any{
						"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
					},
				},
			},
		},
	}, "mcpConfig")
	if err != nil {
		t.Fatalf("ConfigMCPBinaries() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "postgres" || got[0].Command[0] != "mcp-server-postgres" {
		t.Fatalf("binaries = %#v, want postgres global stdio binary", got)
	}
	if len(got[0].Command) != 2 || got[0].Command[1] != "postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable" {
		t.Fatalf("postgres command = %#v, want direct postgres command", got[0].Command)
	}
}

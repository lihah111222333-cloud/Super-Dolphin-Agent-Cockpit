package shared

import (
	"path/filepath"
	"testing"
)

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

func TestConfigMCPBinariesAcceptsNPXSQLiteStdioServer(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".super-dolphin", "super-dolphin.db")
	dsn := "sqlite:///" + filepath.ToSlash(dbPath)
	got, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"sqlite": map[string]any{
					"transport": "stdio",
					"command":   "npx",
					"args": []any{
						"-y",
						"@bytebase/dbhub",
						"--dsn=" + dsn,
					},
				},
			},
		},
	}, "mcpConfig")
	if err != nil {
		t.Fatalf("ConfigMCPBinaries() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "sqlite" || got[0].Command[0] != "npx" {
		t.Fatalf("binaries = %#v, want sqlite npx stdio binary", got)
	}
	if len(got[0].Command) != 4 || got[0].Command[2] != "@bytebase/dbhub" || got[0].Command[3] != "--dsn="+dsn {
		t.Fatalf("sqlite command = %#v, want dbhub sqlite npx package", got[0].Command)
	}
}

func TestConfigMCPBinariesAcceptsNPXPlaywrightStdioServer(t *testing.T) {
	got, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"playwright": map[string]any{
					"transport": "stdio",
					"command":   "npx",
					"args": []any{
						"@playwright/mcp@latest",
					},
				},
			},
		},
	}, "mcpConfig")
	if err != nil {
		t.Fatalf("ConfigMCPBinaries() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "playwright" || got[0].Command[0] != "npx" {
		t.Fatalf("binaries = %#v, want playwright npx stdio binary", got)
	}
	if len(got[0].Command) != 2 || got[0].Command[1] != "@playwright/mcp@latest" {
		t.Fatalf("playwright command = %#v, want playwright npx package", got[0].Command)
	}
}

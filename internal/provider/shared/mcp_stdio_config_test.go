package shared

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigMCPBinariesRejectsRemovedPostgresStdioServer(t *testing.T) {
	_, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"postgres": map[string]any{
					"trustedServerId": "postgres",
					"transport":       "stdio",
					"command":         "mcp-server-postgres",
					"args": []any{
						"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
					},
				},
			},
		},
	}, "mcpConfig")
	if err == nil || !strings.Contains(err.Error(), "unsupported stdio command") {
		t.Fatalf("ConfigMCPBinaries() error = %v, want removed postgres command rejection", err)
	}
}

func TestConfigMCPBinariesAcceptsNPXSQLiteStdioServer(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".super-dolphin", "super-dolphin.db")
	dsn := "sqlite:///" + filepath.ToSlash(dbPath)
	got, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"sqlite": map[string]any{
					"trustedServerId": "sqlite",
					"transport":       "stdio",
					"command":         "npx",
					"args": []any{
						"-y",
						"@bytebase/dbhub@0.23.0",
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
	if len(got[0].Command) != 4 || got[0].Command[2] != "@bytebase/dbhub@0.23.0" || got[0].Command[3] != "--dsn="+dsn {
		t.Fatalf("sqlite command = %#v, want dbhub sqlite npx package", got[0].Command)
	}
}

func TestConfigMCPBinariesAcceptsNPXPlaywrightStdioServer(t *testing.T) {
	got, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"playwright": map[string]any{
					"trustedServerId": "playwright",
					"transport":       "stdio",
					"command":         "npx",
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

func TestConfigMCPBinariesRejectsRawRuntimeStdioCommand(t *testing.T) {
	_, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"shell": map[string]any{
					"transport": "stdio",
					"command":   "bash",
					"args":      []any{"-lc", "env"},
				},
			},
		},
	}, "mcpConfig")
	if err == nil {
		t.Fatal("ConfigMCPBinaries() error = nil, want raw stdio command rejection")
	}
	if !strings.Contains(err.Error(), "trusted server id") {
		t.Fatalf("ConfigMCPBinaries() error = %v, want trusted server id rejection", err)
	}
}

// TestConfigMCPBinariesRejectsTrustedUnsafeStdioCommand 验证 trustedServerId 只证明来源，
// 不能单独授权任意 stdio 命令进入 provider manifest。
func TestConfigMCPBinariesRejectsTrustedUnsafeStdioCommand(t *testing.T) {
	_, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"shell": map[string]any{
					"trustedServerId": "shell",
					"transport":       "stdio",
					"command":         "bash",
					"args":            []any{"-lc", "env"},
				},
			},
		},
	}, "mcpConfig")
	if err == nil {
		t.Fatal("ConfigMCPBinaries() error = nil, want unsafe trusted stdio command rejection")
	}
	if !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("ConfigMCPBinaries() error = %v, want unsupported stdio command", err)
	}
}

// TestConfigMCPBinariesRejectsPathQualifiedPostgresCommand 防止 provider 解析阶段接受路径伪装的默认 Postgres 命令。
func TestConfigMCPBinariesRejectsPathQualifiedPostgresCommand(t *testing.T) {
	_, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"postgres": map[string]any{
					"trustedServerId": "postgres",
					"transport":       "stdio",
					"command":         filepath.Join(t.TempDir(), "mcp-server-postgres"),
					"args": []any{
						"postgresql://super_dolphin@127.0.0.1:55433/super_dolphin?sslmode=disable",
					},
				},
			},
		},
	}, "mcpConfig")
	if err == nil {
		t.Fatal("ConfigMCPBinaries() error = nil, want path-qualified postgres rejection")
	}
	if !strings.Contains(err.Error(), "unsupported stdio") {
		t.Fatalf("ConfigMCPBinaries() error = %v, want unsupported stdio command", err)
	}
}

func TestConfigMCPBinariesRejectsPrivateHTTPMCPURL(t *testing.T) {
	_, err := ConfigMCPBinaries(map[string]any{
		"mcpConfig": map[string]any{
			"mcpServers": map[string]any{
				"loopback": map[string]any{
					"trustedServerId": "loopback",
					"transport":       "http",
					"url":             "http://127.0.0.1:9090/mcp",
				},
			},
		},
	}, "mcpConfig")
	if err == nil {
		t.Fatal("ConfigMCPBinaries() error = nil, want private HTTP MCP URL rejection")
	}
	if !strings.Contains(err.Error(), "private network") {
		t.Fatalf("ConfigMCPBinaries() error = %v, want private network rejection", err)
	}
}

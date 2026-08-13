package main

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/BurntSushi/toml"
)

const wantDevSQLitePath = "$(HOME)/.super-dolphin/super-dolphin.db"

func TestNewUIDesktopShellExportsSQLiteAndDevMode(t *testing.T) {
	script := readScript(t, "../run-new-ui-desktop.sh")
	assertScriptContains(t, script, `SUPER_DOLPHIN_SQLITE_PATH="${SUPER_DOLPHIN_SQLITE_PATH:-$SUPER_DOLPHIN_HOME/super-dolphin.db}"`)
	assertScriptContains(t, script, `sqlite:       $SUPER_DOLPHIN_SQLITE_PATH`)
	assertScriptContains(t, script, `SUPER_DOLPHIN_RUNTIME_MODE="${SUPER_DOLPHIN_RUNTIME_MODE:-dev}"`)
	assertScriptContains(t, script, `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="${SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR:-$PROJECT_DIR}"`)
	assertScriptContains(t, script, `SUPER_DOLPHIN_DEPENDENCY_PROFILE="desktop_host"`)
	assertScriptContains(t, script, `SUPER_DOLPHIN_DEV_ENTRYPOINT="${SUPER_DOLPHIN_DEV_ENTRYPOINT:-run-new-ui-desktop.sh}"`)
	assertScriptDoesNotContain(t, script, "DATABASE_URL")
}

func TestNewUIDesktopPowerShellExportsSQLiteAndDevMode(t *testing.T) {
	script := readScript(t, "../run-new-ui-desktop.ps1")
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_SQLITE_PATH' -Value (Join-Path $env:SUPER_DOLPHIN_HOME 'super-dolphin.db')`)
	assertScriptContains(t, script, `Write-Host "  sqlite:       $($env:SUPER_DOLPHIN_SQLITE_PATH)"`)
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_RUNTIME_MODE' -Value 'dev'`)
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR' -Value $ProjectDir`)
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEPENDENCY_PROFILE' -Value 'desktop_host'`)
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_DEV_ENTRYPOINT' -Value 'run-new-ui-desktop.ps1'`)
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_HOME' -Value $script:DefaultSuperDolphinHome`)
	assertScriptContains(t, script, `Protect-OwnerOnlyDirectory -Path $parent`)
	assertScriptDoesNotContain(t, script, "DATABASE_URL")
}

func TestMakeDebugExportsSQLiteAndDevMode(t *testing.T) {
	makefile := readScript(t, "../Makefile")
	assertScriptContains(t, makefile, "DEV_SQLITE_PATH ?= "+wantDevSQLitePath)
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_SQLITE_PATH ?= $(DEV_SQLITE_PATH)")
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_RUNTIME_MODE := dev")
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR := $(CURDIR)")
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_DEPENDENCY_PROFILE := desktop_host")
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_DEV_ENTRYPOINT := make run-agent-terminal-debug")
	assertScriptDoesNotContain(t, makefile, "\nexport SUPER_DOLPHIN_RUNTIME_MODE := dev")
	assertScriptDoesNotContain(t, makefile, "\nexport SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR := $(CURDIR)")
	assertScriptDoesNotContain(t, makefile, "\nexport SUPER_DOLPHIN_DEV_ENTRYPOINT := make run-agent-terminal-debug")
	assertScriptContains(t, makefile, "run-agent-terminal-debug-plain")
}

func TestNewUIDesktopEntrypointsDoNotExportDatabaseURL(t *testing.T) {
	for name, source := range map[string]string{
		"run-new-ui-desktop.sh":  readScript(t, "../run-new-ui-desktop.sh"),
		"run-new-ui-desktop.ps1": readScript(t, "../run-new-ui-desktop.ps1"),
		"Makefile":               readScript(t, "../Makefile"),
	} {
		assertScriptDoesNotContain(t, source, "export DATABASE_URL")
		assertScriptDoesNotContain(t, source, "DEV_DATABASE_URL")
		assertScriptDoesNotContain(t, source, "$DevDatabaseUrl")
		t.Logf("%s keeps product DB config on SQLite envs", name)
	}
}

func TestStandaloneMCPRuntimeContractDocumented(t *testing.T) {
	for _, path := range []string{
		"../README.md",
		"../README.zh-CN.md",
		"../README.ja.md",
		"../README.ko.md",
		"../README.es.md",
		"../README.de.md",
	} {
		readme := readScript(t, path)
		for _, required := range []string{
			"SUPER_DOLPHIN_RUNTIME_MODE=dev",
			"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=",
			"SUPER_DOLPHIN_DEPENDENCY_PROFILE=production",
			"PowerShell",
			"WSL",
		} {
			assertScriptContains(t, readme, required)
		}
	}

	envExample := readScript(t, "../.env.example")
	assertScriptContains(t, envExample, "SUPER_DOLPHIN_RUNTIME_MODE=dev")
	assertScriptContains(t, envExample, "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=.")
	assertScriptContains(t, envExample, "SUPER_DOLPHIN_DEPENDENCY_PROFILE=desktop_host")
	assertScriptContains(t, envExample, "Standalone cmd/mcp-lsp and cmd/mcp-orch")

	clientGuide := readScript(t, "../docs/reference/mcp-lsp-standalone-clients.md")
	for _, required := range []string{
		"## Codex：`.codex/config.toml`",
		"## Claude Code：`.mcp.json`",
		"## Google Antigravity：`.agents/mcp_config.json`",
		"SUPER_DOLPHIN_RUNTIME_MODE",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR",
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE",
		"Windows 原生 PowerShell/Desktop",
		"WSL 内客户端",
		"GOTOOLCHAIN=auto",
		"path_outside_workspace",
	} {
		assertScriptContains(t, clientGuide, required)
	}
	assertStandaloneMCPExamplesParse(t, clientGuide)
	for _, required := range []string{"WSL_INTEROP", "/proc/sys/kernel/osrelease", "PROCESSOR_ARCHITECTURE", "uname -m"} {
		assertScriptContains(t, clientGuide, required)
	}

	for _, path := range []string{
		"../bin/LSP/README.md",
		"../bin/LSP/AGENTS.md",
		"../bin/LSP/mcp-lsp-project-config-skill/SKILL.md",
		"../bin/LSP/mcp-lsp-project-config-skill/references/provider-configs.md",
	} {
		artifactDoc := readScript(t, path)
		for _, required := range []string{
			"SUPER_DOLPHIN_RUNTIME_MODE",
			"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR",
			"SUPER_DOLPHIN_DEPENDENCY_PROFILE",
			"PowerShell",
			"WSL",
			"GOTOOLCHAIN=auto",
		} {
			assertScriptContains(t, artifactDoc, required)
		}
	}
	for _, path := range []string{
		"../bin/LSP/README.md",
		"../bin/LSP/mcp-lsp-project-config-skill/SKILL.md",
		"../bin/LSP/mcp-lsp-project-config-skill/references/provider-configs.md",
	} {
		detectionDoc := readScript(t, path)
		for _, required := range []string{"WSL_INTEROP", "/proc/sys/kernel/osrelease"} {
			assertScriptContains(t, detectionDoc, required)
		}
	}
}

func assertStandaloneMCPExamplesParse(t *testing.T, guide string) {
	t.Helper()
	jsonBlocks := regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindAllStringSubmatch(guide, -1)
	if len(jsonBlocks) != 2 {
		t.Fatalf("standalone MCP guide JSON example count = %d, want 2", len(jsonBlocks))
	}
	for _, match := range jsonBlocks {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(match[1]), &decoded); err != nil {
			t.Fatalf("parse standalone MCP JSON example: %v", err)
		}
		assertStandaloneMCPServerEnv(t, decoded, "mcpServers")
	}

	tomlBlocks := regexp.MustCompile("(?s)```toml\\n(.*?)\\n```").FindAllStringSubmatch(guide, -1)
	if len(tomlBlocks) != 1 {
		t.Fatalf("standalone MCP guide TOML example count = %d, want 1", len(tomlBlocks))
	}
	var decoded map[string]any
	if _, err := toml.Decode(tomlBlocks[0][1], &decoded); err != nil {
		t.Fatalf("parse standalone MCP TOML example: %v", err)
	}
	assertStandaloneMCPServerEnv(t, decoded, "mcp_servers")
}

func assertStandaloneMCPServerEnv(t *testing.T, decoded map[string]any, serversKey string) {
	t.Helper()
	servers, ok := decoded[serversKey].(map[string]any)
	if !ok {
		t.Fatalf("standalone MCP example missing %s object", serversKey)
	}
	lsp, ok := servers["lsp"].(map[string]any)
	if !ok {
		t.Fatal("standalone MCP example missing lsp server")
	}
	env, ok := lsp["env"].(map[string]any)
	if !ok {
		t.Fatal("standalone MCP lsp server missing env object")
	}
	for key, want := range map[string]string{
		"SUPER_DOLPHIN_RUNTIME_MODE":       "dev",
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE": "production",
	} {
		if got, ok := env[key].(string); !ok || got != want {
			t.Fatalf("standalone MCP env %s = %#v, want %q", key, env[key], want)
		}
	}
	for _, key := range []string{
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR",
		"GO_AGENT_LSP_ROOT",
		"GO_AGENT_LSP_ROOTS",
	} {
		if got, ok := env[key].(string); !ok || got == "" {
			t.Fatalf("standalone MCP env %s = %#v, want non-empty string", key, env[key])
		}
	}
}

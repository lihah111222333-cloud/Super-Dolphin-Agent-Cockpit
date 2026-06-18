package main

import "testing"

const wantDevSQLitePath = "$(HOME)/.super-dolphin/super-dolphin.db"

func TestNewUIDesktopShellExportsSQLiteAndDevMode(t *testing.T) {
	script := readScript(t, "../run-new-ui-desktop.sh")
	assertScriptContains(t, script, `SUPER_DOLPHIN_SQLITE_PATH="${SUPER_DOLPHIN_SQLITE_PATH:-$SUPER_DOLPHIN_HOME/super-dolphin.db}"`)
	assertScriptContains(t, script, `sqlite:       $SUPER_DOLPHIN_SQLITE_PATH`)
	assertScriptContains(t, script, `SUPER_DOLPHIN_RUNTIME_MODE="${SUPER_DOLPHIN_RUNTIME_MODE:-dev}"`)
	assertScriptContains(t, script, `SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR="${SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR:-$PROJECT_DIR}"`)
	assertScriptContains(t, script, `SUPER_DOLPHIN_DEV_ENTRYPOINT="${SUPER_DOLPHIN_DEV_ENTRYPOINT:-run-new-ui-desktop.sh}"`)
	assertScriptDoesNotContain(t, script, "DATABASE_URL")
}

func TestNewUIDesktopPowerShellExportsSQLiteAndDevMode(t *testing.T) {
	script := readScript(t, "../run-new-ui-desktop.ps1")
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_SQLITE_PATH' -Value (Join-Path $env:SUPER_DOLPHIN_HOME 'super-dolphin.db')`)
	assertScriptContains(t, script, `Write-Host "  sqlite:       $($env:SUPER_DOLPHIN_SQLITE_PATH)"`)
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_RUNTIME_MODE' -Value 'dev'`)
	assertScriptContains(t, script, `Set-DefaultEnv -Name 'SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR' -Value $ProjectDir`)
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

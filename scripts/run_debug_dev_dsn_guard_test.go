package main

import (
	"os"
	"strings"
	"testing"
)

const wantDevSQLitePath = "$(HOME)/.super-dolphin/super-dolphin.db"

func TestRunDebugShellExportsSQLiteAndDevMode(t *testing.T) {
	script := readScript(t, "../run-debug.sh")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_SQLITE_PATH=\"${SUPER_DOLPHIN_SQLITE_PATH:-$SUPER_DOLPHIN_HOME/super-dolphin.db}\"")
	assertScriptContains(t, script, "SQLite DB: $SUPER_DOLPHIN_SQLITE_PATH")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_RUNTIME_MODE=dev")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=\"$BUILD_DIR\"")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_DEV_ENTRYPOINT=run-debug.sh")
	assertScriptDoesNotContain(t, script, "export DATABASE_URL=")
	for _, stale := range []string{
		"psql",
		"DB_URL",
		"schema_migrations",
		"PGCONNECT",
		"PostgreSQL",
		"postgres",
	} {
		assertScriptDoesNotContain(t, script, stale)
	}
}

func TestPowerShellRunDebugExportsSQLiteAndDevMode(t *testing.T) {
	script := readScript(t, "../run-debug.ps1")
	assertScriptContains(t, script, "$env:SUPER_DOLPHIN_SQLITE_PATH = Join-Path (Get-SuperDolphinHome) 'super-dolphin.db'")
	assertScriptContains(t, script, "Write-Host \"> SQLite DB: $($env:SUPER_DOLPHIN_SQLITE_PATH)\"")
	assertScriptContains(t, script, "$env:SUPER_DOLPHIN_RUNTIME_MODE = 'dev'")
	assertScriptContains(t, script, "$env:SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = $BuildDir")
	assertScriptContains(t, script, "$env:SUPER_DOLPHIN_DEV_ENTRYPOINT = 'run-debug.ps1'")
	assertScriptDoesNotContain(t, script, "$DevDatabaseUrl =")
	for _, stale := range []string{
		"DATABASE_URL",
		"POSTGRES_CONNECTION_STRING",
		"Get-EffectiveDatabaseUrl",
		"Format-DatabaseUrlForLog",
		"Get-DatabaseEndpoint",
		"Find-PsqlCommand",
		"Get-PostgresPortHint",
		"Get-MaintenanceDatabaseUrl",
		"Test-PsqlDatabaseConnect",
		"Test-PsqlCanCreateDatabase",
		"Assert-DatabaseConfigured",
		"PostgreSQL TCP",
	} {
		assertScriptDoesNotContain(t, script, stale)
	}
}

func TestMakeDebugExportsSQLiteAndDevMode(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
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

func TestRunDebugShellRebuildsKilledCodeSizeGuardCache(t *testing.T) {
	script := readScript(t, "../run-debug.sh")
	assertScriptContains(t, script, "rebuild_code_size_guard()")
	assertScriptContains(t, script, "code_size_guard 缓存执行失败")
	assertScriptContains(t, script, `rm -f "$_GUARD_BIN" "$_GUARD_HASH_FILE"`)
	assertScriptContains(t, script, `[ "$_GUARD_STATUS" -eq 126 ] || [ "$_GUARD_STATUS" -eq 137 ]`)
	assertScriptOrder(t, script, "rebuild_code_size_guard()", "if [ ! -f \"$_GUARD_BIN\" ]")
	assertScriptOrder(t, script, "code_size_guard 缓存执行失败", "rm -f \"$_GUARD_BIN\" \"$_GUARD_HASH_FILE\"")
	assertScriptOrder(t, script, "rm -f \"$_GUARD_BIN\" \"$_GUARD_HASH_FILE\"", "  rebuild_code_size_guard\n  if \"$_GUARD_BIN\"; then")
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func TestDebugEntrypointsDoNotExportDatabaseURL(t *testing.T) {
	sources := map[string]string{
		"run-debug.sh":  readScript(t, "../run-debug.sh"),
		"run-debug.ps1": readScript(t, "../run-debug.ps1"),
		"Makefile":      readRepoFile(t, "../Makefile"),
	}
	for name, source := range sources {
		if strings.Contains(source, "export DATABASE_URL") || strings.Contains(source, "DEV_DATABASE_URL") || strings.Contains(source, "$DevDatabaseUrl") {
			t.Fatalf("%s still exports PostgreSQL DATABASE_URL", name)
		}
	}
}

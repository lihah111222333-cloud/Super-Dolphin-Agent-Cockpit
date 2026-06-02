package main

import (
	"os"
	"strings"
	"testing"
)

const wantDevDatabaseURL = "postgres://postgres:123@127.0.0.1:5432/go_agent_v2?sslmode=disable"

func TestRunDebugShellExportsDevDSNAndDevMode(t *testing.T) {
	script := readScript(t, "../run-debug.sh")
	assertScriptContains(t, script, "DEV_DATABASE_URL=\""+wantDevDatabaseURL+"\"")
	assertScriptContains(t, script, "export DATABASE_URL=\"${DATABASE_URL:-$DEV_DATABASE_URL}\"")
	assertScriptContains(t, script, "DB_URL=\"$DATABASE_URL\"")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_RUNTIME_MODE=dev")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=\"$BUILD_DIR\"")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_DEV_ENTRYPOINT=run-debug.sh")
	assertScriptOrder(t, script, "export DATABASE_URL=\"${DATABASE_URL:-$DEV_DATABASE_URL}\"", "DB_URL=\"$DATABASE_URL\"")
}

func TestPowerShellRunDebugPreservesUserDSNAndExportsDevMode(t *testing.T) {
	script := readScript(t, "../run-debug.ps1")
	assertScriptContains(t, script, "$DevDatabaseUrl = '"+wantDevDatabaseURL+"'")
	assertScriptContains(t, script, "if (-not $env:DATABASE_URL) { $env:DATABASE_URL = $DevDatabaseUrl }")
	assertScriptContains(t, script, "$DbUrl = $env:DATABASE_URL")
	assertScriptContains(t, script, "$env:SUPER_DOLPHIN_RUNTIME_MODE = 'dev'")
	assertScriptContains(t, script, "$env:SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = $BuildDir")
	assertScriptContains(t, script, "$env:SUPER_DOLPHIN_DEV_ENTRYPOINT = 'run-debug.ps1'")
	assertScriptOrder(t, script, "if (-not $env:DATABASE_URL) { $env:DATABASE_URL = $DevDatabaseUrl }", "$DbUrl = $env:DATABASE_URL")
}

func TestMakeDebugExportsSameDevDSNAndDevMode(t *testing.T) {
	makefile := readRepoFile(t, "../Makefile")
	assertScriptContains(t, makefile, "DEV_DATABASE_URL ?= "+wantDevDatabaseURL)
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export DATABASE_URL ?= $(DEV_DATABASE_URL)")
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_RUNTIME_MODE := dev")
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR := $(CURDIR)")
	assertScriptContains(t, makefile, "run-agent-terminal-debug run-agent-terminal-debug-plain: export SUPER_DOLPHIN_DEV_ENTRYPOINT := make run-agent-terminal-debug")
	assertScriptDoesNotContain(t, makefile, "\nexport SUPER_DOLPHIN_RUNTIME_MODE := dev")
	assertScriptDoesNotContain(t, makefile, "\nexport SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR := $(CURDIR)")
	assertScriptDoesNotContain(t, makefile, "\nexport SUPER_DOLPHIN_DEV_ENTRYPOINT := make run-agent-terminal-debug")
	assertScriptContains(t, makefile, "run-agent-terminal-debug-plain")
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func TestDevDSNConstantMatchesAcrossEntrypoints(t *testing.T) {
	sources := map[string]string{
		"run-debug.sh":  readScript(t, "../run-debug.sh"),
		"run-debug.ps1": readScript(t, "../run-debug.ps1"),
		"Makefile":      readRepoFile(t, "../Makefile"),
	}
	for name, source := range sources {
		if count := strings.Count(source, wantDevDatabaseURL); count == 0 {
			t.Fatalf("%s missing dev DSN %q", name, wantDevDatabaseURL)
		}
	}
}

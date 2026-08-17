//go:build darwin

package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func sqliteReleaseGatePackageSmokeCommand(t *testing.T, stage sqliteReleaseGateUnsignedPackage) *exec.Cmd {
	t.Helper()
	command := exec.Command(stage.entrypoint)
	command.Dir = stage.root
	return sqliteReleaseGatePackageSmokeCommandWithWorkerXvfb(t, command)
}

func writeSQLiteReleaseGateUnsignedPackage(t *testing.T) sqliteReleaseGateUnsignedPackage {
	t.Helper()
	stageDir := t.TempDir()
	app := filepath.Join(stageDir, "Super Dolphin.app")
	root := filepath.Join(app, "Contents", "Resources")
	return writeSQLiteReleaseGateUnsignedPackageWithLayout(t, sqliteReleaseGateUnsignedPackage{
		root:        root,
		entrypoint:  filepath.Join(app, "Contents", "MacOS", "agent-terminal"),
		binaryNames: []string{"mcp-orch", "mcp-lsp", "mcp-ida", "codex", "gopls"},
	}, false)
}

func sqlitePackageSmokeCommandTarget(stage sqliteReleaseGateUnsignedPackage) string {
	return stage.entrypoint
}

func executableForPackageSmoke(name string) string {
	return name
}

func samePackageSmokePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func unusedPackagePeerBody(name string) string {
	return "#!/bin/sh\necho sqlite release gate smoke unused peer " + name + "\nexit ${SUPER_DOLPHIN_UNUSED_PEER_STATUS:-0}\n"
}

func packageSmokeLauncherBody() string {
	return "#!/usr/bin/env bash\nset -euo pipefail\nhere=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")\" && pwd)\"\nexport SUPER_DOLPHIN_PACKAGE_ROOT=\"$here\"\nexport PROJECT_ROOT=\"$here\"\nexport PATH=\"$here/bin:${PATH:-}\"\nexport SUPER_DOLPHIN_RUNTIME_MODE=packaged\nexport SUPER_DOLPHIN_PACKAGED_LAUNCHER=1\nexec \"$here/bin/agent-terminal\" \"$@\"\n"
}

func assertSQLitePackageSmokeCommand(t *testing.T, fixture sqlitePackageSmokeCommandFixture) {
	t.Helper()
	if fixture.command.Path != fixture.xvfbRun {
		t.Fatalf("remote worker smoke command = %q, want fixed Xvfb runner %q", fixture.command.Path, fixture.xvfbRun)
	}
	target := sqlitePackageSmokeCommandTarget(fixture.stage)
	if len(fixture.command.Args) != 4 || fixture.command.Args[3] != target || fixture.command.Args[1] != "--auto-servernum" || fixture.command.Args[2] != "--server-args=-screen 0 1280x1024x24" {
		t.Fatalf("remote worker Xvfb arguments = %q", fixture.command.Args[1:])
	}
	if fixture.command.Dir != fixture.stage.root {
		t.Fatalf("remote worker Xvfb directory = %q, want %q", fixture.command.Dir, fixture.stage.root)
	}
}

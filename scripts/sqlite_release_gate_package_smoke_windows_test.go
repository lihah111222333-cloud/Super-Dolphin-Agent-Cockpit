//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sqliteReleaseGatePackageSmokeCommand 只在 Windows 选择 cmd launcher；非 Windows
// 的入口和显示服务包装由各自的精确 build tag 文件提供。
func sqliteReleaseGatePackageSmokeCommand(t *testing.T, stage sqliteReleaseGateUnsignedPackage) *exec.Cmd {
	t.Helper()
	command := exec.Command("cmd", "/c", filepath.Base(stage.launchers[0]))
	command.Dir = stage.root
	return command
}

func writeSQLiteReleaseGateUnsignedPackage(t *testing.T) sqliteReleaseGateUnsignedPackage {
	t.Helper()
	stageDir := t.TempDir()
	root := filepath.Join(stageDir, fmt.Sprintf("super-dolphin-0.1.0-windows-%s", runtime.GOARCH))
	return writeSQLiteReleaseGateUnsignedPackageWithLayout(t, sqliteReleaseGateUnsignedPackage{
		root:        root,
		entrypoint:  filepath.Join(root, "bin", "agent-terminal.exe"),
		launchers:   []string{filepath.Join(root, "run.cmd"), filepath.Join(root, "run.ps1")},
		binaryNames: []string{"agent-terminal.exe", "mcp-orch.exe", "mcp-lsp.exe", "mcp-ida.exe", "codex.exe", "gopls.exe"},
	}, true)
}

func executableForPackageSmoke(name string) string {
	return name + ".exe"
}

func samePackageSmokePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func unusedPackagePeerBody(name string) string {
	return "@echo off\r\necho sqlite release gate smoke unused peer " + name + "\r\nexit /b %SUPER_DOLPHIN_UNUSED_PEER_STATUS%\r\n"
}

func packageSmokeLauncherBody() string {
	return "@echo off\r\nsetlocal\r\nset \"here=%~dp0\"\r\nfor %%I in (\"%here%.\") do set \"here=%%~fI\"\r\nset \"SUPER_DOLPHIN_PACKAGE_ROOT=%here%\"\r\nset \"PROJECT_ROOT=%here%\"\r\nset \"PATH=%here%\\bin;%PATH%\"\r\nset \"SUPER_DOLPHIN_RUNTIME_MODE=packaged\"\r\nset \"SUPER_DOLPHIN_PACKAGED_LAUNCHER=1\"\r\n\"%here%\\bin\\agent-terminal.exe\" %*\r\nexit /b %ERRORLEVEL%\r\n"
}

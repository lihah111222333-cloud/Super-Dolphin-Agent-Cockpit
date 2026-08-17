//go:build windows

package main

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// createSQLiteDiagnosticsDirectoryAlias 在 Windows 上直接创建目录 junction。
// junction 同样经过 EvalSymlinks 解析，但不要求 SeCreateSymbolicLinkPrivilege，
// 因而该路径别名测试不会把宿主开发者模式误当成产品 ACL 前置条件。
func createSQLiteDiagnosticsDirectoryAlias(t *testing.T, target, alias string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "cmd.exe", "/d", "/c", "mklink", "/J", alias, target).CombinedOutput()
	if err != nil {
		t.Fatalf("create Windows SQLite diagnostics junction: %v; output=%q", err, string(output))
	}
}

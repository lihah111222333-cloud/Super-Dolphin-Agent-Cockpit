package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixCommitAllowsAgentTerminalRecoveryIntegrationTest(t *testing.T) {
	tests := []struct {
		name           string
		productionPath string
	}{
		{name: "application recovery graph", productionPath: "internal/app/recovery_graph.go"},
		{name: "rollback restart", productionPath: "internal/platform/appupdaterecovery/rollback_restart.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := prepareFixTestGuardRepo(t)
			writeFixTestGuardFile(t, root, tt.productionPath, "package recoveryfixture\n\nfunc recoverApplication() {}\n")
			writeFixTestGuardFile(t, root, "cmd/agent-terminal/main_recovery_test.go", "package main\n\nimport \"testing\"\n\nfunc TestProductionRecovery(t *testing.T) {}\n")
			runFixTestGuardGit(t, root, "add", ".")

			msgFile := filepath.Join(root, "COMMIT_EDITMSG")
			if err := os.WriteFile(msgFile, []byte("修复 Recovery 生产回滚助手入口\n"), 0o644); err != nil {
				t.Fatalf("write commit message: %v", err)
			}

			out, err := runFixTestGuard(t, root, "--cached", msgFile)
			if err != nil {
				t.Fatalf("guard rejected agent-terminal recovery integration test: %v\n%s", err, out)
			}
			assertOutputContainsAll(t, out, "fix-test guard OK")
		})
	}
}

func TestFixCommitRejectsAgentTerminalTestForUnrelatedInternalPackage(t *testing.T) {
	root := prepareFixTestGuardRepo(t)
	writeFixTestGuardFile(t, root, "internal/provider/provider.go", "package provider\n\nfunc provide() {}\n")
	writeFixTestGuardFile(t, root, "cmd/agent-terminal/main_recovery_test.go", "package main\n\nimport \"testing\"\n\nfunc TestProductionRecovery(t *testing.T) {}\n")
	runFixTestGuardGit(t, root, "add", ".")

	msgFile := filepath.Join(root, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("修复 provider 初始化\n"), 0o644); err != nil {
		t.Fatalf("write commit message: %v", err)
	}

	out, err := runFixTestGuard(t, root, "--cached", msgFile)
	if err == nil {
		t.Fatalf("guard accepted agent-terminal test for unrelated internal package\n%s", out)
	}
	assertOutputContainsAll(t, out, "fix 提交缺少锁定 bug 的测试", "修复 provider 初始化")
}

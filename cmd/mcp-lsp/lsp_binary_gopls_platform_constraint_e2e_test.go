//go:build e2e && (darwin || linux)

package main

import (
	"path/filepath"
	"testing"
)

const wjbootCandidatePublisherRelativePath = "backend/cmd/ai_maintenance_candidate_publisher/main.go"

// TestMcpLSPBinaryWJBootCandidatePublisherPlatformConstraintKeepsRootCohortConfig
// 固化现场红测：同一 WJBoot root 先诊断普通 Go 文件，再诊断 darwin/linux 文件；
// 平台约束不得变成自定义 -tags 并制造第二份 immutable root-cohort config。
func TestMcpLSPBinaryWJBootCandidatePublisherPlatformConstraintKeepsRootCohortConfig(t *testing.T) {
	skipLSPBinaryResidualE2EInShortMode(t)
	requireRealGopls(t)

	wjbootRoot := canonicalToolTestRoot(t, t.TempDir())
	writeLSPBinaryFixture(t, filepath.Join(wjbootRoot, "go.mod"), "module example.com/wjboot-red\n\ngo 1.26.0\n")
	ordinaryTarget := filepath.Join(wjbootRoot, "backend", "ordinary.go")
	writeLSPBinaryFixture(t, ordinaryTarget, "package backend\n\nfunc Ordinary() {}\n")
	target := filepath.Join(wjbootRoot, wjbootCandidatePublisherRelativePath)
	writeLSPBinaryFixture(t, target, "//go:build darwin || linux\n\npackage main\n\nfunc main() {}\n")

	client := startPrebuiltLSPBinaryClient(t, goWorktreeLSPBinaryUnderTest(t), wjbootRoot)
	ordinaryDiagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": ordinaryTarget,
	})
	if ordinaryDiagnostics.IsError {
		t.Fatalf("WJBoot ordinary diagnostics returned MCP error; text=%q stderr=%s",
			ordinaryDiagnostics.ContentText(), client.stderr.String())
	}
	diagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	})
	if diagnostics.IsError {
		t.Fatalf("WJBoot candidate publisher diagnostics returned MCP error; text=%q stderr=%s",
			diagnostics.ContentText(), client.stderr.String())
	}
}

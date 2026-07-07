package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRenameScriptMainDryRunWritesReportWithoutMutatingFiles(t *testing.T) {
	root := t.TempDir()
	targetFile := filepath.Join(root, "demo.go")
	original := "package demo\n\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk\"\n"
	if err := os.WriteFile(targetFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write demo.go: %v", err)
	}
	reportPath := filepath.Join(root, "out", "report.json")

	stdout, stderr, err := runRenameScript(t, "--root", root, "--report", reportPath)
	if err != nil {
		t.Fatalf("run rename script err=%v stdout=%s stderr=%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "mode=dry-run") {
		t.Fatalf("stdout = %q, want dry-run summary", stdout)
	}
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read demo.go: %v", err)
	}
	if string(data) != original {
		t.Fatalf("dry-run should not mutate files: got %q want %q", string(data), original)
	}
	assertRenameReportSummary(t, reportPath, "dry-run", 1, 1)
}

func TestRenameScriptMainRejectsMutuallyExclusiveModes(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, err := runRenameScript(t, "--root", root, "--dry-run", "--apply")
	if err == nil {
		t.Fatalf("expected mutually exclusive modes to fail; stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "--dry-run and --apply are mutually exclusive") {
		t.Fatalf("stderr = %q, want mutual exclusion message", stderr)
	}
}

func TestRenameScriptMainDefaultDryRunTrimsInputsAndPrintsReportPath(t *testing.T) {
	root := t.TempDir()
	targetFile := filepath.Join(root, "demo.go")
	original := "package demo\n\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk\"\n"
	if err := os.WriteFile(targetFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write demo.go: %v", err)
	}
	reportPath := filepath.Join(root, "trimmed", "report.json")

	stdout, stderr, err := runRenameScript(t, "--root", "  "+root+"  ", "--report", "  "+reportPath+"  ")
	if err != nil {
		t.Fatalf("run rename script err=%v stdout=%s stderr=%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "mode=dry-run files=1 replacements=1") {
		t.Fatalf("stdout = %q, want dry-run summary line", stdout)
	}
	if !strings.Contains(stdout, "report="+reportPath) {
		t.Fatalf("stdout = %q, want trimmed report path", stdout)
	}
	report := readRenameReport(t, reportPath)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	if got := report["root"].(string); got != rootAbs {
		t.Fatalf("report root = %q, want %q", got, rootAbs)
	}
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read demo.go after dry-run: %v", err)
	}
	if string(data) != original {
		t.Fatalf("default dry-run should not mutate files: got %q want %q", string(data), original)
	}
}

func assertRenameReportSummary(t *testing.T, path, wantMode string, wantFiles, wantReplacements int) {
	t.Helper()

	report := readRenameReport(t, path)
	if got := strings.TrimSpace(report["mode"].(string)); got != wantMode {
		t.Fatalf("report mode = %q, want %q", got, wantMode)
	}
	if got := int(report["totalFiles"].(float64)); got != wantFiles {
		t.Fatalf("report totalFiles = %d, want %d", got, wantFiles)
	}
	if got := int(report["totalReplacements"].(float64)); got != wantReplacements {
		t.Fatalf("report totalReplacements = %d, want %d", got, wantReplacements)
	}
}

func readRenameReport(t *testing.T, path string) map[string]any {
	t.Helper()

	reportData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	return report
}

func TestRenameScriptMainApplyRewritesImports(t *testing.T) {
	root := t.TempDir()
	targetFile := filepath.Join(root, "demo.go")
	original := strings.Join([]string{
		"package demo",
		"",
		"import (",
		"	_ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk\"",
		"	runtimepkg \"github.com/multi-agent/go-agent-v2/pkg/codexsdk/service/runtime\"",
		")",
		"",
		"var _ = runtimepkg.TurnRuntime{}",
		"",
	}, "\n")
	if err := os.WriteFile(targetFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write demo.go: %v", err)
	}

	stdout, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script err=%v stdout=%s stderr=%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "mode=apply") {
		t.Fatalf("stdout = %q, want apply summary", stdout)
	}
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read demo.go: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "/pkg/codexsdk") {
		t.Fatalf("apply mode should remove old import root, got %q", content)
	}
	expectedImportRoot := "/pkg/" + "agentsdk"
	if !strings.Contains(content, expectedImportRoot) {
		t.Fatalf("apply mode should rewrite to agentsdk import root, got %q", content)
	}
}

func TestRenameScriptMainApplyPreservesFilePermissions(t *testing.T) {
	root := t.TempDir()
	targetFile := filepath.Join(root, "demo.go")
	original := "package demo\n\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk/service/runtime\"\n"
	if err := os.WriteFile(targetFile, []byte(original), 0o600); err != nil {
		t.Fatalf("write demo.go: %v", err)
	}
	beforeInfo, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("stat demo.go before apply: %v", err)
	}

	stdout, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script err=%v stdout=%s stderr=%s", err, stdout, stderr)
	}
	afterInfo, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("stat demo.go after apply: %v", err)
	}
	if afterInfo.Mode().Perm() != beforeInfo.Mode().Perm() {
		t.Fatalf("file mode after apply = %o, want %o", afterInfo.Mode().Perm(), beforeInfo.Mode().Perm())
	}
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read demo.go after apply: %v", err)
	}
	if strings.Contains(string(data), "/pkg/codexsdk") {
		t.Fatalf("apply mode should rewrite old import root, got %q", string(data))
	}
}

func TestRenameScriptMainFailsWhenReportPathParentIsFile(t *testing.T) {
	root := t.TempDir()
	targetFile := filepath.Join(root, "demo.go")
	original := "package demo\n\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk\"\n"
	if err := os.WriteFile(targetFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write demo.go: %v", err)
	}
	blocker := filepath.Join(root, "report-blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write report blocker: %v", err)
	}

	stdout, stderr, err := runRenameScript(t, "--root", root, "--report", filepath.Join(blocker, "report.json"))
	if err == nil {
		t.Fatalf("expected report path failure; stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "write report failed:") {
		t.Fatalf("stderr = %q, want report failure", stderr)
	}
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read demo.go: %v", err)
	}
	if string(data) != original {
		t.Fatalf("report failure must not mutate files: got %q want %q", string(data), original)
	}
}

func runRenameScript(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	scriptDir := filepath.Dir(thisFile)
	cmdArgs := append([]string{"run", "./rename_codexsdk_to_agentsdk.go"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = scriptDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

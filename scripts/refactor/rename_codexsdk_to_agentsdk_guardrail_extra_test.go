package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameScriptMainSkipsNonGoFilesAndSortsReport(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package demo\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk\"\n"), 0o644); err != nil {
		t.Fatalf("write b.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package demo\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk/service/runtime\"\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("github.com/multi-agent/go-agent-v2/pkg/codexsdk"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	reportPath := filepath.Join(root, "report.json")
	_, stderr, err := runRenameScript(t, "--root", root, "--report", reportPath)
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	names := reportFileNames(t, data, 2)
	if got := strings.Join(names, ","); got != "a.go,b.go" {
		t.Fatalf("report order = %s, want a.go,b.go", got)
	}
	notes, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatalf("read notes.txt: %v", err)
	}
	if !strings.Contains(string(notes), "/pkg/codexsdk") {
		t.Fatalf("non-go file should remain untouched, got %q", string(notes))
	}
}

func reportFileNames(t *testing.T, data []byte, wantCount int) []string {
	t.Helper()

	var rep map[string]any
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	files, ok := rep["files"].([]any)
	if !ok || len(files) != wantCount {
		t.Fatalf("report files = %#v, want %d entries", rep["files"], wantCount)
	}
	names := make([]string, 0, len(files))
	for _, item := range files {
		names = append(names, reportFileName(t, item))
	}
	return names
}

func reportFileName(t *testing.T, item any) string {
	t.Helper()

	row, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("unexpected file row: %#v", item)
	}
	name, _ := row["file"].(string)
	return name
}

func TestRenameScriptMainFailsOnInvalidGoFile(t *testing.T) {
	root := t.TempDir()
	badFile := filepath.Join(root, "broken.go")
	if err := os.WriteFile(badFile, []byte("package demo\nimport (\n"), 0o644); err != nil {
		t.Fatalf("write broken.go: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root)
	if err == nil {
		t.Fatal("expected invalid Go file to fail scan")
	}
	if !strings.Contains(stderr, "collect edits for broken.go") {
		t.Fatalf("stderr = %q, want collect edits failure", stderr)
	}
}

func TestRenameScriptMainApplyDoesNotMutateFilesWhenScanFails(t *testing.T) {
	root := t.TempDir()
	goodFile := filepath.Join(root, "a.go")
	original := "package demo\n\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk\"\n"
	if err := os.WriteFile(goodFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	badFile := filepath.Join(root, "z_broken.go")
	if err := os.WriteFile(badFile, []byte("package demo\nimport (\n"), 0o644); err != nil {
		t.Fatalf("write z_broken.go: %v", err)
	}

	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err == nil {
		t.Fatal("expected apply mode to fail when scan hits invalid Go file")
	}
	if !strings.Contains(stderr, "collect edits for z_broken.go") {
		t.Fatalf("stderr = %q, want collect edits failure", stderr)
	}
	data, err := os.ReadFile(goodFile)
	if err != nil {
		t.Fatalf("read a.go: %v", err)
	}
	if string(data) != original {
		t.Fatalf("apply failure must roll back prior files: got %q want %q", string(data), original)
	}
}

func TestRenameScriptMainApplyRollsBackWhenReportWriteFails(t *testing.T) {
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

	_, stderr, err := runRenameScript(t, "--root", root, "--apply", "--report", filepath.Join(blocker, "report.json"))
	if err == nil {
		t.Fatal("expected apply mode to fail when report write fails")
	}
	if !strings.Contains(stderr, "write report failed:") {
		t.Fatalf("stderr = %q, want report failure", stderr)
	}
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("read demo.go: %v", err)
	}
	if string(data) != original {
		t.Fatalf("report failure must roll back apply changes: got %q want %q", string(data), original)
	}
}

func TestRenameScriptMainApplyRollsBackWhenFileWriteFails(t *testing.T) {
	root := t.TempDir()
	firstFile := filepath.Join(root, "a.go")
	firstOriginal := "package demo\n\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk\"\n"
	if err := os.WriteFile(firstFile, []byte(firstOriginal), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	blockedFile := filepath.Join(root, "z_blocked.go")
	blockedOriginal := "package demo\n\nimport _ \"github.com/multi-agent/go-agent-v2/pkg/codexsdk/service/runtime\"\n"
	if err := os.WriteFile(blockedFile, []byte(blockedOriginal), 0o400); err != nil {
		t.Fatalf("write z_blocked.go: %v", err)
	}
	if err := os.WriteFile(blockedFile, []byte("probe"), 0o600); err == nil {
		t.Skip("environment ignores 0400 file permissions (e.g. running as root)")
	}

	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err == nil {
		t.Fatal("expected apply mode to fail when a planned file write is denied")
	}
	if !strings.Contains(stderr, "apply failed: write z_blocked.go:") {
		t.Fatalf("stderr = %q, want apply failure for z_blocked.go", stderr)
	}
	data, err := os.ReadFile(firstFile)
	if err != nil {
		t.Fatalf("read a.go after rollback: %v", err)
	}
	if string(data) != firstOriginal {
		t.Fatalf("apply failure must roll back prior file updates: got %q want %q", string(data), firstOriginal)
	}
}

func TestRenameScriptMainSkipsVendorDirectory(t *testing.T) {
	root := t.TempDir()
	vendorFile := filepath.Join(root, "vendor", "dep", "v.go")
	if err := os.MkdirAll(filepath.Dir(vendorFile), 0o755); err != nil {
		t.Fatalf("mkdir vendor dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(vendorFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write vendor file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(vendorFile)
	if err != nil {
		t.Fatalf("read vendor file: %v", err)
	}
	if string(data) != original {
		t.Fatalf("vendor file should stay untouched, got %q want %q", string(data), original)
	}
}

func TestRenameScriptMainSkipsNodeModulesDirectory(t *testing.T) {
	root := t.TempDir()
	nmFile := filepath.Join(root, "node_modules", "dep", "v.go")
	if err := os.MkdirAll(filepath.Dir(nmFile), 0o755); err != nil {
		t.Fatalf("mkdir node_modules dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(nmFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write node_modules file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(nmFile)
	if err != nil {
		t.Fatalf("read node_modules file: %v", err)
	}
	if string(data) != original {
		t.Fatalf("node_modules file should stay untouched, got %q want %q", string(data), original)
	}
}

func TestRenameScriptMainSkipsGitDirectory(t *testing.T) {
	root := t.TempDir()
	gitFile := filepath.Join(root, ".git", "hooks", "hook.go")
	if err := os.MkdirAll(filepath.Dir(gitFile), 0o755); err != nil {
		t.Fatalf("mkdir .git dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(gitFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(gitFile)
	if err != nil {
		t.Fatalf("read .git file: %v", err)
	}
	if string(data) != original {
		t.Fatalf(".git file should stay untouched, got %q want %q", string(data), original)
	}
}

func TestRenameScriptMainSkipsWorktreesDirectory(t *testing.T) {
	root := t.TempDir()
	wtFile := filepath.Join(root, ".worktrees", "dep", "v.go")
	if err := os.MkdirAll(filepath.Dir(wtFile), 0o755); err != nil {
		t.Fatalf("mkdir .worktrees dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(wtFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write .worktrees file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(wtFile)
	if err != nil {
		t.Fatalf("read .worktrees file: %v", err)
	}
	if string(data) != original {
		t.Fatalf(".worktrees file should stay untouched, got %q want %q", string(data), original)
	}
}

func TestRenameScriptMainSkipsAgentDirectory(t *testing.T) {
	root := t.TempDir()
	agentFile := filepath.Join(root, ".agent", "dep", "v.go")
	if err := os.MkdirAll(filepath.Dir(agentFile), 0o755); err != nil {
		t.Fatalf("mkdir .agent dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(agentFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write .agent file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatalf("read .agent file: %v", err)
	}
	if string(data) != original {
		t.Fatalf(".agent file should stay untouched, got %q want %q", string(data), original)
	}
}

func TestRenameScriptMainSkipsIdeaDirectory(t *testing.T) {
	root := t.TempDir()
	ideaFile := filepath.Join(root, ".idea", "dep", "v.go")
	if err := os.MkdirAll(filepath.Dir(ideaFile), 0o755); err != nil {
		t.Fatalf("mkdir .idea dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(ideaFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write .idea file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(ideaFile)
	if err != nil {
		t.Fatalf("read .idea file: %v", err)
	}
	if string(data) != original {
		t.Fatalf(".idea file should stay untouched, got %q want %q", string(data), original)
	}
}
func TestRenameScriptMainSkipsDistDirectory(t *testing.T) {
	root := t.TempDir()
	distFile := filepath.Join(root, "dist", "dep", "v.go")
	if err := os.MkdirAll(filepath.Dir(distFile), 0o755); err != nil {
		t.Fatalf("mkdir dist dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(distFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write dist file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(distFile)
	if err != nil {
		t.Fatalf("read dist file: %v", err)
	}
	if string(data) != original {
		t.Fatalf("dist file should stay untouched, got %q want %q", string(data), original)
	}
}
func TestRenameScriptMainSkipsBuildDirectory(t *testing.T) {
	root := t.TempDir()
	buildFile := filepath.Join(root, "build", "dep", "v.go")
	if err := os.MkdirAll(filepath.Dir(buildFile), 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(buildFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write build file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(buildFile)
	if err != nil {
		t.Fatalf("read build file: %v", err)
	}
	if string(data) != original {
		t.Fatalf("build file should stay untouched, got %q want %q", string(data), original)
	}
}

func TestRenameScriptMainSkipsVSCodeDirectory(t *testing.T) {
	root := t.TempDir()
	vscodeFile := filepath.Join(root, ".vscode", "dep", "v.go")
	if err := os.MkdirAll(filepath.Dir(vscodeFile), 0o755); err != nil {
		t.Fatalf("mkdir .vscode dir: %v", err)
	}
	original := `package dep
import _ "github.com/multi-agent/go-agent-v2/pkg/codexsdk"
`
	if err := os.WriteFile(vscodeFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write .vscode file: %v", err)
	}
	_, stderr, err := runRenameScript(t, "--root", root, "--apply")
	if err != nil {
		t.Fatalf("run rename script: %v stderr=%s", err, stderr)
	}
	data, err := os.ReadFile(vscodeFile)
	if err != nil {
		t.Fatalf("read .vscode file: %v", err)
	}
	if string(data) != original {
		t.Fatalf(".vscode file should stay untouched, got %q want %q", string(data), original)
	}
}

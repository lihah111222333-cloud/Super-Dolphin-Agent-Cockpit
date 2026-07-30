package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestProjectMapCLIRefreshesAndChecksExactTreeWithCompiledGenerator(t *testing.T) {
	repository := newProjectMapCLIRepository(t)
	enterProjectMapRepository(t, repository)

	initialTree := projectMapTestGit(t, repository, "write-tree")
	requireProjectMapCLICode(t, gatecontract.ExitOK, "initial refresh", "refresh", "--tree", initialTree)
	assertProjectMapCandidateEntrypointsNotExecuted(t, repository)

	projectMapTestGit(t, repository, "add", "docs/doc/codemap/project-map")
	refreshedTree := projectMapTestGit(t, repository, "write-tree")
	requireProjectMapCLICode(t, gatecontract.ExitOK, "refreshed check", "check", "--tree", refreshedTree)
	requireProjectMapCLICode(t, gatecontract.ExitOK, "index-tree check", "check", "--tree-from-index")

	mapPath := filepath.Join(repository, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MAP.md")
	stagedMap, err := os.ReadFile(mapPath)
	if err != nil {
		t.Fatalf("read refreshed project map: %v", err)
	}
	unstagedMap := append(bytes.Clone(stagedMap), []byte("unstaged project-map overlay\n")...)
	if err := os.WriteFile(mapPath, unstagedMap, 0o644); err != nil {
		t.Fatalf("write unstaged project-map overlay: %v", err)
	}
	untrackedMapPath := filepath.Join(repository, "docs", "doc", "codemap", "project-map", "untracked.md")
	if err := os.WriteFile(untrackedMapPath, []byte("untracked project-map output\n"), 0o644); err != nil {
		t.Fatalf("write untracked project-map output: %v", err)
	}
	userPath := filepath.Join(repository, "untracked-user.txt")
	if err := os.WriteFile(userPath, []byte("user work\n"), 0o644); err != nil {
		t.Fatalf("write untracked user file: %v", err)
	}
	requireProjectMapCLICode(t, gatecontract.ExitOK, "overlay refresh", "refresh", "--tree", refreshedTree)
	assertProjectMapRefreshReplacesOnlyManagedOutputs(t, mapPath, stagedMap, untrackedMapPath, userPath)

	readmePath := filepath.Join(repository, "README.md")
	if err := os.WriteFile(readmePath, []byte("unstaged source overlay\n"), 0o644); err != nil {
		t.Fatalf("write unstaged source overlay: %v", err)
	}
	requireProjectMapCLICode(t, gatecontract.ExitOK, "exact-tree check read unstaged source", "check", "--tree", refreshedTree)
	assertProjectMapCandidateEntrypointsNotExecuted(t, repository)
}

func TestProjectMapCLIRefreshesOnceThenRequiresRefreshedTree(t *testing.T) {
	repository := newProjectMapCLIRepository(t)
	enterProjectMapRepository(t, repository)

	initialTree := projectMapTestGit(t, repository, "write-tree")
	requireProjectMapCLICode(t, gatecontract.ExitOK, "initial refresh", "refresh", "--tree", initialTree)
	projectMapTestGit(t, repository, "add", "docs/doc/codemap/project-map")

	readmePath := filepath.Join(repository, "README.md")
	if err := os.WriteFile(readmePath, []byte("staged source change\n"), 0o644); err != nil {
		t.Fatalf("write staged source change: %v", err)
	}
	projectMapTestGit(t, repository, "add", "README.md")
	driftedTree := projectMapTestGit(t, repository, "write-tree")
	requireProjectMapCLICode(t, gatecontract.ExitGateViolation, "drifted check", "check", "--tree", driftedTree)
	requireProjectMapCLICode(t, gatecontract.ExitOK, "drifted refresh", "refresh", "--tree", driftedTree)
	projectMapTestGit(t, repository, "add", "docs/doc/codemap/project-map")
	refreshedTree := projectMapTestGit(t, repository, "write-tree")
	if refreshedTree == driftedTree {
		t.Fatal("project-map refresh did not produce a new staged tree")
	}
	requireProjectMapCLICode(t, gatecontract.ExitOK, "post-refresh check", "check", "--tree", refreshedTree)
}

func TestProjectMapCLIRejectsMutableTreeReference(t *testing.T) {
	repository := newProjectMapCLIRepository(t)
	enterProjectMapRepository(t, repository)

	code, _, stderr := executeProjectMapCLI("check", "--tree", "HEAD")
	if code != int(gatecontract.ExitSourceMismatch) ||
		!strings.Contains(stderr, "40- or 64-hex") {
		t.Fatalf("mutable tree code=%d stderr=%q", code, stderr)
	}
}

func executeProjectMapCLI(args ...string) (int, string, string) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command := append([]string{"project-map"}, args...)
	code := runCLI(command, stdout, stderr)
	return code, stdout.String(), stderr.String()
}

func newProjectMapCLIRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	projectMapTestGit(t, repository, "init", "-q")
	projectMapTestGit(t, repository, "config", "user.name", "Project Map CLI Test")
	projectMapTestGit(t, repository, "config", "user.email", "project-map-cli@example.invalid")

	files := map[string]string{
		".ai-project-map.overrides.json":     `{"drift_thresholds_patch":{"max_unknown_ratio":1}}` + "\n",
		"AGENTS.md":                          "Use docs/adr/*.md as current decisions.\n",
		"CLAUDE.md":                          "Project map CLI fixture.\n",
		"Makefile":                           "$(shell touch candidate-make-executed)\nall:\n\t@false\n",
		"README.md":                          "Project map CLI fixture.\n",
		"docs/README.md":                     "Current documentation.\n",
		"docs/adr/current.md":                "Current ADR.\n",
		"docs/archive/reviews/old.md":        "Historical review.\n",
		"docs/work/plans/current.md":         "Current plan.\n",
		"docs/契约/README.md":                  "Architecture decisions live in docs/adr.\n",
		"docs/契约/fix-workflow-convention.md": "docs/work/plans/\ndocs/archive/reviews/\ndocs/adr/\n",
		"docs/契约/mcp-service-convention.md":  "Current MCP service convention.\n",
		"go.mod":                             "module example.invalid/project-map-cli\n\ngo 1.25.7\n",
		"scripts/codemap_policy.txt": strings.Join([]string{
			"schema\t1",
			"historical\tdocs/archive",
			"shard\tapp-ui\tapp-ui.tsv",
			"shard\torchestration\torchestration.tsv",
			"shard\tmodules\tmodules.tsv",
			"shard\tplatform-provider\tplatform-provider.tsv",
			"shard\tstore-sql\tstore-sql.tsv",
			"shard\tdocs-agent\tdocs-agent.tsv",
			"shard\tother\tother.tsv",
			"",
		}, "\n"),
		"scripts/generate_ai_project_map.mjs": "import fs from 'node:fs';\nfs.writeFileSync('candidate-generator-executed', 'executed');\n",
	}
	for relative, content := range files {
		path := filepath.Join(repository, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create fixture parent %s: %v", relative, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture file %s: %v", relative, err)
		}
	}
	projectMapTestGit(t, repository, "add", "-A")
	return repository
}

func enterProjectMapRepository(t *testing.T, repository string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(repository); err != nil {
		t.Fatalf("enter project-map repository: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func projectMapTestGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func assertProjectMapCandidateEntrypointsNotExecuted(t *testing.T, repository string) {
	t.Helper()
	for _, marker := range []string{"candidate-generator-executed", "candidate-make-executed"} {
		if _, err := os.Stat(filepath.Join(repository, marker)); !os.IsNotExist(err) {
			t.Fatalf("candidate entrypoint executed: %s", marker)
		}
	}
}

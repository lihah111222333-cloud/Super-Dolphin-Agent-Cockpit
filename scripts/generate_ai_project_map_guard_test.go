package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)

	writeFixTestGuardFile(t, root, ".agent/report/noise.md", "agent report\n")
	writeFixTestGuardFile(t, root, ".agents/report/noise.md", "agents report\n")
	writeFixTestGuardFile(t, root, ".local/cache.txt", "cache\n")
	writeFixTestGuardFile(t, root, ".mypy_cache/cache.json", "{}\n")
	writeFixTestGuardFile(t, root, "docs/guide.md", "tracked docs\n")
	writeFixTestGuardFile(t, root, ".agent/skills/demo/SKILL.md", "tracked but not docs\n")
	runFixTestGuardGit(t, root, "add", "docs/guide.md", ".agent/skills/demo/SKILL.md")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 更新 project map fixture")

	out, err := runProjectMapGenerator(t, root)
	if err != nil {
		t.Fatalf("project map generator failed: %v\n%s", err, out)
	}

	writeFixTestGuardFile(t, root, "docs/guide.md", "dirty docs with more bytes\n")
	out, err = runProjectMapGenerator(t, root)
	if err != nil {
		t.Fatalf("project map generator with dirty worktree failed: %v\n%s", err, out)
	}

	generated := readProjectMapOutputs(t, root)
	assertOutputContainsAll(t, generated, "internal/app/app.go", "docs/guide.md")
	assertOutputContainsAll(t, generated, "docs/guide.md\tdocs\tdocs-agent\tdoc\t13\t")
	assertOutputOmitsAll(t, generated, ".agent/report", ".agents/report", ".agent/skills", ".local/", ".mypy_cache")

	manifest := readProjectMapManifest(t, root)
	if _, ok := manifest.Domains["docs-agent"]; !ok {
		t.Fatalf("manifest missing docs-agent domain: %#v", manifest.Domains)
	}
}

func TestProjectMapGeneratorRequiresExplicitFilesystemScanWithoutGit(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, false)

	out, err := runProjectMapGenerator(t, root)
	if err == nil {
		t.Fatalf("project map generator succeeded without git and without explicit scan\n%s", out)
	}
	assertOutputContainsAll(t, out, "git ls-files failed", "--filesystem-scan")

	out, err = runProjectMapGenerator(t, root, "--filesystem-scan")
	if err != nil {
		t.Fatalf("project map filesystem scan failed: %v\n%s", err, out)
	}
	generated := readProjectMapOutputs(t, root)
	assertOutputContainsAll(t, generated, "internal/app/app.go", "docs/guide.md")
	assertOutputContainsAll(t, generated, "run-new-ui-desktop.ps1\t(root)\tother\tscript\t8\t")
}

func prepareProjectMapFixture(t *testing.T, initGit bool) string {
	t.Helper()
	root := t.TempDir()
	writeFixTestGuardFile(t, root, "go.mod", "module example.com/projectmap\n\ngo 1.25\n")
	writeFixTestGuardFile(t, root, "CLAUDE.md", "fixture\n")
	writeFixTestGuardFile(t, root, "README.md", "fixture\n")
	writeFixTestGuardFile(t, root, "run-new-ui-desktop.ps1", "one\r\ntwo\r\n")
	writeFixTestGuardFile(t, root, "internal/app/app.go", "package app\n\nfunc App() {}\n")
	writeFixTestGuardFile(t, root, "docs/guide.md", "docs\n")
	if !initGit {
		return root
	}
	runFixTestGuardGit(t, root, "init", "-q")
	runFixTestGuardGit(t, root, "config", "user.email", "projectmap@example.test")
	runFixTestGuardGit(t, root, "config", "user.name", "Project Map Test")
	runFixTestGuardGit(t, root, "add", "go.mod", "CLAUDE.md", "README.md", "internal/app/app.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 初始化 project map fixture")
	return root
}

func requireNodeForProjectMap(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node is required for project map generator tests: %v", err)
	}
}

func runProjectMapGenerator(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	script := filepath.Join(scriptRepoRoot(t), "scripts", "generate_ai_project_map.js")
	cmdArgs := append([]string{script}, args...)
	cmd := exec.Command("node", cmdArgs...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readProjectMapOutputs(t *testing.T, root string) string {
	t.Helper()
	var parts []string
	for _, rel := range []string{
		"docs/doc/codemap/project-map/AI_PROJECT_MAP.md",
		"docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md",
		"docs/doc/codemap/project-map/index/docs-agent.tsv",
		"docs/doc/codemap/project-map/index/modules.tsv",
		"docs/doc/codemap/project-map/index/other.tsv",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if data, err := os.ReadFile(path); err == nil {
			parts = append(parts, string(data))
		}
	}
	return strings.Join(parts, "\n")
}

func readProjectMapManifest(t *testing.T, root string) struct {
	Domains map[string]int `json:"domains"`
} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MANIFEST.json"))
	if err != nil {
		t.Fatalf("read project map manifest: %v", err)
	}
	var manifest struct {
		Domains map[string]int `json:"domains"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse project map manifest: %v", err)
	}
	return manifest
}

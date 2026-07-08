package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	writeFixTestGuardFile(t, root, "internal/module/thread/thread.go", "package thread\n")
	writeFixTestGuardFile(t, root, "internal/provider/codexapp/provider.go", "package codexapp\n")
	runFixTestGuardGit(t, root, "add", "docs/guide.md", ".agent/skills/demo/SKILL.md", "internal/module/thread/thread.go", "internal/provider/codexapp/provider.go")
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
	assertOutputContainsAll(t, generated,
		"扫描规则：",
		"| 索引文件 | 文件数 | 大小 | 覆盖范围 |",
		"rg \"thread.*resume|fork\" docs/doc/codemap/project-map/index/modules.tsv",
		"rg \"provider.*manifest|toolbridge\" docs/doc/codemap/project-map/index/platform-provider.tsv",
	)
	assertOutputContainsAll(t, generated,
		"## 4. 运行入口地图",
		"| Desktop host | `cmd/agent-terminal/main.go` |",
		"| MCP orchestration peer | `cmd/mcp-orch/main.go` |",
		"| MCP LSP peer | `cmd/mcp-lsp/main.go` |",
		"| 运行单元 | 入口文件 | 默认端口/端点 | 说明 |",
		"## 6. 重点子系统地图",
		"`internal/module/thread`",
		"`internal/provider/codexapp`",
	)
	assertOutputContainsAll(t, generated,
		"## 7. 文档与知识地图",
		"`.agents/skills/*/SKILL.md` 是 repo-local skill 指令入口",
		"## 8. 索引字段说明",
		"| `search_keys` | 建议检索关键词 |",
	)

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

func TestProjectMapGeneratorAppliesRuleOverrides(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)

	writeFixTestGuardFile(t, root, ".ai-project-map.overrides.json", `{
  "purpose_rules_append": [
    ["internal/testutil/", "测试辅助工具与夹具"]
  ],
  "top_module_desc_patch": {
    "test": "测试夹具和辅助资源"
  },
  "quick_routes_append": [
    ["查测试夹具", "test/fixtures/", "internal/testutil/", "fixture testutil golden"]
  ],
  "archive_prefixes": [
    "docs/legacy/"
  ]
}
`)
	writeFixTestGuardFile(t, root, "internal/testutil/golden/golden.go", "package golden\n")
	writeFixTestGuardFile(t, root, "docs/legacy/old.md", "old docs\n")
	runFixTestGuardGit(t, root, "add", ".ai-project-map.overrides.json", "internal/testutil/golden/golden.go", "docs/legacy/old.md")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 添加 project map override fixture")

	out, err := runProjectMapGenerator(t, root)
	if err != nil {
		t.Fatalf("project map generator with overrides failed: %v\n%s", err, out)
	}

	generated := readProjectMapOutputs(t, root)
	assertOutputContainsAll(t, generated,
		"internal/testutil/golden/golden.go\tinternal\tother\tgo-source",
		"测试辅助工具与夹具",
		"查测试夹具",
	)
	assertOutputOmitsAll(t, generated, "docs/legacy/old.md")

	manifest := readProjectMapManifestDetails(t, root)
	if !stringSliceContains(manifest.RulesSources, ".ai-project-map.overrides.json") {
		t.Fatalf("manifest rules_sources = %#v, want .ai-project-map.overrides.json", manifest.RulesSources)
	}
}

func TestProjectMapManifestIncludesRoutesAndShardStats(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)

	out, err := runProjectMapGenerator(t, root)
	if err != nil {
		t.Fatalf("project map generator failed: %v\n%s", err, out)
	}

	manifest := readProjectMapManifestDetails(t, root)
	if len(manifest.IndexFiles.Shards) == 0 {
		t.Fatalf("manifest missing shard stats")
	}
	if len(manifest.QuickRoutes) == 0 {
		t.Fatalf("manifest missing quick routes")
	}
	if len(manifest.RuntimeEntries) == 0 {
		t.Fatalf("manifest missing runtime entries")
	}
	if manifest.RuntimeEntries[0].Endpoint == "" {
		t.Fatalf("manifest runtime entries missing endpoint")
	}
	if manifest.Drift.Thresholds["max_unknown_ratio"] == 0 {
		t.Fatalf("manifest missing drift threshold details")
	}
}

func TestProjectMapGeneratorAppliesDriftThresholdOverride(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)

	writeFixTestGuardFile(t, root, ".ai-project-map.overrides.json", `{
  "drift_thresholds_patch": {
    "max_unknown_ratio": 0.0001
  }
}
`)
	writeFixTestGuardFile(t, root, "internal/unknown/foo.go", "package unknown\n")
	runFixTestGuardGit(t, root, "add", ".ai-project-map.overrides.json", "internal/unknown/foo.go")
	runFixTestGuardGit(t, root, "commit", "-m", "chore: 添加 drift threshold fixture")

	out, err := runProjectMapGenerator(t, root, "--strict-drift")
	if err == nil {
		t.Fatalf("project map strict drift unexpectedly passed\n%s", out)
	}
	assertOutputContainsAll(t, out, "drift status WARN")
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
	script := filepath.Join(scriptRepoRoot(t), "scripts", "generate_ai_project_map.mjs")
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
		"docs/doc/codemap/project-map/index/app-ui.tsv",
		"docs/doc/codemap/project-map/index/modules.tsv",
		"docs/doc/codemap/project-map/index/platform-provider.tsv",
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

func readProjectMapManifestDetails(t *testing.T, root string) struct {
	Domains map[string]int `json:"domains"`
	Drift   struct {
		Thresholds map[string]float64 `json:"thresholds"`
	} `json:"drift"`
	IndexFiles struct {
		Shards []struct {
			Key       string  `json:"key"`
			File      string  `json:"file"`
			FileCount int     `json:"file_count"`
			SizeKB    float64 `json:"size_kb"`
			Desc      string  `json:"desc"`
		} `json:"shards"`
	} `json:"index_files"`
	QuickRoutes []struct {
		Goal      string `json:"goal"`
		Primary   string `json:"primary"`
		Secondary string `json:"secondary"`
		Keywords  string `json:"keywords"`
	} `json:"quick_routes"`
	RulesSources   []string `json:"rules_sources"`
	RuntimeEntries []struct {
		Unit     string `json:"unit"`
		Entry    string `json:"entry"`
		Endpoint string `json:"endpoint"`
		Desc     string `json:"desc"`
	} `json:"runtime_entries"`
} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MANIFEST.json"))
	if err != nil {
		t.Fatalf("read project map manifest: %v", err)
	}
	var manifest struct {
		Domains map[string]int `json:"domains"`
		Drift   struct {
			Thresholds map[string]float64 `json:"thresholds"`
		} `json:"drift"`
		IndexFiles struct {
			Shards []struct {
				Key       string  `json:"key"`
				File      string  `json:"file"`
				FileCount int     `json:"file_count"`
				SizeKB    float64 `json:"size_kb"`
				Desc      string  `json:"desc"`
			} `json:"shards"`
		} `json:"index_files"`
		QuickRoutes []struct {
			Goal      string `json:"goal"`
			Primary   string `json:"primary"`
			Secondary string `json:"secondary"`
			Keywords  string `json:"keywords"`
		} `json:"quick_routes"`
		RulesSources   []string `json:"rules_sources"`
		RuntimeEntries []struct {
			Unit     string `json:"unit"`
			Entry    string `json:"entry"`
			Endpoint string `json:"endpoint"`
			Desc     string `json:"desc"`
		} `json:"runtime_entries"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse project map manifest: %v", err)
	}
	return manifest
}

func stringSliceContains(values []string, want string) bool {
	return slices.Contains(values, want)
}

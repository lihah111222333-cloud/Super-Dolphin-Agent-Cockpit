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
	writeFixTestGuardFile(t, root, "internal/app/storeadapter/prompt/adapter.go", "package promptadapter\n")
	writeFixTestGuardFile(t, root, "internal/app/runtimeadapter/toolbridge/adapter.go", "package toolbridgeadapter\n")
	writeFixTestGuardFile(t, root, "internal/app/internal/storeguard/nil.go", "package storeguard\n")
	writeFixTestGuardFile(t, root, "internal/module/thread/thread.go", "package thread\n")
	writeFixTestGuardFile(t, root, "internal/provider/codexapp/provider.go", "package codexapp\n")
	runFixTestGuardGit(t, root, "add",
		"docs/guide.md",
		".agent/skills/demo/SKILL.md",
		"internal/app/storeadapter/prompt/adapter.go",
		"internal/app/runtimeadapter/toolbridge/adapter.go",
		"internal/app/internal/storeguard/nil.go",
		"internal/module/thread/thread.go",
		"internal/provider/codexapp/provider.go",
	)
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
	assertAppAdapterProjectMapRoutes(t, generated)
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
		"## 5. Root Fx 装配阅读顺序",
		"`internal/app/modules.go` 是根装配清单，不是严格的业务执行时序",
		"理解 root Fx 装配顺序",
		"## 7. 重点子系统地图",
		"### internal/app assembly and adapters",
		"`internal/app`",
		"`internal/module/thread`",
		"`internal/provider/codexapp`",
	)
	assertOutputContainsAll(t, generated,
		"## 8. 文档与知识地图",
		"`.agents/skills/*/SKILL.md` 是 repo-local skill 指令入口",
		"## 9. 索引字段说明",
		"| `search_keys` | 建议检索关键词 |",
	)

	manifest := readProjectMapManifest(t, root)
	if _, ok := manifest.Domains["docs-agent"]; !ok {
		t.Fatalf("manifest missing docs-agent domain: %#v", manifest.Domains)
	}
}

func TestProjectMapGeneratorReusesExistingGenerationDate(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)
	initialDate := generateProjectMapAndReadGenerationDate(t, root)
	const stableDate = "2000-02-29"
	want := seedProjectMapGenerationDate(t, root, initialDate, stableDate)
	if out, err := runProjectMapGenerator(t, root); err != nil {
		t.Fatalf("repeat project map generation failed: %v\n%s", err, out)
	}
	got := readProjectMapDatedOutputs(t, root)
	if !slices.Equal(got.joined(), want.joined()) {
		t.Fatalf("generation date changed across repeated generation\nwant date: %s\ngot manifest:\n%s", stableDate, got.manifest)
	}
	if out, err := runProjectMapGenerator(t, root, "--check", "--strict-drift"); err != nil {
		t.Fatalf("stable project map check failed: %v\n%s", err, out)
	}
}

type projectMapDatedOutputs struct {
	manifest []byte
	project  []byte
}

func generateProjectMapAndReadGenerationDate(t *testing.T, root string) string {
	t.Helper()
	if out, err := runProjectMapGenerator(t, root); err != nil {
		t.Fatalf("initial project map generation failed: %v\n%s", err, out)
	}
	outputs := readProjectMapDatedOutputs(t, root)
	var manifest map[string]any
	if err := json.Unmarshal(outputs.manifest, &manifest); err != nil {
		t.Fatalf("decode initial manifest: %v", err)
	}
	generationDate, ok := manifest["generated_at"].(string)
	if !ok || generationDate == "" {
		t.Fatalf("initial manifest has invalid generated_at: %#v", manifest["generated_at"])
	}
	return generationDate
}

func seedProjectMapGenerationDate(t *testing.T, root, initialDate, stableDate string) projectMapDatedOutputs {
	t.Helper()
	outputs := readProjectMapDatedOutputs(t, root)
	outputs.manifest = []byte(strings.Replace(
		string(outputs.manifest),
		`"generated_at": "`+initialDate+`"`,
		`"generated_at": "`+stableDate+`"`,
		1,
	))
	outputs.project = []byte(strings.Replace(
		string(outputs.project),
		"> 生成时间："+initialDate,
		"> 生成时间："+stableDate,
		1,
	))
	outputDir := filepath.Join(root, "docs", "doc", "codemap", "project-map")
	if err := os.WriteFile(filepath.Join(outputDir, "AI_PROJECT_MANIFEST.json"), outputs.manifest, 0o600); err != nil {
		t.Fatalf("write seeded manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "AI_PROJECT_MAP.md"), outputs.project, 0o600); err != nil {
		t.Fatalf("write seeded map: %v", err)
	}
	return outputs
}

func readProjectMapDatedOutputs(t *testing.T, root string) projectMapDatedOutputs {
	t.Helper()
	outputDir := filepath.Join(root, "docs", "doc", "codemap", "project-map")
	manifest, err := os.ReadFile(filepath.Join(outputDir, "AI_PROJECT_MANIFEST.json"))
	if err != nil {
		t.Fatalf("read project map manifest: %v", err)
	}
	project, err := os.ReadFile(filepath.Join(outputDir, "AI_PROJECT_MAP.md"))
	if err != nil {
		t.Fatalf("read project map markdown: %v", err)
	}
	return projectMapDatedOutputs{manifest: manifest, project: project}
}

func (o projectMapDatedOutputs) joined() []byte {
	return append(append([]byte(nil), o.manifest...), o.project...)
}

func TestProjectMapGeneratorRejectsInvalidExistingGenerationDate(t *testing.T) {
	requireNodeForProjectMap(t)
	tests := []struct {
		name     string
		manifest string
	}{
		{name: "malformed json", manifest: "{"},
		{name: "missing date", manifest: "{\"version\":\"1.0\"}\n"},
		{name: "wrong type", manifest: "{\"generated_at\":7}\n"},
		{name: "invalid calendar date", manifest: "{\"generated_at\":\"2026-02-30\"}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := prepareProjectMapFixture(t, true)
			manifestPath := filepath.Join(root, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MANIFEST.json")
			relManifest := filepath.ToSlash(strings.TrimPrefix(manifestPath, root+string(filepath.Separator)))
			writeFixTestGuardFile(t, root, relManifest, tt.manifest)
			out, err := runProjectMapGenerator(t, root)
			if err == nil {
				t.Fatalf("generator accepted invalid manifest:\n%s", out)
			}
			if !strings.Contains(out, "generated_at") {
				t.Fatalf("error does not identify generated_at: %s", out)
			}
		})
	}
}

// TestProjectMapGeneratorIndexesLocalizedRootReadmes 锁定 GitHub 语言导航对应的根 README 都进入项目地图。
func TestProjectMapGeneratorIndexesLocalizedRootReadmes(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)
	localizedReadmes := []string{
		"README.zh-CN.md",
		"README.ja.md",
		"README.ko.md",
		"README.es.md",
		"README.de.md",
	}
	for _, file := range localizedReadmes {
		writeFixTestGuardFile(t, root, file, "localized\n")
	}
	gitAddArgs := append([]string{"add"}, localizedReadmes...)
	runFixTestGuardGit(t, root, gitAddArgs...)
	runFixTestGuardGit(t, root, "commit", "-m", "docs: 添加多语言 README fixture")

	out, err := runProjectMapGenerator(t, root)
	if err != nil {
		t.Fatalf("project map generator failed: %v\n%s", err, out)
	}
	generated := readProjectMapOutputs(t, root)
	for _, file := range localizedReadmes {
		assertOutputContainsAll(t, generated, file+"\t(root)\tdocs-agent\tdoc\t10\t")
	}
}

func assertAppAdapterProjectMapRoutes(t *testing.T, generated string) {
	t.Helper()
	assertProjectMapPurpose(t, generated, "internal/app/storeadapter/prompt/adapter.go", "业务 Store 到 module 窄端口的适配器")
	assertProjectMapPurpose(t, generated, "internal/app/runtimeadapter/toolbridge/adapter.go", "runtime consumer 的 Store/module 窄端口适配器")
	assertProjectMapPurpose(t, generated, "internal/app/internal/storeguard/nil.go", "adapter 共享的 typed-nil fail-fast 检查 helper")
	assertOutputContainsAll(t, generated,
		"| 3 | Store adapters | `internal/app/storeadapter` |",
		"| 6 | Runtime adapters | `internal/app/runtimeadapter` |",
		"| 9 | Graph guards | `internal/app/modules_graph_test.go、internal/archtest/fx_graph_test.go` |",
		"| 修改 App adapter 分包 | `internal/app/storeadapter/` | `internal/app/runtimeadapter/` | `store runtime adapter` |",
		"| `internal/app/storeadapter` | 1 | 业务 Store 到 module 窄端口的适配器 |",
		"| `internal/app/runtimeadapter` | 1 | runtime consumer 的 Store/module 窄端口适配器 |",
		"| `internal/app/internal/storeguard` | 1 | adapter 共享的 typed-nil fail-fast 检查 helper |",
		"| `internal/app` | 4 | 全域汇总（root + adapter packages） |",
	)
	assertOutputOmitsAll(t, generated, "keepalive lookups、native tool descriptors")
}

func assertProjectMapPurpose(t *testing.T, generated, path, want string) {
	t.Helper()
	for line := range strings.SplitSeq(generated, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 6 || fields[0] != path {
			continue
		}
		if fields[5] != want {
			t.Fatalf("project map purpose mismatch for %q: got %q, want %q", path, fields[5], want)
		}
		return
	}
	t.Fatalf("project map TSV row missing for %q", path)
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

func TestProjectMapGeneratorRejectsSymlinkRuleOverrides(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)
	external := filepath.Join(t.TempDir(), "external-overrides.json")
	if err := os.WriteFile(external, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write external override: %v", err)
	}
	override := filepath.Join(root, ".ai-project-map.overrides.json")
	if err := os.Symlink(external, override); err != nil {
		t.Fatalf("create override symlink: %v", err)
	}
	runFixTestGuardGit(t, root, "add", ".ai-project-map.overrides.json")

	out, err := runProjectMapGenerator(t, root)
	if err == nil {
		t.Fatalf("project map accepted external override symlink:\n%s", out)
	}
	assertOutputContainsAll(t, out, "rules file must not be a symbolic link", ".ai-project-map.overrides.json")
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

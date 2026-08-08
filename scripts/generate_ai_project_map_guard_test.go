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

	prepareTrackedProjectMapFixtures(t, root)
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
	assertRemoteCIProjectMapRoutes(t, generated)
	assertAppAdapterProjectMapRoutes(t, generated)
	assertOutputContainsAll(t, generated, "docs/guide.md\tdocs\tdocs-agent\tdoc\t13\t")
	assertOutputOmitsAll(
		t,
		generated,
		".agent/report",
		".agents/report",
		".agent/skills",
		".local/",
		".mypy_cache",
		"docs/plans/obsolete.md",
		"docs/superpowers/plans/obsolete.md",
	)
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
	for _, historicalRoot := range codemapHistoricalRoots(t) {
		assertOutputOmitsAll(t, generated, historicalRoot+"/project-map-fixture.md")
		assertOutputContainsAll(t, generated, "`"+historicalRoot+"/*`")
	}

	manifest := readProjectMapManifest(t, root)
	if _, ok := manifest.Domains["docs-agent"]; !ok {
		t.Fatalf("manifest missing docs-agent domain: %#v", manifest.Domains)
	}
	if _, ok := manifest.Domains["remote-ci"]; !ok {
		t.Fatalf("manifest missing remote-ci domain: %#v", manifest.Domains)
	}
}

// TestProjectMapGeneratorOmitsTrackedFilesDeletedFromWorktree 锁定未暂存删除不能留在可导航 TSV 中。
func TestProjectMapGeneratorOmitsTrackedFilesDeletedFromWorktree(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)
	deleted := filepath.Join(root, "internal", "app", "app.go")
	if err := os.Remove(deleted); err != nil {
		t.Fatalf("remove tracked project-map fixture: %v", err)
	}

	out, err := runProjectMapGenerator(t, root)
	if err != nil {
		t.Fatalf("project map generator failed after tracked worktree deletion: %v\n%s", err, out)
	}
	shard, err := os.ReadFile(filepath.Join(root, "docs", "doc", "codemap", "project-map", "index", "other.tsv"))
	if err != nil {
		t.Fatalf("read project-map other shard: %v", err)
	}
	assertOutputOmitsAll(t, string(shard), "internal/app/app.go")
}

func prepareTrackedProjectMapFixtures(t *testing.T, root string) {
	t.Helper()
	writeFixTestGuardFile(t, root, ".agent/report/noise.md", "agent report\n")
	writeFixTestGuardFile(t, root, ".agents/report/noise.md", "agents report\n")
	writeFixTestGuardFile(t, root, ".local/cache.txt", "cache\n")
	writeFixTestGuardFile(t, root, ".mypy_cache/cache.json", "{}\n")
	writeFixTestGuardFile(t, root, "docs/guide.md", "tracked docs\n")
	writeFixTestGuardFile(t, root, "docs/plans/obsolete.md", "historical plan\n")
	writeFixTestGuardFile(t, root, "docs/superpowers/plans/obsolete.md", "historical superpowers plan\n")
	writeFixTestGuardFile(t, root, ".agent/skills/demo/SKILL.md", "tracked but not docs\n")
	writeFixTestGuardFile(t, root, "internal/app/storeadapter/prompt/adapter.go", "package promptadapter\n")
	writeFixTestGuardFile(t, root, "internal/app/runtimeadapter/toolbridge/adapter.go", "package toolbridgeadapter\n")
	writeFixTestGuardFile(t, root, "internal/app/internal/storeguard/nil.go", "package storeguard\n")
	writeFixTestGuardFile(t, root, "internal/module/thread/thread.go", "package thread\n")
	writeFixTestGuardFile(t, root, "internal/provider/codexapp/provider.go", "package codexapp\n")
	writeFixTestGuardFile(t, root, "cmd/super-dolphin-gate/main.go", "package main\n")
	writeFixTestGuardFile(t, root, "internal/devtools/remoteci/coordinator.go", "package remoteci\n")
	writeFixTestGuardFile(t, root, "internal/devtools/cicontract/contract.go", "package cicontract\n")
	writeFixTestGuardFile(t, root, "internal/devtools/alicloud/eci/client.go", "package eci\n")
	writeFixTestGuardFile(t, root, "internal/devtools/alicloud/oss/client.go", "package oss\n")
	writeFixTestGuardFile(t, root, "config/remote-ci/aliyun.json", "{}\n")
	writeFixTestGuardFile(t, root, ".githooks/pre-commit", "#!/usr/bin/env bash\n")
	writeFixTestGuardFile(t, root, "scripts/remote_ci_git_credentials_test.go", "package main\n")
	writeFixTestGuardFile(t, root, "docs/契约/remote-ci-eci-imagecache-contract.md", "# Remote CI contract\n")
	historicalFixtures := make([]string, 0)
	for _, historicalRoot := range codemapHistoricalRoots(t) {
		relative := historicalRoot + "/project-map-fixture.md"
		writeFixTestGuardFile(t, root, relative, "historical fixture\n")
		historicalFixtures = append(historicalFixtures, relative)
	}
	addPaths := []string{
		"docs/guide.md",
		"docs/plans/obsolete.md",
		"docs/superpowers/plans/obsolete.md",
		".agent/skills/demo/SKILL.md",
		"internal/app/storeadapter/prompt/adapter.go",
		"internal/app/runtimeadapter/toolbridge/adapter.go",
		"internal/app/internal/storeguard/nil.go",
		"internal/module/thread/thread.go",
		"internal/provider/codexapp/provider.go",
		"cmd/super-dolphin-gate/main.go",
		"internal/devtools/remoteci/coordinator.go",
		"internal/devtools/cicontract/contract.go",
		"internal/devtools/alicloud/eci/client.go",
		"internal/devtools/alicloud/oss/client.go",
		"config/remote-ci/aliyun.json",
		".githooks/pre-commit",
		"scripts/remote_ci_git_credentials_test.go",
		"docs/契约/remote-ci-eci-imagecache-contract.md",
	}
	runFixTestGuardGit(t, root, append([]string{"add"}, append(addPaths, historicalFixtures...)...)...)
}

func assertRemoteCIProjectMapRoutes(t *testing.T, generated string) {
	t.Helper()
	for _, file := range []string{
		"cmd/super-dolphin-gate/main.go",
		"internal/devtools/remoteci/coordinator.go",
		"internal/devtools/cicontract/contract.go",
		"internal/devtools/alicloud/eci/client.go",
		"internal/devtools/alicloud/oss/client.go",
		"config/remote-ci/aliyun.json",
		".githooks/pre-commit",
		"scripts/remote_ci_git_credentials_test.go",
		"docs/契约/remote-ci-eci-imagecache-contract.md",
	} {
		assertOutputContainsAll(t, generated, file)
	}
	assertOutputContainsAll(t, generated,
		"\tremote-ci\t",
		"remote-ci.tsv",
		"修改远程 CI/ECI/ImageCache",
		"### remote CI",
	)
}

// codemapHistoricalRoots 从 Go/JS 共用策略读取历史根，避免测试复制第二份列表。
func codemapHistoricalRoots(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(scriptRepoRoot(t), "scripts", "codemap_policy.txt"))
	if err != nil {
		t.Fatalf("read codemap policy: %v", err)
	}
	var roots []string
	for line := range strings.SplitSeq(string(body), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 2 && fields[0] == "historical" {
			roots = append(roots, fields[1])
		}
	}
	if len(roots) == 0 {
		t.Fatal("codemap policy has no historical roots")
	}
	return roots
}

func TestProjectMapGeneratorOutputsIgnoreWallClockAndDetectTrackedDrift(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)
	firstInstant := "2026-07-15T23:59:59Z"
	secondInstant := "2026-07-16T00:00:01Z"

	out, err := runProjectMapGeneratorAt(t, root, firstInstant, "Pacific/Kiritimati")
	if err != nil {
		t.Fatalf("initial project map generation failed: %v\n%s", err, out)
	}
	firstMap, firstManifest := readCanonicalProjectMapOutputs(t, root)
	assertOutputOmitsAll(t, firstMap, "生成时间")
	assertOutputOmitsAll(t, firstManifest, "\"generated_at\"")

	out, err = runProjectMapGeneratorAt(t, root, secondInstant, "America/Los_Angeles", "--check", "--strict-drift")
	if err != nil {
		t.Fatalf("same tree failed next-day project map check: %v\n%s", err, out)
	}
	out, err = runProjectMapGeneratorAt(t, root, secondInstant, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("next-day project map generation failed: %v\n%s", err, out)
	}
	secondMap, secondManifest := readCanonicalProjectMapOutputs(t, root)
	if firstMap != secondMap {
		t.Fatal("AI_PROJECT_MAP.md changed across UTC day or timezone for the same tree")
	}
	if firstManifest != secondManifest {
		t.Fatal("AI_PROJECT_MANIFEST.json changed across UTC day or timezone for the same tree")
	}

	writeFixTestGuardFile(t, root, "internal/app/app.go", "package app\n\nfunc App() { panic(\"changed\") }\n")
	runFixTestGuardGit(t, root, "add", "internal/app/app.go")
	out, err = runProjectMapGeneratorAt(t, root, secondInstant, "America/Los_Angeles", "--check", "--strict-drift")
	if err == nil {
		t.Fatalf("project map check accepted real tracked input drift\n%s", out)
	}
	assertOutputContainsAll(t, out, "differs from generated output")
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

func TestProjectMapGeneratorRejectsMissingCurrentDocumentationRoot(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)
	if err := os.RemoveAll(filepath.Join(root, "docs", "adr")); err != nil {
		t.Fatalf("remove current documentation fixture: %v", err)
	}
	out, err := runProjectMapGenerator(t, root)
	if err == nil {
		t.Fatalf("project map accepted missing current documentation root\n%s", out)
	}
	assertOutputContainsAll(t, out, "canonical current documentation path is missing: docs/adr")
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
	writeFixTestGuardFile(t, root, "AGENTS.md", "Use `docs/adr/*.md`.\n")
	writeFixTestGuardFile(t, root, "docs/README.md", "# Docs\n")
	writeFixTestGuardFile(t, root, "docs/adr/README.md", "# ADR\n")
	writeFixTestGuardFile(t, root, "docs/work/plans/README.md", "# Plans\n")
	writeFixTestGuardFile(t, root, "docs/archive/reviews/README.md", "# Archived reviews\n")
	writeFixTestGuardFile(t, root, "docs/契约/README.md", "Accepted decisions: `docs/adr`.\n")
	writeFixTestGuardFile(t, root, "docs/契约/fix-workflow-convention.md", "`docs/work/plans/` `docs/archive/reviews/` `docs/adr/`\n")
	writeFixTestGuardFile(t, root, "docs/契约/mcp-service-convention.md", "# MCP\n")
	writeFixTestGuardFile(t, root, "run-new-ui-desktop.ps1", "one\r\ntwo\r\n")
	writeFixTestGuardFile(t, root, "internal/app/app.go", "package app\n\nfunc App() {}\n")
	writeFixTestGuardFile(t, root, "docs/guide.md", "docs\n")
	if !initGit {
		return root
	}
	runFixTestGuardGit(t, root, "init", "-q")
	runFixTestGuardGit(t, root, "config", "user.email", "projectmap@example.test")
	runFixTestGuardGit(t, root, "config", "user.name", "Project Map Test")
	runFixTestGuardGit(t, root, "add", "go.mod", "CLAUDE.md", "README.md", "AGENTS.md", "docs/README.md", "docs/adr", "docs/work/plans", "docs/archive/reviews", "docs/契约", "internal/app/app.go")
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

func runProjectMapGeneratorAt(t *testing.T, root, instant, zone string, args ...string) (string, error) {
	t.Helper()
	preload := filepath.Join(t.TempDir(), "project-map-test-clock.cjs")
	clockSource := `const fixedInstant = process.env.PROJECT_MAP_TEST_NOW;
if (!fixedInstant) throw new Error('PROJECT_MAP_TEST_NOW is required');
const NativeDate = global.Date;
global.Date = class extends NativeDate {
  constructor(...args) {
    super(...(args.length === 0 ? [fixedInstant] : args));
  }
  static now() {
    return new NativeDate(fixedInstant).getTime();
  }
};
`
	if err := os.WriteFile(preload, []byte(clockSource), 0o600); err != nil {
		t.Fatalf("write project map test clock: %v", err)
	}
	script := filepath.Join(scriptRepoRoot(t), "scripts", "generate_ai_project_map.mjs")
	cmdArgs := append([]string{"--require", preload, script}, args...)
	cmd := exec.Command("node", cmdArgs...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PROJECT_MAP_TEST_NOW="+instant, "TZ="+zone)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readCanonicalProjectMapOutputs(t *testing.T, root string) (string, string) {
	t.Helper()
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read canonical project map output %s: %v", rel, err)
		}
		return string(data)
	}
	return read("docs/doc/codemap/project-map/AI_PROJECT_MAP.md"),
		read("docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json")
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
		"docs/doc/codemap/project-map/index/remote-ci.tsv",
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

# Project Map AI Routing Enhancement Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring `super-agent-v3`'s generated AI project map closer to the proven `wjboot-v2` routing model, so agents can locate runtime entries, index shards, governance routes, and drift risks with fewer broad searches.

**Architecture:** Keep `docs/doc/codemap/project-map/*` generated from `scripts/generate_ai_project_map.mjs`; do not hand-edit generated map, drift, manifest, or TSV files. Add small generator helpers for rule overrides, shard metadata, runtime/subsystem tables, and manifest expansion, then refresh generated outputs through the existing Make targets.

**Tech Stack:** Node.js generator (`scripts/generate_ai_project_map.mjs`), Go guard tests (`scripts/generate_ai_project_map_guard_test.go`), generated Markdown/JSON/TSV under `docs/doc/codemap/project-map`, Make targets `project-map-refresh` and `project-map-check`.

**Verification Surface:** `go test ./scripts -run 'TestProjectMap(Generator|Manifest)' -count=1`, `make project-map-refresh`, `make project-map-check`, `make codemap-check`, `git diff --check`, and LSP diagnostics for `scripts/generate_ai_project_map.mjs`.

---

## Review Summary

### Current Evidence

- `docs/doc/codemap/project-map/AI_PROJECT_MAP.md` currently indexes 4071 files and reports drift `OK`.
- `docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md` currently reports 57 unknown-purpose files, 1.40% unknown ratio.
- `docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json` currently contains only generator metadata, domain counts, and drift summary.
- `scripts/generate_ai_project_map.mjs` renders the project map in `renderMap(entries, grouped, drift)`.
- Pre-implementation LSP diagnostics found two Hints on the former CommonJS generator path: `File is a CommonJS module; it may be converted to an ES module` and `'EXCLUDES' is declared but its value is never read`. Implementation must remove both Hints or record a precise blocker before claiming completion.
- LSP evidence captured during review:
  - `grep(text_search)` found `renderMap` at `scripts/generate_ai_project_map.mjs:469`.
  - `structure(document_symbol)` found `buildDrift`, `renderMap`, `checkOutputs`, `DOMAIN_FILES`, and `DOMAIN_DESCRIPTIONS`.
  - `inspect(definition)` resolved `renderMap` to `scripts/generate_ai_project_map.mjs:469`.
  - `xref(references)` found `renderMap` called from `renderAll` at `scripts/generate_ai_project_map.mjs:453`.
  - `file(read_file)` read the full `renderMap` function.
  - `file(diagnostics)` must be rerun after implementation and must be clean before merge.

### Comparison With `wjboot-v2`

`wjboot-v2/docs/guide/AI_PROJECT_MAP.md` has several AI-routing affordances that `super-agent-v3` does not yet expose:

- A visible scan-rule line showing included/excluded path policy.
- A shard route table with file counts and size in KB.
- Concrete `rg` examples for low-token lookup against TSV shards.
- A runtime entry map with entry files, default ports, and purpose.
- Deeper subsystem maps for major implementation areas.
- An override file (`.ai-project-map.overrides.json`) that can append route and purpose rules without editing the generator body.
- A richer manifest containing shard routes, module counts, quick routes, and rule sources.
- A runtime entry table with a default port/endpoint column, not only entry path and description.
- A knowledge-map section and a standalone TSV field reference section.
- Configurable drift thresholds with strict-drift failure reasons that point to override-based remediation.

The current `super-agent-v3` generator already has stable basics: tracked-file scanning by default, `--filesystem-scan` for exported snapshots, generated drift report, manifest, index shards, guard tests, and `make project-map-check`. The improvement should therefore extend the existing generator instead of replacing it.

## File Structure

- Modify: `scripts/generate_ai_project_map.mjs`
  - Add rule override loading.
  - Add shard-size metadata.
  - Add runtime entry rows.
  - Add focused subsystem count tables.
  - Add configurable drift thresholds and explicit drift remediation details.
  - Expand manifest payload.
  - Render scan policy, search examples, runtime endpoint information, knowledge-map sections, TSV field documentation, and additional sections into the generated Markdown.
- Modify: `scripts/generate_ai_project_map_guard_test.go`
  - Extend fixture assertions for override loading, search examples, runtime rows, shard size, and manifest schema.
- Create: `.ai-project-map.overrides.json`
  - Add repo-specific route and purpose extensions without mutating the generator for every future routing refinement.
- Refresh generated files:
  - `docs/doc/codemap/project-map/AI_PROJECT_MAP.md`
  - `docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md`
  - `docs/doc/codemap/project-map/AI_PROJECT_MANIFEST.json`
  - `docs/doc/codemap/project-map/index/*.tsv`

## Implementation Tasks

### Task 1: Add Rule Override Support

**Files:**
- Modify: `scripts/generate_ai_project_map.mjs`
- Modify: `scripts/generate_ai_project_map_guard_test.go`
- Create: `.ai-project-map.overrides.json`

- [ ] **Step 1: Record existing generator LSP hints before editing**

Before editing, record the current diagnostics in the implementation log or review summary:

```text
scripts/generate_ai_project_map.mjs:4:12 Hint typescript 80001 File is a CommonJS module; it may be converted to an ES module.
scripts/generate_ai_project_map.mjs:54:7 Hint typescript 6133 'EXCLUDES' is declared but its value is never read.
```

Do not add `// @ts-nocheck` to this source script. The unused `EXCLUDES` hint must be removed by Task 2 when `scanPolicySummary()` reads `EXCLUDES`. The CommonJS module-conversion hint must be fixed with a scoped module-format change or recorded as a retained blocker with file, line, severity, code, and reason. Do not claim LSP diagnostics are clean while any Hint remains.

- [ ] **Step 2: Write a failing guard test for override loading**

Add a test to `scripts/generate_ai_project_map_guard_test.go`:

```go
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
  ]
}
`)
	writeFixTestGuardFile(t, root, "internal/testutil/golden/golden.go", "package golden\n")
	runFixTestGuardGit(t, root, "add", ".ai-project-map.overrides.json", "internal/testutil/golden/golden.go")
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

	manifest := readProjectMapManifestDetails(t, root)
	if !stringSliceContains(manifest.RulesSources, ".ai-project-map.overrides.json") {
		t.Fatalf("manifest rules_sources = %#v, want .ai-project-map.overrides.json", manifest.RulesSources)
	}
}
```

Also add the helper types/functions used by the test:

```go
func readProjectMapManifestDetails(t *testing.T, root string) struct {
	Domains      map[string]int `json:"domains"`
	RulesSources []string       `json:"rules_sources"`
} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MANIFEST.json"))
	if err != nil {
		t.Fatalf("read project map manifest: %v", err)
	}
	var manifest struct {
		Domains      map[string]int `json:"domains"`
		RulesSources []string       `json:"rules_sources"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse project map manifest: %v", err)
	}
	return manifest
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Run the test and confirm it fails**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorAppliesRuleOverrides -count=1
```

Expected: FAIL because `--rules`/default override loading and `rules_sources` manifest output do not exist yet.

- [ ] **Step 4: Implement minimal override loading**

In `scripts/generate_ai_project_map.mjs`, first extract the hard-coded object currently inside `moduleDescription()` into a top-level constant, then replace the fixed `const` rule declarations for mutable route/purpose state with runtime containers:

```js
const RULES_CANDIDATES = [
  '.ai-project-map.overrides.json',
];

const MODULE_DESCRIPTIONS = {
  cmd: '可执行入口与 MCP peer',
  internal: '应用内部模块、平台、provider、store 与守卫',
  docs: '代码地图、ADR、计划、迁移和内部说明',
  pkg: '可复用公共库',
  scripts: '工程自动化脚本',
  sql: 'SQL query 源文件',
  migrations: '数据库 migration',
  test: '测试夹具和辅助资源',
  tests: '跨包测试资源',
  '(root)': '仓库根级配置和说明',
};
```

Keep `runtime` initialization after both `PURPOSE_RULES` and `QUICK_ROUTES` have been declared. In the current file this means placing the `runtime` block immediately after the existing `QUICK_ROUTES` array declaration ends:

```js
const runtime = {
  purposeRules: PURPOSE_RULES.slice(),
  quickRoutes: QUICK_ROUTES.slice(),
  topModuleDesc: { ...MODULE_DESCRIPTIONS },
  subsystemDesc: {},
  archivePrefixes: ['docs/archive/'],
  driftThresholds: {
    max_unknown_ratio: 0.18,
  },
  rulesSources: [],
};
```

Add `--rules` argument parsing. Keep existing boolean flags and reject unknown arguments. Replace the current `VALID_ARGS` loop and direct `process.argv.includes(...)` constants with:

```js
function parseArgs(argv) {
  const options = { check: false, strictDrift: false, filesystemScan: false, rulesPath: '' };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--check') options.check = true;
    else if (arg === '--strict-drift') options.strictDrift = true;
    else if (arg === '--filesystem-scan') options.filesystemScan = true;
    else if (arg === '--rules' && i + 1 < argv.length) options.rulesPath = argv[++i];
    else {
      console.error(`project-map: unknown argument ${arg}`);
      process.exit(2);
    }
  }
  return options;
}

const OPTIONS = parseArgs(process.argv.slice(2));
const CHECK = OPTIONS.check;
const STRICT_DRIFT = OPTIONS.strictDrift;
const FILESYSTEM_SCAN = OPTIONS.filesystemScan;
```

Add strict JSON loading:

```js
function loadRuleOverrides(options) {
  const candidates = [];
  if (options.rulesPath) candidates.push(path.resolve(ROOT, options.rulesPath));
  for (const rel of RULES_CANDIDATES) candidates.push(path.join(ROOT, rel));
  const seen = new Set();

  for (const absPath of candidates) {
    if (seen.has(absPath)) continue;
    seen.add(absPath);
    if (!fs.existsSync(absPath)) continue;
    const raw = fs.readFileSync(absPath, 'utf8');
    const patch = JSON.parse(raw);
    applyRulesPatch(patch, absPath);
  }
}

function applyRulesPatch(patch, absPath) {
  if (Array.isArray(patch.purpose_rules_append)) {
    runtime.purposeRules = runtime.purposeRules.concat(patch.purpose_rules_append);
  }
  if (Array.isArray(patch.quick_routes_append)) {
    runtime.quickRoutes = runtime.quickRoutes.concat(patch.quick_routes_append);
  }
  if (patch.top_module_desc_patch && typeof patch.top_module_desc_patch === 'object') {
    Object.assign(runtime.topModuleDesc, patch.top_module_desc_patch);
  }
  if (patch.subsystem_desc_patch && typeof patch.subsystem_desc_patch === 'object') {
    Object.assign(runtime.subsystemDesc, patch.subsystem_desc_patch);
  }
  if (Array.isArray(patch.archive_prefixes)) {
    runtime.archivePrefixes = patch.archive_prefixes.map((value) => String(value || '').trim()).filter(Boolean);
  }
  if (patch.drift_thresholds_patch && typeof patch.drift_thresholds_patch === 'object') {
    Object.assign(runtime.driftThresholds, patch.drift_thresholds_patch);
  }
  runtime.rulesSources.push(path.relative(ROOT, absPath));
}
```

Update `purposeFor`, `moduleDescription`, `renderMap`, and manifest rendering to read from `runtime`:

```js
function purposeFor(file) {
  const rule = runtime.purposeRules.find(([prefix]) => file.startsWith(prefix));
  if (rule) return rule[1];
  if (!file.includes('/')) return '仓库根级配置、入口或说明文件';
  return '未细分职责：请结合路径、文件名和相邻代码判断';
}

function moduleDescription(module) {
  return runtime.topModuleDesc[module] || '其他项目资源';
}
```

Call `loadRuleOverrides(OPTIONS)` once before files are scanned. The current `main()` begins by calling `scanFiles()`, so add the override load as its first statement:

```js
function main() {
  loadRuleOverrides(OPTIONS);
  const files = scanFiles();
  const entries = files.map(buildEntry);
  const grouped = groupByDomain(entries);
  const drift = buildDrift(entries);
  const outputs = renderAll(entries, grouped, drift);
  // Keep the existing check/write branch below this point.
}
```

- [ ] **Step 5: Add the repo override file**

Create `.ai-project-map.overrides.json` with focused current routes:

```json
{
  "purpose_rules_append": [
    ["cmd/agent-runtime/", "Agent runtime command entry and tests"],
    ["cmd/super-dolphin-updater/", "Super-Dolphin updater install and detach command"],
    ["cmd/super-dolphin-release-manifest/", "Release manifest generation command"],
    ["internal/testutil/", "Internal test helpers and golden fixtures"],
    ["internal/util/", "Internal shared utility packages"],
    ["internal/guards/", "Internal guard baselines and guard tests"],
    ["test/fixtures/", "Repository test fixtures"],
    ["tests/scripts/", "Shell-level regression tests"],
    ["third_party/", "Vendored or mirrored third-party reference material"]
  ],
  "quick_routes_append": [
    ["查 AI maintenance gates", "scripts/ai_maintenance/", ".github/workflows/ai-maintenance-gates.yml", "ai maintenance gates validation generated files"],
    ["查 runtime skill 行为", "internal/module/skill/", "internal/provider/shared/provider_home.go", "skill canonical mirror provider home personal hub"],
    ["查 LSP 工作流规则", "docs/internal-notes/LSP系统提示词.md", "cmd/mcp-lsp/tools/", "lsp diagnostics grep inspect xref"],
    ["查 provider bridge", "internal/provider/", "internal/platform/toolbridge/", "provider manifest session toolbridge codex claude"]
  ],
  "top_module_desc_patch": {
    "frontend-app": "当前 React/Vite 新 UI",
    "test": "测试夹具和辅助资源",
    "third_party": "第三方参考材料",
    "tests": "跨包和脚本级测试资源"
  },
  "subsystem_desc_patch": {
    "internal/util": "Internal shared utility packages",
    "internal/guards": "Internal guard baselines and guard tests"
  },
  "archive_prefixes": [
    "docs/archive/"
  ],
  "drift_thresholds_patch": {
    "max_unknown_ratio": 0.05
  }
}
```

- [ ] **Step 6: Run the focused test**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorAppliesRuleOverrides -count=1
```

Expected: PASS.

### Task 2: Add Shard Size, Scan Policy, and Search Examples

**Files:**
- Modify: `scripts/generate_ai_project_map.mjs`
- Modify: `scripts/generate_ai_project_map_guard_test.go`

- [ ] **Step 1: Write a failing test for generated map content**

Extend `TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs` with assertions:

```go
assertOutputContainsAll(t, generated,
	"扫描规则：",
	"| 索引文件 | 文件数 | 大小 | 覆盖范围 |",
	"rg \"thread.*resume|fork\" docs/doc/codemap/project-map/index/modules.tsv",
	"rg \"provider.*manifest|toolbridge\" docs/doc/codemap/project-map/index/platform-provider.tsv",
)
```

- [ ] **Step 2: Run the test and confirm it fails**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs -count=1
```

Expected: FAIL because the current map omits scan policy, shard size, and search examples.

- [ ] **Step 3: Implement shard statistics helpers**

Add helpers:

```js
function scanPolicySummary() {
  return `git ls-files tracked files; excludes: ${EXCLUDES.join(', ')}`;
}

function sizeKB(content) {
  return (Buffer.byteLength(content, 'utf8') / 1024).toFixed(1);
}

function shardStats(grouped) {
  return Object.entries(DOMAIN_FILES)
    .filter(([domain]) => grouped[domain])
    .map(([domain, file]) => {
      const tsv = renderTSV(grouped[domain]);
      return {
        domain,
        file: path.posix.join('docs/doc/codemap/project-map/index', file),
        count: grouped[domain].length,
        size_kb: Number(sizeKB(tsv)),
        description: DOMAIN_DESCRIPTIONS[domain],
      };
    });
}
```

Use `shardStats(grouped)` in `renderAll` so `renderMap` and manifest use the same values.

- [ ] **Step 4: Render examples into `AI_PROJECT_MAP.md`**

Add a section immediately after the shard table:

````markdown
**检索示例：**

```bash
# 1) 先读此 MAP.md 确定目标域
# 2) 搜索对应 TSV 分片
rg "thread.*resume|fork" docs/doc/codemap/project-map/index/modules.tsv
rg "provider.*manifest|toolbridge" docs/doc/codemap/project-map/index/platform-provider.tsv
rg "lsp.*diagnostics|grep" docs/doc/codemap/project-map/index/platform-provider.tsv
rg "ChatPage|composer|timeline" docs/doc/codemap/project-map/index/app-ui.tsv
# 3) 打开目标源码和同包测试
rg --line-number "func .*Resume|func .*Fork" internal/module/thread -g '*.go'
```
````

- [ ] **Step 5: Run the focused test**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs -count=1
```

Expected: PASS.

### Task 3: Add Runtime Entry and Subsystem Maps

**Files:**
- Modify: `scripts/generate_ai_project_map.mjs`
- Modify: `scripts/generate_ai_project_map_guard_test.go`

- [ ] **Step 1: Write failing assertions for new sections**

Add assertions to `TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs`:

```go
assertOutputContainsAll(t, generated,
	"## 4. 运行入口地图",
	"| Desktop host | `cmd/agent-terminal/main.go` |",
	"| MCP orchestration peer | `cmd/mcp-orch/main.go` |",
	"| MCP LSP peer | `cmd/mcp-lsp/main.go` |",
	"## 6. 重点子系统地图",
	"`internal/module/thread`",
	"`internal/provider/codexapp`",
)
```

- [ ] **Step 2: Run the test and confirm it fails**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs -count=1
```

Expected: FAIL because runtime and subsystem sections are absent.

- [ ] **Step 3: Add runtime entry rows**

Add:

```js
const RUNTIME_ENTRY_ROWS = [
  ['Desktop host', 'cmd/agent-terminal/main.go', 'local desktop host', 'Wails desktop host, HTTP/RPC bridge, frontend embed host'],
  ['MCP orchestration peer', 'cmd/mcp-orch/main.go', 'stdio / managed peer', 'Agent lifecycle, DAG, wakeup, workspace and shared file tools'],
  ['MCP LSP peer', 'cmd/mcp-lsp/main.go', 'stdio / managed peer', 'Generic multi-language LSP peer and code intelligence tools'],
  ['React UI', 'frontend-app/src/main.jsx', '5175 dev server', 'Current React/Vite frontend entry'],
  ['macOS dev runner', 'run-new-ui-desktop.sh', '5175 dev UI + local desktop host', 'Desktop host plus Vite dev flow'],
  ['Windows dev runner', 'run-new-ui-desktop.ps1', '5175 dev UI + local desktop host', 'PowerShell desktop host plus Vite dev flow'],
];
```

Render the runtime table with concrete rows:

```markdown
## 4. 运行入口地图

| 运行单元 | 入口文件 | 默认端口/端点 | 说明 |
|---|---|---|---|
${runtimeEntryRows}
```

Build `runtimeEntryRows` in `renderMap()` before returning the template:

```js
const runtimeEntryRows = RUNTIME_ENTRY_ROWS
  .map(([unit, entry, endpoint, desc]) => `| ${unit} | \`${entry}\` | ${endpoint} | ${desc} |`)
  .join('\n');
```

- [ ] **Step 4: Add subsystem count tables**

Add prefix-count helper:

```js
function countByPrefix(entries, prefixes) {
  return prefixes.map(([prefix, description]) => ({
    prefix,
    description,
    count: entries.filter((entry) => entry.path === prefix || entry.path.startsWith(`${prefix}/`)).length,
  })).filter((row) => row.count > 0);
}
```

Use these prefix sets:

```js
const SUBSYSTEM_PREFIX_GROUPS = [
  ['internal/module', [
    ['internal/module/thread', 'thread start/resume/fork/stop 生命周期与绑定真相源'],
    ['internal/module/turn', 'turn 启动、执行、审批与 provider 调度'],
    ['internal/module/prompt', 'prompt 模板、启用条件与 system prompt 组装'],
    ['internal/module/memory', 'memory canonical 管理、检索与持久化接线'],
    ['internal/module/skill', 'skill canonical 管理与 provider-native mirror'],
    ['internal/module/uistate', 'UI 事件投影与 timeline/sidebar 状态'],
  ]],
  ['internal/platform', [
    ['internal/platform/rpc', 'JSON-RPC transport、dispatch、push 与审批框架'],
    ['internal/platform/mcpcontrol', 'MCP 控制平面与 peer 注册'],
    ['internal/platform/toolbridge', 'provider 与 MCP tools 桥接'],
    ['internal/platform/hooks', 'hook 配置、执行与三阶段拦截'],
    ['internal/platform/config', '运行配置、env、provider 与超时策略'],
  ]],
  ['internal/provider', [
    ['internal/provider/codexapp', 'Codex app/server provider 集成'],
    ['internal/provider/claudecli', 'Claude CLI provider 集成'],
    ['internal/provider/shared', 'provider home、配置和共享 helpers'],
    ['internal/provider/unified', '统一 provider 会话解析与 manifest'],
  ]],
  ['cmd peers', [
    ['cmd/mcp-orch/tools', 'mcp-orch MCP tool schema、registry 与 handler'],
    ['cmd/mcp-orch/orchestration', 'agent 生命周期、DAG、wakeup、report 与 hook 消费'],
    ['cmd/mcp-lsp/tools', 'LSP MCP tools 实现'],
    ['cmd/mcp-lsp/multilsp', '多语言 LSP manager、transport 与缓存'],
  ]],
];
```

Render the subsystem tables with concrete Markdown:

```js
function renderSubsystemSections(entries) {
  return SUBSYSTEM_PREFIX_GROUPS.map(([title, prefixes]) => {
    const rows = countByPrefix(entries, prefixes)
      .map((row) => `| \`${row.prefix}\` | ${row.count} | ${row.description} |`)
      .join('\n');
    if (!rows) return '';
    return `### ${title}\n\n| 子系统 | 文件数 | 职责 |\n|---|---:|---|\n${rows}`;
  }).filter(Boolean).join('\n\n');
}
```

Build `subsystemSections` in `renderMap()` and insert it as `## 6. 重点子系统地图`. After adding the new runtime section, keep the generated map order as:

```text
## 1. 项目功能总览
## 2. 索引路由表
## 3. 顶层结构
## 4. 运行入口地图
## 5. 快速定位路由
## 6. 重点子系统地图
## 7. 文档与知识地图
## 8. 索引字段说明
## 9. 维护命令
```

Add these generated knowledge-map and field-reference sections:

```markdown
## 7. 文档与知识地图

- 主线文档（L1）：`README.md`、`docs/doc/codemap/README.md`、`docs/adr/*`、`docs/decisions/*`
- 工作文档（L2）：`docs/plans/*`、`docs/internal-notes/*`
- 历史归档（L3）：`docs/archive/*`（默认不递归索引）
- Agent 体系：`.agents/skills/*/SKILL.md` 是 repo-local skill 指令入口；不要把 `.agents` 当作普通项目源码递归扫描。

## 8. 索引字段说明

| 字段 | 含义 |
|---|---|
| `path` | 相对路径 |
| `module` | 顶层模块 |
| `domain` | project-map 分片域 |
| `type` | 文件类型 |
| `size_bytes` | 文件大小（字节） |
| `purpose` | 文件职责说明 |
| `search_keys` | 建议检索关键词 |
```

- [ ] **Step 5: Run the focused test**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs -count=1
```

Expected: PASS.

### Task 4: Expand Manifest and Drift Gates for Machine Consumers

**Files:**
- Modify: `scripts/generate_ai_project_map.mjs`
- Modify: `scripts/generate_ai_project_map_guard_test.go`

- [ ] **Step 1: Write a failing manifest schema test**

Add:

```go
func TestProjectMapManifestIncludesRoutesAndShardStats(t *testing.T) {
	requireNodeForProjectMap(t)
	root := prepareProjectMapFixture(t, true)

	out, err := runProjectMapGenerator(t, root)
	if err != nil {
		t.Fatalf("project map generator failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(root, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MANIFEST.json"))
	if err != nil {
		t.Fatalf("read project map manifest: %v", err)
	}

	var manifest struct {
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
		RuntimeEntries []struct {
			Unit     string `json:"unit"`
			Entry    string `json:"entry"`
			Endpoint string `json:"endpoint"`
			Desc     string `json:"desc"`
		} `json:"runtime_entries"`
		Drift struct {
			Thresholds map[string]float64 `json:"thresholds"`
		} `json:"drift"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse project map manifest: %v", err)
	}
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
```

- [ ] **Step 2: Run the test and confirm it fails**

Run:

```bash
go test ./scripts -run TestProjectMapManifestIncludesRoutesAndShardStats -count=1
```

Expected: FAIL because manifest lacks these sections.

- [ ] **Step 3: Write a failing strict-drift threshold test**

Add:

```go
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
```

- [ ] **Step 4: Run the threshold test and confirm it fails**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorAppliesDriftThresholdOverride -count=1
```

Expected: FAIL because `drift_thresholds_patch.max_unknown_ratio` is not wired into `buildDrift()` yet.

- [ ] **Step 5: Wire thresholds into drift calculation**

Change `buildDrift()` to use `runtime.driftThresholds.max_unknown_ratio` instead of the hard-coded `0.18`:

```js
function buildDrift(entries) {
  const unknown = entries.filter((entry) => entry.purpose.startsWith('未细分职责'));
  const topUnknown = countBy(unknown.map((entry) => entry.module));
  const unknownRatio = entries.length ? unknown.length / entries.length : 0;
  const maxUnknownRatio = Number(runtime.driftThresholds.max_unknown_ratio);
  const status = unknownRatio > maxUnknownRatio ? 'WARN' : 'OK';
  const warnings = status === 'WARN'
    ? [`unknown_ratio ${(unknownRatio * 100).toFixed(2)}% exceeds max_unknown_ratio ${(maxUnknownRatio * 100).toFixed(2)}%`]
    : [];
  return { status, unknown, topUnknown, unknownRatio, warnings, thresholds: { max_unknown_ratio: maxUnknownRatio } };
}
```

Update `renderDrift()` so the report shows threshold and warning reason:

```markdown
| 最大未细分职责占比阈值 | ${(drift.thresholds.max_unknown_ratio * 100).toFixed(2)}% |
```

and add a warning section that lists `drift.warnings`.

- [ ] **Step 6: Expand manifest rendering**

Render manifest with a `stats` value declared in `renderAll()` and passed to both map and manifest rendering:

```js
function renderAll(entries, grouped, drift) {
  const outputs = {};
  const stats = shardStats(grouped);
  outputs[MAP_MD] = renderMap(entries, grouped, drift, stats);
  outputs[DRIFT_MD] = renderDrift(entries, drift);
  outputs[MANIFEST_JSON] = `${JSON.stringify(renderManifest(entries, grouped, drift, stats), null, 2)}\n`;
  for (const [domain, items] of Object.entries(grouped)) {
    outputs[path.join(INDEX_DIR, DOMAIN_FILES[domain])] = renderTSV(items);
  }
  return outputs;
}
```

Add `renderManifest()`:

```js
function renderManifest(entries, grouped, drift, stats) {
  return {
    version: '1.0',
    generator: 'node:scripts/generate_ai_project_map.mjs',
    generated_at: today(),
    rules_sources: runtime.rulesSources,
    files: entries.length,
    domains: Object.fromEntries(Object.entries(grouped).map(([domain, items]) => [domain, items.length])),
    index_files: {
      map_markdown: 'docs/doc/codemap/project-map/AI_PROJECT_MAP.md',
      drift_report: 'docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md',
      shards: stats.map((item) => ({
        key: item.domain,
        file: item.file,
        file_count: item.count,
        size_kb: item.size_kb,
        desc: item.description,
      })),
    },
    module_counts: countBy(entries.map((entry) => entry.module)),
    quick_routes: runtime.quickRoutes.map(([goal, primary, secondary, keywords]) => ({ goal, primary, secondary, keywords })),
    runtime_entries: RUNTIME_ENTRY_ROWS.map(([unit, entry, endpoint, desc]) => ({ unit, entry, endpoint, desc })),
    drift: {
      status: drift.status,
      unknown_ratio: Number(drift.unknownRatio.toFixed(4)),
      unknown_files: drift.unknown.length,
      thresholds: drift.thresholds,
      warnings: drift.warnings,
      top_unknown_modules: drift.topUnknown,
    },
  };
}
```

- [ ] **Step 7: Run the focused manifest and threshold tests**

Run:

```bash
go test ./scripts -run 'TestProjectMap(Manifest|GeneratorAppliesDriftThresholdOverride)' -count=1
```

Expected: PASS.

### Task 5: Render Knowledge Map and TSV Field Reference

**Files:**
- Modify: `scripts/generate_ai_project_map.mjs`
- Modify: `scripts/generate_ai_project_map_guard_test.go`

- [ ] **Step 1: Add failing assertions for knowledge and field sections**

Extend `TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs` with:

```go
assertOutputContainsAll(t, generated,
	"## 7. 文档与知识地图",
	"`.agents/skills/*/SKILL.md` 是 repo-local skill 指令入口",
	"## 8. 索引字段说明",
	"| `search_keys` | 建议检索关键词 |",
)
```

- [ ] **Step 2: Run the test and confirm it fails**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs -count=1
```

Expected: FAIL because the generated map has no standalone knowledge-map and field-reference sections yet.

- [ ] **Step 3: Add the generated sections**

Insert the exact sections defined in Task 3 after `## 6. 重点子系统地图` and before maintenance commands.

- [ ] **Step 4: Run the focused test**

Run:

```bash
go test ./scripts -run TestProjectMapGeneratorIndexesOnlyTrackedCodeAndDocs -count=1
```

Expected: PASS.

### Task 6: Refresh Generated Project Map Outputs

**Files:**
- Modify generated files under `docs/doc/codemap/project-map/`

- [ ] **Step 1: Refresh the generated map**

Run:

```bash
make project-map-refresh
```

Expected output includes:

```text
project map refreshed
```

- [ ] **Step 2: Inspect generated diff**

Run:

```bash
git diff -- docs/doc/codemap/project-map scripts/generate_ai_project_map.mjs scripts/generate_ai_project_map_guard_test.go .ai-project-map.overrides.json
```

Expected:
- Generated Markdown includes scan policy, shard sizes, search examples, runtime entry map, subsystem map, and enriched manifest.
- No unrelated file changes are included.
- Existing generated TSV paths remain under `docs/doc/codemap/project-map/index/`.

- [ ] **Step 3: Run generated output check**

Run:

```bash
make project-map-check
```

Expected: PASS with `project map generated files are up to date`.

### Task 7: Run Final Verification

**Files:**
- Verify all files changed in Tasks 1-6.

- [ ] **Step 1: Run focused generator tests**

Run:

```bash
go test ./scripts -run 'TestProjectMap(Generator|Manifest)' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run project map check**

Run:

```bash
make project-map-check
```

Expected: PASS.

- [ ] **Step 3: Run codemap check**

Run:

```bash
make codemap-check
```

Expected: PASS.

- [ ] **Step 4: Run LSP diagnostics**

Use LSP:

```text
file(diagnostics, file_path="scripts/generate_ai_project_map.mjs")
```

Expected: no Error, Warning, Information, or Hint diagnostics. If any diagnostic remains, fix it or record it as a blocker with file, line, severity, rule/code, and reason. If the LSP tool is unavailable or times out, record the exact blocker with tool/action, work_dir, file path, error, and retry attempts.

- [ ] **Step 5: Run whitespace and diff sanity checks**

Run:

```bash
git diff --check
```

Expected: no output and exit code 0.

- [ ] **Step 6: Confirm worktree scope**

Run:

```bash
git status --short --untracked-files=all
```

Expected:
- Only owned files from this plan are modified or created.
- `.github/workflows/ai-maintenance-gates.yml` is an existing tracked workflow and remains unmodified unless the user explicitly expands scope.

## Acceptance Criteria

- `AI_PROJECT_MAP.md` exposes scan policy, shard sizes, search examples, runtime entries with default port/endpoint, quick routes, subsystem maps, knowledge-map guidance, TSV field reference, and maintenance commands.
- `AI_PROJECT_MANIFEST.json` exposes `rules_sources`, shard metadata, module counts, quick routes, runtime entries with endpoint, drift thresholds, top unknown modules, and drift summary.
- `.ai-project-map.overrides.json` can add purpose rules, route rows, top-module descriptions, subsystem descriptions, archive prefixes, and drift threshold patches without changing generator core logic.
- `go test ./scripts -run 'TestProjectMap(Generator|Manifest)' -count=1`, `make project-map-check`, `make codemap-check`, and `git diff --check` pass.
- LSP diagnostics for `scripts/generate_ai_project_map.mjs` are clean, or a precise blocker is recorded without claiming completion.
- No manual edits are made to generated project-map files except through `make project-map-refresh`.

## Execution Notes

- Keep this work in a dedicated branch or worktree with prefix `codex/`.
- Do not edit `docs/doc/codemap/project-map/*.md`, `AI_PROJECT_MANIFEST.json`, or `index/*.tsv` by hand.
- Do not scan `.worktrees`, `.agents`, `.claude`, `frontend-app/node_modules`, `frontend-app/dist`, or generated report directories.
- Preserve unrelated local work. `.github/workflows/ai-maintenance-gates.yml` is tracked and outside this plan unless the user explicitly expands scope.
- File-level semantic purpose/search-key inference from comments, package names, and file-name semantics is valuable in `wjboot-v2`, but it is intentionally left for a follow-up plan because it is a larger inference change than this routing/manifest enhancement.

#!/usr/bin/env node

import childProcess from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const SCRIPT_DIR = path.dirname(fileURLToPath(import.meta.url));

const ROOT = findRepoRoot(process.cwd());
const OUTPUT_DIR = path.join(ROOT, 'docs', 'doc', 'codemap', 'project-map');
const INDEX_DIR = path.join(OUTPUT_DIR, 'index');
const MAP_MD = path.join(OUTPUT_DIR, 'AI_PROJECT_MAP.md');
const DRIFT_MD = path.join(OUTPUT_DIR, 'AI_PROJECT_DRIFT.md');
const MANIFEST_JSON = path.join(OUTPUT_DIR, 'AI_PROJECT_MANIFEST.json');

const INDEXED_TOP_LEVEL_DIRS = new Set([
  'cmd',
  'docs',
  'frontend-app',
  'internal',
  'migrations',
  'pkg',
  'scripts',
  'sql',
  'test',
  'tests',
  'third_party',
]);

const INDEXED_ROOT_FILES = new Set([
  'AGENTS.md',
  'CLAUDE.md',
  'Makefile',
  'README.md',
  'go.mod',
  'package-lock.json',
  'package.json',
  'run-new-ui-desktop.ps1',
  'run-new-ui-desktop.sh',
]);

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
let GIT_BLOB_SIZES = null;

const EXCLUDES = [
  '.git/**',
  '.idea/**',
  '.claude/**',
  '.workspace/**',
  '.worktrees/**',
  '.agent/code_exec/**',
  '.agent/workspaces/**',
  '.agnet/report/**',
  '.agnet/shared/**',
  'bin/**',
  'reports/**',
  'docs/archive/**',
  '**/node_modules/**',
  '**/dist/**',
  '**/web-dist/**',
  '**/coverage/**',
  '**/.vite/**',
  '**/.tmp/**',
  '**/tmp/**',
  '**/.gocache/**',
  '**/.gomodcache/**',
  '**/.npm-cache/**',
  'docs/doc/codemap/project-map/**',
  'docs/doc/codemap/ai-index.json',
  'go.sum',
  'test_output.txt',
  'naked_go.txt',
];

const DOMAIN_FILES = {
  'app-ui': 'app-ui.tsv',
  orchestration: 'orchestration.tsv',
  modules: 'modules.tsv',
  'platform-provider': 'platform-provider.tsv',
  'store-sql': 'store-sql.tsv',
  'docs-agent': 'docs-agent.tsv',
  other: 'other.tsv',
};

const DOMAIN_DESCRIPTIONS = {
  'app-ui': '桌面应用、Wails host、React/Vite 前端与 UI 测试',
  orchestration: 'mcp-orch 编排 peer、DAG、workspace、prompt、command、shared-file 工具',
  modules: '业务模块层：dashboard、memory、prompt、skill、thread、turn、uistate 等',
  'platform-provider': '基础设施与 provider 集成：RPC、hooks、toolbridge、Claude/Codex/统一 provider',
  'store-sql': '持久化层：store、sqlc、SQL queries、migrations',
  'docs-agent': '代码地图、ADR/决策、计划与 docs 项目知识',
  other: '公共库、脚本、测试、配置与其他根级资源',
};

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

const PURPOSE_RULES = [
  ['frontend-app/src/pages/', 'React 页面与路由级 UI'],
  ['frontend-app/src/entities/', 'React 客户端状态、thread/composer/store 模型'],
  ['frontend-app/src/shared/api/', 'React 前端 API facade 与 Wails bridge'],
  ['frontend-app/src/shared/ui/', 'React 共享 UI 组件'],
  ['frontend-app/tests/e2e/', 'React 前端 Playwright E2E 与视觉回归测试'],
  ['frontend-app/scripts/', 'React 前端构建与校验脚本'],
  ['frontend-app/', '当前 React/Vite 前端包'],
  ['cmd/agent-terminal/web-dist/', 'frontend-app 构建同步出的 Go embed 静态资源目录'],
  ['cmd/agent-terminal/', 'Wails 桌面 UI、HTTP server、app host 与前端嵌入入口'],

  ['cmd/mcp-orch/orchestration/nodeexec/', 'DAG 节点执行器、typed ops、输入输出与自动化执行'],
  ['cmd/mcp-orch/orchestration/cron/', 'DAG/任务 cron 调度支持'],
  ['cmd/mcp-orch/orchestration/metrics/', 'DAG/编排指标事件与度量'],
  ['cmd/mcp-orch/orchestration/', 'agent 生命周期、turn 队列、DAG、wakeup、report 与 hook 消费'],
  ['cmd/mcp-orch/tools/', 'mcp-orch MCP tool schema、registry 与 handler'],
  ['cmd/mcp-orch/workspace/', 'workspace run 生命周期、merge、dry-run 与文件回写'],
  ['cmd/mcp-orch/store/taskdag/', 'DAG/node/wakeup/worker lease 持久化 store'],
  ['cmd/mcp-orch/store/prompt/', 'prompt template 资源 store'],
  ['cmd/mcp-orch/store/sharedfile/', 'shared file 资源 store'],
  ['cmd/mcp-orch/store/workspace/', 'workspace run/file 持久化 store'],
  ['cmd/mcp-orch/store/sqlc/', 'mcp-orch sqlc 生成层'],
  ['cmd/mcp-orch/sql/queries/', 'mcp-orch SQL query 源文件'],
  ['cmd/mcp-orch/', 'orchestration MCP peer、bootstrap、registry、store 与 sidecar 入口'],

  ['cmd/mcp-lsp/tools/', 'LSP MCP tools 实现'],
  ['cmd/mcp-lsp/multilsp/', '多语言 LSP manager、transport 与缓存'],
  ['cmd/mcp-lsp/search/', '文件搜索与 grep 搜索工具实现'],
  ['cmd/mcp-lsp/', '代码智能/LSP MCP peer'],
  ['cmd/mcp-ida/', 'IDA MCP peer 与逆向分析工具入口'],

  ['internal/app/storeadapter/', '业务 Store 到 module 窄端口的适配器'],
  ['internal/app/runtimeadapter/', 'runtime consumer 的 Store/module 窄端口适配器'],
  ['internal/app/internal/storeguard/', 'adapter 共享的 typed-nil fail-fast 检查 helper'],
  ['internal/app/', 'root Fx/app 装配、runner 与 graph closure'],
  ['internal/contract/', '跨模块稳定接口、事件和 DTO 边界'],
  ['internal/dto/', 'provider/shared DTO 与事件协议'],
  ['internal/module/dashboard/', 'dashboard 读模型与 UI RPC 服务'],
  ['internal/module/memory/', 'memory canonical 管理、UI RPC 与持久化接线'],
  ['internal/module/prompt/', 'prompt 模板、启用条件、system prompt 组装'],
  ['internal/module/skill/', 'skill canonical 管理与 provider-native mirror'],
  ['internal/module/thread/', 'thread start/resume/fork/stop 生命周期与绑定真相源'],
  ['internal/module/turn/', 'turn 启动、执行、审批与 provider 调度'],
  ['internal/module/uistate/', 'UI 事件投影、sidebar/timeline 状态'],
  ['internal/module/', '业务模块层'],

  ['internal/platform/mcpcontrol/', 'MCP 控制平面与 peer 注册'],
  ['internal/platform/toolbridge/', 'provider 与 MCP tools 桥接'],
  ['internal/platform/rpc/', 'JSON-RPC transport、dispatch、push 与审批框架'],
  ['internal/platform/hooks/', 'hook 配置、执行与三阶段拦截'],
  ['internal/platform/config/', '运行配置、env、provider 与超时策略'],
  ['internal/platform/bus/', '事件总线'],
  ['internal/platform/shared/', '共享基础工具'],
  ['internal/platform/sharedfilefs/', 'shared file 磁盘落盘工具'],
  ['internal/platform/sharedfilepath/', 'shared file 路径安全策略'],
  ['internal/platform/', '基础设施层'],
  ['internal/provider/claudecli/', 'Claude CLI provider 集成'],
  ['internal/provider/codexapp/', 'Codex app/server provider 集成'],
  ['internal/provider/unified/', '统一 provider 会话解析与 manifest'],
  ['internal/provider/shared/', 'provider home、配置和共享 helpers'],
  ['internal/provider/', 'AI provider 集成层'],
  ['internal/mcpserver/', 'MCP server 公共框架、bootstrap 与 transport'],
  ['internal/ui/wails/', 'Wails binding 与 UI RPC 桥接'],
  ['internal/archtest/', '架构守卫、baseline ratchet 与冻结规则'],
  ['internal/devtools/', '开发工具与生成器辅助包'],
  ['internal/store/', '应用级持久化 store'],

  ['sql/queries/', '仓库级 SQL query 源文件'],
  ['migrations/', '数据库 migration'],
  ['pkg/logger/', '统一日志、采样、relay、watchdog 与 trace context'],
  ['pkg/dagmetrics/', 'DAG 指标公共库'],
  ['pkg/dreammetrics/', 'dream pipeline 指标公共库'],
  ['pkg/skillmetrics/', 'skill 指标公共库'],
  ['pkg/', '可复用公共库'],
  ['scripts/', '工程自动化、测试守卫、代码地图与开发脚本'],
  ['docs/doc/codemap/', '手写代码地图卷与自动 ai-index'],
  ['docs/decisions/', 'ADR/决策记录'],
  ['docs/adr/', '架构决策记录'],
  ['docs/plans/', '历史计划与迁移执行文档'],
  ['docs/internal-notes/', '内部提示词与工程方法记录'],
  ['docs/', '项目文档体系'],
];

const QUICK_ROUTES = [
  ['修改桌面 Go/Wails host', 'cmd/agent-terminal/', 'internal/ui/wails/', 'wails binding rpc app host'],
  ['修改 React 聊天 UI', 'frontend-app/src/pages/chat/', 'frontend-app/src/entities/client/model/', 'ChatPage composer timeline store sendDraft'],
  ['修改 DAG 编排执行', 'cmd/mcp-orch/orchestration/', 'cmd/mcp-orch/store/taskdag/', 'dag wakeup nodeexec dispatcher retry'],
  ['修改 MCP orchestration tools', 'cmd/mcp-orch/tools/', 'cmd/mcp-orch/orchestration/rpc.go', 'task_dag agent_launch schema registry'],
  ['修改 LSP 工具', 'cmd/mcp-lsp/tools/', 'cmd/mcp-lsp/multilsp/', 'lsp tool grep file search diagnostics'],
  ['修改 thread/turn 生命周期', 'internal/module/thread/', 'internal/module/turn/', 'thread start resume fork turn provider'],
  ['修改 memory/prompt/skill', 'internal/module/memory/', 'internal/module/prompt/', 'memory prompt skill canonical mirror'],
  ['修改 provider 接入', 'internal/provider/', 'internal/platform/toolbridge/', 'claude codex provider session manifest toolbridge'],
  ['理解 root Fx 装配顺序', 'internal/app/modules.go', 'internal/app/modules_graph_test.go', 'app module fx graph modules runtime order toolbridge provider'],
  ['修改 App adapter 分包', 'internal/app/storeadapter/', 'internal/app/runtimeadapter/', 'store runtime adapter'],
  ['修改控制面/bootstrap', 'internal/platform/mcpcontrol/', 'internal/mcpserver/common/bootstrap/', 'peer register bootstrap hooks'],
  ['修改持久化/SQL', 'internal/store/', 'sql/queries/', 'store sqlc migration queries'],
  ['修改代码地图', 'docs/doc/codemap/', 'scripts/codemap_index.go', 'codemap ai-index make codemap-refresh'],
  ['修改架构守卫', 'internal/archtest/', 'internal/archtest/freeze_baseline.json', 'guard baseline ratchet freeze'],
];

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

const RUNTIME_ENTRY_ROWS = [
  ['Desktop host', 'cmd/agent-terminal/main.go', 'local desktop host', 'Wails desktop host, HTTP/RPC bridge, frontend embed host'],
  ['MCP orchestration peer', 'cmd/mcp-orch/main.go', 'stdio / managed peer', 'Agent lifecycle, DAG, wakeup, workspace and shared file tools'],
  ['MCP LSP peer', 'cmd/mcp-lsp/main.go', 'stdio / managed peer', 'Generic multi-language LSP peer and code intelligence tools'],
  ['React UI', 'frontend-app/src/main.jsx', '5175 dev server', 'Current React/Vite frontend entry'],
  ['macOS dev runner', 'run-new-ui-desktop.sh', '5175 dev UI + local desktop host', 'Desktop host plus Vite dev flow'],
  ['Windows dev runner', 'run-new-ui-desktop.ps1', '5175 dev UI + local desktop host', 'PowerShell desktop host plus Vite dev flow'],
];

const APP_ASSEMBLY_ROWS = [
  ['1', 'Root shell', 'internal/app/modules.go', 'NewLogger、pidregistry、config/db/bus/rpc/hooks/runner/observability；先读作基础设施供给层，不读作业务执行顺序。'],
  ['2', 'Persistence and control plane', 'internal/store、internal/platform/mcpcontrol、internal/mcpserver', 'store 与 MCP 控制面先提供持久化、peer 注册、server/bootstrap 能力，后续 module 通过 contract 端口消费。'],
  ['3', 'Store adapters', 'internal/app/storeadapter', '把 canonical Store 实现投影为业务 module 消费的窄端口；按 domain child 路由，业务映射留在各 child。'],
  ['4', 'Business semantics', 'internal/module/{dashboard,memory,prompt,skill,thread,turn,uistate}', 'memory/prompt/skill 支撑 thread/turn；thread 负责 start/resume/fork 绑定真相源，turn 负责回合执行与审批调度，uistate 投影事件给 UI。'],
  ['5', 'Provider and tools', 'internal/provider/{unified,codexapp}、internal/platform/toolbridge', 'unified 管 session/manifest，codexapp 提供 provider driver，toolbridge 把 host/MCP tools 暴露给 provider；claudecli 当前不在 root Module 中启用。'],
  ['6', 'Runtime adapters', 'internal/app/runtimeadapter', '为 mcpcontrol/toolbridge/cachekeepalive/builtintools 等 runtime consumer 提供窄端口与 root-scope 接线。'],
  ['7', 'Root adapters', 'internal/app/modules.go:fx.Provide', 'AsRPCRunner、DAGRuntime、thread.OrchestrationFacade、RuntimeReporter、SessionPorts 是仍由 root 持有的跨边界裁剪端口。'],
  ['8', 'Runtime owner', 'internal/app/app.go、internal/app/runner.go', 'newFXApp/newDesktopFXApp 叠加 Module + BindRuntime；桌面态额外装 uiwails.Module；实际 start/stop 由 Fx 依赖图与 group:"runners" 决定。'],
  ['9', 'Graph guards', 'internal/app/modules_graph_test.go、internal/archtest/fx_graph_test.go', 'ValidateApp 与定向 Populate 测试冻结 app 图闭合、thread/turn 配置、toolbridge lifecycle、datasource、workflowtemplate、orchestration facade 等供给点。'],
];

const SUBSYSTEM_PREFIX_GROUPS = [
  ['internal/app assembly and adapters', [
    ['internal/app/storeadapter', '业务 Store 到 module 窄端口的适配器'],
    ['internal/app/runtimeadapter', 'runtime consumer 的 Store/module 窄端口适配器'],
    ['internal/app/internal/storeguard', 'adapter 共享的 typed-nil fail-fast 检查 helper'],
    ['internal/app', '全域汇总（root + adapter packages）'],
  ]],
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

function main() {
  loadRuleOverrides(OPTIONS);
  GIT_BLOB_SIZES = FILESYSTEM_SCAN ? null : loadGitTrackedBlobSizes();
  const files = scanFiles();
  const entries = files.map(buildEntry);
  const grouped = groupByDomain(entries);
  const drift = buildDrift(entries);
  const outputs = renderAll(entries, grouped, drift);

  if (STRICT_DRIFT && drift.status !== 'OK') {
    console.error(`project-map-check: drift status ${drift.status}`);
    for (const warning of drift.warnings) console.error(`project-map-check: ${warning}`);
    process.exit(1);
  }

  if (CHECK) {
    checkOutputs(outputs, drift);
    return;
  }

  fs.mkdirSync(INDEX_DIR, { recursive: true });
  for (const file of existingGeneratedFiles()) {
    if (!Object.prototype.hasOwnProperty.call(outputs, file)) fs.unlinkSync(file);
  }
  for (const [file, content] of Object.entries(outputs)) {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, content);
  }
  console.log(`project-map: ${entries.length} files, ${Object.keys(grouped).length} domains, drift=${drift.status}`);
}

function scanFiles() {
  if (!FILESYSTEM_SCAN) return scanGitTrackedFiles();
  return scanFilesystemFiles();
}

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
  if (!patch || typeof patch !== 'object' || Array.isArray(patch)) {
    throw new Error(`project-map: rules file must contain a JSON object: ${path.relative(ROOT, absPath)}`);
  }
  if (Array.isArray(patch.purpose_rules_append)) {
    runtime.purposeRules = runtime.purposeRules.concat(patch.purpose_rules_append);
  }
  if (Array.isArray(patch.quick_routes_append)) {
    runtime.quickRoutes = runtime.quickRoutes.concat(patch.quick_routes_append);
  }
  if (patch.top_module_desc_patch && typeof patch.top_module_desc_patch === 'object' && !Array.isArray(patch.top_module_desc_patch)) {
    Object.assign(runtime.topModuleDesc, patch.top_module_desc_patch);
  }
  if (patch.subsystem_desc_patch && typeof patch.subsystem_desc_patch === 'object' && !Array.isArray(patch.subsystem_desc_patch)) {
    Object.assign(runtime.subsystemDesc, patch.subsystem_desc_patch);
  }
  if (Array.isArray(patch.archive_prefixes)) {
    runtime.archivePrefixes = patch.archive_prefixes.map((value) => String(value || '').trim()).filter(Boolean);
  }
  if (patch.drift_thresholds_patch && typeof patch.drift_thresholds_patch === 'object' && !Array.isArray(patch.drift_thresholds_patch)) {
    Object.assign(runtime.driftThresholds, patch.drift_thresholds_patch);
  }
  runtime.rulesSources.push(path.relative(ROOT, absPath));
}

function scanGitTrackedFiles() {
  return [...GIT_BLOB_SIZES.keys()].sort();
}

function loadGitTrackedBlobSizes() {
  const lsResult = childProcess.spawnSync('git', ['-C', ROOT, 'ls-files', '-s', '-z'], {
    encoding: 'buffer',
    maxBuffer: 16 * 1024 * 1024,
  });
  if (lsResult.status !== 0) {
    const stderr = lsResult.stderr ? lsResult.stderr.toString('utf8').trim() : '';
    console.error(`project-map: git ls-files failed; use --filesystem-scan only for explicit exported snapshots${stderr ? `: ${stderr}` : ''}`);
    process.exit(1);
  }

  const records = lsResult.stdout
    .toString('utf8')
    .split('\0')
    .filter(Boolean)
    .map((entry) => {
      const match = entry.match(/^\d+ ([0-9a-f]{40,64}) \d+\t(.+)$/);
      if (!match) return null;
      return { oid: match[1], rel: normalize(match[2]) };
    })
    .filter((record) => record && shouldIndexPath(record.rel) && !shouldSkipPath(record.rel));

  const objectSizes = loadGitObjectSizes([...new Set(records.map((record) => record.oid))]);
  const sizes = new Map();
  for (const record of records) {
    const size = objectSizes.get(record.oid);
    if (size === undefined) continue;
    sizes.set(record.rel, size);
  }
  return sizes;
}

function loadGitObjectSizes(objectIds) {
  if (objectIds.length === 0) return new Map();
  const result = childProcess.spawnSync('git', ['-C', ROOT, 'cat-file', '--batch'], {
    input: `${objectIds.join('\n')}\n`,
    maxBuffer: 64 * 1024 * 1024,
  });
  if (result.status !== 0) {
    const stderr = result.stderr ? result.stderr.toString('utf8').trim() : '';
    console.error(`project-map: git cat-file failed${stderr ? `: ${stderr}` : ''}`);
    process.exit(1);
  }

  const sizes = new Map();
  let offset = 0;
  while (offset < result.stdout.length) {
    const headerEnd = result.stdout.indexOf(10, offset);
    if (headerEnd === -1) break;
    const header = result.stdout.subarray(offset, headerEnd).toString('utf8');
    offset = headerEnd + 1;
    if (!header) continue;
    const [oid, type, sizeText] = header.split(' ');
    const size = Number(sizeText);
    if (type !== 'blob' || !Number.isFinite(size)) {
      offset += Math.max(0, size) + 1;
      continue;
    }
    const content = result.stdout.subarray(offset, offset + size);
    sizes.set(oid, normalizedContentSize(content));
    offset += size + 1;
  }
  return sizes;
}

function scanFilesystemFiles() {
  const files = [];
  walk(ROOT, '', files);
  return files.sort();
}

function walk(absDir, relDir, files) {
  for (const dirent of fs.readdirSync(absDir, { withFileTypes: true })) {
    const rel = normalize(relDir ? path.posix.join(relDir, dirent.name) : dirent.name);
    const abs = path.join(absDir, dirent.name);
    if (dirent.isDirectory()) {
      if (shouldDescendIntoDir(rel) && !shouldSkipDir(rel)) walk(abs, rel, files);
      continue;
    }
    if (dirent.isFile() && shouldIndexPath(rel) && !shouldSkipFile(rel)) files.push(rel);
  }
}

function shouldSkipDir(rel) {
  const name = path.posix.basename(rel);
  if (['.build-cache', '.git', '.idea', '.claude', '.workspace', '.worktrees', '.local', '.mypy_cache', 'codex-app 2', 'bin', 'node_modules', 'dist', 'web-dist', 'coverage', '.vite', '.tmp', 'tmp', '.gocache', '.gomodcache', '.npm-cache', '__pycache__'].includes(name)) return true;
  return [
    '.agent/code_exec',
    '.agent/report',
    '.agent/workspaces',
    '.agents/report',
    '.agents/shared',
    '.agnet/report',
    '.agnet/shared',
    ...runtime.archivePrefixes.map((prefix) => prefix.replace(/\/+$/, '')),
    'docs/doc/codemap/project-map',
    'reports',
  ].some((prefix) => rel === prefix || rel.startsWith(`${prefix}/`));
}

function shouldSkipPath(rel) {
  const parts = rel.split('/');
  for (let i = 0; i < parts.length - 1; i += 1) {
    if (shouldSkipDir(parts.slice(0, i + 1).join('/'))) return true;
  }
  return shouldSkipFile(rel);
}

function shouldDescendIntoDir(rel) {
  return INDEXED_TOP_LEVEL_DIRS.has(rel.split('/')[0]);
}

function shouldIndexPath(rel) {
  if (!rel.includes('/')) return INDEXED_ROOT_FILES.has(rel);
  return INDEXED_TOP_LEVEL_DIRS.has(rel.split('/')[0]);
}

function shouldSkipFile(rel) {
  if (path.posix.basename(rel) === '.DS_Store') return true;
  return ['go.sum', 'test_output.txt', 'naked_go.txt', 'docs/doc/codemap/ai-index.json'].includes(rel);
}

function buildEntry(file) {
  return {
    path: file,
    module: topModule(file),
    domain: classifyDomain(file),
    type: classifyType(file),
    size: safeSize(file),
    purpose: purposeFor(file),
    searchKeys: searchKeysFor(file),
  };
}

function topModule(file) {
  const first = file.split('/')[0] || '(root)';
  return file.includes('/') ? first : '(root)';
}

function classifyDomain(file) {
  if (file.startsWith('frontend-app/')) return 'app-ui';
  if (file.startsWith('cmd/agent-terminal/')) return 'app-ui';
  if (file.startsWith('cmd/mcp-orch/')) return 'orchestration';
  if (file.startsWith('internal/module/')) return 'modules';
  if (file.startsWith('internal/platform/') || file.startsWith('internal/provider/') || file.startsWith('internal/mcpserver/') || file.startsWith('cmd/mcp-lsp/') || file.startsWith('cmd/mcp-ida/')) return 'platform-provider';
  if (file.startsWith('internal/store/') || file.startsWith('sql/') || file.startsWith('migrations/') || file.startsWith('cmd/mcp-orch/store/') || file.startsWith('cmd/mcp-orch/sql/')) return 'store-sql';
  if (file.startsWith('docs/') || file === 'CLAUDE.md' || file === 'AGENTS.md' || file === 'README.md') return 'docs-agent';
  return 'other';
}

function classifyType(file) {
  if (file.endsWith('_test.go')) return 'go-test';
  if (file.endsWith('.go')) return 'go-source';
  if (
    file.endsWith('.test.js') ||
    file.endsWith('.spec.js') ||
    file.endsWith('.test.mjs') ||
    file.endsWith('.spec.mjs') ||
    file.endsWith('.test.cjs') ||
    file.endsWith('.spec.cjs') ||
    file.endsWith('.test.ts') ||
    file.endsWith('.spec.ts')
  ) return 'js-test';
  if (file.endsWith('.js') || file.endsWith('.cjs') || file.endsWith('.mjs')) return 'js-source';
  if (file.endsWith('.ts') || file.endsWith('.tsx')) return 'ts-source';
  if (file.endsWith('.vue')) return 'vue-source';
  if (file.endsWith('.md')) return 'doc';
  if (file.endsWith('.sql')) return 'sql';
  if (file.endsWith('.json')) return 'json';
  if (file.endsWith('.yaml') || file.endsWith('.yml')) return 'yaml';
  if (file.endsWith('.sh') || file.endsWith('.ps1')) return 'script';
  return path.extname(file).replace(/^\./, '') || 'file';
}

function safeSize(file) {
  if (GIT_BLOB_SIZES && GIT_BLOB_SIZES.has(file)) return GIT_BLOB_SIZES.get(file);
  if (FILESYSTEM_SCAN) return safeFilesystemScanSize(file);
  try {
    return fs.statSync(path.join(ROOT, file)).size;
  } catch {
    return 0;
  }
}

function safeFilesystemScanSize(file) {
  const abs = path.join(ROOT, file);
  try {
    const data = fs.readFileSync(abs);
    return normalizedContentSize(data);
  } catch {
    return 0;
  }
}

function normalizedContentSize(data) {
  if (data.includes(0)) return data.length;
  return Buffer.byteLength(data.toString('utf8').replace(/\r\n/g, '\n'), 'utf8');
}

function purposeFor(file) {
  const rule = runtime.purposeRules.find(([prefix]) => file.startsWith(prefix));
  if (rule) return rule[1];
  if (!file.includes('/')) return '仓库根级配置、入口或说明文件';
  return '未细分职责：请结合路径、文件名和相邻代码判断';
}

function searchKeysFor(file) {
  const parts = file.replace(/\.[^.]+$/, '').split(/[\/_.-]+/).filter(Boolean);
  const purpose = purposeFor(file).replace(/[，。、（）/]/g, ' ');
  const keys = [...parts, ...purpose.split(/\s+/)].filter(Boolean);
  return [...new Set(keys)].slice(0, 16).join(',');
}

function groupByDomain(entries) {
  const grouped = {};
  for (const entry of entries) {
    (grouped[entry.domain] ||= []).push(entry);
  }
  return grouped;
}

function buildDrift(entries) {
  const unknown = entries.filter((entry) => entry.purpose.startsWith('未细分职责'));
  const topUnknown = countBy(unknown.map((entry) => entry.module));
  const unknownRatio = entries.length ? unknown.length / entries.length : 0;
  const maxUnknownRatio = Number(runtime.driftThresholds.max_unknown_ratio);
  if (!Number.isFinite(maxUnknownRatio)) {
    throw new Error('project-map: drift_thresholds.max_unknown_ratio must be a number');
  }
  const status = unknownRatio > maxUnknownRatio ? 'WARN' : 'OK';
  const warnings = status === 'WARN'
    ? [`unknown_ratio ${(unknownRatio * 100).toFixed(2)}% exceeds max_unknown_ratio ${(maxUnknownRatio * 100).toFixed(2)}%`]
    : [];
  return { status, unknown, topUnknown, unknownRatio, warnings, thresholds: { max_unknown_ratio: maxUnknownRatio } };
}

function renderAll(entries, grouped, drift) {
  const outputs = {};
  const stats = shardStats(grouped);
  outputs[MAP_MD] = renderMap(entries, drift, stats);
  outputs[DRIFT_MD] = renderDrift(entries, drift);
  outputs[MANIFEST_JSON] = `${JSON.stringify(renderManifest(entries, grouped, drift, stats), null, 2)}\n`;
  for (const [domain, items] of Object.entries(grouped)) {
    outputs[path.join(INDEX_DIR, DOMAIN_FILES[domain])] = renderTSV(items);
  }
  return outputs;
}

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

function scanPolicySummary() {
  return `allowlisted project files; excludes: ${EXCLUDES.join(', ')}`;
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

function countByPrefix(entries, prefixes) {
  return prefixes.map(([prefix, description]) => ({
    prefix,
    description: runtime.subsystemDesc[prefix] || description,
    count: entries.filter((entry) => entry.path === prefix || entry.path.startsWith(`${prefix}/`)).length,
  })).filter((row) => row.count > 0);
}

function renderSubsystemSections(entries) {
  return SUBSYSTEM_PREFIX_GROUPS.map(([title, prefixes]) => {
    const rows = countByPrefix(entries, prefixes)
      .map((row) => `| \`${row.prefix}\` | ${row.count} | ${row.description} |`)
      .join('\n');
    if (!rows) return '';
    return `### ${title}\n\n| 子系统 | 文件数 | 职责 |\n|---|---:|---|\n${rows}`;
  }).filter(Boolean).join('\n\n') || '无匹配子系统。';
}

function renderAppAssemblyRows() {
  return APP_ASSEMBLY_ROWS
    .map(([step, layer, anchors, reading]) => `| ${step} | ${layer} | \`${anchors}\` | ${reading} |`)
    .join('\n');
}

function renderMap(entries, drift, stats) {
  const topCounts = countBy(entries.map((entry) => entry.module));
  const domainRows = stats
    .map((item) => `| \`${item.file}\` | ${item.count} | ${item.size_kb.toFixed(1)} KB | ${item.description} |`)
    .join('\n');
  const topRows = Object.entries(topCounts)
    .sort((a, b) => b[1] - a[1])
    .map(([module, count]) => `| \`${module}\` | ${count} | ${moduleDescription(module)} |`)
    .join('\n');
  const routeRows = runtime.quickRoutes.map(([target, first, second, keys]) => `| ${target} | \`${first}\` | \`${second}\` | \`${keys}\` |`).join('\n');
  const runtimeEntryRows = RUNTIME_ENTRY_ROWS
    .map(([unit, entry, endpoint, desc]) => `| ${unit} | \`${entry}\` | ${endpoint} | ${desc} |`)
    .join('\n');
  const appAssemblyRows = renderAppAssemblyRows();
  const subsystemSections = renderSubsystemSections(entries);

  return `# AI 项目地图（Super-Dolphin）\n\n> 生成时间：${today()}\n>\n> 已索引文件：**${entries.length}**\n>\n> 扫描规则：${scanPolicySummary()}\n>\n> 漂移状态：**${drift.status}**（详见 \`docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md\`）\n\n## 1. 项目功能总览\n\nSuper-Dolphin / super-agent-v3 是一个本地多 Agent 桌面应用与 MCP peer 体系，核心由以下能力构成：\n\n- **桌面控制台**：\`cmd/agent-terminal\` 提供 Wails/Go host、HTTP/RPC 桥，\`frontend-app\` 提供 React/Vite 前端。\n- **编排 peer**：\`cmd/mcp-orch\` 管理 agent 生命周期、DAG、wakeup、workspace、prompt、command card 与 shared file tools。\n- **代码智能 peer**：\`cmd/mcp-lsp\` 提供多语言 LSP、文件搜索、结构和诊断工具。\n- **业务模块层**：\`internal/module\` 承载 dashboard、memory、prompt、skill、thread、turn、uistate 等运行语义。\n- **基础设施与 provider**：\`internal/platform\`、\`internal/provider\` 负责 RPC、hooks、toolbridge、控制面、Claude/Codex provider 集成。\n- **持久化与治理**：\`internal/store\`、\`sql\`、\`migrations\`、\`internal/archtest\`、\`docs/doc/codemap\` 提供数据访问、schema、架构守卫和代码地图。\n\n## 2. 索引路由表\n\n| 索引文件 | 文件数 | 大小 | 覆盖范围 |\n|---|---:|---:|---|\n${domainRows}\n\n**检索示例：**\n\n\`\`\`bash\n# 1) 先读此 MAP.md 确定目标域\n# 2) 搜索对应 TSV 分片\nrg "thread.*resume|fork" docs/doc/codemap/project-map/index/modules.tsv\nrg "provider.*manifest|toolbridge" docs/doc/codemap/project-map/index/platform-provider.tsv\nrg "lsp.*diagnostics|grep" docs/doc/codemap/project-map/index/platform-provider.tsv\nrg "ChatPage|composer|timeline" docs/doc/codemap/project-map/index/app-ui.tsv\n# 3) 打开目标源码和同包测试\nrg --line-number "func .*Resume|func .*Fork" internal/module/thread -g '*.go'\n\`\`\`\n\n## 3. 顶层结构\n\n| 模块 | 文件数 | 职责 |\n|---|---:|---|\n${topRows}\n\n## 4. 运行入口地图\n\n| 运行单元 | 入口文件 | 默认端口/端点 | 说明 |\n|---|---|---|---|\n${runtimeEntryRows}\n\n## 5. Root Fx 装配阅读顺序\n\n\`internal/app/modules.go\` 是根装配清单，不是严格的业务执行时序。阅读时先按下面的依赖层理解，再用 Fx graph tests 确认供给点是否闭合。\n\n| 步骤 | 层 | 锚点 | AI 阅读提示 |\n|---:|---|---|---|\n${appAssemblyRows}\n\n## 6. 快速定位路由\n\n| 目标 | 首选路径 | 次选路径 | 检索关键词 |\n|---|---|---|---|\n${routeRows}\n\n## 7. 重点子系统地图\n\n${subsystemSections}\n\n## 8. 文档与知识地图\n\n- 主线文档（L1）：\`README.md\`、\`docs/doc/codemap/README.md\`、\`docs/adr/*\`、\`docs/decisions/*\`\n- 工作文档（L2）：\`docs/plans/*\`、\`docs/internal-notes/*\`\n- 历史归档（L3）：\`${runtime.archivePrefixes.join('`、`')}\`（默认不递归索引）\n- Agent 体系：\`.agents/skills/*/SKILL.md\` 是 repo-local skill 指令入口；不要把 \`.agents\` 当作普通项目源码递归扫描。\n\n## 9. 索引字段说明\n\n| 字段 | 含义 |\n|---|---|\n| \`path\` | 相对路径 |\n| \`module\` | 顶层模块 |\n| \`domain\` | project-map 分片域 |\n| \`type\` | 文件类型 |\n| \`size_bytes\` | 文件大小（字节） |\n| \`purpose\` | 文件职责说明 |\n| \`search_keys\` | 建议检索关键词 |\n\n## 10. 维护命令\n\n\`\`\`bash\nnode scripts/generate_ai_project_map.mjs\nnode scripts/generate_ai_project_map.mjs --check\nnode scripts/generate_ai_project_map.mjs --strict-drift\nnode scripts/generate_ai_project_map.mjs --rules path/to/overrides.json\n\`\`\`\n\n现有手写代码地图仍以 \`docs/doc/codemap/README.md\` 和 \`make codemap-check\` / \`make codemap-refresh\` 为准；本目录提供低 token 的全仓文件级索引补充。\n`;
}

function renderDrift(entries, drift) {
  const topUnknownRows = Object.entries(drift.topUnknown)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 20)
    .map(([module, count]) => `| \`${module}\` | ${count} |`)
    .join('\n') || '| - | 0 |';
  const sample = drift.unknown.slice(0, 50).map((entry) => `- \`${entry.path}\``).join('\n') || '- 无';
  const warnings = drift.warnings.map((warning) => `- ${warning}`).join('\n') || '- 无';
  return `# AI 项目地图漂移报告\n\n> 状态：**${drift.status}**\n>\n> 已索引文件：${entries.length}\n>\n> 未细分职责文件：${drift.unknown.length}\n\n## 1. 漂移指标\n\n| 指标 | 当前值 |\n|---|---:|\n| 未细分职责文件数 | ${drift.unknown.length} |\n| 未细分职责占比 | ${(drift.unknownRatio * 100).toFixed(2)}% |\n| 最大未细分职责占比阈值 | ${(drift.thresholds.max_unknown_ratio * 100).toFixed(2)}% |\n\n## 2. 漂移告警\n\n${warnings}\n\n## 3. 未细分职责分布\n\n| 模块 | 文件数 |\n|---|---:|\n${topUnknownRows}\n\n## 4. 样例文件\n\n${sample}\n\n## 5. 修复方式\n\n优先在 \`.ai-project-map.overrides.json\` 中补充 \`purpose_rules_append\`，或用 \`--rules\` 传入显式规则文件，然后重新运行：\n\n\`\`\`bash\nnode scripts/generate_ai_project_map.mjs\n\`\`\`\n`;
}

function renderTSV(items) {
  const lines = ['path\tmodule\tdomain\ttype\tsize_bytes\tpurpose\tsearch_keys'];
  for (const entry of items.sort((a, b) => a.path.localeCompare(b.path))) {
    lines.push([entry.path, entry.module, entry.domain, entry.type, entry.size, entry.purpose, entry.searchKeys].map(tsvCell).join('\t'));
  }
  return `${lines.join('\n')}\n`;
}

function checkOutputs(outputs, drift) {
  let stale = false;
  for (const file of existingGeneratedFiles()) {
    if (!Object.prototype.hasOwnProperty.call(outputs, file)) {
      stale = true;
      console.error(`project-map-check: stale generated file ${path.relative(ROOT, file)}`);
    }
  }
  for (const [file, want] of Object.entries(outputs)) {
    let got = null;
    try {
      got = fs.readFileSync(file, 'utf8');
    } catch {
      stale = true;
      console.error(`project-map-check: missing ${path.relative(ROOT, file)}`);
      continue;
    }
    if (got !== want) {
      stale = true;
      console.error(`project-map-check: ${path.relative(ROOT, file)} differs from generated output`);
    }
  }
  if (STRICT_DRIFT && drift.status !== 'OK') {
    stale = true;
    console.error(`project-map-check: drift status ${drift.status}`);
  }
  if (stale) process.exit(1);
  console.log(`project-map: ${Object.keys(outputs).length} files up to date, drift=${drift.status}`);
}

function findRepoRoot(start) {
  let dir = path.resolve(start);
  while (true) {
    if (fs.existsSync(path.join(dir, 'go.mod')) && fs.existsSync(path.join(dir, 'CLAUDE.md'))) return dir;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  return path.resolve(SCRIPT_DIR, '..');
}

function existingGeneratedFiles() {
  const files = [];
  for (const file of [MAP_MD, DRIFT_MD, MANIFEST_JSON]) {
    if (fs.existsSync(file)) files.push(file);
  }
  if (fs.existsSync(INDEX_DIR)) {
    for (const name of fs.readdirSync(INDEX_DIR)) {
      if (name.endsWith('.tsv')) files.push(path.join(INDEX_DIR, name));
    }
  }
  return files;
}

function countBy(values) {
  const counts = {};
  for (const value of values) counts[value] = (counts[value] || 0) + 1;
  return counts;
}

function moduleDescription(module) {
  return runtime.topModuleDesc[module] || '其他项目资源';
}

function tsvCell(value) {
  return String(value).replace(/[\t\r\n]+/g, ' ').trim();
}

function normalize(file) {
  return file.replace(/\\/g, '/');
}

function today() {
  return new Date().toISOString().slice(0, 10);
}

main();

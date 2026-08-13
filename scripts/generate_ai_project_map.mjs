#!/usr/bin/env node

import childProcess from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const GENERATOR_PATH = fileURLToPath(import.meta.url);
const SCRIPT_DIR = path.dirname(GENERATOR_PATH);

const ROOT = findRepoRoot(process.cwd());
const OUTPUT_DIR = path.join(ROOT, 'docs', 'doc', 'codemap', 'project-map');
const INDEX_DIR = path.join(OUTPUT_DIR, 'index');
const MAP_MD = path.join(OUTPUT_DIR, 'AI_PROJECT_MAP.md');
const DRIFT_MD = path.join(OUTPUT_DIR, 'AI_PROJECT_DRIFT.md');
const MANIFEST_JSON = path.join(OUTPUT_DIR, 'AI_PROJECT_MANIFEST.json');
const INPUT_FINGERPRINT_SCHEMA = 'project-map-input/v1';

const INDEXED_TOP_LEVEL_DIRS = new Set([
  'cmd',
  'config',
  'docs',
  'frontend-app',
  'internal',
  'pkg',
  'scripts',
  'sql',
  'test',
  'tests',
  'third_party',
  '.githooks',
]);

const INDEXED_ROOT_FILES = new Set([
  'AGENTS.md',
  'CLAUDE.md',
  'Makefile',
  'README.de.md',
  'README.es.md',
  'README.ja.md',
  'README.ko.md',
  'README.md',
  'README.zh-CN.md',
  'go.mod',
  'package-lock.json',
  'package.json',
  'run-new-ui-desktop.ps1',
  'run-new-ui-desktop.sh',
]);

function parseArgs(argv) {
  const options = { check: false, full: false, strictDrift: false, filesystemScan: false, rulesPath: '' };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === '--check') options.check = true;
    else if (arg === '--full') options.full = true;
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

function loadCodemapPolicy() {
  const policyPath = path.join(SCRIPT_DIR, 'codemap_policy.txt');
  let body;
  try {
    body = fs.readFileSync(policyPath, 'utf8');
  } catch (error) {
    throw new Error(`project-map: cannot load ${policyPath}: ${error.message}`);
  }
  const policy = { schema_version: 0, historical_doc_roots: [], project_map_shards: {} };
  for (const [index, rawLine] of body.split(/\r?\n/).entries()) {
    if (rawLine === '') continue;
    const fields = rawLine.split('\t');
    if (fields[0] === 'schema' && fields.length === 2 && policy.schema_version === 0) {
      policy.schema_version = Number(fields[1]);
    } else if (fields[0] === 'historical' && fields.length === 2) {
      policy.historical_doc_roots.push(fields[1]);
    } else if (fields[0] === 'shard' && fields.length === 3 && !Object.hasOwn(policy.project_map_shards, fields[1])) {
      policy.project_map_shards[fields[1]] = fields[2];
    } else {
      throw new Error(`project-map: malformed codemap policy line ${index + 1}`);
    }
  }
  if (policy.schema_version !== 1) {
    throw new Error(`project-map: unsupported codemap policy schema ${policy.schema_version}`);
  }
  validatePolicyRoots(policy.historical_doc_roots);
  validatePolicyShards(policy.project_map_shards);
  return policy;
}

function validatePolicyRoots(roots) {
  if (!Array.isArray(roots) || roots.length === 0) {
    throw new Error('project-map: codemap policy historical_doc_roots must be a non-empty array');
  }
  const unique = new Set();
  for (const root of roots) {
    if (typeof root !== 'string' || !/^docs\/[^/]+(?:\/[^/]+)*$/.test(root) || path.posix.normalize(root) !== root) {
      throw new Error(`project-map: invalid historical document root ${String(root)}`);
    }
    const overlapping = [...unique].find(
      (existing) => root === existing || root.startsWith(`${existing}/`) || existing.startsWith(`${root}/`),
    );
    if (overlapping) {
      throw new Error(`project-map: duplicate or overlapping historical document root ${root}`);
    }
    unique.add(root);
  }
}

function validatePolicyShards(shards) {
  if (!shards || typeof shards !== 'object' || Array.isArray(shards) || Object.keys(shards).length === 0) {
    throw new Error('project-map: codemap policy project_map_shards must be a non-empty object');
  }
  const files = new Set();
  for (const [domain, file] of Object.entries(shards)) {
    if (!/^[a-z][a-z0-9-]*$/.test(domain) || !/^[a-z][a-z0-9-]*\.tsv$/.test(file)) {
      throw new Error(`project-map: invalid shard mapping ${domain}=${String(file)}`);
    }
    if (files.has(file)) throw new Error(`project-map: duplicate shard file ${file}`);
    files.add(file);
  }
}

const OPTIONS = parseArgs(process.argv.slice(2));
const CHECK = OPTIONS.check;
const FULL_SCAN = OPTIONS.full || CHECK || OPTIONS.filesystemScan;
const STRICT_DRIFT = OPTIONS.strictDrift;
const FILESYSTEM_SCAN = OPTIONS.filesystemScan;
let GENERATOR_RULES_FINGERPRINT = '';

const CODEMAP_POLICY = loadCodemapPolicy();
const HISTORICAL_DOC_PREFIXES = CODEMAP_POLICY.historical_doc_roots;

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
  ...HISTORICAL_DOC_PREFIXES.map((prefix) => `${prefix}/**`),
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
  'config/remote-ci/aliyun.baseline-state.sqlite',
  'go.sum',
  'test_output.txt',
  'naked_go.txt',
];

const DOMAIN_FILES = CODEMAP_POLICY.project_map_shards;

const DOMAIN_DESCRIPTIONS = {
  'app-ui': '桌面应用、Wails host、React/Vite 前端与 UI 测试',
  orchestration: 'mcp-orch 编排 peer、DAG、workspace、prompt、command、shared-file 工具',
  modules: '业务模块层：dashboard、memory、prompt、skill、thread、turn、uistate 等',
  'platform-provider': '基础设施与 provider 集成：RPC、hooks、toolbridge、Claude/Codex/统一 provider',
  'store-sql': '持久化层：store、sqlc、SQL queries、migrations',
  'remote-ci': '远程 CI：Git hooks、strict SQLite authority、阿里云 ECI/OSS、ImageCache 与 shard worker',
  'docs-agent': '代码地图、ADR、契约与 docs 项目知识',
  other: '公共库、脚本、测试、配置与其他根级资源',
};

const RULES_CANDIDATES = [
  '.ai-project-map.overrides.json',
];

const MODULE_DESCRIPTIONS = {
  cmd: '可执行入口与 MCP peer',
  internal: '应用内部模块、平台、provider、store 与守卫',
  docs: '当前文档、生成索引、开发中材料与历史证据',
  pkg: '可复用公共库',
  scripts: '工程自动化脚本',
  sql: 'SQL query 源文件',
  test: '测试夹具和辅助资源',
  tests: '跨包测试资源',
  '(root)': '仓库根级配置和说明',
};

const PURPOSE_RULES = [
  ['cmd/super-dolphin-gate/', '远程 CI/Git gate CLI、trusted launcher、materializer 与 worker 入口'],
  ['internal/devtools/remoteci/', '远程 CI coordinator、source transport、workload identity、ECI shard 与 timing ledger'],
  ['internal/devtools/cicontract/', '远程 CI ECI/ImageCache/SQLite 单路径代码契约 owner'],
  ['internal/devtools/alicloud/eci/', '阿里云 ECI container group 与 ImageCache 只读客户端'],
  ['internal/devtools/alicloud/oss/', '远程 CI OSS 内容寻址传输客户端'],
  ['config/remote-ci/', '远程 CI strict 配置与 generation-one receipt 输入'],
  ['.githooks/pre-commit', '精确 staged tree 的本地代码守卫入口'],
  ['.githooks/pre-push', '精确 ref update 的远程 CI pre-push 门禁入口'],
  ['.githooks/trusted-gate-launcher.sh', '远程 CI trusted gate launcher 验证入口'],
  ['scripts/ci_truth_image_gate.sh', '远程 CI release/full gate 启动脚本'],
  ['scripts/git_with_remote_ci_credentials.sh', '远程 CI 短期凭据与 agent token Git 启动器'],
  ['scripts/init_remote_ci_local.sh', '远程 CI 本地配置、SQLite 与 Git alias 初始化脚本'],
  ['scripts/remote_ci_', '远程 CI 脚本守卫与凭据回归测试'],
  ['scripts/tests/test_gate_hook_', '远程 CI Git hook 入口与生产 E2E shell 回归'],
  ['docs/契约/remote-ci-eci-imagecache-contract.md', '远程 CI ECI/ImageCache/SQLite 唯一路径 Accepted 契约'],

  ['frontend-app/src/pages/', 'React 页面与路由级 UI'],
  ['frontend-app/src/entities/', 'React 客户端状态、thread/composer/store 模型'],
  ['frontend-app/src/shared/api/', 'React 前端 API facade 与 Wails bridge'],
  ['frontend-app/src/shared/ui/', 'React 共享 UI 组件'],
  ['frontend-app/tests/e2e/', 'React 前端 Playwright E2E 与视觉回归测试'],
  ['frontend-app/scripts/', 'React 前端构建与校验脚本'],
  ['frontend-app/', '当前 React/Vite 前端包'],
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
  ['internal/platform/db/sqlite/migrations/', 'SQLite schema migrations 与版本演进脚本'],
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
  ['pkg/logger/', '统一日志、采样、relay、watchdog 与 trace context'],
  ['pkg/dagmetrics/', 'DAG 指标公共库'],
  ['pkg/dreammetrics/', 'dream pipeline 指标公共库'],
  ['pkg/skillmetrics/', 'skill 指标公共库'],
  ['pkg/', '可复用公共库'],
  ['scripts/', '工程自动化、测试守卫、代码地图与开发脚本'],
  ['docs/doc/codemap/', '手写代码地图卷与自动 ai-index'],
  ['docs/adr/', '架构决策记录'],
  ['docs/automation/', '当前自动化协议、门禁巡检与问题接管流程'],
  ['docs/scripts/', '当前文档维护、发布 smoke 与验证脚本'],
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
  ['修改持久化/SQL', 'internal/store/', 'internal/platform/db/sqlite/migrations/', 'store sqlc migration queries'],
  ['修改 SQLite migrations', 'internal/platform/db/sqlite/migrations/', 'internal/platform/db/', 'sqlite migration schema version'],
  ['修改远程 CI/ECI/ImageCache', 'internal/devtools/remoteci/', 'cmd/super-dolphin-gate/', 'remote ci eci imagecache sqlite workload shard receipt'],
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
  rulesSources: ['scripts/codemap_policy.txt'],
};

const RUNTIME_ENTRY_ROWS = [
  ['Desktop host', 'cmd/agent-terminal/main.go', 'local desktop host', 'Wails desktop host, HTTP/RPC bridge, frontend embed host'],
  ['MCP orchestration peer', 'cmd/mcp-orch/main.go', 'stdio / managed peer', 'Agent lifecycle, DAG, wakeup, workspace and shared file tools'],
  ['MCP LSP peer', 'cmd/mcp-lsp/main.go', 'stdio / managed peer', 'Generic multi-language LSP peer and code intelligence tools'],
  ['Remote CI gate', 'cmd/super-dolphin-gate/main.go', 'Git hooks / manual gate', 'Exact-tree SQLite authority to unbounded Alibaba Cloud ECI shards'],
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
  ['remote CI', [
    ['cmd/super-dolphin-gate', 'remote run/hook/materializer/manifest installer/worker CLI'],
    ['internal/devtools/remoteci', 'coordinator、source transport、workload identity 与 timing'],
    ['internal/devtools/cicontract', 'ECI/ImageCache/SQLite canonical contract owner'],
    ['internal/devtools/alicloud/eci', '阿里云 ECI 生命周期与 ImageCache 只读验证'],
    ['internal/devtools/alicloud/oss', 'OSS 内容寻址传输'],
  ]],
];

function main() {
  loadRuleOverrides(OPTIONS);
  validateDomainPolicy();
  GENERATOR_RULES_FINGERPRINT = buildGeneratorRulesFingerprint();
  const entries = FULL_SCAN ? scanFiles().map(buildEntry) : loadIncrementalEntries();
  validatePurposeRulePrefixes();
  validateLifecycleEntries(entries);
  validateCurrentDocumentationNavigation();
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
  const mode = FULL_SCAN ? 'full' : 'incremental';
  console.log(`project-map: ${entries.length} files, ${Object.keys(grouped).length} domains, drift=${drift.status}, mode=${mode}`);
}

function validateDomainPolicy() {
  const described = Object.keys(DOMAIN_DESCRIPTIONS).sort();
  const mapped = Object.keys(DOMAIN_FILES).sort();
  if (described.join('\n') !== mapped.join('\n')) {
    throw new Error(`project-map: shard policy domains differ from descriptions: mapped=${mapped.join(',')} described=${described.join(',')}`);
  }
}

function scanFiles() {
  if (FILESYSTEM_SCAN) return scanFilesystemFiles();
  return scanGitWorktreeFiles();
}

function loadIncrementalEntries() {
  const { entries: previous } = loadExistingEntries();
  const currentFiles = scanGitWorktreeFiles();
  const currentSet = new Set(currentFiles);
  const changed = changedWorktreePaths();
  const merged = new Map(previous.map((entry) => [entry.path, entry]));

  for (const file of merged.keys()) {
    if (!currentSet.has(file)) merged.delete(file);
  }
  for (const file of currentFiles) {
    if (!merged.has(file) || changed.has(file)) merged.set(file, buildEntry(file));
  }
  const entries = [...merged.values()].sort(compareEntryPath);
  const currentFingerprint = currentWorktreeSourceFingerprint(currentFiles);
  if (sourceFingerprint(entries) !== currentFingerprint) {
    throw new Error('project-map: incremental source fingerprint mismatch; rerun with --full');
  }
  return entries;
}

function loadExistingEntries() {
  const entries = [];
  const seenPaths = new Map();
  if (!fs.existsSync(MANIFEST_JSON)) {
    throw new Error('project-map: incremental refresh requires existing generated outputs; rerun with --full');
  }
  let manifest;
  try {
    manifest = JSON.parse(fs.readFileSync(MANIFEST_JSON, 'utf8'));
  } catch (error) {
    throw new Error(`project-map: invalid incremental manifest; rerun with --full: ${error.message}`);
  }
  const fingerprint = manifest.input_fingerprint;
  if (!fingerprint || fingerprint.schema !== INPUT_FINGERPRINT_SCHEMA) {
    throw new Error('project-map: incremental manifest fingerprint is missing or unsupported; rerun with --full');
  }
  if (fingerprint.generator_rules_sha256 !== GENERATOR_RULES_FINGERPRINT) {
    throw new Error('project-map: incremental generator or rules fingerprint mismatch; rerun with --full');
  }
  if (typeof fingerprint.source_sha256 !== 'string' || typeof fingerprint.entries_sha256 !== 'string') {
    throw new Error('project-map: incremental manifest fingerprint is incomplete; rerun with --full');
  }
  for (const [domain, file] of Object.entries(DOMAIN_FILES)) {
    const shardPath = path.join(INDEX_DIR, file);
    if (!fs.existsSync(shardPath)) continue;
    const lines = fs.readFileSync(shardPath, 'utf8').split(/\r?\n/).filter(Boolean);
    if (lines.shift() !== 'path\tmodule\tdomain\ttype\tsize_bytes\tpurpose\tsearch_keys') {
      throw new Error(`project-map: invalid incremental shard header ${path.relative(ROOT, shardPath)}; rerun with --full`);
    }
    for (const line of lines) {
      const fields = line.split('\t');
      if (fields.length !== 7 || !Number.isSafeInteger(Number(fields[4]))) {
        throw new Error(`project-map: invalid incremental shard row in ${path.relative(ROOT, shardPath)}; rerun with --full`);
      }
      const relative = normalize(fields[0]);
      if (relative !== fields[0] || !shouldIndexPath(relative) || shouldSkipPath(relative) || fields[2] !== domain) {
        throw new Error(`project-map: invalid incremental shard row path ${fields[0]} in ${path.relative(ROOT, shardPath)}; rerun with --full`);
      }
      if (seenPaths.has(relative)) {
        throw new Error(`project-map: duplicate incremental project-map row path ${relative} in ${seenPaths.get(relative)} and ${path.relative(ROOT, shardPath)}; rerun with --full`);
      }
      seenPaths.set(relative, path.relative(ROOT, shardPath));
      entries.push({
        path: relative, module: fields[1], domain: fields[2], type: fields[3],
        size: Number(fields[4]), purpose: fields[5], searchKeys: fields[6],
      });
    }
  }
  entries.sort(compareEntryPath);
  if (fingerprint.entries_sha256 !== entriesFingerprint(entries) || fingerprint.source_sha256 !== sourceFingerprint(entries)) {
    throw new Error('project-map: incremental manifest and shard fingerprints disagree; rerun with --full');
  }
  return { entries, fingerprint };
}

function compareEntryPath(left, right) {
  return left.path < right.path ? -1 : left.path > right.path ? 1 : 0;
}

function scanGitWorktreeFiles() {
  requireGitWorktreeRoot();
  const result = childProcess.spawnSync(
    'git',
    ['-C', ROOT, 'ls-files', '-z', '--cached', '--others', '--exclude-standard'],
    { encoding: 'buffer', maxBuffer: 16 * 1024 * 1024 },
  );
  if (result.status !== 0) {
    const stderr = result.stderr ? result.stderr.toString('utf8').trim() : '';
    throw new Error(`project-map: git ls-files failed; use --filesystem-scan only for explicit exported snapshots${stderr ? `: ${stderr}` : ''}`);
  }
  return [...new Set(result.stdout.toString('utf8').split('\0').filter(Boolean).map(normalize))]
    .filter((file) => shouldIndexPath(file) && !shouldSkipPath(file) && trackedWorktreeFileExists(file))
    .sort();
}

function requireGitWorktreeRoot() {
  const result = childProcess.spawnSync('git', ['-C', ROOT, 'rev-parse', '--show-toplevel'], {
    encoding: 'utf8',
    maxBuffer: 1024 * 1024,
  });
  const resolved = result.status === 0 ? result.stdout.trim() : '';
  if (!resolved || fs.realpathSync(resolved) !== fs.realpathSync(ROOT)) {
    const stderr = result.stderr ? result.stderr.trim() : '';
    throw new Error(`project-map: git ls-files failed; use --filesystem-scan only for explicit exported snapshots${stderr ? `: ${stderr}` : ''}`);
  }
}

function currentWorktreeSourceFingerprint(currentFiles) {
  return sourceFingerprint(currentFiles.map((file) => ({ path: file, size: normalizedWorktreeSize(file) })));
}

function changedWorktreePaths() {
  const result = childProcess.spawnSync(
    'git',
    ['-C', ROOT, 'status', '--porcelain=v1', '-z', '--untracked-files=all'],
    { encoding: 'buffer', maxBuffer: 16 * 1024 * 1024 },
  );
  if (result.status !== 0) {
    const stderr = result.stderr ? result.stderr.toString('utf8').trim() : '';
    throw new Error(`project-map: incremental git status failed${stderr ? `: ${stderr}` : ''}`);
  }
  const records = result.stdout.toString('utf8').split('\0').filter(Boolean);
  const changed = new Set();
  for (let index = 0; index < records.length; index += 1) {
    const record = records[index];
    if (record.length < 4) continue;
    changed.add(normalize(record.slice(3)));
    const status = record.slice(0, 2);
    if ((status.includes('R') || status.includes('C')) && index + 1 < records.length) {
      changed.add(normalize(records[++index]));
    }
  }
  return changed;
}

function validateLifecycleEntries(entries) {
  const historical = entries.find((entry) => HISTORICAL_DOC_PREFIXES.some(
    (prefix) => entry.path === prefix || entry.path.startsWith(`${prefix}/`),
  ));
  if (historical) {
    throw new Error(`project-map: historical document entered current index: ${historical.path}`);
  }
}

function validatePurposeRulePrefixes() {
  const deadRules = runtime.purposeRules.filter(([prefix]) => {
    if (typeof prefix !== 'string' || prefix.length === 0) {
      throw new Error('project-map: purpose rule prefix must be a non-empty string');
    }
    const probe = prefix.endsWith('/') ? `${prefix}__project_map_probe__` : prefix;
    return shouldSkipPath(probe);
  });
  if (deadRules.length > 0) {
    const prefixes = deadRules.map(([prefix]) => prefix).join(', ');
    throw new Error(`project-map: purpose rules match excluded paths and are dead: ${prefixes}`);
  }
}

function validateCurrentDocumentationNavigation() {
  const docsReadme = path.join(ROOT, 'docs', 'README.md');
  if (!fs.existsSync(docsReadme)) {
    throw new Error('project-map: canonical docs/README.md is missing');
  }
  for (const relative of ['docs/adr', 'docs/work/plans', 'docs/archive/reviews']) {
    const absolute = path.join(ROOT, relative);
    if (!fs.existsSync(absolute) || !fs.statSync(absolute).isDirectory()) {
      throw new Error(`project-map: canonical current documentation path is missing: ${relative}`);
    }
  }
  const checks = [
    ['AGENTS.md', ['docs/adr/*.md'], ['docs/decisions/*.md']],
    ['docs/README.md', ['[自动化协议](automation/)', '[文档脚本](scripts/)'], []],
    ['docs/契约/README.md', ['docs/adr'], ['docs/decisions']],
    [
      'docs/契约/fix-workflow-convention.md',
      ['docs/work/plans/', 'docs/archive/reviews/', 'docs/adr/'],
      ['docs/decisions', 'docs/li/', 'docs/plans/', 'docs/reviews/'],
    ],
    ['docs/契约/mcp-service-convention.md', [], ['docs/decisions/ADR-003-mcp-input-enum-validation.md']],
  ];
  for (const [relative, required, forbidden] of checks) {
    const body = fs.readFileSync(path.join(ROOT, relative), 'utf8');
    for (const value of required) {
      if (!body.includes(value)) {
        throw new Error(`project-map: current documentation ${relative} is missing ${value}`);
      }
    }
    for (const value of forbidden) {
      if (body.includes(value)) {
        throw new Error(`project-map: current documentation ${relative} points to historical ${value}`);
      }
    }
  }
}

function loadRuleOverrides(options) {
  const candidates = [];
  if (options.rulesPath) candidates.push(path.resolve(ROOT, options.rulesPath));
  for (const rel of RULES_CANDIDATES) candidates.push(path.join(ROOT, rel));
  const seen = new Set();

  for (const absPath of candidates) {
    if (seen.has(absPath)) continue;
    seen.add(absPath);
    let info;
    try {
      info = fs.lstatSync(absPath);
    } catch (error) {
      if (error?.code === 'ENOENT') continue;
      throw error;
    }
    if (info.isSymbolicLink()) {
      throw new Error(`project-map: rules file must not be a symbolic link: ${path.relative(ROOT, absPath)}`);
    }
    if (!info.isFile()) {
      throw new Error(`project-map: rules path must be a regular file: ${path.relative(ROOT, absPath)}`);
    }
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

function trackedWorktreeFileExists(relative) {
  try {
    return fs.lstatSync(path.join(ROOT, relative)).isFile();
  } catch (error) {
    if (error?.code === 'ENOENT') return false;
    throw error;
  }
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
    ...HISTORICAL_DOC_PREFIXES,
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
  return [
    'go.sum',
    'test_output.txt',
    'naked_go.txt',
    'docs/doc/codemap/ai-index.json',
    'config/remote-ci/aliyun.baseline-state.sqlite',
  ].includes(rel);
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
  if (isRemoteCIPath(file)) return 'remote-ci';
  if (file.startsWith('frontend-app/')) return 'app-ui';
  if (file.startsWith('cmd/agent-terminal/')) return 'app-ui';
  if (file.startsWith('cmd/mcp-orch/')) return 'orchestration';
  if (file.startsWith('internal/module/')) return 'modules';
  if (file.startsWith('internal/platform/db/sqlite/migrations/')) return 'store-sql';
  if (file.startsWith('internal/platform/') || file.startsWith('internal/provider/') || file.startsWith('internal/mcpserver/') || file.startsWith('cmd/mcp-lsp/') || file.startsWith('cmd/mcp-ida/')) return 'platform-provider';
  if (file.startsWith('internal/store/') || file.startsWith('sql/') || file.startsWith('cmd/mcp-orch/store/') || file.startsWith('cmd/mcp-orch/sql/')) return 'store-sql';
  if (file.startsWith('docs/') || file === 'CLAUDE.md' || file === 'AGENTS.md' || file === 'README.md' || (file.startsWith('README.') && file.endsWith('.md'))) return 'docs-agent';
  return 'other';
}

function isRemoteCIPath(file) {
  return file.startsWith('cmd/super-dolphin-gate/') ||
    file.startsWith('internal/devtools/remoteci/') ||
    file.startsWith('internal/devtools/cicontract/') ||
    file.startsWith('internal/devtools/alicloud/eci/') ||
    file.startsWith('internal/devtools/alicloud/oss/') ||
    file.startsWith('config/remote-ci/') ||
    file === '.githooks/pre-commit' ||
    file === '.githooks/pre-push' ||
    file === '.githooks/trusted-gate-launcher.sh' ||
    file === 'scripts/ci_truth_image_gate.sh' ||
    file === 'scripts/git_with_remote_ci_credentials.sh' ||
    file === 'scripts/init_remote_ci_local.sh' ||
    file.startsWith('scripts/remote_ci_') ||
    file.startsWith('scripts/tests/test_gate_hook_') ||
    file === 'docs/契约/remote-ci-eci-imagecache-contract.md';
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
  return normalizedWorktreeSize(file);
}

function normalizedWorktreeSize(file) {
  try {
    const data = fs.readFileSync(path.join(ROOT, file));
    return normalizedContentSize(data);
  } catch (error) {
    throw new Error(`project-map: cannot read indexed file ${file}: ${error.message}`);
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
  outputs[MAP_MD] = renderPolicyAwareMap(entries, drift, stats);
  outputs[DRIFT_MD] = renderDrift(entries, drift);
  outputs[MANIFEST_JSON] = `${JSON.stringify(renderManifest(entries, grouped, drift, stats), null, 2)}\n`;
  for (const [domain, items] of Object.entries(grouped)) {
    outputs[path.join(INDEX_DIR, DOMAIN_FILES[domain])] = renderTSV(items);
  }
  return outputs;
}

function renderPolicyAwareMap(entries, drift, stats) {
  let rendered = renderMap(entries, drift, stats);
  rendered = rendered.replace(
    'node scripts/generate_ai_project_map.mjs\nnode scripts/generate_ai_project_map.mjs --check',
    'node scripts/generate_ai_project_map.mjs\nnode scripts/generate_ai_project_map.mjs --full\nnode scripts/generate_ai_project_map.mjs --check',
  );
  rendered = rendered.replace(
    '现有手写代码地图仍以',
    '默认刷新增量合并当前 Git 工作树；`--full` 全量重建同一工作树，`--check` 也按同一文件与规范化大小检查。首次生成、生成器或规则指纹变化时必须显式传入 `--full`。现有手写代码地图仍以',
  );
  const historical = HISTORICAL_DOC_PREFIXES.map((prefix) => `\`${prefix}/*\``).join('、');
  return rendered.replace(
    /- 历史归档（L3）：[^\n]*/,
    `- 历史归档（L3）：${historical}（默认不递归索引）`,
  );
}

function renderManifest(entries, grouped, drift, stats) {
  return {
    version: '1.0',
    generator: 'node:scripts/generate_ai_project_map.mjs',
    input_fingerprint: {
      schema: INPUT_FINGERPRINT_SCHEMA,
      generator_rules_sha256: GENERATOR_RULES_FINGERPRINT,
      source_sha256: sourceFingerprint(entries),
      entries_sha256: entriesFingerprint(entries),
    },
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

function buildGeneratorRulesFingerprint() {
  const hash = crypto.createHash('sha256');
  updateFingerprint(hash, INPUT_FINGERPRINT_SCHEMA);
  updateFingerprint(hash, fs.readFileSync(GENERATOR_PATH));
  updateFingerprint(hash, JSON.stringify({ policy: CODEMAP_POLICY, runtime }));
  return `sha256:${hash.digest('hex')}`;
}

function sourceFingerprint(entries) {
  const hash = crypto.createHash('sha256');
  updateFingerprint(hash, `${INPUT_FINGERPRINT_SCHEMA}:source`);
  for (const entry of [...entries].sort(compareEntryPath)) {
    updateFingerprint(hash, entry.path);
    updateFingerprint(hash, String(entry.size));
  }
  return `sha256:${hash.digest('hex')}`;
}

function entriesFingerprint(entries) {
  const hash = crypto.createHash('sha256');
  updateFingerprint(hash, `${INPUT_FINGERPRINT_SCHEMA}:entries`);
  for (const entry of [...entries].sort(compareEntryPath)) {
    updateFingerprint(hash, JSON.stringify([
      entry.path, entry.module, entry.domain, entry.type, entry.size, entry.purpose, entry.searchKeys,
    ]));
  }
  return `sha256:${hash.digest('hex')}`;
}

function updateFingerprint(hash, value) {
  const data = Buffer.isBuffer(value) ? value : Buffer.from(value, 'utf8');
  hash.update(String(data.length));
  hash.update(':');
  hash.update(data);
  hash.update('\n');
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

  return `# AI 项目地图（Super-Dolphin）\n\n> 已索引文件：**${entries.length}**\n>\n> 扫描规则：${scanPolicySummary()}\n>\n> 漂移状态：**${drift.status}**（详见 \`docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md\`）\n\n## 1. 项目功能总览\n\nSuper-Dolphin / super-agent-v3 是一个本地多 Agent 桌面应用与 MCP peer 体系，核心由以下能力构成：\n\n- **桌面控制台**：\`cmd/agent-terminal\` 提供 Wails/Go host、HTTP/RPC 桥，\`frontend-app\` 提供 React/Vite 前端。\n- **编排 peer**：\`cmd/mcp-orch\` 管理 agent 生命周期、DAG、wakeup、workspace、prompt、command card 与 shared file tools。\n- **代码智能 peer**：\`cmd/mcp-lsp\` 提供多语言 LSP、文件搜索、结构和诊断工具。\n- **业务模块层**：\`internal/module\` 承载 dashboard、memory、prompt、skill、thread、turn、uistate 等运行语义。\n- **基础设施与 provider**：\`internal/platform\`、\`internal/provider\` 负责 RPC、hooks、toolbridge、控制面、Claude/Codex provider 集成。\n- **持久化与治理**：\`internal/store\`、\`sql\`、\`internal/platform/db/sqlite/migrations\`、\`internal/archtest\`、\`docs/doc/codemap\` 提供数据访问、schema、架构守卫和代码地图。\n\n## 2. 索引路由表\n\n| 索引文件 | 文件数 | 大小 | 覆盖范围 |\n|---|---:|---:|---|\n${domainRows}\n\n**检索示例：**\n\n\`\`\`bash\n# 1) 先读此 MAP.md 确定目标域\n# 2) 搜索对应 TSV 分片\nrg "thread.*resume|fork" docs/doc/codemap/project-map/index/modules.tsv\nrg "provider.*manifest|toolbridge" docs/doc/codemap/project-map/index/platform-provider.tsv\nrg "lsp.*diagnostics|grep" docs/doc/codemap/project-map/index/platform-provider.tsv\nrg "ChatPage|composer|timeline" docs/doc/codemap/project-map/index/app-ui.tsv\n# 3) 打开目标源码和同包测试\nrg --line-number "func .*Resume|func .*Fork" internal/module/thread -g '*.go'\n\`\`\`\n\n## 3. 顶层结构\n\n| 模块 | 文件数 | 职责 |\n|---|---:|---|\n${topRows}\n\n## 4. 运行入口地图\n\n| 运行单元 | 入口文件 | 默认端口/端点 | 说明 |\n|---|---|---|---|\n${runtimeEntryRows}\n\n## 5. Root Fx 装配阅读顺序\n\n\`internal/app/modules.go\` 是根装配清单，不是严格的业务执行时序。阅读时先按下面的依赖层理解，再用 Fx graph tests 确认供给点是否闭合。\n\n| 步骤 | 层 | 锚点 | AI 阅读提示 |\n|---:|---|---|---|\n${appAssemblyRows}\n\n## 6. 快速定位路由\n\n| 目标 | 首选路径 | 次选路径 | 检索关键词 |\n|---|---|---|---|\n${routeRows}\n\n## 7. 重点子系统地图\n\n${subsystemSections}\n\n## 8. 文档与知识地图\n\n- 当前事实（L1）：\`README.md\`、\`docs/README.md\`、\`docs/adr/*\`、\`docs/契约/*\`、\`docs/架构/*\`、\`docs/reference/*\`、\`docs/运维/*\`、\`docs/automation/*\`、\`docs/scripts/*\`\n- 开发中材料（L2）：\`docs/work/proposals/*\`、\`docs/work/plans/*\`、\`docs/internal-notes/*\`\n- 历史归档（L3）：\`${runtime.archivePrefixes.join('`、`')}\`，以及待迁移的 \`docs/plans/*\`、\`docs/superpowers/plans/*\`（默认不递归索引）\n- Agent 体系：\`.agents/skills/*/SKILL.md\` 是 repo-local skill 指令入口；不要把 \`.agents\` 当作普通项目源码递归扫描。\n\n## 9. 索引字段说明\n\n| 字段 | 含义 |\n|---|---|\n| \`path\` | 相对路径 |\n| \`module\` | 顶层模块 |\n| \`domain\` | project-map 分片域 |\n| \`type\` | 文件类型 |\n| \`size_bytes\` | 文件大小（字节） |\n| \`purpose\` | 文件职责说明 |\n| \`search_keys\` | 建议检索关键词 |\n\n## 10. 维护命令\n\n\`\`\`bash\nnode scripts/generate_ai_project_map.mjs\nnode scripts/generate_ai_project_map.mjs --check\nnode scripts/generate_ai_project_map.mjs --strict-drift\nnode scripts/generate_ai_project_map.mjs --rules path/to/overrides.json\n\`\`\`\n\n现有手写代码地图仍以 \`docs/doc/codemap/README.md\` 和 \`make codemap-check\` / \`make codemap-refresh\` 为准；本目录提供低 token 的全仓文件级索引补充。\n`;
}

function renderDrift(entries, drift) {
  const topUnknownRows = Object.entries(drift.topUnknown)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 20)
    .map(([module, count]) => `| \`${module}\` | ${count} |`)
    .join('\n') || '| - | 0 |';
  const sample = drift.unknown.slice(0, 50).map((entry) => `- \`${entry.path}\``).join('\n') || '- 无';
  const warnings = drift.warnings.map((warning) => `- ${warning}`).join('\n') || '- 无';
  return `# AI 项目地图漂移报告\n\n> 状态：**${drift.status}**\n>\n> 已索引文件：${entries.length}\n>\n> 未细分职责文件：${drift.unknown.length}\n\n## 1. 漂移指标\n\n| 指标 | 当前值 |\n|---|---:|\n| 未细分职责文件数 | ${drift.unknown.length} |\n| 未细分职责占比 | ${(drift.unknownRatio * 100).toFixed(2)}% |\n| 最大未细分职责占比阈值 | ${(drift.thresholds.max_unknown_ratio * 100).toFixed(2)}% |\n\n## 2. 漂移告警\n\n${warnings}\n\n## 3. 未细分职责分布\n\n| 模块 | 文件数 |\n|---|---:|\n${topUnknownRows}\n\n## 4. 样例文件\n\n${sample}\n\n## 5. 修复方式\n\n优先在 \`.ai-project-map.overrides.json\` 中补充 \`purpose_rules_append\`，或用 \`--rules\` 传入显式规则文件，然后重新运行：\n\n\`\`\`bash\nnode scripts/generate_ai_project_map.mjs --full\n\`\`\`\n`;
}

function renderTSV(items) {
  const lines = ['path\tmodule\tdomain\ttype\tsize_bytes\tpurpose\tsearch_keys'];
  for (const entry of items.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))) {
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

main();

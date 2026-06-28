#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');

const ROOT = findRepoRoot(process.cwd());
const OUTPUT_DIR = path.join(ROOT, 'docs', 'doc', 'codemap', 'project-map');
const INDEX_DIR = path.join(OUTPUT_DIR, 'index');
const MAP_MD = path.join(OUTPUT_DIR, 'AI_PROJECT_MAP.md');
const DRIFT_MD = path.join(OUTPUT_DIR, 'AI_PROJECT_DRIFT.md');
const MANIFEST_JSON = path.join(OUTPUT_DIR, 'AI_PROJECT_MANIFEST.json');

const CHECK = process.argv.includes('--check');
const STRICT_DRIFT = process.argv.includes('--strict-drift');

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
  'docs-agent': '代码地图、ADR/决策、计划、agent skills/workflows 与项目知识',
  other: '公共库、脚本、测试、配置与其他根级资源',
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

  ['internal/app/', 'root Fx/app 装配、runner 与 toolbridge adapters'],
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
  ['.agent/skills/', '项目级 agent 技能 canonical'],
  ['.agent/workflows/', 'agent 工作流与执行档案'],
  ['.agents/', 'Codex/agent mirror 入口'],
  ['.github/', 'GitHub 配置'],
  ['.githooks/', '仓库 git hooks'],
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
  ['修改控制面/bootstrap', 'internal/platform/mcpcontrol/', 'internal/mcpserver/common/bootstrap/', 'peer register bootstrap hooks'],
  ['修改持久化/SQL', 'internal/store/', 'sql/queries/', 'store sqlc migration queries'],
  ['修改代码地图', 'docs/doc/codemap/', 'scripts/codemap_index.go', 'codemap ai-index make codemap-refresh'],
  ['修改架构守卫', 'internal/archtest/', 'internal/archtest/baseline.json', 'guard baseline ratchet freeze'],
];

function main() {
  const files = scanFiles();
  const entries = files.map(buildEntry);
  const grouped = groupByDomain(entries);
  const drift = buildDrift(entries);
  const outputs = renderAll(entries, grouped, drift);

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
  const files = [];
  walk(ROOT, '', files);
  return files.sort();
}

function walk(absDir, relDir, files) {
  for (const dirent of fs.readdirSync(absDir, { withFileTypes: true })) {
    const rel = normalize(relDir ? path.posix.join(relDir, dirent.name) : dirent.name);
    const abs = path.join(absDir, dirent.name);
    if (dirent.isDirectory()) {
      if (!shouldSkipDir(rel)) walk(abs, rel, files);
      continue;
    }
    if (dirent.isFile() && !shouldSkipFile(rel)) files.push(rel);
  }
}

function shouldSkipDir(rel) {
  const name = path.posix.basename(rel);
  if (['.build-cache', '.git', '.idea', '.claude', '.workspace', '.worktrees', 'bin', 'node_modules', 'dist', 'coverage', '.vite', '.tmp', 'tmp', '.gocache', '.gomodcache', '.npm-cache'].includes(name)) return true;
  return [
    '.agent/code_exec',
    '.agent/workspaces',
    '.agnet/report',
    '.agnet/shared',
    'docs/archive',
    'docs/doc/codemap/project-map',
    'reports',
  ].some((prefix) => rel === prefix || rel.startsWith(`${prefix}/`));
}

function shouldSkipFile(rel) {
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
  if (file.startsWith('docs/') || file.startsWith('.agent/') || file.startsWith('.agents/') || file === 'CLAUDE.md' || file === 'AGENTS.md') return 'docs-agent';
  return 'other';
}

function classifyType(file) {
  if (file.endsWith('_test.go')) return 'go-test';
  if (file.endsWith('.go')) return 'go-source';
  if (file.endsWith('.test.js') || file.endsWith('.spec.js') || file.endsWith('.test.ts') || file.endsWith('.spec.ts')) return 'js-test';
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
  try {
    return fs.statSync(path.join(ROOT, file)).size;
  } catch {
    return 0;
  }
}

function purposeFor(file) {
  const rule = PURPOSE_RULES.find(([prefix]) => file.startsWith(prefix));
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
  const status = unknownRatio > 0.18 ? 'WARN' : 'OK';
  return { status, unknown, topUnknown, unknownRatio };
}

function renderAll(entries, grouped, drift) {
  const outputs = {};
  outputs[MAP_MD] = renderMap(entries, grouped, drift);
  outputs[DRIFT_MD] = renderDrift(entries, drift);
  outputs[MANIFEST_JSON] = `${JSON.stringify({
    version: '1.0',
    generator: 'node:scripts/generate_ai_project_map.js',
    generated_at: today(),
    files: entries.length,
    domains: Object.fromEntries(Object.entries(grouped).map(([domain, items]) => [domain, items.length])),
    drift: { status: drift.status, unknown_ratio: Number(drift.unknownRatio.toFixed(4)), unknown_files: drift.unknown.length },
  }, null, 2)}\n`;
  for (const [domain, items] of Object.entries(grouped)) {
    outputs[path.join(INDEX_DIR, DOMAIN_FILES[domain])] = renderTSV(items);
  }
  return outputs;
}

function renderMap(entries, grouped, drift) {
  const topCounts = countBy(entries.map((entry) => entry.module));
  const domainRows = Object.entries(DOMAIN_FILES)
    .filter(([domain]) => grouped[domain])
    .map(([domain, file]) => `| \`${path.posix.join('docs/doc/codemap/project-map/index', file)}\` | ${grouped[domain].length} | ${DOMAIN_DESCRIPTIONS[domain]} |`)
    .join('\n');
  const topRows = Object.entries(topCounts)
    .sort((a, b) => b[1] - a[1])
    .map(([module, count]) => `| \`${module}\` | ${count} | ${moduleDescription(module)} |`)
    .join('\n');
  const routeRows = QUICK_ROUTES.map(([target, first, second, keys]) => `| ${target} | \`${first}\` | \`${second}\` | \`${keys}\` |`).join('\n');

  return `# AI 项目地图（Super-Dolphin）\n\n> 生成时间：${today()}\n>\n> 已索引文件：**${entries.length}**\n>\n> 漂移状态：**${drift.status}**（详见 \`docs/doc/codemap/project-map/AI_PROJECT_DRIFT.md\`）\n\n## 1. 项目功能总览\n\nSuper-Dolphin / super-agent-v3 是一个本地多 Agent 桌面应用与 MCP peer 体系，核心由以下能力构成：\n\n- **桌面控制台**：\`cmd/agent-terminal\` 提供 Wails/Go host、HTTP/RPC 桥，\`frontend-app\` 提供 React/Vite 前端。\n- **编排 peer**：\`cmd/mcp-orch\` 管理 agent 生命周期、DAG、wakeup、workspace、prompt、command card 与 shared file tools。\n- **代码智能 peer**：\`cmd/mcp-lsp\` 提供多语言 LSP、文件搜索、结构和诊断工具。\n- **业务模块层**：\`internal/module\` 承载 dashboard、memory、prompt、skill、thread、turn、uistate 等运行语义。\n- **基础设施与 provider**：\`internal/platform\`、\`internal/provider\` 负责 RPC、hooks、toolbridge、控制面、Claude/Codex provider 集成。\n- **持久化与治理**：\`internal/store\`、\`sql\`、\`migrations\`、\`internal/archtest\`、\`docs/doc/codemap\` 提供数据访问、schema、架构守卫和代码地图。\n\n## 2. 索引路由表\n\n| 索引文件 | 文件数 | 覆盖范围 |\n|---|---:|---|\n${domainRows}\n\n每个 TSV 字段为：\`path\`、\`module\`、\`domain\`、\`type\`、\`size_bytes\`、\`purpose\`、\`search_keys\`。\n\n## 3. 顶层结构\n\n| 模块 | 文件数 | 职责 |\n|---|---:|---|\n${topRows}\n\n## 4. 快速定位路由\n\n| 目标 | 首选路径 | 次选路径 | 检索关键词 |\n|---|---|---|---|\n${routeRows}\n\n## 5. 维护命令\n\n\`\`\`bash\nnode scripts/generate_ai_project_map.js\nnode scripts/generate_ai_project_map.js --check\nnode scripts/generate_ai_project_map.js --strict-drift\n\`\`\`\n\n现有手写代码地图仍以 \`docs/doc/codemap/README.md\` 和 \`make codemap-check\` / \`make codemap-refresh\` 为准；本目录提供低 token 的全仓文件级索引补充。\n`;
}

function renderDrift(entries, drift) {
  const topUnknownRows = Object.entries(drift.topUnknown)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 20)
    .map(([module, count]) => `| \`${module}\` | ${count} |`)
    .join('\n') || '| - | 0 |';
  const sample = drift.unknown.slice(0, 50).map((entry) => `- \`${entry.path}\``).join('\n') || '- 无';
  return `# AI 项目地图漂移报告\n\n> 状态：**${drift.status}**\n>\n> 已索引文件：${entries.length}\n>\n> 未细分职责文件：${drift.unknown.length}\n\n## 1. 漂移指标\n\n| 指标 | 当前值 |\n|---|---:|\n| 未细分职责文件数 | ${drift.unknown.length} |\n| 未细分职责占比 | ${(drift.unknownRatio * 100).toFixed(2)}% |\n\n## 2. 未细分职责分布\n\n| 模块 | 文件数 |\n|---|---:|\n${topUnknownRows}\n\n## 3. 样例文件\n\n${sample}\n\n## 4. 修复方式\n\n优先在 \`scripts/generate_ai_project_map.js\` 的 \`PURPOSE_RULES\` 中补充路径前缀和职责说明，然后重新运行：\n\n\`\`\`bash\nnode scripts/generate_ai_project_map.js\n\`\`\`\n`;
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
  return path.resolve(__dirname, '..');
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
  return {
    cmd: '可执行入口与 MCP peer',
    internal: '应用内部模块、平台、provider、store 与守卫',
    docs: '代码地图、ADR、计划、迁移和内部说明',
    pkg: '可复用公共库',
    scripts: '工程自动化脚本',
    sql: 'SQL query 源文件',
    migrations: '数据库 migration',
    test: '测试夹具和辅助资源',
    tests: '跨包测试资源',
    '.agent': '项目级 agent 技能与工作流 canonical',
    '.agents': 'agent/Codex mirror 入口',
    '.githooks': 'Git hooks',
    '.github': 'GitHub 配置',
    '(root)': '仓库根级配置和说明',
  }[module] || '其他项目资源';
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

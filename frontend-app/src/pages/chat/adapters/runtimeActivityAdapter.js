const LSP_TOOL_NAMES = Object.freeze([
  'grep',
  'file',
  'inspect',
  'xref',
  'structure',
  'edit',
  'completion',
  'format_preview',
]);

const JSON_RENDER_TOOL_NAMES = Object.freeze(['json_render']);
const GO_RUN_TOOL_NAMES = Object.freeze(['go_run']);
const PLAYWRIGHT_TOOL_PREFIXES = Object.freeze(['mcp__playwright__', 'playwright_', 'browser_']);

function activityTextValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

function activityObjectEntries(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return [];
  return Object.entries(value);
}

function activityArray(value) {
  return Array.isArray(value) ? value : [];
}

function activityToolCalls(stats) {
  return stats?.toolCalls && typeof stats.toolCalls === 'object' && !Array.isArray(stats.toolCalls)
    ? stats.toolCalls
    : null;
}

function canonicalLspToolName(name) {
  return ({
    lsp_file: 'file',
    lsp_grep: 'grep',
    lsp_inspect: 'inspect',
    lsp_xref: 'xref',
    lsp_structure: 'structure',
    lsp_edit: 'edit',
    lsp_completion: 'completion',
    lsp_format_preview: 'format_preview',
  })[name] || name;
}

function normalizeActivityToolName(name) {
  const raw = activityTextValue(name).trim().toLowerCase();
  const mcpParts = raw.startsWith('mcp__') ? raw.split('__') : [];
  const withoutMCPServer = mcpParts.length >= 3 ? mcpParts.slice(2).join('__') : raw;
  const normalized = withoutMCPServer
    .replace(/[./:-]+/g, '_')
    .replace(/^functions_+/, '')
    .replace(/^function_+/, '')
    .replace(/^tools_+/, '')
    .replace(/^tool_+/, '')
    .replace(/_+/g, '_')
    .replace(/^_+|_+$/g, '');
  return canonicalLspToolName(normalized);
}

function sumToolCallsByMatcher(toolMap, matcher) {
  let sum = 0;
  for (const [rawName, value] of activityObjectEntries(toolMap)) {
    const name = normalizeActivityToolName(rawName);
    if (!name || !matcher(name, activityTextValue(rawName).trim().toLowerCase())) continue;
    const count = Number(value);
    sum += Number.isFinite(count) ? count : 0;
  }
  return sum;
}

function sumToolCallsByNames(toolMap, names) {
  const expected = new Set();
  for (const rawName of activityArray(names)) {
    const name = normalizeActivityToolName(rawName);
    if (name) expected.add(name);
  }
  if (expected.size === 0) return 0;
  return sumToolCallsByMatcher(toolMap, (name) => expected.has(name));
}

function filteredActivityToolEntries(stats = {}, matcher) {
  const merged = {};
  for (const [rawName, value] of activityObjectEntries(activityToolCalls(stats))) {
    const raw = activityTextValue(rawName).trim().toLowerCase();
    const name = normalizeActivityToolName(rawName) || rawName;
    if (!matcher(name, raw)) continue;
    const previous = Number(merged[name]);
    const current = Number(value);
    merged[name] = (Number.isFinite(previous) ? previous : 0) + (Number.isFinite(current) ? current : 0);
  }
  const entries = [];
  for (const [name, count] of Object.entries(merged)) {
    if (count > 0) entries.push({ name, count });
  }
  return entries.sort((left, right) => right.count - left.count || left.name.localeCompare(right.name));
}

function activityToolEntries(stats = {}) {
  return filteredActivityToolEntries(stats, () => true);
}

function activityStatItems(stats = {}) {
  const toolCalls = activityToolCalls(stats);
  const totalTools = Object.values(toolCalls ? toolCalls : Object.create(null)).reduce((sum, value) => {
    const count = Number(value);
    return sum + (Number.isFinite(count) ? count : 0);
  }, 0);
  return [
    { key: 'lsp', label: 'LSP (8 tools)', className: 'stat-lsp', value: sumToolCallsByNames(toolCalls, LSP_TOOL_NAMES) || (Number.isFinite(Number(stats?.lspCalls)) ? Number(stats.lspCalls) : 0) },
    { key: 'jsonRender', label: 'JSON-Render', className: 'stat-json-render', value: sumToolCallsByNames(toolCalls, JSON_RENDER_TOOL_NAMES) },
    {
      key: 'playwright',
      label: 'Playwright',
      className: 'stat-playwright',
      value: sumToolCallsByMatcher(toolCalls, (name, rawName) => PLAYWRIGHT_TOOL_PREFIXES.some((prefix) => name.startsWith(prefix) || rawName.startsWith(prefix))),
    },
    { key: 'goRun', label: 'go-run', className: 'stat-go-run', value: sumToolCallsByNames(toolCalls, GO_RUN_TOOL_NAMES) },
    { key: 'command', label: '命令', className: 'stat-cmd', value: Number.isFinite(Number(stats?.commands)) ? Number(stats.commands) : 0 },
    { key: 'file', label: '文件', className: 'stat-file', value: Number.isFinite(Number(stats?.fileEdits)) ? Number(stats.fileEdits) : 0 },
    { key: 'tool', label: '工具', className: 'stat-tool', value: totalTools },
  ];
}

function activityStatDetailEntries(stats = {}, statKey = '') {
  if (statKey === 'lsp') {
    const lspNames = new Set(LSP_TOOL_NAMES.map((name) => normalizeActivityToolName(name)));
    return filteredActivityToolEntries(stats, (name) => lspNames.has(name));
  }
  if (statKey === 'jsonRender') {
    const names = new Set(JSON_RENDER_TOOL_NAMES.map((name) => normalizeActivityToolName(name)));
    return filteredActivityToolEntries(stats, (name) => names.has(name));
  }
  if (statKey === 'playwright') {
    return filteredActivityToolEntries(stats, (name, rawName) => (
      PLAYWRIGHT_TOOL_PREFIXES.some((prefix) => name.startsWith(prefix) || rawName.startsWith(prefix))
    ));
  }
  if (statKey === 'goRun') {
    const names = new Set(GO_RUN_TOOL_NAMES.map((name) => normalizeActivityToolName(name)));
    return filteredActivityToolEntries(stats, (name) => names.has(name));
  }
  if (statKey === 'command') return Number(stats?.commands) > 0 ? [{ name: '命令调用', count: Number(stats.commands) }] : [];
  if (statKey === 'file') return Number(stats?.fileEdits) > 0 ? [{ name: '文件变更', count: Number(stats.fileEdits) }] : [];
  return activityToolEntries(stats);
}

export {
  activityStatDetailEntries,
  activityStatItems,
};

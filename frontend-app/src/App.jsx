import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Archive, ArrowLeft, Bot, Boxes, Brain, ChevronDown, CircleStop, Code2, Copy, Download, Eye, File, FileText, Folder, FolderOpen, GitBranch, Link2, MemoryStick, MessageCircle, Moon, MoreHorizontal, PanelTopOpen, Pencil, Pin, Plus, RefreshCw, Search, Send, Settings, Sparkles, Sun, Trash2, Workflow, X } from 'lucide-react';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { PromptPageView } from './features/prompts/PromptPageView.jsx';
import { FocusTrapDialog } from './shared/ui/FocusTrapDialog.jsx';
import {
  callBackend,
  applyDagOps,
  applySkillResolution,
  deleteDag,
  deleteMemoryEntry,
  deleteSharedFile,
  getMemoryConsolidationStatus,
  deleteSkill,
  getBuildInfo,
  getDashboardPage,
  getMemoryEntry,
  getMemorySnapshot,
  getDagDetail,
  getDagRun,
  getDagRuns,
  getPreference,
  importSkillDirectories,
  listSharedFiles,
  listSkillFiles,
  listSkillResolutions,
  ignoreMemorySimilarity,
  mergeMemoryEntries,
  onFilesDropped,
  previewSkillResolution,
  readSharedFile,
  readSkill,
  saveTextFile,
  selectProjectDirs,
  setMemoryAutoDreamIntent,
  setPreference,
  startConsolidateMemorySimilarities,
  startDag,
  startThread,
  suggestSkillSummary,
  terminateDagRun,
  upsertMemoryEntry,
  writeSkill,
} from './shared/api/backendApi.js';

const navItems = [
  { id: 'chat', label: 'Chat', icon: MessageCircle },
  { id: 'prompts', label: '提示词', icon: FileText },
  { id: 'workflows', label: '自动化', icon: Workflow },
  { id: 'skills', label: '技能', icon: Sparkles },
  { id: 'memory', label: '记忆中心', icon: Brain },
  { id: 'files', label: '共享文件', icon: FolderOpen },
  { id: 'settings', label: '设置', icon: MoreHorizontal },
];

const PROVIDER_LABELS = Object.freeze({
  claude: 'Claude',
  codex: 'Codex',
});

const DAG_RECENT_RUN_LIMIT = 5;
const DAG_DESIGNER_ENABLED_TOOLS = Object.freeze([
  'list_models',
  'prompt_list',
  'command_list',
  'shared_file_list',
  'task_create_dag',
  'task_get_dag',
  'task_dag_apply_ops',
  'task_start_dag',
]);
const DAG_CATEGORIES = Object.freeze([
  { key: 'running', label: '进行中' },
  { key: 'scheduled', label: '定时任务' },
  { key: 'history', label: '历史记录' },
]);
const STARTABLE_DAG_STATUSES = new Set(['draft', 'ready']);
const STARTABLE_DAG_TRIGGERS = new Set(['manual', 'scheduled', 'schedule', 'cron', '']);
const RUNNING_RUN_STATUSES = new Set(['running', 'pending', 'dispatching', 'waiting_for_assignee']);
const SHARED_FILE_CATEGORIES = Object.freeze([
  { key: 'all', label: '全部' },
  { key: 'final', label: '最终产物' },
  { key: 'work', label: '工作文件' },
]);
const SHARED_FILE_SORTS = Object.freeze([
  { key: 'updated-desc', label: '最新更新' },
  { key: 'updated-asc', label: '最早更新' },
  { key: 'path-asc', label: '按文件名' },
]);
const SKILLS_REQUEST_TIMEOUT_MS = 8000;
const MEMORY_CONSOLIDATION_POLL_MS = 2000;
const MEMORY_CONSOLIDATION_MAX_POLLS = 180;
const DASHBOARD_QUERY_STALE_MS = 30_000;
const DASHBOARD_QUERY_GC_MS = 10 * 60_000;
const MEMORY_CATEGORIES = Object.freeze([
  { key: 'preference', label: '偏好' },
  { key: 'project', label: '项目' },
  { key: 'all', label: '全部' },
]);
const MEMORY_TYPE_INFO = Object.freeze({
  user: { category: 'preference', label: '偏好' },
  feedback: { category: 'preference', label: '偏好' },
  project: { category: 'project', label: '项目' },
  reference: { category: 'project', label: '项目' },
});
const MEMORY_EDITOR_TYPES = Object.freeze([
  { key: 'feedback', label: '偏好' },
  { key: 'project', label: '项目' },
]);
export const APP_PROFILER_ID = 'App';
const COMPOSER_DROP_TARGET_IDS = new Set(['chat-input-bar', 'composer-input', 'chatInput']);

const THEME_STORAGE_KEY = 'super-dolphin-theme';
const COLOR_THEMES = Object.freeze({
  dark: 'dark',
  light: 'light',
});

function normalizeColorTheme(value) {
  return value === COLOR_THEMES.light || value === COLOR_THEMES.dark ? value : COLOR_THEMES.dark;
}

function useColorTheme() {
  const [theme, setTheme] = useState(() => normalizeColorTheme(window.localStorage.getItem(THEME_STORAGE_KEY)));

  const toggleTheme = useCallback(() => {
    setTheme((current) => {
      const next = current === COLOR_THEMES.dark ? COLOR_THEMES.light : COLOR_THEMES.dark;
      window.localStorage.setItem(THEME_STORAGE_KEY, next);
      return next;
    });
  }, []);

  return { theme, toggleTheme };
}

const THREAD_RAIL_MIN_WIDTH = 240;
const THREAD_RAIL_RATIO = 0.2;
const RIGHT_PANEL_CLOSE_THRESHOLD = 0;
const RIGHT_PANEL_DEFAULT_RATIO = 0.2;
const RIGHT_PANEL_MAX_RATIO = 0.4;
const CONVERSATION_MIN_RATIO = 0.4;
const NAV_RAIL_WIDTH = 76;
const SPLITTER_WIDTH = 6;
const RESIZER_KEY_STEP = 16;
const RUNTIME_TOOLBAR_HEIGHT = 67;
const ACTIVITY_ICON_ROW_HEIGHT = 64;
const ACTIVITY_LOG_ROW_HEIGHT = 32;
const ACTIVITY_PANEL_MIN_HEIGHT = ACTIVITY_ICON_ROW_HEIGHT + ACTIVITY_LOG_ROW_HEIGHT;
const ACTIVITY_PANEL_DEFAULT_HEIGHT = ACTIVITY_ICON_ROW_HEIGHT + (ACTIVITY_LOG_ROW_HEIGHT * 3);
const FLOATING_POPOVER_MARGIN = 12;
const RUNTIME_STAT_TOOLTIP_WIDTH = 360;
const RUNTIME_STAT_TOOLTIP_MIN_HEIGHT = 96;
const WARNING_POPOVER_MIN_WIDTH = 280;

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

function clampWidth(value, min, max) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) return min;
  return Math.max(min, Math.min(max, numeric));
}

function currentViewportWidth() {
  if (typeof window === 'undefined') return 0;
  const width = Number(window.innerWidth);
  return Number.isFinite(width) ? width : 0;
}

function currentViewportHeight() {
  if (typeof window === 'undefined') return 0;
  const height = Number(window.innerHeight);
  return Number.isFinite(height) ? height : 0;
}

function chatLayoutWidthBudget(viewportWidth = currentViewportWidth()) {
  return Math.max(0, viewportWidth - NAV_RAIL_WIDTH);
}

function ratioWidth(ratio, viewportWidth = currentViewportWidth()) {
  return Math.floor(chatLayoutWidthBudget(viewportWidth) * ratio);
}

function threadRailTargetWidth(viewportWidth = currentViewportWidth()) {
  return Math.max(THREAD_RAIL_MIN_WIDTH, ratioWidth(THREAD_RAIL_RATIO, viewportWidth));
}

function rightPanelDefaultWidth(viewportWidth = currentViewportWidth()) {
  return Math.max(0, ratioWidth(RIGHT_PANEL_DEFAULT_RATIO, viewportWidth));
}

function rightPanelMaxWidth(viewportWidth, threadRailWidth) {
  const layoutWidth = chatLayoutWidthBudget(viewportWidth);
  const ratioMax = ratioWidth(RIGHT_PANEL_MAX_RATIO, viewportWidth);
  const conversationMin = ratioWidth(CONVERSATION_MIN_RATIO, viewportWidth);
  const remainingAfterConversation = layoutWidth - threadRailWidth - (SPLITTER_WIDTH * 2) - conversationMin;
  return Math.max(0, Math.min(ratioMax, remainingAfterConversation));
}

function runtimePanelContentHeight(viewportHeight = currentViewportHeight()) {
  return Math.max(0, Math.floor(viewportHeight) - RUNTIME_TOOLBAR_HEIGHT);
}

function activityPanelMaxHeight(viewportHeight = currentViewportHeight()) {
  return Math.max(ACTIVITY_PANEL_MIN_HEIGHT, Math.floor(runtimePanelContentHeight(viewportHeight) / 2));
}

function clampActivityPanelHeight(value, viewportHeight = currentViewportHeight()) {
  const numeric = Number(value);
  const height = Number.isFinite(numeric) ? numeric : ACTIVITY_PANEL_DEFAULT_HEIGHT;
  return Math.max(ACTIVITY_PANEL_MIN_HEIGHT, Math.min(activityPanelMaxHeight(viewportHeight), Math.round(height)));
}

function runtimePanelHeightVars(activityPanelHeight, viewportHeight = currentViewportHeight()) {
  const contentHeight = runtimePanelContentHeight(viewportHeight);
  const activityMaxHeight = activityPanelMaxHeight(viewportHeight);
  const diffMinHeight = Math.max(0, Math.floor(contentHeight / 2));
  const diffMaxHeight = Math.max(diffMinHeight, contentHeight - ACTIVITY_PANEL_MIN_HEIGHT);
  return {
    '--runtime-toolbar-height': `${RUNTIME_TOOLBAR_HEIGHT}px`,
    '--activity-panel-height': `${clampActivityPanelHeight(activityPanelHeight, viewportHeight)}px`,
    '--activity-panel-min-height': `${ACTIVITY_PANEL_MIN_HEIGHT}px`,
    '--activity-panel-max-height': `${activityMaxHeight}px`,
    '--diff-panel-min-height': `${diffMinHeight}px`,
    '--diff-panel-max-height': `${diffMaxHeight}px`,
  };
}

function elementViewportRect(element) {
  if (!element?.getBoundingClientRect) return null;
  const rect = element.getBoundingClientRect();
  return {
    left: rect.left,
    right: rect.right,
    top: rect.top,
    bottom: rect.bottom,
    width: rect.width,
    height: rect.height,
  };
}

function runtimeStatTooltipStyle(anchorRect) {
  if (!anchorRect) return {};
  const viewportWidth = currentViewportWidth();
  const viewportHeight = currentViewportHeight();
  const maxLeft = Math.max(FLOATING_POPOVER_MARGIN, viewportWidth - RUNTIME_STAT_TOOLTIP_WIDTH - FLOATING_POPOVER_MARGIN);
  const left = Math.max(FLOATING_POPOVER_MARGIN, Math.min(maxLeft, Math.round(anchorRect.left)));
  const preferredBottom = Math.max(FLOATING_POPOVER_MARGIN, Math.round(viewportHeight - anchorRect.top + 10));
  const maxBottom = Math.max(FLOATING_POPOVER_MARGIN, viewportHeight - FLOATING_POPOVER_MARGIN - RUNTIME_STAT_TOOLTIP_MIN_HEIGHT);
  const bottom = Math.min(preferredBottom, maxBottom);
  const maxHeight = Math.max(
    RUNTIME_STAT_TOOLTIP_MIN_HEIGHT,
    Math.round(viewportHeight - bottom - FLOATING_POPOVER_MARGIN),
  );
  return {
    '--runtime-stat-tooltip-left': `${left}px`,
    '--runtime-stat-tooltip-bottom': `${bottom}px`,
    '--runtime-stat-tooltip-max-height': `${maxHeight}px`,
  };
}

function warningLogPopoverStyle(anchorRect, panelRect) {
  if (!anchorRect || !panelRect) return {};
  const viewportWidth = currentViewportWidth();
  const viewportHeight = currentViewportHeight();
  const preferredLeft = Math.round(panelRect.left + 18);
  const preferredRight = Math.round(viewportWidth - panelRect.right + 18);
  const leftLimit = Math.max(FLOATING_POPOVER_MARGIN, viewportWidth - WARNING_POPOVER_MIN_WIDTH - FLOATING_POPOVER_MARGIN);
  const left = Math.max(FLOATING_POPOVER_MARGIN, Math.min(leftLimit, preferredLeft));
  const right = Math.max(FLOATING_POPOVER_MARGIN, preferredRight);
  const bottom = Math.max(FLOATING_POPOVER_MARGIN, Math.round(viewportHeight - anchorRect.top + 10));
  return {
    '--warning-log-popover-left': `${left}px`,
    '--warning-log-popover-right': `${right}px`,
    '--warning-log-popover-bottom': `${bottom}px`,
  };
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
  const raw = (name || '').toString().trim().toLowerCase();
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
  for (const [rawName, value] of Object.entries(toolMap || {})) {
    const name = normalizeActivityToolName(rawName);
    if (!name || !matcher(name, (rawName || '').toString().trim().toLowerCase())) continue;
    sum += Number(value) || 0;
  }
  return sum;
}

function sumToolCallsByNames(toolMap, names) {
  const expected = new Set((names || []).map((name) => normalizeActivityToolName(name)).filter(Boolean));
  if (expected.size === 0) return 0;
  return sumToolCallsByMatcher(toolMap, (name) => expected.has(name));
}

function activityStatItems(stats = {}) {
  const toolCalls = stats?.toolCalls || {};
  const lspFromTools = sumToolCallsByNames(toolCalls, LSP_TOOL_NAMES);
  const totalTools = Object.values(toolCalls).reduce((sum, value) => sum + (Number(value) || 0), 0);
  return [
    { key: 'lsp', label: 'LSP (8 tools)', icon: Code2, className: 'stat-lsp', value: lspFromTools || Number(stats?.lspCalls) || 0 },
    { key: 'jsonRender', label: 'JSON-Render', icon: Boxes, className: 'stat-json-render', value: sumToolCallsByNames(toolCalls, JSON_RENDER_TOOL_NAMES) },
    {
      key: 'playwright',
      label: 'Playwright',
      icon: Workflow,
      className: 'stat-playwright',
      value: sumToolCallsByMatcher(toolCalls, (name, rawName) => PLAYWRIGHT_TOOL_PREFIXES.some((prefix) => name.startsWith(prefix) || rawName.startsWith(prefix))),
    },
    { key: 'goRun', label: 'go-run', icon: Link2, className: 'stat-go-run', value: sumToolCallsByNames(toolCalls, GO_RUN_TOOL_NAMES) },
    { key: 'command', label: '命令', icon: GitBranch, className: 'stat-cmd', value: Number(stats?.commands) || 0 },
    { key: 'file', label: '文件', icon: FileText, className: 'stat-file', value: Number(stats?.fileEdits) || 0 },
    { key: 'tool', label: '工具', icon: Settings, className: 'stat-tool', value: totalTools },
  ];
}

function activityToolEntries(stats = {}) {
  return filteredActivityToolEntries(stats, () => true);
}

function filteredActivityToolEntries(stats = {}, matcher) {
  const merged = {};
  for (const [rawName, value] of Object.entries(stats?.toolCalls || {})) {
    const raw = (rawName || '').toString().trim().toLowerCase();
    const name = normalizeActivityToolName(rawName) || rawName;
    if (!matcher(name, raw)) continue;
    merged[name] = (merged[name] || 0) + (Number(value) || 0);
  }
  return Object.entries(merged)
    .map(([name, count]) => ({ name, count }))
    .filter((entry) => entry.count > 0)
    .sort((left, right) => right.count - left.count || left.name.localeCompare(right.name));
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
  if (statKey === 'command') {
    return Number(stats?.commands) > 0 ? [{ name: '命令调用', count: Number(stats.commands) }] : [];
  }
  if (statKey === 'file') {
    return Number(stats?.fileEdits) > 0 ? [{ name: '文件变更', count: Number(stats.fileEdits) }] : [];
  }
  return activityToolEntries(stats);
}

function parseDiffFilename(line, prefix) {
  const raw = line.slice(prefix.length).trim();
  if (!raw || raw === '/dev/null') return '';
  return raw.startsWith('a/') || raw.startsWith('b/') ? raw.slice(2) : raw;
}

function summarizeUnifiedDiff(diffText) {
  if (!diffText || typeof diffText !== 'string') {
    return { fileCount: 0, additions: 0, deletions: 0, changedLines: 0, files: [] };
  }

  const files = [];
  let current = null;
  let pendingFileHeader = null;

  const startFile = (filename) => {
    current = { filename: filename || `file-${files.length + 1}`, additions: 0, deletions: 0, lines: [] };
    files.push(current);
  };

  const ensureCurrent = () => {
    if (!current) startFile();
  };

  const appendLine = (line) => {
    ensureCurrent();
    current.lines.push(line);
  };

  for (const line of diffText.split('\n')) {
    if (line.startsWith('diff --git')) {
      const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
      pendingFileHeader = null;
      startFile(match?.[2] || match?.[1] || `file-${files.length + 1}`);
      current.lines.push(line);
      continue;
    }

    if (line.startsWith('*** Begin Patch') || line.startsWith('*** End Patch') || line.startsWith('*** End of File')) {
      pendingFileHeader = null;
      continue;
    }

    if (line.startsWith('*** Update File:') || line.startsWith('*** Add File:') || line.startsWith('*** Delete File:')) {
      const prefix = line.startsWith('*** Update File:')
        ? '*** Update File:'
          : line.startsWith('*** Add File:')
            ? '*** Add File:'
            : '*** Delete File:';
      pendingFileHeader = null;
      startFile(parseDiffFilename(line, prefix) || current?.filename || `file-${files.length + 1}`);
      current.lines.push(line);
      continue;
    }

    if (line.startsWith('*** Move to:')) {
      const filename = parseDiffFilename(line, '*** Move to:');
      if (current && filename) current.filename = filename;
      appendLine(line);
      continue;
    }

    if (line.startsWith('--- ')) {
      pendingFileHeader = {
        oldFilename: parseDiffFilename(line, '---'),
        beginsNewFile: Boolean(current && (current.additions > 0 || current.deletions > 0)),
        line,
      };
      if (current && !pendingFileHeader.beginsNewFile) current.lines.push(line);
      continue;
    }

    if (line.startsWith('+++ ')) {
      const filename = parseDiffFilename(line, '+++');
      const headerFilename = filename || pendingFileHeader?.oldFilename || current?.filename || `file-${files.length + 1}`;
      if (!current || pendingFileHeader?.beginsNewFile) startFile(headerFilename);
      else current.filename = headerFilename || current.filename;
      if (pendingFileHeader?.line && !current.lines.includes(pendingFileHeader.line)) {
        current.lines.push(pendingFileHeader.line);
      }
      current.lines.push(line);
      pendingFileHeader = null;
      continue;
    }

    if (line.startsWith('index ') || line.startsWith('new file') || line.startsWith('deleted file') || line.startsWith('@@')) {
      appendLine(line);
      continue;
    }

    if (line.startsWith('+') && !line.startsWith('+++')) {
      ensureCurrent();
      current.additions += 1;
      current.lines.push(line);
      continue;
    }

    if (line.startsWith('-') && !line.startsWith('---')) {
      ensureCurrent();
      current.deletions += 1;
      current.lines.push(line);
      continue;
    }

    if (current) current.lines.push(line);
  }

  const changedFiles = files.filter((file) => file.additions > 0 || file.deletions > 0 || file.filename);
  const additions = changedFiles.reduce((sum, file) => sum + file.additions, 0);
  const deletions = changedFiles.reduce((sum, file) => sum + file.deletions, 0);
  return {
    fileCount: changedFiles.length,
    additions,
    deletions,
    changedLines: additions + deletions,
    files: changedFiles.map((file) => ({
      filename: file.filename,
      additions: file.additions,
      deletions: file.deletions,
      text: file.lines.join('\n'),
    })),
  };
}

function parseUnifiedDiffLineEntries(fileText) {
  let oldLine = null;
  let newLine = null;

  return String(fileText || '').split('\n').flatMap((line, index) => {
    if (
      line.startsWith('diff --git')
      || line.startsWith('index ')
      || line.startsWith('--- ')
      || line.startsWith('+++ ')
      || line.startsWith('*** Begin Patch')
      || line.startsWith('*** Update File:')
      || line.startsWith('*** Add File:')
      || line.startsWith('*** Delete File:')
      || line.startsWith('*** Move to:')
      || line.startsWith('*** End Patch')
      || line.startsWith('*** End of File')
    ) {
      return [];
    }

    if (line.startsWith('@@')) {
      const match = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
      oldLine = match ? Number(match[1]) : null;
      newLine = match ? Number(match[2]) : null;
      return {
        key: `${index}:hunk`,
        type: 'hunk',
        oldNo: '',
        newNo: '',
        prefix: '',
        content: line,
      };
    }

    if (line.startsWith('+') && !line.startsWith('+++')) {
      const entry = {
        key: `${index}:add`,
        type: 'add',
        oldNo: '',
        newNo: newLine ?? '',
        prefix: '+',
        content: line.slice(1),
      };
      if (newLine !== null) newLine += 1;
      return entry;
    }

    if (line.startsWith('-') && !line.startsWith('---')) {
      const entry = {
        key: `${index}:del`,
        type: 'del',
        oldNo: oldLine ?? '',
        newNo: '',
        prefix: '-',
        content: line.slice(1),
      };
      if (oldLine !== null) oldLine += 1;
      return entry;
    }

    if (line.startsWith(' ')) {
      const entry = {
        key: `${index}:context`,
        type: 'context',
        oldNo: oldLine ?? '',
        newNo: newLine ?? '',
        prefix: '',
        content: line.slice(1),
      };
      if (oldLine !== null) oldLine += 1;
      if (newLine !== null) newLine += 1;
      return entry;
    }

    return {
      key: `${index}:meta`,
      type: 'meta',
      oldNo: '',
      newNo: '',
      prefix: '',
      content: line,
    };
  });
}

function warningDetailText(entry) {
  return entry?.detail || JSON.stringify(entry?.fields ?? {});
}

function runtimeLogTimestamp(entry) {
  return entry?.timestamp || entry?.time || entry?.ts || '';
}

function runtimeLogLabel(entry) {
  return entry?.message || entry?.event || entry?.method || '';
}

function parseSafeLogTimestamp(entry) {
  const ts = runtimeLogTimestamp(entry);
  if (!ts) return 0;
  const text = ts.toString().trim();
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  // 截断高精度时间戳中的多余小数秒，以兼容 JS Date.parse 的 3 位毫秒限制
  const sanitized = text.replace(/(\.\d{3})\d+/g, '$1');
  const parsed = Date.parse(sanitized);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function runtimeLogEntries(warnings = [], results = []) {
  return [
    ...(warnings || []).map((entry) => ({ ...entry, runtimeKind: 'warning' })),
    ...(results || []).map((entry) => ({ ...entry, runtimeKind: 'result' })),
  ].sort((left, right) => {
    const leftTime = parseSafeLogTimestamp(left);
    const rightTime = parseSafeLogTimestamp(right);
    return rightTime - leftTime;
  });
}

function projectDisplayName(path) {
  const value = (path || '').toString().trim();
  if (!value || value === '未选择项目') return '未选择项目';
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function withTimeout(promise, timeoutMs, message) {
  let timeoutID;
  const timeout = new Promise((_, reject) => {
    timeoutID = globalThis.setTimeout(() => reject(new Error(message)), timeoutMs);
  });
  return Promise.race([promise, timeout]).finally(() => {
    if (timeoutID) globalThis.clearTimeout(timeoutID);
  });
}

function delay(ms) {
  return new Promise((resolve) => {
    globalThis.setTimeout(resolve, ms);
  });
}

function normalizeProjectPath(path) {
  const value = (path || '').toString().trim();
  if (!value) return '';
  if (value !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(value)) {
    return value.replace(/[\\/]+$/, '');
  }
  return value;
}

function hasUsableProjectCwd(store) {
  const activeProject = normalizeProjectPath(store?.activeProject);
  const cwd = activeProject && activeProject !== '.' && activeProject !== '未选择项目'
    ? activeProject
    : normalizeProjectPath(store?.cwd);
  return Boolean(cwd && cwd !== '.' && cwd !== '未选择项目');
}

function canUseProjectActionsForStore(store) {
  return store?.bootstrapStatus === 'ready' && hasUsableProjectCwd(store);
}

function shouldIgnoreGlobalEscape(target) {
  const element = target instanceof Element ? target : null;
  if (!element) return false;
  const tagName = element.tagName.toLowerCase();
  if (['input', 'textarea', 'select', 'option'].includes(tagName)) return true;
  if (element.isContentEditable) return true;
  return Boolean(element.closest('[role="dialog"], [role="menu"], [role="listbox"], [data-escape-scope="local"]'));
}

function disambiguateProjectLabels(items) {
  let changed = true;
  while (changed) {
    changed = false;
    const countByLabel = items.reduce((acc, item) => {
      acc[item.label] = (acc[item.label] || 0) + 1;
      return acc;
    }, {});
    for (const item of items) {
      if (countByLabel[item.label] <= 1 || item.label === item.full) continue;
      const nextDepth = Math.min(item.depth + 1, item.segments.length);
      const nextLabel = item.segments.slice(-nextDepth).join('/') || item.full;
      if (nextLabel === item.label) continue;
      item.depth = nextDepth;
      item.label = nextLabel;
      changed = true;
    }
  }
}

function projectOptionsFor(projects = [], activeProject = '', fallbackProject = '') {
  const values = [];
  const addValue = (value) => {
    const normalized = normalizeProjectPath(value);
    if (!normalized || values.includes(normalized)) return;
    values.push(normalized);
  };
  addValue(activeProject);
  addValue(fallbackProject);
  for (const project of projects || []) addValue(project);

  const items = values
    .filter((value) => value !== '.')
    .map((value) => {
      const segments = value.split(/[\\/]/).filter(Boolean);
      const depth = Math.min(2, segments.length);
      return {
        value,
        label: segments.slice(-depth).join('/') || value,
        full: value,
        segments,
        depth,
      };
    });
  disambiguateProjectLabels(items);
  return [
    { value: '.', label: '当前目录 (.)', full: '.' },
    ...items.map(({ value, label, full }) => ({ value, label, full })),
  ];
}

const MODEL_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'gpt-5.5', label: 'GPT-5.5', short: '5.5' },
    { value: 'gpt-5.4', label: 'GPT-5.4', short: '5.4' },
    { value: 'gpt-5.3-codex', label: 'GPT-5.3 Codex', short: '5.3 Codex' },
    { value: 'gpt-5.2', label: 'GPT-5.2', short: '5.2' },
  ]),
  claude: Object.freeze([
    { value: 'opus', label: 'Opus 4.7' },
    { value: 'opus[1m]', label: 'Opus 4.7 [1M]' },
    { value: 'claude-opus-4-6', label: 'Opus 4.6' },
    { value: 'claude-opus-4-6[1m]', label: 'Opus 4.6 [1M]' },
    { value: 'sonnet', label: 'Sonnet 4.7' },
    { value: 'sonnet[1m]', label: 'Sonnet 4.7 [1M]' },
    { value: 'claude-sonnet-4-6', label: 'Sonnet 4.6' },
    { value: 'claude-sonnet-4-6[1m]', label: 'Sonnet 4.6 [1M]' },
    { value: 'haiku', label: 'Haiku 4.5' },
  ]),
});

const EFFORT_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'xhigh', label: '极高' },
    { value: 'high', label: '高' },
    { value: 'medium', label: '中' },
    { value: 'low', label: '低' },
    { value: 'minimal', label: '极低' },
    { value: 'none', label: '关闭' },
  ]),
  claude: Object.freeze([
    { value: 'max', label: 'max' },
    { value: 'high', label: 'high' },
    { value: 'medium', label: 'medium' },
    { value: 'low', label: 'low' },
  ]),
});

const MODEL_DEFAULTS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze({ model: 'gpt-5.5', effort: 'xhigh' }),
  claude: Object.freeze({ model: 'sonnet', effort: 'high' }),
});
const CLAUDE_LONG_TO_SHORT = Object.freeze({
  'claude-opus-4-7': 'opus',
  'claude-opus-4-7[1m]': 'opus[1m]',
  'claude-haiku-4-5': 'haiku',
});
const TURN_STATE_INFO = Object.freeze({
  preparing: Object.freeze({ label: '准备中', tone: 'active', busy: true }),
  running: Object.freeze({ label: '运行中', tone: 'active', busy: true }),
  force_completing: Object.freeze({ label: '强制完成中', tone: 'active', busy: true }),
  interrupting: Object.freeze({ label: '中断中', tone: 'warning', busy: true }),
  interrupted: Object.freeze({ label: '已中断', tone: 'warning', busy: false }),
  completed: Object.freeze({ label: '已完成', tone: 'done', busy: false }),
  failed: Object.freeze({ label: '失败', tone: 'error', busy: false }),
  stalled: Object.freeze({ label: '停滞', tone: 'error', busy: false }),
});
const LEGACY_TURN_STATE_ALIASES = Object.freeze({
  工作中: 'running',
  发送中: 'preparing',
  error: 'failed',
  错误: 'failed',
  失败: 'failed',
});

function normalizeProviderKey(value) {
  return (value || '').toString().trim().toLowerCase() === 'claude' ? 'claude' : 'codex';
}

function knownProviderKey(value) {
  const normalized = (value || '').toString().trim().toLowerCase();
  return normalized === 'claude' || normalized === 'codex' ? normalized : '';
}

function threadProviderLabel(provider) {
  return knownProviderKey(provider) || 'unknown';
}

function normalizedThreadIdentity(value) {
  return (value || '').toString().trim();
}

function threadSortTimestamp(value) {
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? value : 0;
  const text = (value || '').toString().trim();
  if (!text) return 0;
  const asNumber = Number(text);
  if (Number.isFinite(asNumber) && asNumber > 0) return asNumber;
  const parsed = Date.parse(text);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function threadMatchesActiveId(thread, activeThreadId) {
  const id = normalizedThreadIdentity(activeThreadId);
  if (!id || !thread) return false;
  return [
    thread.id,
    thread.threadId,
    thread.thread_id,
    thread.agentId,
    thread.agent_id,
    thread.providerThreadId,
    thread.provider_thread_id,
  ].some((value) => normalizedThreadIdentity(value) === id);
}

function activeThreadIdentifiers(activeThreadId, activeThread) {
  return new Set([
    activeThreadId,
    activeThread?.id,
    activeThread?.threadId,
    activeThread?.thread_id,
    activeThread?.agentId,
    activeThread?.agent_id,
    activeThread?.providerThreadId,
    activeThread?.provider_thread_id,
    activeThread?.sessionId,
    activeThread?.session_id,
  ].map(normalizedThreadIdentity).filter(Boolean));
}

function threadScopedMapValue(map = {}, activeThreadId, activeThread, fallback = null) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  for (const id of ids) {
    if (Object.prototype.hasOwnProperty.call(map || {}, id)) return map[id];
  }
  return fallback;
}

function activityEntryThreadIdentifier(entry = {}) {
  const fields = entry.fields || {};
  const patch = fields._threadPatch || fields._thread_patch || {};
  return normalizedThreadIdentity(
    entry.threadId ||
    entry.thread_id ||
    entry.agentId ||
    entry.agent_id ||
    fields.threadId ||
    fields.thread_id ||
    fields.agentId ||
    fields.agent_id ||
    patch.threadId ||
    patch.thread_id ||
    patch.agentId ||
    patch.agent_id,
  );
}

function scopedActivityEntries(entries = [], activeThreadId, activeThread, options = {}) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  if (ids.size === 0) return [];
  return (entries || []).filter((entry) => {
    const entryThreadId = activityEntryThreadIdentifier(entry);
    if (!entryThreadId) return Boolean(options.includeUnscoped);
    return ids.has(entryThreadId);
  });
}

function activeThreadForStore(store) {
  const activeThreadId = normalizedThreadIdentity(store?.activeThreadId);
  if (!activeThreadId) return null;
  return (store?.threads || []).find((thread) => threadMatchesActiveId(thread, activeThreadId)) || null;
}

function normalizeTurnState(value) {
  const raw = normalizedThreadIdentity(value);
  if (!raw) return '';
  const alias = LEGACY_TURN_STATE_ALIASES[raw] || raw;
  return alias.toLowerCase().replace(/-/g, '_');
}

function firstStatusText(...values) {
  for (const value of values) {
    const text = normalizedThreadIdentity(value);
    if (text) return text;
  }
  return '';
}

function cleanWorkStatusDetails(value) {
  return normalizedThreadIdentity(value)
    .replace(/\uFFFD+/g, '')
    .replace(/\|+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
}

function workStatusForThread({ sending, activeThreadId, activeThread, statusEntry }) {
  if (!activeThreadId) {
    return { label: '待启动', details: '发送首条消息后创建线程', tone: 'idle', busy: false };
  }
  const rawState = firstStatusText(
    statusEntry?.state,
    statusEntry?.status,
    activeThread?.state,
    activeThread?.status,
    sending ? 'preparing' : '',
  );
  const normalizedState = normalizeTurnState(rawState);
  const mapped = TURN_STATE_INFO[normalizedState];
  const label = mapped?.label || firstStatusText(statusEntry?.statusHeader, rawState) || '已连接';
  return {
    label,
    details: cleanWorkStatusDetails(firstStatusText(statusEntry?.statusDetails, activeThread?.lastMessage, `线程 ${activeThreadId}`)),
    tone: mapped?.tone || 'connected',
    busy: mapped?.busy ?? Boolean(sending),
  };
}

function hasAssistantReply(messages = []) {
  return (messages || []).some((message) => (
    (message?.role || '').toString().trim().toLowerCase() === 'assistant'
    && Boolean((message?.text || '').toString().trim())
  ));
}

function providerToggleState(store) {
  const activeThreadId = normalizedThreadIdentity(store?.activeThreadId);
  const activeThread = activeThreadForStore(store);
  const threadConfig = threadScopedMapValue(store?.threadConfigByThread, activeThreadId, activeThread, null);
  const provider = knownProviderKey(activeThread?.provider) || knownProviderKey(threadConfig?.provider) || knownProviderKey(store?.provider) || 'codex';
  return {
    locked: Boolean(activeThreadId),
    provider,
  };
}

function composerConfigThreadId(store, activeThreadId) {
  if (!activeThreadId) return '';
  const thread = activeThreadForStore({ ...store, activeThreadId });
  if (!thread) return activeThreadId;
  if (thread.archived) return '';
  return activeThreadId;
}

function normalizeConfigText(value) {
  return (value || '').toString().trim();
}

function canonicalizeModelValue(provider, value) {
  const normalized = normalizeConfigText(value);
  if (normalizeProviderKey(provider) === 'claude') return CLAUDE_LONG_TO_SHORT[normalized] || normalized;
  return normalized;
}

function isClaudeOpusFamilyModel(model) {
  const normalized = normalizeConfigText(model).toLowerCase();
  return normalized === 'best' || normalized.includes('opus');
}

function modelOptionFor(provider, value) {
  const normalized = canonicalizeModelValue(provider, value);
  const options = MODEL_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || MODEL_OPTIONS_BY_PROVIDER.codex;
  return options.find((item) => canonicalizeModelValue(provider, item.value) === normalized)
    || (normalized ? { value: normalized, label: normalized, short: normalized } : null);
}

function effortOptionFor(provider, value) {
  const normalized = normalizeConfigText(value);
  const options = EFFORT_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  return options.find((item) => item.value === normalized) || (normalized ? { value: normalized, label: normalized } : null);
}

function appendCurrentModelOption(provider, value) {
  const options = MODEL_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || MODEL_OPTIONS_BY_PROVIDER.codex;
  const current = modelOptionFor(provider, value);
  if (!current || options.some((item) => canonicalizeModelValue(provider, item.value) === current.value)) return options;
  return [...options, current];
}

function appendCurrentEffortOption(provider, value, model = '') {
  const providerKey = normalizeProviderKey(provider);
  const baseOptions = EFFORT_OPTIONS_BY_PROVIDER[providerKey] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  const options = providerKey === 'claude' && !isClaudeOpusFamilyModel(model)
    ? baseOptions.filter((item) => item.value !== 'max')
    : baseOptions;
  const current = effortOptionFor(provider, value);
  if (!current || options.some((item) => item.value === current.value)) return options;
  return [...options, current];
}

function composerModelLabel(provider, model, effort) {
  const providerKey = normalizeProviderKey(provider);
  const modelValue = normalizeConfigText(model) || MODEL_DEFAULTS_BY_PROVIDER[providerKey].model;
  const effortValue = normalizeConfigText(effort) || MODEL_DEFAULTS_BY_PROVIDER[providerKey].effort;
  const modelLabel = modelOptionFor(providerKey, modelValue)?.label || modelValue;
  const effortLabel = effortOptionFor(providerKey, effortValue)?.label || effortValue;
  return `${modelLabel} · ${effortLabel}`.trim();
}

const STALE_ARCHIVE_MS = 7 * 24 * 60 * 60 * 1000;

function archivedStaleReason(thread) {
  if (!thread?.archived) return '';
  const archivedAt = Number(thread.archivedAt || 0);
  if (Number.isFinite(archivedAt) && archivedAt > STALE_ARCHIVE_MS && Date.now() - archivedAt > STALE_ARCHIVE_MS) {
    return 'expired';
  }
  if ((thread.name || '').toString().trim() === (thread.id || '').toString().trim()) {
    return 'empty';
  }
  return '';
}

const SETTINGS_KEYS = Object.freeze({
  stallThreshold: 'stallThresholdSec',
  contextThresholds: 'contextUsageAlerts.thresholds',
  activeProvider: 'settings.provider.active',
});

const SETTINGS_DEFAULTS = Object.freeze({
  stallThresholdSec: 30,
  contextThresholds: [70, 85, 95],
  activeProvider: 'codex',
  codexHome: '~/.codex',
  codexInstanceKey: 'default',
  codexModelProvider: 'openai',
  providerModel: 'gpt-5',
  providerEffort: 'high',
  sandboxPolicy: 'workspaceWrite',
  writableRoots: '',
  networkAccess: false,
});

function providerSettingKey(provider, key) {
  return `settings.provider.${provider}.${key}`;
}

function normalizeSettingsCwd(value) {
  const cwd = (value || '').toString().trim();
  if (!cwd || cwd === '.' || cwd === '未选择项目') {
    throw new Error('settings: cwd is required');
  }
  return cwd;
}

function optionalSettingsCwd(value) {
  const cwd = (value || '').toString().trim();
  return cwd && cwd !== '.' && cwd !== '未选择项目' ? cwd : '';
}

function dashboardQueryKey(cwd, page, ...parts) {
  return ['dashboard', 'project', cwd, page, ...parts.map((part) => textValue(part)).filter(Boolean)];
}

function dashboardGlobalQueryKey(page, ...parts) {
  return ['dashboard', 'global', page, ...parts.map((part) => textValue(part)).filter(Boolean)];
}

async function fetchSkillsDashboard(cwd) {
  const response = await withTimeout(
    getDashboardPage({ cwd, page: 'skills' }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '技能列表加载超时，请检查技能目录或后端状态。',
  );
  return normalizeSkillsResponse(response);
}

async function fetchSkillResolutionsDashboard(cwd) {
  const response = await withTimeout(
    listSkillResolutions({ cwd }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '技能冲突检查超时，请检查技能目录或后端状态。',
  );
  return normalizeResolutionResponse(response);
}

async function fetchSharedFilesDashboard() {
  const response = await withTimeout(
    listSharedFiles(),
    SKILLS_REQUEST_TIMEOUT_MS,
    '共享文件加载超时，请检查文件索引或后端状态。',
  );
  return normalizeSharedFilesResponse(response);
}

async function fetchMemoryDashboard(cwd) {
  const response = await withTimeout(
    getMemorySnapshot({ cwd }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '记忆中心加载超时，请检查记忆数据或后端状态。',
  );
  return normalizeMemorySnapshot(response);
}

async function fetchDagsDashboard(cwd) {
  const response = await withTimeout(
    getDashboardPage({ cwd, page: 'dags' }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '自动化加载超时，请检查任务数据或后端状态。',
  );
  return normalizeDagsResponse(response);
}

function queryErrorMessage(query) {
  return query?.error ? errorMessage(query.error) : '';
}

function queryHasSnapshot(query) {
  return query?.data !== undefined;
}

function dashboardQueryErrorState(query, hasSnapshot = queryHasSnapshot(query)) {
  const message = queryErrorMessage(query);
  return {
    cachedSyncError: message && hasSnapshot ? `同步失败，显示的是上次成功的数据：${message}` : '',
    blockingError: message && !hasSnapshot ? message : '',
  };
}

function useDashboardQueryFocusInvalidation(queryKey) {
  const queryClient = useQueryClient();
  useEffect(() => {
    if (!Array.isArray(queryKey) || queryKey.length === 0) return undefined;
    const invalidate = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void queryClient.invalidateQueries({ queryKey });
    };
    window.addEventListener('focus', invalidate);
    document.addEventListener('visibilitychange', invalidate);
    return () => {
      window.removeEventListener('focus', invalidate);
      document.removeEventListener('visibilitychange', invalidate);
    };
  }, [queryClient, queryKey]);
}

function useDashboardFocusInvalidation(cwd, surface) {
  const queryKey = useMemo(
    () => (cwd && surface ? dashboardQueryKey(cwd, surface) : null),
    [cwd, surface],
  );
  useDashboardQueryFocusInvalidation(queryKey);
}

function createDashboardQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        gcTime: DASHBOARD_QUERY_GC_MS,
        retry: false,
        staleTime: DASHBOARD_QUERY_STALE_MS,
        refetchOnMount: 'always',
        refetchOnWindowFocus: 'always',
      },
    },
  });
}

function stringSetting(value, fallback) {
  if (typeof value === 'string' && value.trim()) return value.trim();
  return fallback;
}

function numberSetting(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function normalizeProviderName(value) {
  const provider = stringSetting(value, SETTINGS_DEFAULTS.activeProvider).toLowerCase();
  return provider === 'claude' ? 'claude' : 'codex';
}

function normalizeContextThresholds(value) {
  if (!Array.isArray(value) || value.length < 3) return SETTINGS_DEFAULTS.contextThresholds;
  return [
    numberSetting(value[0], SETTINGS_DEFAULTS.contextThresholds[0]),
    numberSetting(value[1], SETTINGS_DEFAULTS.contextThresholds[1]),
    numberSetting(value[2], SETTINGS_DEFAULTS.contextThresholds[2]),
  ];
}

function sandboxPolicyFromPreference(value) {
  if (typeof value === 'string') return value;
  if (value && typeof value === 'object') {
    return value.type || value.mode || SETTINGS_DEFAULTS.sandboxPolicy;
  }
  return SETTINGS_DEFAULTS.sandboxPolicy;
}

function writableRootsFromPreference(value) {
  if (!value || typeof value !== 'object' || !Array.isArray(value.writableRoots)) return '';
  return value.writableRoots.join('\n');
}

function sandboxPreferenceValue(policy, writableRootsText, networkAccess) {
  if (policy === 'readOnly') return { type: 'readOnly' };
  if (policy === 'dangerFullAccess') return { type: 'dangerFullAccess' };
  const writableRoots = writableRootsText
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);
  return {
    type: 'workspaceWrite',
    writableRoots,
    networkAccess: Boolean(networkAccess),
  };
}

function scopeForSkill(raw) {
  const scope = (raw?.scope || '').toString().trim().toLowerCase();
  if (scope === 'project' || scope === 'personal') return scope;

  const trust = (raw?.trust || '').toString().trim().toLowerCase();
  if (trust === 'user' || trust === 'signed' || trust === 'system' || trust === 'personal') {
    return 'personal';
  }
  return 'project';
}

function scopeLabel(scope) {
  return scope === 'personal' ? '私人使用' : '项目共享';
}

function isInternalSkillReferenceWord(word) {
  const text = (word || '').toString().trim();
  return text.startsWith('@') || /^\[skill:[^\]]+\]$/i.test(text);
}

function normalizeWordList(...groups) {
  const seen = new Set();
  const words = [];
  groups.flat().forEach((word) => {
    const text = (word || '').toString().trim();
    const key = text.toLowerCase();
    if (!text || seen.has(key) || isInternalSkillReferenceWord(text)) return;
    seen.add(key);
    words.push(text);
  });
  return words;
}

function normalizeSkill(raw, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`skills dashboard response item ${index} must be an object`);
  }
  const name = (raw.name || raw.key || '').toString().trim();
  const displayName = (raw.display_name || raw.displayName || raw.title || '').toString().trim();
  const triggerWords = Array.isArray(raw.trigger_words) ? raw.trigger_words : raw.triggerWords || [];
  const forceWords = Array.isArray(raw.force_words) ? raw.force_words : raw.forceWords || [];
  const scope = scopeForSkill(raw);
  const dir = (raw.dir || raw.path || '').toString().trim();
  const description = (raw.description || raw.summary || '').toString().trim();
  const summary = (raw.summary || raw.description || '').toString().trim();
  const title = displayName || name;
  return {
    id: [scope, raw.personal_type || raw.personalType || '', name, dir, index].join(':'),
    name,
    title: title || '未命名技能',
    dir,
    description,
    summary,
    scope,
    personalType: (raw.personal_type || raw.personalType || '').toString().trim(),
    tags: normalizeWordList(triggerWords, forceWords),
  };
}

function normalizeSkillsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('skills dashboard response must be an object');
  }
  if (!Array.isArray(response.skills)) {
    throw new Error('skills dashboard response skills must be an array');
  }
  return response.skills.map((item, index) => normalizeSkill(item, index));
}

function cleanScalar(value) {
  return (value || '').toString().trim().replace(/^['"]|['"]$/g, '').trim();
}

function wordListFromText(value) {
  const text = Array.isArray(value) ? value.join(',') : (value || '').toString();
  return text
    .replace(/[，、；;\n]/g, ',')
    .split(',')
    .map(cleanScalar)
    .filter(Boolean)
    .filter((word, index, list) => list.findIndex((item) => item.toLowerCase() === word.toLowerCase()) === index);
}

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function normalizeSummarySuggestion(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return textValue(value.description);
  }
  return textValue(value);
}

function firstText(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) return text;
  }
  return '';
}

function numberOrNull(value) {
  if (value === null || value === undefined || value === '') return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function objectValue(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function splitSharedFilePath(path) {
  const value = textValue(path);
  if (!value) return { dir: '', base: '未命名文件' };
  const index = value.lastIndexOf('/');
  if (index < 0) return { dir: '', base: value };
  return {
    dir: value.slice(0, index + 1),
    base: value.slice(index + 1) || value,
  };
}

function sharedFileExportName(path) {
  const base = splitSharedFilePath(path).base;
  return base && base !== '未命名文件' ? base : 'shared-file.txt';
}

function sharedFileTimestamp(value) {
  const text = textValue(value);
  if (!text) return '-';
  const date = new Date(text);
  if (!Number.isFinite(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function formatBytes(size) {
  const value = Number(size);
  if (!Number.isFinite(value) || value <= 0) return '0 B';
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

function sharedFileContent(file) {
  return (file?.content || '').toString();
}

function sharedFileSummary(file) {
  const text = sharedFileContent(file).trim();
  if (!text) return '点击“打开”加载全文。';
  const lines = text.split('\n').map((line) => line.trim()).filter(Boolean).slice(0, 2).join(' ');
  return lines.length > 180 ? `${lines.slice(0, 180)}...` : lines;
}

function sharedFilePreview(file) {
  const text = sharedFileContent(file).trim();
  if (!text) return '文件为空';
  const preview = text.split('\n').slice(0, 8).join('\n');
  return preview.length > 600 ? `${preview.slice(0, 600)}...` : preview;
}

function normalizeSharedFile(raw, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`shared file item ${index} must be an object`);
  }
  const path = textValue(raw.path);
  if (!path) throw new Error(`shared file item ${index} path is required`);
  return {
    id: `${path}:${index}`,
    path,
    content: (raw.content || '').toString(),
    updatedBy: firstText(raw.updated_by, raw.updatedBy),
    updatedAt: firstText(raw.updated_at, raw.updatedAt),
    createdAt: firstText(raw.created_at, raw.createdAt),
  };
}

function normalizeFinalOutputRefs(value) {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error('shared files dashboard finalOutputRefs must be an array');
  return value.map((item, index) => {
    if (typeof item === 'string') {
      const path = textValue(item);
      if (!path) throw new Error(`final output ref ${index} path is required`);
      return { path, runKey: '', dagKey: '', sourceNodeKey: '' };
    }
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`final output ref ${index} must be an object`);
    }
    const path = firstText(item.path, item.sharedfile?.path, item.sharedFile?.path, item.shared_file?.path);
    if (!path) throw new Error(`final output ref ${index} path is required`);
    return {
      path,
      runKey: firstText(item.runKey, item.run_key),
      dagKey: firstText(item.dagKey, item.dag_key),
      sourceNodeKey: firstText(item.sourceNodeKey, item.source_node_key),
    };
  });
}

function normalizeSharedFileRetention(value) {
  if (value === undefined) {
    return { items: [], protectedCount: 0, cleanupCandidateCount: 0 };
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('shared files dashboard sharedFileRetention must be an object');
  }
  if (!Array.isArray(value.items)) {
    throw new Error('shared files dashboard sharedFileRetention.items must be an array');
  }
  return {
    items: value.items.map((item, index) => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) {
        throw new Error(`shared file retention item ${index} must be an object`);
      }
      const path = textValue(item.path);
      if (!path) throw new Error(`shared file retention item ${index} path is required`);
      return {
        path,
        protected: Boolean(item.protected),
        cleanupCandidate: Boolean(item.cleanupCandidate),
        reason: textValue(item.reason),
        finalOutput: item.finalOutput || item.final_output || null,
      };
    }),
    protectedCount: Number(value.protectedCount) || 0,
    cleanupCandidateCount: Number(value.cleanupCandidateCount) || 0,
  };
}

function normalizeSharedFilesResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('shared files dashboard response must be an object');
  }
  const rawFiles = Array.isArray(response.files) ? response.files : response.memory;
  if (!Array.isArray(rawFiles)) {
    throw new Error('shared files dashboard response files must be an array');
  }
  const rawRefs = response.finalOutputRefs;
  const rawRetention = response.sharedFileRetention;
  return {
    files: rawFiles.map((item, index) => normalizeSharedFile(item, index)),
    finalOutputRefs: normalizeFinalOutputRefs(rawRefs),
    retention: normalizeSharedFileRetention(rawRetention),
  };
}

function sharedFileMatches(file, query) {
  const needle = textValue(query).toLowerCase();
  if (!needle) return true;
  return [
    file.path,
    file.updatedBy,
    file.content,
  ].some((value) => value.toLowerCase().includes(needle));
}

function sortSharedFiles(files, sortMode) {
  const list = [...files];
  if (sortMode === 'path-asc') {
    return list.sort((left, right) => left.path.localeCompare(right.path));
  }
  const updatedTime = (file) => {
    const parsed = new Date(file.updatedAt || 0).getTime();
    return Number.isFinite(parsed) ? parsed : 0;
  };
  return list.sort((left, right) => (
    sortMode === 'updated-asc'
      ? updatedTime(left) - updatedTime(right)
      : updatedTime(right) - updatedTime(left)
  ));
}

function sharedFileCategoryOf(file, finalOutputByPath) {
  return finalOutputByPath.has(file.path) ? 'final' : 'work';
}

function memoryTemplateForType(type) {
  switch (textValue(type)) {
    case 'feedback':
      return '规则\n原因：\n如何应用：';
    case 'project':
      return '事实\n原因：\n如何应用：';
    case 'reference':
      return '指向：\n为什么重要：';
    default:
      return '用户偏好：';
  }
}

function memoryTargetForType(type) {
  return textValue(type) === 'project' ? 'team' : 'private';
}

function memorySlugText(value) {
  return textValue(value)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48);
}

function memoryNameHash(value) {
  let hash = 5381;
  const text = textValue(value);
  for (let index = 0; index < text.length; index += 1) {
    hash = ((hash << 5) + hash + text.charCodeAt(index)) >>> 0;
  }
  return hash.toString(36);
}

function memoryAutoName(form) {
  const existing = textValue(form?.name);
  if (existing) return existing;
  const type = textValue(form?.type) || 'project';
  const base = firstText(form?.title, form?.description, form?.content, type);
  const slug = memorySlugText(base);
  if (slug) return slug;
  return `${type}-${memoryNameHash(base)}`;
}

function defaultMemoryForm(type = 'project', target = memoryTargetForType(type)) {
  return {
    target,
    existingPath: '',
    name: '',
    description: '',
    title: '',
    type,
    content: memoryTemplateForType(type),
  };
}

function normalizeMemorySnapshot(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('memory snapshot response must be an object');
  }
  return {
    overview: objectValue(response.overview),
    entries: [
      ...normalizeMemorySection(response.private, 'private'),
      ...normalizeMemorySection(response.team, 'team'),
    ],
  };
}

function normalizeMemorySection(section, target) {
  const value = objectValue(section);
  if (!Array.isArray(value.entries)) {
    throw new Error(`memory ${target} entries must be an array`);
  }
  return value.entries.map((item, index) => normalizeMemoryEntry(item, index, target));
}

function normalizeMemoryEntry(raw, index, target) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`memory ${target} entry ${index} must be an object`);
  }
  const path = textValue(raw.path);
  if (!path) throw new Error(`memory ${target} entry ${index} path is required`);
  const type = textValue(raw.type).toLowerCase();
  const typeInfo = MEMORY_TYPE_INFO[type];
  if (!typeInfo) throw new Error(`memory ${target} entry ${index} type is unsupported: ${type || '(empty)'}`);
  const name = firstText(raw.name, raw.title, path);
  if (!name) throw new Error(`memory ${target} entry ${index} name is required`);
  return {
    id: `${target}:${path}:${index}`,
    target,
    path,
    type,
    category: typeInfo.category,
    tag: typeInfo.label,
    name,
    title: firstText(raw.title, raw.name),
    description: firstText(raw.description, raw.summary),
    preview: firstText(raw.preview, raw.content, raw.text),
    updatedAt: firstText(raw.updatedAt, raw.updated_at, raw.createdAt, raw.created_at),
    source: textValue(raw.source),
    raw,
  };
}

function normalizeAutoDreamIntent(value) {
  if (value === true) return true;
  if (value === false) return false;
  return null;
}

function normalizeSimilarityGroups(value) {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error('memory health similarGroups must be an array');
  return value.map((item, index) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`memory similar group ${index} must be an object`);
    }
    const group = {
      targetA: textValue(item.targetA || item.target_a),
      pathA: textValue(item.pathA || item.path_a),
      nameA: firstText(item.nameA, item.name_a),
      targetB: textValue(item.targetB || item.target_b),
      pathB: textValue(item.pathB || item.path_b),
      nameB: firstText(item.nameB, item.name_b),
      score: numberOrNull(item.score) ?? 0,
    };
    for (const key of ['targetA', 'pathA', 'targetB', 'pathB']) {
      if (!group[key]) throw new Error(`memory similar group ${index} ${key} is required`);
    }
    return group;
  });
}

function memoryHealth(overview, counts) {
  const health = overview?.health;
  if (!health || typeof health !== 'object' || Array.isArray(health)) return null;
  return {
    preferenceCount: numberOrNull(health.preferenceCount) ?? counts.preference,
    projectCount: numberOrNull(health.projectCount) ?? counts.project,
    maxPerCategory: numberOrNull(health.maxPerCategory) ?? 15,
    similarGroups: normalizeSimilarityGroups(health.similarGroups),
  };
}

function memorySimilarGroupCount(response) {
  const snapshot = response && typeof response === 'object' && Array.isArray(response.entries)
    ? response
    : normalizeMemorySnapshot(response);
  const counts = {
    preference: snapshot.entries.filter((entry) => entry.category === 'preference').length,
    project: snapshot.entries.filter((entry) => entry.category === 'project').length,
    all: snapshot.entries.length,
  };
  const health = memoryHealth(snapshot.overview, counts);
  return health?.similarGroups?.length || 0;
}

function memoryHealthPercent(count, max) {
  const safeMax = Number(max) || 1;
  const safeCount = Number(count) || 0;
  return Math.min(100, Math.max(0, Math.round((safeCount / safeMax) * 100)));
}

function memoryHealthClass(percent) {
  if (percent >= 100) return 'danger';
  if (percent >= 80) return 'warning';
  return '';
}

function memoryMatches(entry, query) {
  const needle = textValue(query).toLowerCase();
  if (!needle) return true;
  return [entry.title, entry.name, entry.description, entry.path, entry.type, entry.preview]
    .some((value) => textValue(value).toLowerCase().includes(needle));
}

function sortMemoryEntries(entries) {
  return [...entries].sort((left, right) => {
    const leftTime = new Date(left.updatedAt || 0).getTime();
    const rightTime = new Date(right.updatedAt || 0).getTime();
    const safeLeft = Number.isFinite(leftTime) ? leftTime : 0;
    const safeRight = Number.isFinite(rightTime) ? rightTime : 0;
    return safeRight - safeLeft || left.title.localeCompare(right.title);
  });
}

function memoryPairKey(group) {
  return `${group.targetA}:${group.pathA}|${group.targetB}:${group.pathB}`;
}

function formatMemoryScore(score) {
  return `${Math.round((Number(score) || 0) * 100)}%`;
}

function memoryEntryTitle(entry) {
  return firstText(entry.title, entry.description, entry.name, entry.path);
}

function memoryNoticeText(value) {
  const text = textValue(value);
  return text.length > 120 ? `${text.slice(0, 119)}…` : text;
}

function errorMessage(error) {
  return memoryNoticeText(error?.message || String(error || ''));
}

function memoryConsolidationResultMessage(result) {
  const merged = Number(result?.merged) || 0;
  const ignored = Number(result?.ignored) || 0;
  const failed = Number(result?.failed) || 0;
  const skipped = Number(result?.skipped) || 0;
  const parts = [`已整合 ${merged} 组`];
  if (ignored) parts.push(`${ignored} 组判定不应合`);
  if (failed) parts.push(`${failed} 组失败`);
  if (skipped) parts.push(`${skipped} 组跳过`);
  const firstError = Array.isArray(result?.errors) ? result.errors[0] : '';
  return {
    level: failed || skipped ? 'warning' : 'info',
    message: `${parts.join('，')}${firstError ? `，原因：${firstError}` : ''}`,
  };
}

function memoryConsolidationJobFailed(status) {
  const message = textValue(status?.error) || '智能整合暂时失败，请稍后重试';
  return new Error(message);
}

function clearMemorySimilarGroups(snapshot) {
  if (!snapshot || typeof snapshot !== 'object' || Array.isArray(snapshot)) return snapshot;
  const overview = snapshot.overview && typeof snapshot.overview === 'object' && !Array.isArray(snapshot.overview)
    ? snapshot.overview
    : {};
  const health = overview.health && typeof overview.health === 'object' && !Array.isArray(overview.health)
    ? overview.health
    : null;
  if (!health || !Array.isArray(health.similarGroups) || health.similarGroups.length === 0) return snapshot;
  return {
    ...snapshot,
    overview: {
      ...overview,
      health: {
        ...health,
        similarGroups: [],
      },
    },
  };
}

async function waitForMemoryConsolidationJob(cwd, jobID) {
  for (let attempt = 0; attempt < MEMORY_CONSOLIDATION_MAX_POLLS; attempt += 1) {
    const status = await getMemoryConsolidationStatus({ cwd, jobId: jobID });
    if (status?.status === 'succeeded') return status.result || {};
    if (status?.status === 'failed') throw memoryConsolidationJobFailed(status);
    if (status?.status !== 'running') {
      throw new Error('智能整合状态异常，请稍后重试');
    }
    await delay(MEMORY_CONSOLIDATION_POLL_MS);
  }
  throw new Error('智能整合仍在进行，请稍后查看结果');
}

function dagKeyOf(raw) {
  return firstText(raw?.dag_key, raw?.dagKey, raw?.key, raw?.id);
}

function runKeyOf(raw) {
  return firstText(raw?.run_key, raw?.runKey, raw?.key, raw?.id);
}

function nodeKeyOf(raw) {
  return firstText(raw?.node_key, raw?.nodeKey, raw?.key, raw?.id);
}

function dagVersionOf(item) {
  return numberOrNull(item?.version ?? item?.dag_version ?? item?.dagVersion ?? item?.raw?.version);
}

function normalizeDagRun(raw = {}, index = 0) {
  const runKey = runKeyOf(raw);
  return {
    id: runKey || `run:${index}`,
    runKey,
    status: firstText(raw.status, raw.state),
    triggerSource: firstText(raw.trigger_source, raw.triggerSource),
    startedAt: firstText(raw.started_at, raw.startedAt, raw.created_at, raw.createdAt),
    finishedAt: firstText(raw.finished_at, raw.finishedAt),
    metadata: objectValue(raw.metadata),
    raw,
  };
}

function normalizeDagNode(raw = {}, index = 0) {
  const nodeKey = nodeKeyOf(raw);
  const config = objectValue(raw.config);
  const dependsOn = Array.isArray(raw.depends_on)
    ? raw.depends_on
    : (Array.isArray(raw.dependsOn) ? raw.dependsOn : wordListFromText(raw.depends_on || raw.dependsOn || ''));
  return {
    id: nodeKey || `node:${index}`,
    nodeKey,
    title: firstText(raw.title, raw.name, nodeKey, `节点 ${index + 1}`),
    nodeType: firstText(raw.node_type, raw.nodeType, raw.type),
    assignedTo: firstText(raw.assigned_to, raw.assignedTo),
    dependsOn,
    status: firstText(raw.status, raw.state),
    threadId: firstText(raw.spawning_thread_id, raw.spawningThreadId, raw.threadId, raw.thread_id),
    config,
    raw,
  };
}

function normalizeDashboardDag(raw = {}, index = 0) {
  const dagKey = dagKeyOf(raw);
  const latestRun = raw.latest_run || raw.latestRun || null;
  const cronExpr = cronExprFromDagItem(raw);
  return {
    id: dagKey || `dag:${index}`,
    dagKey,
    title: firstText(raw.title, raw.name, dagKey, `自动化 ${index + 1}`),
    description: firstText(raw.description, raw.summary),
    status: firstText(raw.status, raw.state),
    trigger: dagTriggerValue(raw),
    cronExpr,
    nextRunAt: firstText(raw.next_run_at, raw.nextRunAt),
    startedAt: firstText(raw.started_at, raw.startedAt, raw.created_at, raw.createdAt),
    finishedAt: firstText(raw.finished_at, raw.finishedAt),
    version: dagVersionOf(raw),
    latestRun: latestRun ? normalizeDagRun(latestRun) : null,
    scheduleEnabled: scheduleEnabledFromDagItem(raw),
    raw,
  };
}

function normalizeDagsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('dags dashboard response must be an object');
  }
  if (!Array.isArray(response.dags)) {
    throw new Error('dags dashboard response dags must be an array');
  }
  return response.dags.map((item, index) => normalizeDashboardDag(item, index));
}

function dagStatusLabel(value) {
  const status = textValue(value).toLowerCase();
  const labels = {
    draft: '草稿',
    ready: '可运行',
    running: '运行中',
    succeeded: '成功',
    done: '成功',
    success: '成功',
    failed: '失败',
    cancelled: '已取消',
    canceled: '已取消',
    pending: '待开始',
    queued: '排队中',
    starting: '启动中',
    awaiting_verify: '待确认',
    skipped: '已跳过',
    idle: '空闲',
  };
  return labels[status] || textValue(value) || '-';
}

function dagTriggerValue(raw = {}) {
  const trigger = raw.trigger || raw.trigger_config || raw.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return firstText(trigger.type, trigger.kind, raw.trigger_type, raw.triggerType);
  }
  return firstText(trigger, raw.trigger_type, raw.triggerType);
}

function cronExprFromDagItem(item = {}) {
  const trigger = item.trigger || item.trigger_config || item.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return firstText(trigger.schedule, trigger.cron, trigger.expression, item.schedule, item.cron, item.cron_expr, item.cronExpr);
  }
  return firstText(item.schedule, item.cron, item.cron_expr, item.cronExpr);
}

function scheduleEnabledFromDagItem(item = {}) {
  if (typeof item.schedule_enabled === 'boolean') return item.schedule_enabled;
  if (typeof item.scheduleEnabled === 'boolean') return item.scheduleEnabled;
  const trigger = item.trigger || item.trigger_config || item.triggerConfig;
  if (trigger && typeof trigger === 'object' && !Array.isArray(trigger)) {
    return Boolean(firstText(trigger.next_run_at, trigger.nextRunAt, item.next_run_at, item.nextRunAt));
  }
  return Boolean(firstText(item.next_run_at, item.nextRunAt));
}

function isScheduledTrigger(value) {
  return ['scheduled', 'schedule', 'cron'].includes(textValue(value).toLowerCase());
}

function triggerLabel(value) {
  const trigger = textValue(value).toLowerCase();
  const labels = {
    manual: '手动',
    scheduled: '定时',
    schedule: '定时',
    cron: '定时',
  };
  return labels[trigger] || textValue(value) || '-';
}

const DEFAULT_DAG_SCHEDULE = Object.freeze({ preset: 'daily', time: '08:00', weekday: '1', monthDay: '1' });
const DAG_WEEKDAY_OPTIONS = Object.freeze([
  { value: '1', label: '周一' },
  { value: '2', label: '周二' },
  { value: '3', label: '周三' },
  { value: '4', label: '周四' },
  { value: '5', label: '周五' },
  { value: '6', label: '周六' },
  { value: '7', label: '周日' },
]);
const DAG_WEEKDAY_LABELS = Object.freeze(Object.fromEntries(DAG_WEEKDAY_OPTIONS.map((item) => [item.value, item.label])));

function twoDigits(value) {
  return value.toString().padStart(2, '0');
}

function parseScheduleTime(value) {
  const text = textValue(value);
  const match = /^(\d{1,2}):(\d{2})$/.exec(text);
  if (!match) return null;
  const hour = Number(match[1]);
  const minute = Number(match[2]);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return null;
  }
  return { hour, minute, label: `${twoDigits(hour)}:${twoDigits(minute)}` };
}

function scheduleStateFromCron(cronExpr) {
  const text = textValue(cronExpr);
  if (!text) return { ...DEFAULT_DAG_SCHEDULE, warning: '' };
  const parts = text.split(/\s+/);
  if (parts.length !== 5) return { ...DEFAULT_DAG_SCHEDULE, warning: '已有计划格式无法识别，请重新选择运行频率和时间。' };
  const [minuteText, hourText, dayOfMonth, month, dayOfWeek] = parts;
  const hour = Number(hourText);
  const minute = Number(minuteText);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > 23 || minute < 0 || minute > 59) {
    return { ...DEFAULT_DAG_SCHEDULE, warning: '已有计划格式无法识别，请重新选择运行频率和时间。' };
  }
  const time = `${twoDigits(hour)}:${twoDigits(minute)}`;
  if (dayOfMonth === '*' && month === '*' && dayOfWeek === '1-5') return { ...DEFAULT_DAG_SCHEDULE, preset: 'weekdays', time };
  if (dayOfMonth === '*' && month === '*' && Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, dayOfWeek)) {
    return { ...DEFAULT_DAG_SCHEDULE, preset: 'weekly', weekday: dayOfWeek, time };
  }
  const monthDay = Number(dayOfMonth);
  if (Number.isInteger(monthDay) && monthDay >= 1 && monthDay <= 31 && month === '*' && dayOfWeek === '*') {
    return { ...DEFAULT_DAG_SCHEDULE, preset: 'monthly', monthDay: monthDay.toString(), time };
  }
  if (dayOfMonth === '*' && month === '*' && dayOfWeek === '*') return { ...DEFAULT_DAG_SCHEDULE, preset: 'daily', time };
  return { ...DEFAULT_DAG_SCHEDULE, warning: '已有计划超出简化设置范围，请重新选择运行频率和时间。' };
}

function scheduleLabelFromState(schedule) {
  const parsed = parseScheduleTime(schedule?.time);
  if (!parsed) return '';
  if (schedule?.preset === 'daily') return `每天 ${parsed.label}`;
  if (schedule?.preset === 'weekdays') return `工作日 ${parsed.label}`;
  if (schedule?.preset === 'weekly') return `${DAG_WEEKDAY_LABELS[schedule.weekday] ? `每${DAG_WEEKDAY_LABELS[schedule.weekday]}` : '每周'} ${parsed.label}`;
  if (schedule?.preset === 'monthly') return `每月 ${schedule.monthDay || DEFAULT_DAG_SCHEDULE.monthDay} 日 ${parsed.label}`;
  return '';
}

function scheduleLabelFromCron(cronExpr) {
  if (!textValue(cronExpr)) return '';
  const state = scheduleStateFromCron(cronExpr);
  if (state.warning) return '';
  return scheduleLabelFromState(state);
}

function scheduleLabelFromDag(item) {
  return scheduleLabelFromCron(cronExprFromDagItem(item));
}

function cronExprFromSchedule(preset, time, weekday, monthDay) {
  const parsed = parseScheduleTime(time);
  if (!parsed) return { cronExpr: '', error: '请选择运行时间' };
  const minute = parsed.minute.toString();
  const hour = parsed.hour.toString();
  if (preset === 'daily') return { cronExpr: `${minute} ${hour} * * *`, error: '' };
  if (preset === 'weekdays') return { cronExpr: `${minute} ${hour} * * 1-5`, error: '' };
  if (preset === 'weekly') {
    if (!Object.prototype.hasOwnProperty.call(DAG_WEEKDAY_LABELS, weekday)) return { cronExpr: '', error: '请选择星期几' };
    return { cronExpr: `${minute} ${hour} * * ${weekday}`, error: '' };
  }
  if (preset === 'monthly') {
    const day = Number(monthDay);
    if (!Number.isInteger(day) || day < 1 || day > 31) return { cronExpr: '', error: '请选择每月几号' };
    return { cronExpr: `${minute} ${hour} ${day} * *`, error: '' };
  }
  return { cronExpr: '', error: '请选择运行频率' };
}

function schedulePlanLabel(item) {
  const readable = scheduleLabelFromDag(item);
  if (readable) return readable;
  return triggerLabel(item?.trigger);
}

function latestDagRunLabel(item) {
  const status = firstText(item?.latestRun?.status, item?.latest_run_status, item?.latestRunStatus);
  if (status) return dagStatusLabel(status);
  if (item?.latestRun?.runKey) return '有运行记录';
  if (isScheduledTrigger(item?.trigger)) return '未运行';
  return textValue(item?.status).toLowerCase() === 'draft' || textValue(item?.status).toLowerCase() === 'ready' ? '未启动' : '-';
}

function displayDagStatusLabel(item) {
  const trigger = textValue(item?.trigger).toLowerCase();
  const status = textValue(item?.status).toLowerCase();
  if (isScheduledTrigger(trigger) && cronExprFromDagItem(item)) {
    if (status === 'running') return '运行中';
    return scheduleEnabledFromDagItem(item) ? '已启用' : '已暂停';
  }
  return dagStatusLabel(item?.status);
}

function isRunningStatus(value) {
  return RUNNING_RUN_STATUSES.has(textValue(value).toLowerCase());
}

function dagHasActiveRun(item) {
  return isRunningStatus(item?.latestRun?.status) || isRunningStatus(item?.status);
}

function isScheduledDag(item) {
  const trigger = textValue(item?.trigger).toLowerCase();
  return ['scheduled', 'schedule', 'cron'].includes(trigger) || Boolean(item?.cronExpr || item?.nextRunAt);
}

function dagCategoryOf(item) {
  if (dagHasActiveRun(item)) return 'running';
  if (isScheduledDag(item)) return 'scheduled';
  return 'history';
}

function categoryCounts(items) {
  return DAG_CATEGORIES.reduce((acc, category) => {
    acc[category.key] = items.filter((item) => dagCategoryOf(item) === category.key).length;
    return acc;
  }, {});
}

function firstAvailableCategory(items) {
  const counts = categoryCounts(items);
  const found = DAG_CATEGORIES.find((category) => counts[category.key] > 0);
  return found?.key || DAG_CATEGORIES[0].key;
}

function finalOutputText(raw) {
  const source = raw?.run || raw || {};
  const metadata = objectValue(source.metadata);
  const value = source.final_output || source.finalOutput || metadata.final_output || metadata.finalOutput;
  if (typeof value === 'string') return value.trim();
  if (value && typeof value === 'object') {
    return firstText(value.text, value.content, value.message, value.output, value.summary) || JSON.stringify(value);
  }
  return '';
}

function validThreadIdText(value) {
  const text = textValue(value);
  if (!text || /^launch[_-]/i.test(text)) return '';
  return text;
}

function firstValidThreadId(...values) {
  for (const value of values) {
    const text = validThreadIdText(value);
    if (text) return text;
  }
  return '';
}

function threadIdFromStartResponse(value) {
  return firstValidThreadId(
    value?.threadId,
    value?.thread_id,
    value?.thread?.threadId,
    value?.thread?.thread_id,
    value?.id,
    value?.thread?.id,
    value?.agentId,
    value?.agent_id,
    value?.thread?.agentId,
    value?.thread?.agent_id,
  );
}

function dagNodeFormFromNode(node) {
  const config = objectValue(node?.config);
  return {
    nodeKey: textValue(node?.nodeKey),
    title: textValue(node?.title),
    provider: firstText(config.provider, config.agentKey, config.agent_key),
    model: textValue(config.model),
    promptKey: firstText(config.prompt_key, config.promptKey),
    dependsOn: listToText(node?.dependsOn || []),
    firstTurn: firstText(config.first_turn, config.firstTurn, config.prompt),
    outputFile: firstText(config.output_file, config.outputFile),
  };
}

function dagNodePatchFromForm(form, node) {
  const config = {
    ...objectValue(node?.config),
    provider: textValue(form.provider),
    model: textValue(form.model),
    prompt_key: textValue(form.promptKey),
    first_turn: textValue(form.firstTurn),
    output_file: textValue(form.outputFile),
  };
  return {
    title: textValue(form.title),
    depends_on: wordListFromText(form.dependsOn),
    config: cleanObject(config),
  };
}

function listToText(words) {
  return Array.isArray(words) ? words.join(', ') : '';
}

function parseWordsValue(value) {
  if (Array.isArray(value)) return wordListFromText(value);
  const raw = (value || '').toString().trim();
  if (!raw) return [];
  return wordListFromText(raw.startsWith('[') && raw.endsWith(']') ? raw.slice(1, -1) : raw);
}

function parseSkillMarkdown(content, fallbackName = '') {
  const text = (content || '').replace(/\r\n/g, '\n');
  if (!text.startsWith('---\n')) {
    return {
      name: fallbackName,
      displayName: '',
      description: '',
      triggerWords: [],
      body: text,
    };
  }
  const rest = text.slice(4);
  const end = rest.indexOf('\n---');
  if (end < 0) return { name: fallbackName, displayName: '', description: '', triggerWords: [], body: text };
  const attrs = {};
  for (const line of rest.slice(0, end).split('\n')) {
    const idx = line.indexOf(':');
    if (idx <= 0) continue;
    attrs[line.slice(0, idx).trim().toLowerCase().replace(/-/g, '_')] = line.slice(idx + 1).trim();
  }
  return {
    name: cleanScalar(attrs.name) || fallbackName,
    displayName: cleanScalar(attrs.display_name || attrs.displayname || attrs.title),
    description: cleanScalar(attrs.description || attrs.summary || attrs.digest),
    triggerWords: wordListFromText([
      ...parseWordsValue(attrs.trigger_words || attrs.triggerwords || attrs.keywords || attrs.tags),
      ...parseWordsValue(attrs.force_words || attrs.forcewords),
    ]),
    body: rest.slice(end + 4).replace(/^\n/, '').trim(),
  };
}

function quoteYAML(value) {
  return `"${(value || '').toString().replace(/"/g, '\\"')}"`;
}

function skillNameFromDisplayName(value) {
  const text = (value || '').toString().trim();
  let slug = '';
  let lastDash = false;
  for (const char of Array.from(text)) {
    if (/[\p{L}\p{N}_-]/u.test(char)) {
      slug += char;
      lastDash = false;
    } else if (!lastDash) {
      slug += '-';
      lastDash = true;
    }
  }
  return slug.replace(/^-+|-+$/g, '');
}

function buildSkillMarkdown(form) {
  const name = (form.name || '').trim();
  const displayName = (form.displayName || '').trim();
  const description = (form.description || '').trim();
  const words = wordListFromText(form.keywords);
  const body = (form.body || '').trim();
  const lines = ['---', `name: ${quoteYAML(name)}`];
  if (displayName) lines.push(`display_name: ${quoteYAML(displayName)}`);
  if (description) lines.push(`description: ${quoteYAML(description)}`);
  if (words.length > 0) lines.push(`trigger_words: [${words.map(quoteYAML).join(', ')}]`);
  lines.push('---', '', body || '## 说明\n\n请补充技能规则。');
  return lines.join('\n');
}

function SkillMarkdownPreview({ content }) {
  const text = (content || '').toString().trim();
  if (!text) return <p>暂无内容，点击“编辑正文”开始编写。</p>;
  const blocks = [];
  let paragraph = [];
  let list = [];
  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    blocks.push({ type: 'p', text: paragraph.join(' ') });
    paragraph = [];
  };
  const flushList = () => {
    if (list.length === 0) return;
    blocks.push({ type: 'ul', items: list });
    list = [];
  };
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = /^(#{1,6})\s+(.+)$/.exec(trimmed);
    if (heading) {
      flushParagraph();
      flushList();
      blocks.push({ type: 'heading', level: Math.min(heading[1].length, 3), text: heading[2] });
      continue;
    }
    const bullet = /^[-*]\s+(.+)$/.exec(trimmed);
    if (bullet) {
      flushParagraph();
      list.push(bullet[1]);
      continue;
    }
    paragraph.push(trimmed);
  }
  flushParagraph();
  flushList();
  return (
    <>
      {blocks.map((block, index) => {
        if (block.type === 'heading') {
          const Tag = block.level <= 1 ? 'h3' : 'h4';
          return <Tag key={`heading-${index}`}>{block.text}</Tag>;
        }
        if (block.type === 'ul') {
          return <ul key={`list-${index}`}>{block.items.map((item, itemIndex) => <li key={`${index}-${itemIndex}`}>{item}</li>)}</ul>;
        }
        return <p key={`p-${index}`}>{block.text}</p>;
      })}
    </>
  );
}

function emptySkillForm() {
  return {
    name: '',
    displayName: '',
    description: '',
    keywords: '',
    body: '',
    scope: 'project',
    personalType: '',
  };
}

function normalizeSkillFileList(response) {
  if (!response || typeof response !== 'object' || !Array.isArray(response.files)) return [];
  return response.files
    .map((file) => ({
      name: (file?.name || '').toString().trim(),
      path: (file?.path || '').toString().trim(),
      isMain: Boolean(file?.is_main || file?.isMain),
    }))
    .filter((file) => file.name && file.path);
}

function isMainSkillFile(path) {
  return /(^|[\\/])SKILL\.md$/i.test((path || '').toString().trim());
}

function normalizeResolutionResponse(response) {
  if (Array.isArray(response)) return response;
  if (!response || typeof response !== 'object') {
    throw new Error('skill resolutions response must be an object');
  }
  if (Array.isArray(response?.items)) return response.items;
  if (Array.isArray(response?.conflicts)) return response.conflicts;
  throw new Error('skill resolutions response items must be an array');
}

function resolutionKindLabel(kind) {
  return ({
    mirror_drift: '外部版本有改动',
    unmanaged_provider_skill: '发现外部技能',
    unmanaged: '发现外部技能',
    same_name: '同名技能',
    same_name_scope_conflict: '同名技能',
    canonical_deleted_with_drift: '旧版本需要处理',
    external_personal_project_same_name: '私人和项目同名',
  }[(kind || '').toString().trim().toLowerCase()] || '需要处理');
}

function resolutionActionLabel(action) {
  return ({
    view_diff: '查看两个版本',
    view_unmanaged: '查看外部位置',
    sync_back_to_canonical: '用外部修改更新本项目',
    canonical_overwrite_mirror: '用本项目内容覆盖外部版本',
    save_as_new_skill: '另存为新技能',
    confirm_delete_drifted_mirror: '删除旧版本',
    sync_back_to_personal: '继续私人使用',
    personal_overwrite_mirror: '用私人技能覆盖外部版本',
    save_as_new_personal_skill: '另存为新私人技能',
    import_to_personal_imported: '导入到私人使用',
    import_to_project: '导入到项目共享',
    takeover_provider_skill: '纳入管理',
    use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
    use_external_provider_skill: '继续私人使用，替换项目共享版本',
    replace_provider_root_symlink: '接管外部技能目录',
    rename_personal: '改名保存',
    keep_selected: '用选中的版本，删除其他版本',
  }[(action || '').toString().trim()] || '处理');
}

function resolutionActionHelp(action) {
  return ({
    view_diff: '只查看差异，不写入文件。',
    view_unmanaged: '查看外部技能位置，不写入文件。',
    sync_back_to_canonical: '把外部修改同步回当前管理的技能。',
    canonical_overwrite_mirror: '用当前项目共享技能覆盖 Claude/Codex 中的外部版本。',
    save_as_new_skill: '保留两边内容，把外部版本保存成新的项目共享技能。',
    confirm_delete_drifted_mirror: '删除 Claude/Codex 里保留的旧版本。',
    sync_back_to_personal: '恢复为私人使用，外部运行时会继续读取这个私人版本。',
    personal_overwrite_mirror: '用当前私人技能覆盖 Claude/Codex 中的外部版本。',
    save_as_new_personal_skill: '保留两边内容，把外部版本保存成新的私人技能。',
    import_to_personal_imported: '把外部技能导入到私人使用。',
    import_to_project: '把外部技能导入到项目共享。',
    takeover_provider_skill: '把外部技能纳入当前技能管理。',
    use_project_shared_skill: '使用项目共享版本，并删除同名旧私人版本。',
    use_external_provider_skill: '继续私人使用，并替换项目共享版本。',
    replace_provider_root_symlink: '用当前技能根目录接管外部技能目录。',
    rename_personal: '把选中的版本改名保存，两个版本都会保留。',
    keep_selected: '保留选中的版本，删除其他同名版本。',
  }[(action || '').toString().trim()] || '');
}

function requiresResolutionNewName(action) {
  return action === 'save_as_new_skill'
    || action === 'save_as_new_personal_skill'
    || action === 'rename_personal';
}

function isResolutionViewAction(action) {
  return action === 'view_diff' || action === 'view_unmanaged';
}

function resolutionRequiresApply(action) {
  return !isResolutionViewAction(action);
}

function defaultResolutionNewName(conflict, action) {
  const base = (conflict?.name || conflict?.skill_name || 'skill').toString().trim() || 'skill';
  return `${base}${action === 'save_as_new_personal_skill' ? '-private' : '-copy'}`;
}

const actionableResolutionActions = new Set([
  'view_diff',
  'view_unmanaged',
  'sync_back_to_canonical',
  'canonical_overwrite_mirror',
  'save_as_new_skill',
  'confirm_delete_drifted_mirror',
  'sync_back_to_personal',
  'personal_overwrite_mirror',
  'save_as_new_personal_skill',
  'import_to_personal_imported',
  'import_to_project',
  'takeover_provider_skill',
  'use_project_shared_skill',
  'use_external_provider_skill',
  'replace_provider_root_symlink',
  'rename_personal',
  'keep_selected',
]);

function resolutionActionUnsupported(action) {
  return !actionableResolutionActions.has((action || '').toString().trim());
}

function resolutionSourceID(source) {
  return (source?.canonical_id || source?.canonicalID || source?.source_id || source?.sourceID || '').toString().trim();
}

function resolutionSourceScope(source) {
  return (source?.scope || '').toString().trim().toLowerCase();
}

function resolutionSourcePersonalType(source) {
  return (source?.personal_type || source?.personalType || '').toString().trim().toLowerCase();
}

function resolutionSourcePathLeaf(source) {
  const path = (source?.path || source?.skill_file || source?.skillFile || '').toString().trim().replace(/\\/g, '/');
  if (!path) return '';
  const parts = path.split('/').filter(Boolean);
  const leaf = parts[parts.length - 1] || '';
  return leaf === 'SKILL.md' && parts.length > 1 ? parts[parts.length - 2] || '' : leaf;
}

function sameNameResolutionConflict(conflict) {
  const kind = (conflict?.kind || '').toString().trim().toLowerCase();
  return kind === 'same_name' || kind === 'same_name_scope_conflict';
}

function sameNameProjectSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => resolutionSourceScope(source) === 'project');
}

function sameNamePersonalSources(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.filter((source) => resolutionSourceScope(source) === 'personal');
}

function sameNameHasProjectSource(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return sources.some((source) => resolutionSourceScope(source) === 'project');
}

function firstResolutionSourceID(conflict) {
  const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
  return resolutionSourceID(sources[0]);
}

function sameNamePersonalVersionText(source, hasProjectSource = false) {
  const suffix = hasProjectSource ? '私人版本' : '版本';
  const value = resolutionSourcePersonalType(source);
  return ({
    user: `自己创建的${suffix}`,
    agent: `自动生成的${suffix}`,
    imported: `导入的${suffix}`,
    hub: `市场下载的${suffix}`,
  }[value] || `私人${suffix}`);
}

function sameNameSourceShortText(source, includeSourceLeaf = false) {
  if (resolutionSourceScope(source) === 'project') {
    const leaf = includeSourceLeaf ? resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') : '';
    return leaf ? `项目共享版本：${leaf}` : '项目共享版本';
  }
  return sameNamePersonalVersionText(source, true);
}

function sameNameProjectVersionEntry(source, multipleProjectSources = false) {
  const leaf = multipleProjectSources ? resolutionSourcePathLeaf(source) || resolutionSourceID(source).replace(/^project\//, '') : '';
  return {
    action: 'keep_selected',
    label: leaf ? `用项目共享版本：${leaf}，删除其他版本` : '用项目共享版本，删除其他版本',
    help: '保留这个项目共享版本，删除其他同名版本。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function sameNameRenameEntry(source, includeSourceLeaf = false) {
  return {
    action: 'rename_personal',
    label: `改名保存${sameNameSourceShortText(source, includeSourceLeaf)}`,
    help: '把这个版本改成新名称，原来的同名冲突会保留为不同技能。',
    source,
    sourceID: resolutionSourceID(source),
  };
}

function personalDeletedDriftResolutionConflict(conflict) {
  return (conflict?.kind || '').toString().trim().toLowerCase() === 'canonical_deleted_with_drift'
    && (conflict?.scope || '').toString().trim().toLowerCase() === 'personal';
}

function externalPersonalProjectResolutionConflict(conflict) {
  return (conflict?.kind || '').toString().trim().toLowerCase() === 'external_personal_project_same_name';
}

function resolutionProviderLabel(provider) {
  return ({
    codex: 'Codex',
    claude: 'Claude',
  }[(provider || '').toString().trim().toLowerCase()] || (provider || '').toString().trim());
}

function resolutionProviderEntryLabel(entry) {
  const label = (entry?.display_label || '').toString().trim();
  if (label) return label;
  const group = Array.isArray(entry?.provider_group) ? entry.provider_group.map(resolutionProviderLabel).filter(Boolean) : [];
  if (group.length > 0) return group.join('、');
  return resolutionProviderLabel(entry?.provider || entry?.source_provider) || '外部版本';
}

function resolutionProviderEntries(conflict) {
  const entries = Array.isArray(conflict?.provider_entries) ? conflict.provider_entries : [];
  if (entries.length > 0) return entries;
  const provider = (conflict?.provider || conflict?.source_provider || '').toString().trim();
  if (!provider) return [{}];
  return [{
    provider,
    source_path_id: conflict?.source_path_id || conflict?.sourcePathId || '',
  }];
}

function resolutionActionEntries(conflict) {
  const actions = (Array.isArray(conflict?.available_actions) ? conflict.available_actions : [])
    .filter((action) => !resolutionActionUnsupported(action));
  if (personalDeletedDriftResolutionConflict(conflict)) {
    return actions.map((action) => ({
      action,
      label: ({
        sync_back_to_personal: '继续私人使用',
        confirm_delete_drifted_mirror: '使用项目共享版本，删除旧私人版本',
      }[action] || resolutionActionLabel(action)),
      help: resolutionActionHelp(action),
    }));
  }
  if (externalPersonalProjectResolutionConflict(conflict)) {
    const allowed = new Set(['view_diff', 'use_project_shared_skill', 'use_external_provider_skill', 'save_as_new_personal_skill']);
    return actions
      .filter((action) => allowed.has(action))
      .map((action) => ({
        action,
        label: ({
          use_project_shared_skill: '使用项目共享版本，删除旧私人版本',
          use_external_provider_skill: '继续私人使用，替换项目共享版本',
        }[action] || resolutionActionLabel(action)),
        help: resolutionActionHelp(action),
      }));
  }
  if (!sameNameResolutionConflict(conflict)) {
    return actions.map((action) => ({ action, help: resolutionActionHelp(action) }));
  }
  const entries = [];
  const personalSources = sameNamePersonalSources(conflict);
  const projectSources = sameNameProjectSources(conflict);
  const hasProjectSource = sameNameHasProjectSource(conflict);
  if (actions.includes('keep_selected')) {
    projectSources.forEach((source) => entries.push(sameNameProjectVersionEntry(source, projectSources.length > 1)));
    personalSources.forEach((source) => {
      const versionText = sameNamePersonalVersionText(source, hasProjectSource);
      entries.push({
        action: 'keep_selected',
        label: `用${versionText}，删除其他版本`,
        help: `保留这个${versionText}，删除其他同名版本。`,
        source,
        sourceID: resolutionSourceID(source),
      });
    });
  }
  if (actions.includes('rename_personal')) {
    [...projectSources, ...personalSources].forEach((source) => {
      entries.push(sameNameRenameEntry(source, projectSources.length > 1));
    });
  }
  return entries.length > 0 ? entries : actions.map((action) => ({ action, help: resolutionActionHelp(action) }));
}

function resolutionActionEntryLabel(entry) {
  return entry?.label || resolutionActionLabel(entry?.action || entry);
}

function resolutionActionEntryHelp(entry) {
  return entry?.help || resolutionActionHelp(entry?.action || entry);
}

function resolutionActionEntryTarget(actionEntry, providerEntry) {
  if (providerEntry?.merged_provider_entry && actionEntry?.action === 'view_unmanaged') {
    return {
      ...providerEntry,
      provider: '',
      source_path_id: '',
      sourcePathId: '',
    };
  }
  return actionEntry?.source ? actionEntry : providerEntry;
}

function resolutionSameNamePayloadFields(conflict, action, entry = null) {
  switch (action) {
    case 'rename_personal':
    case 'keep_selected': {
      const sources = Array.isArray(conflict?.sources) ? conflict.sources : [];
      const selected = entry?.source
        || sources.find((source) => resolutionSourceScope(source) === 'personal')
        || sources.find((source) => resolutionSourceScope(source) === 'project');
      const keepSourceID = resolutionSourceID(selected) || firstResolutionSourceID(conflict);
      return keepSourceID ? { keep_source_id: keepSourceID } : {};
    }
    case 'merge_manually': {
      const mergeContentHash = (conflict?.merge_content_hash || conflict?.mergeContentHash || '').toString().trim();
      return {
        keep_source_id: firstResolutionSourceID(conflict),
        merge_content_hash: mergeContentHash,
      };
    }
    default:
      return {};
  }
}

function resolutionActionAutoApplies(action) {
  return action === 'keep_selected';
}

function resolutionActionAutoAppliesForConflict(action, conflict) {
  if (resolutionActionAutoApplies(action)) return true;
  if (action === 'rename_personal') return true;
  if (externalPersonalProjectResolutionConflict(conflict)) {
    return action === 'use_project_shared_skill'
      || action === 'use_external_provider_skill'
      || action === 'save_as_new_personal_skill';
  }
  return false;
}

function resolutionApplyKey(conflict, action, entry = null) {
  const source = (
    entry?.source_path_id
    || entry?.sourcePathId
    || entry?.provider
    || entry?.sourceID
    || resolutionSourceID(entry?.source)
    || ''
  ).toString().trim();
  return `${conflict?.conflict_id || conflict?.conflictId || ''}:${source}:${action || ''}`;
}

function previewItemPaths(item) {
  return [
    ['来源', item?.source_path || item?.sourcePath],
    ['目标', item?.target_path || item?.targetPath],
  ].map(([label, value]) => ({ label, value: (value || '').toString().trim() }))
    .filter((itemPath) => itemPath.value);
}

function parsePositiveInteger(label, value) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isInteger(parsed)) throw new Error(`${label} 必须是整数`);
  return parsed;
}

function validateRuntimeThresholds(form) {
  const stallThresholdSec = parsePositiveInteger('统一超时阈值', form.stallThresholdSec);
  if (stallThresholdSec < 30) throw new Error('统一超时阈值必须大于或等于 30 秒');

  const warn = parsePositiveInteger('Warn 阈值', form.contextWarn);
  const danger = parsePositiveInteger('Danger 阈值', form.contextDanger);
  const critical = parsePositiveInteger('Critical 阈值', form.contextCritical);
  if (!(warn > 0 && warn < danger && danger < critical && critical <= 100)) {
    throw new Error('上下文阈值必须满足 0 < warn < danger < critical <= 100');
  }
  return { stallThresholdSec, contextThresholds: [warn, danger, critical] };
}

function AppShell({ skipBootstrap = false }) {
  const store = useClientStore();
  const bootstrap = store.bootstrap;
  const addWarning = store.addWarning;
  const queryClient = useQueryClient();
  const memoryRevision = Number(store.memoryRevision || 0);
  const [memoryPageSimilarCount, setMemoryPageSimilarCount] = useState(null);

  useEffect(() => {
    if (skipBootstrap) return undefined;
    let cancelled = false;
    bootstrap().catch((error) => {
      if (!cancelled) {
        console.error('[frontend-app] bootstrap failed', error);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [bootstrap, skipBootstrap]);

  const projectPath = store.activeProject && store.activeProject !== '.' ? store.activeProject : store.cwd || '未选择项目';
  const memoryCwd = optionalSettingsCwd(projectPath);
  useDashboardFocusInvalidation(memoryCwd, 'memory');
  const memoryBadgeQuery = useQuery({
    queryKey: dashboardQueryKey(memoryCwd, 'memory'),
    queryFn: () => fetchMemoryDashboard(memoryCwd),
    enabled: Boolean(memoryCwd),
    select: memorySimilarGroupCount,
  });
  const memorySimilarCount = Math.max(0, Number(memoryBadgeQuery.data) || 0);

  useEffect(() => {
    if (store.activePage !== 'memory') {
      setMemoryPageSimilarCount(null);
    }
  }, [store.activePage, memoryCwd]);

  useEffect(() => {
    if (!memoryBadgeQuery.error) return;
    addWarning('warn', 'memory.badge.refresh.failed', { error: errorMessage(memoryBadgeQuery.error) });
  }, [addWarning, memoryBadgeQuery.error]);

  useEffect(() => {
    if (!memoryCwd || memoryRevision <= 0) return;
    void queryClient.invalidateQueries({ queryKey: dashboardQueryKey(memoryCwd, 'memory') });
  }, [memoryCwd, memoryRevision, queryClient]);

  const activeLabel = useMemo(() => (
    navItems.find((item) => item.id === store.activePage)?.label || 'Chat'
  ), [store.activePage]);

  const { theme, toggleTheme } = useColorTheme();

  return (
    <div className="sa-window" data-theme={theme} data-testid="frontend-app">
      <Titlebar theme={theme} onToggleTheme={toggleTheme} />
      <div className="sa-body">
        <NavRail
          activePage={store.activePage}
          setActivePage={store.setActivePage}
          memorySimilarCount={memoryPageSimilarCount ?? memorySimilarCount}
        />
        <main className="sa-main">
          {store.activePage === 'chat' ? <ChatPage store={store} projectPath={projectPath} /> : null}
          {store.activePage === 'prompts' ? <PromptPage projectPath={projectPath} store={store} refreshKey={store.promptRevision} /> : null}
          {store.activePage === 'workflows' ? <WorkflowPage projectPath={projectPath} store={store} refreshKey={store.workflowRevision} /> : null}
          {store.activePage === 'skills' ? <SkillsPage projectPath={projectPath} refreshKey={store.skillRevision} resolveLaunchPreferences={store.resolveLaunchPreferences} /> : null}
          {store.activePage === 'memory' ? (
            <MemoryPage
              projectPath={projectPath}
              refreshKey={memoryRevision}
              onSimilarCountChange={setMemoryPageSimilarCount}
              resolveLaunchPreferences={store.resolveLaunchPreferences}
            />
          ) : null}
          {store.activePage === 'files' ? <FilesPage projectPath={projectPath} store={store} /> : null}
          {store.activePage === 'settings' ? <SettingsPage projectPath={projectPath} /> : null}
          <span className="sr-only">当前页面：{activeLabel}</span>
        </main>
      </div>
    </div>
  );
}

function App(props) {
  const [queryClient] = useState(createDashboardQueryClient);
  return (
    <QueryClientProvider client={queryClient}>
      <AppShell {...props} />
    </QueryClientProvider>
  );
}

function Titlebar({ theme, onToggleTheme }) {
  const isDark = theme === COLOR_THEMES.dark;
  const ThemeIcon = isDark ? Sun : Moon;
  const label = isDark ? '白天模式' : '黑夜模式';

  return (
    <header className="titlebar">
      <div className="titlebar-brand">
        <span className="brand-orb" aria-hidden="true" />
        <strong>Super Agent</strong>
      </div>
      <button
        type="button"
        className="theme-toggle"
        onClick={onToggleTheme}
        aria-label={`切换到${label}`}
      >
        <ThemeIcon size={16} aria-hidden="true" />
        <span>{label}</span>
      </button>
    </header>
  );
}

function NavRail({ activePage, setActivePage, memorySimilarCount = 0 }) {
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);
  return (
    <aside className="nav-rail" data-testid="sidebar-nav">
      <nav>
        {navItems.map((item) => {
          const Icon = item.icon;
          const badgeCount = item.id === 'memory' ? memoryBadgeCount : 0;
          return (
            <button
              key={item.id}
              type="button"
              className={activePage === item.id ? 'active' : ''}
              onClick={() => setActivePage(item.id)}
              aria-label={item.label}
            >
              <Icon size={22} aria-hidden="true" />
              <span>{item.label}</span>
              {badgeCount > 0 ? <i aria-hidden="true" title={`${badgeCount} 条待整合相似记忆`} /> : null}
            </button>
          );
        })}
      </nav>
    </aside>
  );
}

function ChatPage({ store, projectPath }) {
  const activeThreadId = store.activeThreadId;
  const modelThreadId = composerConfigThreadId(store, activeThreadId);
  const activeThread = activeThreadForStore(store);
  const timelineBlocked = Boolean(activeThreadId && store.threadStateLoadingByThread?.[activeThreadId]);
  const messages = timelineBlocked ? [] : (threadScopedMapValue(store.timelinesByThread, activeThreadId, activeThread, []) || []);
  const tokenUsage = threadScopedMapValue(store.tokenUsageByThread, activeThreadId, activeThread, null);
  const activityStats = threadScopedMapValue(store.activityStatsByThread, activeThreadId, activeThread, null);
  const diffText = threadScopedMapValue(store.diffTextByThread, activeThreadId, activeThread, '') || '';
  const warningEntries = scopedActivityEntries(store.warningEntries, activeThreadId, activeThread, { includeUnscoped: true });
  const runtimeResultEntriesForThread = scopedActivityEntries(store.runtimeResultEntries, activeThreadId, activeThread, { includeUnscoped: true });
  const statusEntry = activeThreadId ? store.statuses?.[activeThreadId] : null;
  const canUseProjectActions = canUseProjectActionsForStore(store);
  const [viewportWidth, setViewportWidth] = useState(currentViewportWidth);
  const [threadRailWidth, setThreadRailWidth] = useState(() => threadRailTargetWidth());
  const threadRailResizedRef = useRef(false);
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const rightPanelResizedRef = useRef(false);
  const chatLayoutRef = useRef(null);
  const threadRailMaxWidth = threadRailTargetWidth(viewportWidth);
  const effectiveThreadRailWidth = clampWidth(threadRailWidth, THREAD_RAIL_MIN_WIDTH, threadRailMaxWidth);
  const maxRightPanelWidth = rightPanelMaxWidth(viewportWidth, effectiveThreadRailWidth);
  const rightPanelWidth = clampWidth(store.rightPanelWidth, 0, maxRightPanelWidth);

  useEffect(() => {
    const onResize = () => setViewportWidth(currentViewportWidth());
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  useEffect(() => {
    setThreadRailWidth((width) => {
      const targetWidth = threadRailTargetWidth(viewportWidth);
      if (!threadRailResizedRef.current) return targetWidth;
      return clampWidth(width, THREAD_RAIL_MIN_WIDTH, targetWidth);
    });
  }, [viewportWidth]);

  useEffect(() => {
    if (!rightPanelOpen) return;
    const targetWidth = rightPanelResizedRef.current
      ? clampWidth(store.rightPanelWidth, 0, maxRightPanelWidth)
      : clampWidth(rightPanelDefaultWidth(viewportWidth), 0, maxRightPanelWidth);
    if (targetWidth <= 0) {
      store.setRightPanelWidth?.(0);
      setRightPanelOpen(false);
      return;
    }
    if (targetWidth !== store.rightPanelWidth) {
      store.setRightPanelWidth?.(targetWidth);
    }
  }, [maxRightPanelWidth, rightPanelOpen, store, viewportWidth]);

  useEffect(() => {
    const onKeyDown = (event) => {
      if (event.defaultPrevented || event.key !== 'Escape' || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
      if (shouldIgnoreGlobalEscape(event.target)) return;
      if (!store.hasActiveThreadActions?.()) return;
      event.preventDefault();
      void store.interruptActiveThread?.();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [store, activeThreadId]);

  const beginThreadRailResize = (event) => {
    event.preventDefault();
    threadRailResizedRef.current = true;
    const startX = event.clientX;
    const startWidth = effectiveThreadRailWidth;
    const move = (moveEvent) => {
      const next = clampWidth(startWidth + (moveEvent.clientX - startX), THREAD_RAIL_MIN_WIDTH, threadRailMaxWidth);
      setThreadRailWidth(next);
    };
    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
  };

  const handleThreadRailResizeKeyDown = (event) => {
    if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    let nextWidth = null;
    if (event.key === 'ArrowLeft') nextWidth = effectiveThreadRailWidth - RESIZER_KEY_STEP;
    else if (event.key === 'ArrowRight') nextWidth = effectiveThreadRailWidth + RESIZER_KEY_STEP;
    else if (event.key === 'Home') nextWidth = THREAD_RAIL_MIN_WIDTH;
    else if (event.key === 'End') nextWidth = threadRailMaxWidth;
    if (nextWidth === null) return;
    event.preventDefault();
    threadRailResizedRef.current = true;
    setThreadRailWidth(clampWidth(nextWidth, THREAD_RAIL_MIN_WIDTH, threadRailMaxWidth));
  };

  const beginRightPanelResize = (event) => {
    event.preventDefault();
    event.currentTarget?.setPointerCapture?.(event.pointerId);
    rightPanelResizedRef.current = true;
    const startX = event.clientX;
    const startWidth = rightPanelWidth;
    let latestWidth = startWidth;
    let stopped = false;
    const layoutColumnsForWidth = (width) => `${effectiveThreadRailWidth}px ${SPLITTER_WIDTH}px minmax(0, 1fr) ${SPLITTER_WIDTH}px ${width}px`;
    const applyDragWidth = (width) => {
      if (chatLayoutRef.current) {
        chatLayoutRef.current.style.gridTemplateColumns = layoutColumnsForWidth(width);
      }
    };
    const finish = () => {
      if (stopped) return;
      stopped = true;
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', finish);
      window.removeEventListener('pointercancel', finish);
      window.removeEventListener('blur', finish);
      event.currentTarget?.releasePointerCapture?.(event.pointerId);
      if (latestWidth <= RIGHT_PANEL_CLOSE_THRESHOLD) {
        store.setRightPanelWidth?.(0);
        setRightPanelOpen(false);
        return;
      }
      store.setRightPanelWidth?.(latestWidth);
    };
    const move = (moveEvent) => {
      if (Number(moveEvent.buttons) === 0) {
        finish();
        return;
      }
      const rawNext = startWidth - (moveEvent.clientX - startX);
      if (rawNext <= RIGHT_PANEL_CLOSE_THRESHOLD) {
        latestWidth = 0;
        applyDragWidth(0);
        finish();
        return;
      }
      latestWidth = clampWidth(rawNext, 0, maxRightPanelWidth);
      applyDragWidth(latestWidth);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', finish);
    window.addEventListener('pointercancel', finish);
    window.addEventListener('blur', finish);
  };

  const handleRightPanelResizeKeyDown = (event) => {
    if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    let nextWidth = null;
    if (event.key === 'ArrowLeft') nextWidth = rightPanelWidth + RESIZER_KEY_STEP;
    else if (event.key === 'ArrowRight') nextWidth = rightPanelWidth - RESIZER_KEY_STEP;
    else if (event.key === 'Home') nextWidth = 0;
    else if (event.key === 'End') nextWidth = maxRightPanelWidth;
    if (nextWidth === null) return;
    event.preventDefault();
    rightPanelResizedRef.current = true;
    const clampedWidth = clampWidth(nextWidth, 0, maxRightPanelWidth);
    if (clampedWidth <= RIGHT_PANEL_CLOSE_THRESHOLD) {
      store.setRightPanelWidth?.(0);
      setRightPanelOpen(false);
      return;
    }
    store.setRightPanelWidth?.(clampedWidth);
  };

  const layoutColumns = rightPanelOpen
    ? `${effectiveThreadRailWidth}px ${SPLITTER_WIDTH}px minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightPanelWidth}px`
    : `${effectiveThreadRailWidth}px ${SPLITTER_WIDTH}px minmax(0, 1fr)`;

  const toggleRightPanel = () => {
    const next = !rightPanelOpen;
    if (next) {
      rightPanelResizedRef.current = false;
      store.setRightPanelWidth?.(clampWidth(rightPanelDefaultWidth(viewportWidth), 0, maxRightPanelWidth));
    }
    setRightPanelOpen(next);
  };

  return (
    <section className="chat-page" data-testid="chat-page">
      <TopCommandBar
        store={store}
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        toggleRightPanel={toggleRightPanel}
      />
      <div
        ref={chatLayoutRef}
        className="chat-layout"
        data-testid="chat-layout"
        style={{ gridTemplateColumns: layoutColumns }}
      >
        <ThreadRail store={store} />
        <div
          role="separator"
          className="splitter splitter--left"
          aria-orientation="vertical"
          aria-label="调整会话栏宽度"
          aria-valuemin={THREAD_RAIL_MIN_WIDTH}
          aria-valuemax={threadRailMaxWidth}
          aria-valuenow={effectiveThreadRailWidth}
          data-testid="thread-rail-resizer"
          tabIndex={0}
          onKeyDown={handleThreadRailResizeKeyDown}
          onPointerDown={beginThreadRailResize}
        />
        <Conversation
          messages={messages}
          draft={store.draft}
          setDraft={store.setDraft}
          sendMessage={store.sendDraft}
          attachments={store.attachments}
          selectFiles={store.selectFilesForComposer}
          attachPaths={store.attachPathsForComposer}
          attachDroppedFiles={store.attachDroppedFilesForComposer}
          attachPastedImages={store.attachPastedImagesForComposer}
          removeAttachment={store.removeAttachment}
          sending={store.sending}
          store={store}
          projectPath={projectPath}
          permission={store.permission}
          setPermission={store.setPermission}
          tokenUsage={tokenUsage}
          activeThreadId={activeThreadId}
          activeThread={activeThread}
          statusEntry={statusEntry}
          modelThreadId={modelThreadId}
          timelineBlocked={timelineBlocked}
          canUseProjectActions={canUseProjectActions}
        />
        {rightPanelOpen ? (
          <div
            role="separator"
            className="splitter splitter--right"
            aria-orientation="vertical"
            aria-label="调整侧边栏宽度"
            aria-valuemin={0}
            aria-valuemax={maxRightPanelWidth}
            aria-valuenow={rightPanelWidth}
            data-testid="right-panel-resizer"
            tabIndex={0}
            onKeyDown={handleRightPanelResizeKeyDown}
            onPointerDown={beginRightPanelResize}
          />
        ) : null}
        {rightPanelOpen ? (
          <RuntimePanel
            diffText={diffText}
            tokenUsage={tokenUsage}
            activityStats={activityStats}
            warnings={warningEntries}
            runtimeResults={runtimeResultEntriesForThread}
          />
        ) : null}
      </div>
    </section>
  );
}

function ProjectSelector({ store, projectPath }) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef(null);
  const activeProject = store.activeProject || projectPath;
  const options = useMemo(
    () => projectOptionsFor(store.projects, activeProject, projectPath),
    [store.projects, activeProject, projectPath],
  );
  const selectedValue = normalizeProjectPath(activeProject) || '.';
  const selected = options.find((item) => item.value === selectedValue)
    || options.find((item) => item.value === '.')
    || { value: '.', label: '当前目录 (.)', full: '.' };
  const selectedButtonLabel = selected.value === '.'
    ? projectDisplayName(projectPath)
    : projectDisplayName(selected.full || selected.value);

  useEffect(() => {
    if (!open) return undefined;
    const onPointerDown = (event) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target)) {
        setOpen(false);
      }
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () => document.removeEventListener('pointerdown', onPointerDown, true);
  }, [open]);

  const selectProject = async (value) => {
    setOpen(false);
    await store.setActiveProjectPath?.(value);
  };

  const addProject = async () => {
    setOpen(false);
    await store.addProjectFromPicker?.();
  };

  const removeProject = async (event, value) => {
    event.stopPropagation();
    await store.removeProjectPath?.(value);
  };

  return (
    <div className="project-select-wrap" ref={wrapRef}>
      <button
        type="button"
        className="project-select"
        aria-label="选择项目"
        aria-haspopup="menu"
        aria-expanded={open}
        title={selected.full === '.' ? projectPath : selected.full}
        onClick={() => setOpen((value) => !value)}
      >
        <Folder size={15} />
        <span>{selectedButtonLabel}</span>
        <ChevronDown size={14} />
      </button>
      {open ? (
        <div className="project-dropdown" role="menu" aria-label="项目列表">
          {options.map((item) => (
            <div key={item.value} className={`project-dropdown-row ${item.value === selected.value ? 'selected' : ''}`} role="none" title={item.full}>
              <button
                type="button"
                className="project-dropdown-item"
                role="menuitem"
                onClick={() => void selectProject(item.value)}
              >
                <span className="project-option-check" aria-hidden="true">{item.value === selected.value ? '✓' : ''}</span>
                <span className="project-dropdown-label">{item.label}</span>
              </button>
              {item.value !== '.' ? (
                <button
                  type="button"
                  className="project-dropdown-remove"
                  aria-label={`移除此项目 ${item.label}`}
                  title="移除此项目"
                  onClick={(event) => void removeProject(event, item.value)}
                >
                  <X size={12} />
                </button>
              ) : null}
            </div>
          ))}
          <div className="project-dropdown-divider" />
          <button
            type="button"
            className="project-dropdown-item project-dropdown-add"
            role="menuitem"
            onClick={() => void addProject()}
          >
            <Plus size={13} />
            <span>添加项目</span>
          </button>
        </div>
      ) : null}
    </div>
  );
}

function ProviderToggle({ store, canUseProjectActions = true }) {
  const { locked, provider } = providerToggleState(store);
  const isClaude = provider === 'claude';
  const providerLabel = isClaude ? 'Claude' : 'Codex';
  const projectActionBlocked = !canUseProjectActions;
  const disabled = locked || projectActionBlocked;
  const unavailableLabel = '请先连接后端并选择项目';
  let title = '切换 Claude / Codex provider';
  if (projectActionBlocked) title = unavailableLabel;
  if (locked) title = '已开启的聊天不能更改 provider，请新建对话后切换';
  return (
    <button
      type="button"
      className={`provider ${isClaude ? 'active' : ''} ${disabled ? 'locked' : ''}`}
      aria-label={projectActionBlocked ? unavailableLabel : '切换 Claude / Codex provider'}
      aria-pressed={isClaude}
      aria-disabled={disabled}
      disabled={disabled}
      title={title}
      onClick={() => {
        if (disabled) return;
        void store.toggleProviderMode();
      }}
    >
      <span className="provider-track" aria-hidden="true">
        <span className="provider-thumb" />
      </span>
      <span className="provider-label">{providerLabel}</span>
    </button>
  );
}

function TopCommandBar({ store, projectPath, rightPanelOpen = false, toggleRightPanel = () => {} }) {
  const canUseThreadActions = Boolean(store.hasActiveThreadActions?.());
  const bootstrapFailureMessage = store.bootstrapStatus === 'failed' && textValue(store.error)
    ? `连接后端失败：${textValue(store.error)}`
    : '';
  const feedback = store.actionNotice?.message
    ? store.actionNotice
    : (bootstrapFailureMessage ? { message: bootstrapFailureMessage, tone: 'error' } : null);
  return (
    <div className="top-command" data-testid="chat-toolbar">
      <ProjectSelector store={store} projectPath={projectPath} />
      <button
        type="button"
        className="icon-btn"
        aria-label="新窗口（独立进程）"
        title="新窗口（独立进程）"
        onClick={() => void store.openNewWindow?.()}
      >
        <PanelTopOpen size={15} />
      </button>
      {canUseThreadActions ? (
        <button type="button" className="icon-btn" aria-label="复制当前线程" title="复制当前线程" onClick={() => void store.copyActiveThreadInfo()}><Copy size={15} /></button>
      ) : null}
      {canUseThreadActions ? (
        <button type="button" className="icon-btn" aria-label="停止" title="中断当前执行" onClick={() => void store.interruptActiveThread()}><CircleStop size={15} /></button>
      ) : null}
      <button
        type="button"
        className="icon-btn"
        aria-label={canUseThreadActions ? '进程恢复' : '请先选择会话'}
        title={canUseThreadActions ? '手动杀进程并恢复连接' : '请先选择会话'}
        disabled={!canUseThreadActions}
        onClick={() => void store.recoverActiveThread()}
      >
        <RefreshCw size={15} />
      </button>
      {feedback?.message ? (
        <span
          className={`action-feedback ${feedback.tone || 'info'}`}
          data-testid="chat-action-feedback"
          role="status"
        >
          {feedback.message}
        </span>
      ) : null}
      <span className="project-pill" aria-label="当前工作目录" title={`当前窗口 CWD：${projectPath}`}><Folder size={14} /> {projectDisplayName(projectPath)}</span>
      <button
        type="button"
        className={`icon-btn sidebar-toggle ${rightPanelOpen ? 'active' : ''}`}
        aria-label={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        title={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        aria-pressed={rightPanelOpen}
        onClick={toggleRightPanel}
      >
        {rightPanelOpen ? <X size={15} /> : <Eye size={15} />}
      </button>
    </div>
  );
}

function ThreadRail({ store }) {
  const [showArchivedThreads, setShowArchivedThreads] = useState(false);
  const [confirmCleanMode, setConfirmCleanMode] = useState(false);
  const [hoveredPinThreadId, setHoveredPinThreadId] = useState('');
  const [editingThreadId, setEditingThreadId] = useState('');
  const [editingName, setEditingName] = useState('');
  const [renamingThreadId, setRenamingThreadId] = useState('');
  const activeThreads = store.threads.filter((thread) => !thread.archived);
  const archivedThreads = store.threads.filter((thread) => thread.archived);
  const threads = showArchivedThreads ? archivedThreads : activeThreads;
  const visibleThreads = threads
    .map((thread, index) => ({
      ...thread,
      staleReason: archivedStaleReason(thread),
      listIndex: index,
      pinnedAt: Number(store.pinnedThreadAtById?.[thread.id] || thread.pinnedAt || 0),
      activityAt: threadSortTimestamp(store.activityThreadAtById?.[thread.id] || thread.updatedAt),
    }))
    .sort((left, right) => {
      const leftPinned = left.pinnedAt > 0;
      const rightPinned = right.pinnedAt > 0;
      if (leftPinned !== rightPinned) return leftPinned ? -1 : 1;
      if (leftPinned && rightPinned && left.pinnedAt !== right.pinnedAt) return right.pinnedAt - left.pinnedAt;
      if (!leftPinned && !rightPinned && left.activityAt !== right.activityAt) return right.activityAt - left.activityAt;
      return left.listIndex - right.listIndex;
    });
  const staleThreadIds = showArchivedThreads
    ? visibleThreads.filter((thread) => thread.staleReason).map((thread) => thread.id)
    : [];
  const toggleArchiveLabel = showArchivedThreads ? '返回会话列表' : '打开归档列表';
  const toggleArchiveList = () => {
    setShowArchivedThreads((value) => {
      const next = !value;
      if (!next) setConfirmCleanMode(false);
      return next;
    });
  };
  const beginRename = (thread) => {
    setEditingThreadId(thread.id);
    setEditingName((thread.name || '').toString());
  };
  const cancelRename = () => {
    if (renamingThreadId) return;
    setEditingThreadId('');
    setEditingName('');
  };
  const submitRename = async (thread) => {
    const nextName = editingName.trim();
    if (!nextName || renamingThreadId) return;
    if (nextName === (thread.name || '').toString().trim()) {
      cancelRename();
      return;
    }
    setRenamingThreadId(thread.id);
    try {
      await store.renameThread(thread.id, nextName);
      setEditingThreadId('');
      setEditingName('');
    } finally {
      setRenamingThreadId('');
    }
  };
  const handleRenameBlur = (event, thread) => {
    const saveFor = event.relatedTarget?.dataset?.renameSaveButtonFor || '';
    if (saveFor === thread.id) return;
    cancelRename();
  };
  return (
    <aside className="thread-rail" data-testid="thread-rail" aria-label={showArchivedThreads ? '归档列表' : '会话列表'}>
      <div className="thread-tools">
        <button type="button" className="round thread-new-primary" aria-label="新建对话" title="新对话：发送第一条消息时才会创建会话" onClick={store.newThread}>
          <Pencil size={17} />
        </button>
        <span className="count thread-count" role="img" aria-label={`${visibleThreads.length} 个 Agent`} title={`${visibleThreads.length} 个 Agent`}>
          <Bot size={14} />
          <strong>{visibleThreads.length}</strong>
        </span>
        {showArchivedThreads && staleThreadIds.length > 0 && !confirmCleanMode ? (
          <button
            type="button"
            className="round thread-clean"
            aria-label="清理无用对话"
            title="清理无用对话"
            onClick={() => setConfirmCleanMode(true)}
          >
            <Trash2 size={15} />
          </button>
        ) : null}
        {showArchivedThreads && confirmCleanMode ? (
          <>
            <button
              type="button"
              className="thread-clean-confirm"
              onClick={() => {
                setConfirmCleanMode(false);
                void store.deleteStaleThreads(staleThreadIds);
              }}
            >
              确认
            </button>
            <button type="button" className="thread-clean-cancel" onClick={() => setConfirmCleanMode(false)}>取消</button>
          </>
        ) : null}
        <button
          type="button"
          className={`round thread-archive-toggle ${showArchivedThreads ? 'active' : ''}`}
          aria-label={toggleArchiveLabel}
          title={toggleArchiveLabel}
          onClick={toggleArchiveList}
        >
          {showArchivedThreads ? <ArrowLeft size={15} /> : <Archive size={15} />}
        </button>
      </div>
      <div className="thread-list">
        {visibleThreads.length === 0 ? (
          <p className="thread-empty">
            {showArchivedThreads ? '暂无归档会话' : '暂无会话，点击「新建对话」开始草稿'}
          </p>
        ) : null}
        {visibleThreads.map((thread) => {
          const selectedThreadId = store.pendingActiveThreadId || store.activeThreadId;
          const active = selectedThreadId === thread.id;
          const running = ['running', '工作中', 'pending', 'recovering'].includes((thread.status || '').toLowerCase()) || thread.status === '工作中';
          const archiveLabel = thread.archived ? '恢复会话' : '归档会话';
          const pinned = thread.pinnedAt > 0 || thread.pinned;
          const pinLabel = pinned ? '取消置顶对话' : '置顶对话';
          const editing = editingThreadId === thread.id;
          return (
            <div
              key={thread.id}
              className={`thread-card ${active ? 'active' : ''}`}
            >
              {editing ? (
                <>
                  <span className="thread-pin thread-pin--placeholder" aria-hidden="true">
                    <Pin size={20} strokeWidth={2.2} />
                  </span>
                  <div className="thread-main thread-main--editing">
                    <input
                      className="thread-name-input"
                      aria-label="会话别名"
                      value={editingName}
                      maxLength={64}
                      disabled={renamingThreadId === thread.id}
                      autoFocus
                      onFocus={(event) => event.currentTarget.select()}
                      onChange={(event) => setEditingName(event.target.value)}
                      onClick={(event) => event.stopPropagation()}
                      onBlur={(event) => handleRenameBlur(event, thread)}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter') {
                          event.preventDefault();
                          void submitRename(thread);
                        }
                        if (event.key === 'Escape') {
                          event.preventDefault();
                          cancelRename();
                        }
                      }}
                    />
                    <button
                      type="button"
                      className="thread-rename-save"
                      aria-label="保存别名"
                      data-rename-save-button-for={thread.id}
                      disabled={renamingThreadId === thread.id}
                      onMouseDown={(event) => event.preventDefault()}
                      onClick={() => void submitRename(thread)}
                    >
                      保存
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <button
                    type="button"
                    className={`thread-pin ${pinned ? 'active' : ''}`}
                    aria-label={pinLabel}
                    title={pinLabel}
                    aria-pressed={pinned}
                    onClick={() => void store.toggleThreadPin(thread.id)}
                    onMouseEnter={() => setHoveredPinThreadId(thread.id)}
                    onMouseLeave={() => setHoveredPinThreadId((current) => (current === thread.id ? '' : current))}
                    onFocus={() => setHoveredPinThreadId(thread.id)}
                    onBlur={() => setHoveredPinThreadId((current) => (current === thread.id ? '' : current))}
                  >
                    <Pin size={20} strokeWidth={2.2} />
                    {hoveredPinThreadId === thread.id ? (
                      <span className="thread-pin-tooltip" data-testid="thread-pin-tooltip" role="tooltip">
                        {pinLabel}
                      </span>
                    ) : null}
                  </button>
                  <button
                    type="button"
                    className="thread-main"
                    onClick={() => void store.setActiveThread(thread.id)}
                  >
                    <span
                      className="thread-name"
                      title="点击重命名"
                      onClick={(event) => {
                        event.stopPropagation();
                        beginRename(thread);
                      }}
                    >
                      {thread.name}
                    </span>
                    <b>{threadProviderLabel(thread.provider)}</b>
                    <em className={running ? 'running' : ''}>
                      {running ? '工作中' : thread.status || '等待指示'}
                      {thread.staleReason ? (
                        <span className="thread-stale-badge" data-stale-reason={thread.staleReason}>
                          {thread.staleReason === 'expired' ? '超7天' : '空对话'}
                        </span>
                      ) : null}
                    </em>
                  </button>
                </>
              )}
              <button
                type="button"
                className={`thread-archive ${thread.archived ? 'active' : ''}`}
                aria-label={archiveLabel}
                title={archiveLabel}
                onClick={() => void store.archiveThread(thread.id, !thread.archived)}
              >
                <Archive size={15} />
              </button>
            </div>
          );
        })}
      </div>
    </aside>
  );
}

function ModelSelector({ store, activeThreadId, disabled = false }) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef(null);
  const activeThreadConfig = activeThreadId ? store.threadConfigByThread?.[activeThreadId] : null;
  const providerKey = normalizeProviderKey(activeThreadConfig?.provider || store.providerConfig?.provider || store.provider);
  const providerDefaults = MODEL_DEFAULTS_BY_PROVIDER[providerKey] || MODEL_DEFAULTS_BY_PROVIDER.codex;
  const canOverrideThread = Boolean(activeThreadId && activeThreadConfig?.supportsThreadOverride);
  const activeModel = canOverrideThread
    ? normalizeConfigText(activeThreadConfig?.override?.model || activeThreadConfig?.effective?.model || providerDefaults.model)
    : normalizeConfigText(store.providerConfig?.model || providerDefaults.model);
  const activeEffort = canOverrideThread
    ? normalizeConfigText(activeThreadConfig?.override?.effort || activeThreadConfig?.effective?.effort || providerDefaults.effort)
    : normalizeConfigText(store.providerConfig?.effort || providerDefaults.effort);
  const draftModel = canOverrideThread ? normalizeConfigText(activeThreadConfig?.override?.model) : activeModel;
  const draftEffort = canOverrideThread ? normalizeConfigText(activeThreadConfig?.override?.effort) : activeEffort;
  const [draft, setDraft] = useState({ model: draftModel, effort: draftEffort });

  useEffect(() => {
    if (!open) {
      setDraft({ model: draftModel, effort: draftEffort });
    }
  }, [draftModel, draftEffort, open]);

  useEffect(() => {
    if (!open) return undefined;
    const onPointerDown = (event) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target)) setOpen(false);
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () => document.removeEventListener('pointerdown', onPointerDown, true);
  }, [open]);

  useEffect(() => {
    if (disabled && open) setOpen(false);
  }, [disabled, open]);

  const openSelector = async () => {
    if (disabled) return;
    const nextOpen = !open;
    setDraft({ model: draftModel, effort: draftEffort });
    setOpen(nextOpen);
    if (!nextOpen || !activeThreadId) return;
    const loaded = await store.loadThreadConfig?.(activeThreadId);
    if (!loaded) return;
    const loadedCanOverride = Boolean(loaded.supportsThreadOverride);
    setDraft({
      model: loadedCanOverride ? normalizeConfigText(loaded.override?.model) : activeModel,
      effort: loadedCanOverride ? normalizeConfigText(loaded.override?.effort) : activeEffort,
    });
  };

  const saveModelConfig = async (patch) => {
    let next = { ...draft, ...patch };
    if (
      providerKey === 'claude'
      && normalizeConfigText(next.effort).toLowerCase() === 'max'
      && !isClaudeOpusFamilyModel(next.model || activeModel)
    ) {
      next = { ...next, effort: 'high' };
    }
    setDraft(next);
    await store.saveComposerModelConfig?.({ threadId: activeThreadId, model: next.model, effort: next.effort });
  };

  const restoreInheritance = async () => {
    await store.restoreComposerModelInheritance?.({ threadId: activeThreadId });
    setOpen(false);
  };

  const selectedModel = canonicalizeModelValue(providerKey, draft.model || activeModel);
  const selectedEffort = draft.effort || activeEffort;
  const selectModelValue = canOverrideThread
    ? canonicalizeModelValue(providerKey, draft.model)
    : canonicalizeModelValue(providerKey, draft.model || activeModel);
  const selectEffortValue = canOverrideThread ? draft.effort : draft.effort || activeEffort;
  const modelOptions = appendCurrentModelOption(providerKey, selectedModel);
  const effortOptions = appendCurrentEffortOption(providerKey, selectedEffort, selectedModel);
  const inherited = canOverrideThread && !activeThreadConfig?.override?.model && !activeThreadConfig?.override?.effort;
  const label = composerModelLabel(providerKey, activeModel, activeEffort);
  const inheritModelLabel = activeModel ? `默认（当前：${modelOptionFor(providerKey, activeModel)?.label || activeModel}）` : '默认';
  const inheritEffortLabel = activeEffort ? `默认（当前：${effortOptionFor(providerKey, activeEffort)?.label || activeEffort}）` : '默认';
  const selectorBusy = Boolean(store.threadConfigSaving || (activeThreadId && store.threadConfigLoadingByThread?.[activeThreadId]));
  const unavailableTitle = '请先连接后端并选择项目';

  return (
    <div className="composer-model-wrap" ref={wrapRef}>
      <button
        type="button"
        className="composer-model"
        aria-label="选择模型"
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-busy={selectorBusy}
        title={disabled ? unavailableTitle : (canOverrideThread ? '线程执行配置' : '全局模型配置')}
        disabled={disabled}
        onClick={() => void openSelector()}
      >
        {label}
        <ChevronDown size={12} />
      </button>
      {open ? (
        <div className="model-dropdown" role="dialog" aria-label="模型配置">
          <label>
            <span>模型</span>
            <select aria-label="模型" value={selectModelValue} disabled={disabled || selectorBusy} onChange={(event) => void saveModelConfig({ model: event.target.value })}>
              {canOverrideThread ? <option value="">{inheritModelLabel}</option> : null}
              {modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </label>
          <label>
            <span>强度</span>
            <select aria-label="推理强度" value={selectEffortValue} disabled={disabled || selectorBusy} onChange={(event) => void saveModelConfig({ effort: event.target.value })}>
              {canOverrideThread ? <option value="">{inheritEffortLabel}</option> : null}
              {effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </label>
          {canOverrideThread && !inherited ? (
            <button type="button" className="model-inherit" disabled={disabled || selectorBusy} onClick={() => void restoreInheritance()}>
              继承全局
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function attachmentKey(item) {
  return textValue(item?.path || item?.previewUrl || item?.url);
}

function hasFilesTransfer(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return false;
  if (transfer.files && transfer.files.length > 0) return true;
  return Array.from(transfer.types || []).includes('Files');
}

function collectTransferFiles(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return [];
  const files = Array.from(transfer.files || []).filter(Boolean);
  if (files.length > 0) return files;
  return Array.from(transfer.items || [])
    .filter((item) => item?.kind === 'file')
    .map((item) => item.getAsFile?.())
    .filter(Boolean);
}

function extractClipboardImageFiles(event) {
  const clipboard = event?.clipboardData;
  if (!clipboard) return [];
  const images = [];
  const seen = new Set();
  const add = (file) => {
    if (!file || seen.has(file)) return;
    const type = textValue(file.type).toLowerCase();
    const name = textValue(file.name).toLowerCase();
    if (!type.startsWith('image/') && !/\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(name)) return;
    seen.add(file);
    images.push(file);
  };
  Array.from(clipboard.files || []).forEach(add);
  Array.from(clipboard.items || []).forEach((item) => {
    if (!item) return;
    const type = textValue(item.type).toLowerCase();
    if (type.startsWith('image/') || item.kind === 'file') {
      add(item.getAsFile?.());
    }
  });
  return images;
}

function AttachmentPreviewModal({ attachment, onClose, onRemove }) {
  const isImage = attachment.kind === 'image' && attachment.previewUrl;
  return (
    <FocusTrapDialog ariaLabel="附件预览" className="modal-box attachment-preview-modal" onClose={onClose}>
        <header>
          <div>
            <strong>{attachment.name || attachment.path}</strong>
            <p>{attachment.path}</p>
          </div>
          <button type="button" aria-label="关闭附件预览" onClick={onClose}><X size={16} /></button>
        </header>
        {isImage ? (
          <img className="attachment-preview-image" src={attachment.previewUrl} alt={attachment.name || '附件图片预览'} />
        ) : (
          <div className="attachment-preview-file">
            <File size={28} />
            <code>{attachment.path}</code>
          </div>
        )}
        <footer>
          <button type="button" onClick={onRemove}><Trash2 size={14} /> 移除附件</button>
          <button type="button" onClick={onClose}>关闭</button>
        </footer>
    </FocusTrapDialog>
  );
}

function markdownTableCells(line) {
  return line
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((cell) => cell.trim());
}

function isMarkdownTableDivider(line) {
  const cells = markdownTableCells(line);
  return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function safeMarkdownUrl(rawUrl, options = {}) {
  const value = (rawUrl || '').toString().trim();
  if (!value) return '';
  try {
    const parsed = new URL(value, window.location?.origin || 'http://localhost');
    const protocol = parsed.protocol.toLowerCase();
    if (options.image) {
      if (protocol === 'http:' || protocol === 'https:' || protocol === 'data:' || protocol === 'file:') return value;
      return '';
    }
    if (protocol === 'http:' || protocol === 'https:' || protocol === 'mailto:' || protocol === 'file:') return parsed.href;
  } catch {
    return '';
  }
  return '';
}

function renderInlineMarkdown(text, keyPrefix) {
  const source = (text || '').toString();
  const parts = [];
  const pattern = /(!\[[^\]]*]\([^)]+\)|\[[^\]]+]\([^)]+\)|`[^`]+`|\*\*[^*]+\*\*|__[^_]+__|~~[^~]+~~|\*[^*]+\*|_[^_]+_)/g;
  let lastIndex = 0;
  let matchIndex = 0;
  for (const match of source.matchAll(pattern)) {
    if (match.index > lastIndex) parts.push(source.slice(lastIndex, match.index));
    const token = match[0];
    const key = `${keyPrefix}-inline-${matchIndex}`;
    if (token.startsWith('![')) {
      const parsed = token.match(/^!\[([^\]]*)]\(([^)]+)\)$/);
      const src = safeMarkdownUrl(parsed?.[2], { image: true });
      parts.push(src ? <img key={key} src={src} alt={parsed?.[1] || ''} /> : token);
    } else if (token.startsWith('[')) {
      const parsed = token.match(/^\[([^\]]+)]\(([^)]+)\)$/);
      const href = safeMarkdownUrl(parsed?.[2]);
      parts.push(href ? <a key={key} href={href} target="_blank" rel="noreferrer">{parsed?.[1]}</a> : parsed?.[1] || token);
    } else if (token.startsWith('`')) {
      parts.push(<code key={key}>{token.slice(1, -1)}</code>);
    } else if (token.startsWith('~~')) {
      parts.push(<del key={key}>{token.slice(2, -2)}</del>);
    } else if (token.startsWith('*') && !token.startsWith('**')) {
      parts.push(<em key={key}>{token.slice(1, -1)}</em>);
    } else if (token.startsWith('_') && !token.startsWith('__')) {
      parts.push(<em key={key}>{token.slice(1, -1)}</em>);
    } else {
      parts.push(<strong key={key}>{token.slice(2, -2)}</strong>);
    }
    lastIndex = match.index + token.length;
    matchIndex += 1;
  }
  if (lastIndex < source.length) parts.push(source.slice(lastIndex));
  return parts.length > 0 ? parts : source;
}

function renderMarkdownParagraph(lines, key) {
  return (
    <p key={key}>
      {lines.flatMap((line, index) => [
        ...(index > 0 ? [<br key={`${key}-br-${index}`} />] : []),
        ...renderInlineMarkdown(line, `${key}-${index}`),
      ])}
    </p>
  );
}

function normalizeMessageText(text) {
  return (text || '').toString().replace(/\r\n/g, '\n');
}

function standaloneCodeFence(text) {
  const trimmed = normalizeMessageText(text).trim();
  const match = trimmed.match(/^```([^\n`]*)\n([\s\S]*?)\n```$/);
  if (!match) return null;
  return {
    language: (match[1] || '').trim().toLowerCase(),
    body: match[2],
  };
}

function candidatePayload(text) {
  const fenced = standaloneCodeFence(text);
  if (!fenced) return { language: '', body: normalizeMessageText(text) };
  return fenced;
}

function parseJsonOutput(text) {
  const payload = candidatePayload(text);
  if (payload.language && !['json', 'jsonc'].includes(payload.language)) return null;
  const trimmed = payload.body.trim();
  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return null;
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return null;
  }
}

function isDiffOutput(text) {
  const payload = candidatePayload(text);
  const body = payload.body.trim();
  if (['diff', 'patch'].includes(payload.language)) return body.length > 0;
  const lines = body.split('\n');
  if (body.startsWith('diff --git ') || body.startsWith('*** Begin Patch')) return true;
  const hasOldHeader = lines.some((line) => line.startsWith('--- '));
  const hasNewHeader = lines.some((line) => line.startsWith('+++ '));
  const hasHunk = lines.some((line) => line.startsWith('@@ '));
  return hasOldHeader && hasNewHeader && hasHunk;
}

function isLogOutput(text) {
  const payload = candidatePayload(text);
  const lines = payload.body.split('\n').map((line) => line.trimEnd()).filter(Boolean);
  if (lines.length === 0) return false;
  if (['log', 'logs', 'console', 'terminal'].includes(payload.language)) return true;

  const levelLines = lines.filter((line) => /^(\[[A-Z]+]|\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}|(?:TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\b)/.test(line));
  const stackTrace = lines.some((line) => /^(?:\w+\s*)?Error:/.test(line))
    && lines.some((line) => /^\s*at\s+.+:\d+:\d+\)?$/.test(line));
  const terminalPrompt = lines.some((line) => /^[$#]\s+[A-Za-z0-9_./-]+/.test(line));
  return stackTrace || levelLines.length > 0 || terminalPrompt;
}

function isConfigOutput(text) {
  const payload = candidatePayload(text);
  const lines = payload.body.split('\n').map((line) => line.trim()).filter(Boolean);
  if (lines.length < 2) return false;
  if (['yaml', 'yml', 'toml', 'ini', 'env', 'dotenv', 'properties'].includes(payload.language)) return true;
  const keyValueLines = lines.filter((line) => /^[-\w."']+(\s*[:=]\s*|\s+=\s+).+/.test(line));
  return keyValueLines.length >= 2 && keyValueLines.length / lines.length >= 0.6;
}

function detectMessageOutput(text) {
  const json = parseJsonOutput(text);
  if (json) return { kind: 'json', text: json };
  const payload = candidatePayload(text);
  const body = payload.body.trimEnd();
  if (isDiffOutput(text)) return { kind: 'diff', text: body };
  if (isLogOutput(text)) return { kind: 'log', text: body };
  if (isConfigOutput(text)) return { kind: 'config', text: body };
  return { kind: 'markdown', text: normalizeMessageText(text) };
}

function diffLineClass(line) {
  if (line.startsWith('@@')) return 'diff-line diff-line--hunk';
  if (line.startsWith('+++') || line.startsWith('---') || line.startsWith('diff --git') || line.startsWith('index ')) return 'diff-line diff-line--meta';
  if (line.startsWith('+')) return 'diff-line diff-line--added';
  if (line.startsWith('-')) return 'diff-line diff-line--deleted';
  return 'diff-line';
}

function StructuredMessage({ kind, text }) {
  const outputText = normalizeMessageText(text);
  if (kind === 'diff') {
    return (
      <div className={`message-output message-output--${kind}`} data-output-kind={kind}>
        <pre>
          <code>
            {outputText.split('\n').map((line, index) => (
              <span key={`${kind}-${index}`} className={diffLineClass(line)}>{line || ' '}</span>
            ))}
          </code>
        </pre>
      </div>
    );
  }
  return (
    <div className={`message-output message-output--${kind}`} data-output-kind={kind}>
      <pre><code>{outputText}</code></pre>
    </div>
  );
}

function MarkdownMessage({ text }) {
  const lines = normalizeMessageText(text).split('\n');
  const nodes = [];
  let index = 0;
  const nextKey = (kind) => `${kind}-${nodes.length}`;

  while (index < lines.length) {
    const line = lines[index];
    const trimmed = line.trim();
    if (!trimmed) {
      index += 1;
      continue;
    }

    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) {
      nodes.push(<hr key={nextKey('separator')} />);
      index += 1;
      continue;
    }

    if (trimmed.startsWith('```')) {
      const key = nextKey('code');
      const codeLines = [];
      index += 1;
      while (index < lines.length && !lines[index].trim().startsWith('```')) {
        codeLines.push(lines[index]);
        index += 1;
      }
      if (index < lines.length) index += 1;
      nodes.push(<pre key={key}><code>{codeLines.join('\n')}</code></pre>);
      continue;
    }

    const heading = trimmed.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      const level = Math.min(6, heading[1].length);
      const HeadingTag = `h${level}`;
      nodes.push(<HeadingTag key={nextKey('heading')}>{renderInlineMarkdown(heading[2], `heading-${nodes.length}`)}</HeadingTag>);
      index += 1;
      continue;
    }

    if (index + 1 < lines.length && trimmed.includes('|') && isMarkdownTableDivider(lines[index + 1])) {
      const key = nextKey('table');
      const headers = markdownTableCells(line);
      index += 2;
      const rows = [];
      while (index < lines.length && lines[index].trim().includes('|')) {
        rows.push(markdownTableCells(lines[index]));
        index += 1;
      }
      nodes.push(
        <table key={key}>
          <thead>
            <tr>{headers.map((cell, cellIndex) => <th key={`${key}-h-${cellIndex}`}>{renderInlineMarkdown(cell, `${key}-h-${cellIndex}`)}</th>)}</tr>
          </thead>
          <tbody>
            {rows.map((row, rowIndex) => (
              <tr key={`${key}-r-${rowIndex}`}>
                {headers.map((_, cellIndex) => <td key={`${key}-r-${rowIndex}-${cellIndex}`}>{renderInlineMarkdown(row[cellIndex] || '', `${key}-r-${rowIndex}-${cellIndex}`)}</td>)}
              </tr>
            ))}
          </tbody>
        </table>,
      );
      continue;
    }

    if (trimmed.startsWith('>')) {
      const key = nextKey('quote');
      const quoteLines = [];
      while (index < lines.length && lines[index].trim().startsWith('>')) {
        quoteLines.push(lines[index].trim().replace(/^>\s?/, ''));
        index += 1;
      }
      nodes.push(<blockquote key={key}>{renderMarkdownParagraph(quoteLines, `${key}-p`)}</blockquote>);
      continue;
    }

    const unordered = trimmed.match(/^[-*]\s+(.+)$/);
    const ordered = trimmed.match(/^\d+\.\s+(.+)$/);
    const task = trimmed.match(/^[-*]\s+\[([ xX])]\s+(.+)$/);
    if (task) {
      const key = nextKey('task-list');
      const items = [];
      while (index < lines.length) {
        const itemMatch = lines[index].trim().match(/^[-*]\s+\[([ xX])]\s+(.+)$/);
        if (!itemMatch) break;
        items.push({ checked: itemMatch[1].toLowerCase() === 'x', text: itemMatch[2] });
        index += 1;
      }
      nodes.push(
        <ul key={key} className="task-list">
          {items.map((item, itemIndex) => (
            <li key={`${key}-${itemIndex}`}>
              <input type="checkbox" checked={item.checked} disabled readOnly />
              <span>{renderInlineMarkdown(item.text, `${key}-${itemIndex}`)}</span>
            </li>
          ))}
        </ul>,
      );
      continue;
    }
    if (unordered || ordered) {
      const key = nextKey('list');
      const ListTag = ordered ? 'ol' : 'ul';
      const items = [];
      while (index < lines.length) {
        const itemMatch = lines[index].trim().match(ordered ? /^\d+\.\s+(.+)$/ : /^[-*]\s+(.+)$/);
        if (!itemMatch) break;
        items.push(itemMatch[1]);
        index += 1;
      }
      nodes.push(
        <ListTag key={key}>
          {items.map((item, itemIndex) => <li key={`${key}-${itemIndex}`}>{renderInlineMarkdown(item, `${key}-${itemIndex}`)}</li>)}
        </ListTag>,
      );
      continue;
    }

    const paragraph = [line];
    index += 1;
    while (index < lines.length) {
      const next = lines[index];
      const nextTrimmed = next.trim();
      if (!nextTrimmed) break;
      if (
        nextTrimmed.startsWith('```') ||
        nextTrimmed.startsWith('>') ||
        nextTrimmed.match(/^(-{3,}|\*{3,}|_{3,})$/) ||
        nextTrimmed.match(/^(#{1,6})\s+(.+)$/) ||
        nextTrimmed.match(/^[-*]\s+(.+)$/) ||
        nextTrimmed.match(/^\d+\.\s+(.+)$/) ||
        (index + 1 < lines.length && nextTrimmed.includes('|') && isMarkdownTableDivider(lines[index + 1]))
      ) break;
      paragraph.push(next);
      index += 1;
    }
    nodes.push(renderMarkdownParagraph(paragraph, nextKey('paragraph')));
  }

  return <div className="message-markdown">{nodes.length > 0 ? nodes : <p />}</div>;
}

function MessageContent({ text }) {
  const output = detectMessageOutput(text);
  if (output.kind === 'markdown') return <MarkdownMessage text={output.text} />;
  return <StructuredMessage kind={output.kind} text={output.text} />;
}

function ComposerDock({
  floating = false,
  draft,
  setDraft,
  sendMessage,
  attachments,
  selectFiles,
  attachPaths,
  attachDroppedFiles,
  attachPastedImages,
  removeAttachment,
  sending,
  store,
  permission,
  setPermission,
  modelThreadId,
  showProviderToggle = true,
  canUseProjectActions = true,
}) {
  const composerClass = `composer ${floating ? 'composer--floating' : 'composer--docked'}`;
  const [previewAttachment, setPreviewAttachment] = useState(null);
  const [dropActive, setDropActive] = useState(false);
  const isComposingRef = useRef(false);
  const activePreview = previewAttachment && attachments.some((item) => attachmentKey(item) === attachmentKey(previewAttachment))
    ? previewAttachment
    : null;
  const hasComposerInput = Boolean(textValue(draft) || attachments.length > 0);
  const canSend = canUseProjectActions && !sending && hasComposerInput;
  const projectActionBlocked = !canUseProjectActions;
  const projectActionBlockedTitle = '请先连接后端并选择项目';

  useEffect(() => {
    if (typeof attachPaths !== 'function') return undefined;
    return onFilesDropped((event) => {
      if (!canUseProjectActions) return;
      const payload = event && typeof event === 'object' ? event : {};
      const files = Array.isArray(payload.files) ? payload.files : [];
      if (files.length === 0) return;
      const details = payload.details && typeof payload.details === 'object' ? payload.details : {};
      const targetId = textValue(details.id);
      if (targetId && !COMPOSER_DROP_TARGET_IDS.has(targetId)) return;
      attachPaths(files);
      setDropActive(false);
    });
  }, [attachPaths, canUseProjectActions]);

  const preview = (item) => {
    setPreviewAttachment(item);
  };
  const remove = (item) => {
    removeAttachment(attachmentKey(item));
    if (activePreview && attachmentKey(activePreview) === attachmentKey(item)) {
      setPreviewAttachment(null);
    }
  };
  const handlePaste = async (event) => {
    const images = extractClipboardImageFiles(event);
    if (images.length === 0) return;
    event.preventDefault();
    if (projectActionBlocked) return;
    await attachPastedImages(images);
  };
  const handleDragEnter = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    if (projectActionBlocked) return;
    setDropActive(true);
  };
  const handleDragOver = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    if (projectActionBlocked) return;
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    setDropActive(true);
  };
  const handleDragLeave = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    setDropActive(false);
  };
  const handleDrop = async (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    setDropActive(false);
    if (projectActionBlocked) return;
    const files = collectTransferFiles(event);
    if (files.length > 0) await attachDroppedFiles(files);
  };
  const handleKeyDown = (event) => {
    if (event.key !== 'Enter' || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return;
    const keyCode = Number(event.keyCode || event.which || 0);
    const imeLikely = event.isComposing || isComposingRef.current || keyCode === 229 || event.key === 'Process' || event.key === 'Unidentified';
    if (imeLikely) return;
    event.preventDefault();
    if (!canSend) return;
    void sendMessage();
  };

  return (
    <footer
      id="chat-input-bar"
      className={`${composerClass}${dropActive ? ' drop-active' : ''}`}
      data-testid="composer-dock"
      data-file-drop-target=""
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <div className="composer-card">
        {dropActive ? <div className="composer-drop-hint" aria-live="polite">松开即可添加附件</div> : null}
        {attachments.length > 0 ? (
          <div className="attachments">
            {attachments.map((item) => (
              <span key={attachmentKey(item)} className={`attachment-pill${item.kind === 'image' ? ' attachment-pill--image' : ''}`}>
                <button type="button" className="attachment-preview" aria-label={`预览附件 ${item.name || item.path}`} onClick={() => preview(item)}>
                  {item.kind === 'image' && item.previewUrl ? <img src={item.previewUrl} alt="" /> : <File size={14} />}
                  <span>{item.name || item.path}</span>
                </button>
                <button type="button" className="attachment-remove" aria-label={`移除附件 ${item.name || item.path}`} onClick={() => remove(item)}>
                  <X size={12} />
                </button>
              </span>
            ))}
          </div>
        ) : null}
        <textarea
          id="composer-input"
          data-testid="composer-input"
          data-file-drop-target=""
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onPaste={(event) => { void handlePaste(event); }}
          onCompositionStart={() => { isComposingRef.current = true; }}
          onCompositionEnd={() => { isComposingRef.current = false; }}
          onKeyDown={handleKeyDown}
          placeholder="输入给 Agent 的内容，Enter 发送，Shift+Enter 换行"
        />
        <div className="composer-meta">
          <button
            type="button"
            className="composer-attach"
            aria-label="添加文件"
            title={projectActionBlocked ? projectActionBlockedTitle : '添加文件'}
            disabled={projectActionBlocked}
            onClick={() => {
              if (!projectActionBlocked) void selectFiles();
            }}
          >
            <Plus size={18} />
          </button>
          <label className="permission-chip">
            <span className="sr-only">发送权限</span>
            <select
              aria-label="发送权限"
              value={permission}
              disabled={projectActionBlocked}
              title={projectActionBlocked ? projectActionBlockedTitle : undefined}
              onChange={(event) => setPermission(event.target.value)}
            >
              <option>完全访问权限</option>
              <option>工作区写入</option>
              <option>只读模式</option>
            </select>
          </label>
          <div className="composer-actions">
            {showProviderToggle ? <ProviderToggle store={store} canUseProjectActions={canUseProjectActions} /> : null}
            <ModelSelector store={store} activeThreadId={modelThreadId} disabled={projectActionBlocked} />
            <button
              type="button"
              className="send"
              aria-label="发送消息"
              disabled={!canSend}
              onClick={() => {
                if (canSend) void sendMessage();
              }}
            >
              <Send size={18} />
            </button>
          </div>
        </div>
      </div>
      {activePreview ? (
        <AttachmentPreviewModal
          attachment={activePreview}
          onClose={() => setPreviewAttachment(null)}
          onRemove={() => remove(activePreview)}
        />
      ) : null}
    </footer>
  );
}

function Conversation({
  messages,
  draft,
  setDraft,
  sendMessage,
  attachments,
  selectFiles,
  attachPaths,
  attachDroppedFiles,
  attachPastedImages,
  removeAttachment,
  sending,
  store,
  projectPath,
  permission,
  setPermission,
  tokenUsage,
  activeThreadId,
  activeThread,
  statusEntry,
  modelThreadId,
  timelineBlocked,
  canUseProjectActions = true,
}) {
  const introMode = !activeThreadId && !timelineBlocked && messages.length === 0;
  const showProviderToggle = !hasAssistantReply(messages);
  const composer = (
    <ComposerDock
      floating={introMode}
      draft={draft}
      setDraft={setDraft}
      sendMessage={sendMessage}
      attachments={attachments}
      selectFiles={selectFiles}
      attachPaths={attachPaths}
      attachDroppedFiles={attachDroppedFiles}
      attachPastedImages={attachPastedImages}
      removeAttachment={removeAttachment}
      sending={sending}
      store={store}
      permission={permission}
      setPermission={setPermission}
      modelThreadId={modelThreadId}
      showProviderToggle={showProviderToggle}
      canUseProjectActions={canUseProjectActions}
    />
  );
  return (
    <section className={`conversation${introMode ? ' conversation--intro' : ''}`}>
      <div className="timeline" data-testid="chat-timeline">
        {introMode ? (
          <div className="intro-chat-stage">
            <div className="empty-chat">
              <h2>我们应该在 {projectDisplayName(projectPath)} 中构建什么？</h2>
              <p>{projectPath}</p>
            </div>
            {composer}
          </div>
        ) : null}
        {!introMode && !timelineBlocked ? messages.map((message) => (
          <article key={message.id} className={`message ${message.role}`}>
            <div className="avatar">{message.role === 'user' ? 'U' : 'AI'}</div>
            <div className="bubble">
              <header><span>{message.role === 'user' ? '你' : 'AI'}</span><time>{formatTime(message.time)}</time></header>
              <MessageContent text={message.text} />
            </div>
          </article>
        )) : null}
      </div>
      {!introMode ? (
        <WorkStatus
          sending={sending}
          activeThreadId={activeThreadId}
          activeThread={activeThread}
          statusEntry={statusEntry}
          tokenUsage={tokenUsage}
        />
      ) : null}
      {!introMode ? composer : null}
    </section>
  );
}

function WorkStatus({ sending, activeThreadId, activeThread, statusEntry, tokenUsage }) {
  const status = workStatusForThread({ sending, activeThreadId, activeThread, statusEntry });
  const className = `work-status work-status--${status.tone}${status.busy ? ' is-busy' : ''}`;
  const tokenText = tokenUsage ? `${tokenUsage.usedTokens} / ${tokenUsage.contextWindowTokens} tokens` : 'token usage 等待后端同步';
  return (
    <div className={className}>
      <span className="spinner" />
      <span className="work-status-label">{status.label}</span>
      <em>{status.details}</em>
      <code title={tokenText}>{tokenText}</code>
    </div>
  );
}

function RuntimePanel({ diffText, tokenUsage, activityStats, warnings, runtimeResults }) {
  const [viewportHeight, setViewportHeight] = useState(currentViewportHeight);
  const [activityPanelHeight, setActivityPanelHeight] = useState(() => clampActivityPanelHeight(ACTIVITY_PANEL_DEFAULT_HEIGHT));
  const [collapsedDiffFiles, setCollapsedDiffFiles] = useState(() => new Set());
  const diffSummary = useMemo(() => summarizeUnifiedDiff(diffText), [diffText]);
  const activityPanelMax = activityPanelMaxHeight(viewportHeight);

  useEffect(() => {
    const onResize = () => {
      const nextHeight = currentViewportHeight();
      setViewportHeight(nextHeight);
      setActivityPanelHeight((height) => clampActivityPanelHeight(height, nextHeight));
    };
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);

  const beginActivityPanelResize = (event, inputType = 'pointer') => {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = activityPanelHeight;
    const moveEventName = inputType === 'mouse' ? 'mousemove' : 'pointermove';
    const stopEventName = inputType === 'mouse' ? 'mouseup' : 'pointerup';
    const move = (moveEvent) => {
      setActivityPanelHeight(clampActivityPanelHeight(startHeight + (startY - moveEvent.clientY), viewportHeight));
    };
    const stop = () => {
      window.removeEventListener(moveEventName, move);
      window.removeEventListener(stopEventName, stop);
    };
    window.addEventListener(moveEventName, move);
    window.addEventListener(stopEventName, stop);
  };

  const handleActivityPanelResizeKeyDown = (event) => {
    if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    let nextHeight = null;
    if (event.key === 'ArrowUp' || event.key === 'PageUp') nextHeight = activityPanelHeight + RESIZER_KEY_STEP;
    else if (event.key === 'ArrowDown' || event.key === 'PageDown') nextHeight = activityPanelHeight - RESIZER_KEY_STEP;
    else if (event.key === 'Home') nextHeight = ACTIVITY_PANEL_MIN_HEIGHT;
    else if (event.key === 'End') nextHeight = activityPanelMax;
    if (nextHeight === null) return;
    event.preventDefault();
    setActivityPanelHeight(clampActivityPanelHeight(nextHeight, viewportHeight));
  };

  const toggleDiffFile = (filename) => {
    setCollapsedDiffFiles((current) => {
      const next = new Set(current);
      if (next.has(filename)) next.delete(filename);
      else next.add(filename);
      return next;
    });
  };

  return (
    <aside
      className="runtime-panel"
      data-testid="runtime-panel"
      style={runtimePanelHeightVars(activityPanelHeight, viewportHeight)}
    >
      <div className="runtime-toolbar">
        <button type="button" aria-label="代码变更文件数" title={`代码变更文件数: ${diffSummary.fileCount}`}>
          <FileText size={14} /> {diffSummary.fileCount}
        </button>
        <button type="button" aria-label="代码变更行数" title={`代码变更行数: ${diffSummary.changedLines}`}>
          <Code2 size={14} /> {diffSummary.changedLines}
        </button>
        <span className="score good" aria-label="代码新增行数" title={`代码新增行数: ${diffSummary.additions}`}>+{diffSummary.additions}</span>
        <span className="score bad" aria-label="代码删除行数" title={`代码删除行数: ${diffSummary.deletions}`}>-{diffSummary.deletions}</span>
      </div>
      <div className="diff-empty">
        {diffText ? (
          <div className="diff-view" data-testid="diff-view">
            {diffSummary.files.map((file, index) => {
              const groupKey = `${file.filename}:${index}`;
              const collapsed = collapsedDiffFiles.has(groupKey);
              return (
                <section className={`diff-file-group${collapsed ? ' is-collapsed' : ''}`} key={groupKey}>
                  <div className="diff-file-header">
                    <button
                      type="button"
                      className="diff-file-toggle"
                      aria-expanded={!collapsed}
                      aria-controls={`runtime-diff-file-${index}`}
                      onClick={() => toggleDiffFile(groupKey)}
                    >
                      <span className="diff-file-title">
                        <ChevronDown className="diff-file-caret" size={14} aria-hidden="true" />
                        <span className="diff-file-name">{file.filename}</span>
                      </span>
                      <span className="diff-file-stats" aria-hidden="true">
                        <b className="good">+{file.additions}</b>
                        <b className="bad">-{file.deletions}</b>
                      </span>
                    </button>
                  </div>
                  {!collapsed ? (
                    <div className="diff-file-lines" id={`runtime-diff-file-${index}`}>
                      {parseUnifiedDiffLineEntries(file.text).map((line) => (
                        <div className={`diff-line ${line.type}`} key={line.key}>
                          <span className="diff-line-num diff-line-old">{line.oldNo}</span>
                          <span className="diff-line-num diff-line-new">{line.newNo}</span>
                          <span className="diff-line-prefix">{line.prefix}</span>
                          <span className="diff-line-content">{line.content}</span>
                        </div>
                      ))}
                    </div>
                  ) : null}
                </section>
              );
            })}
          </div>
        ) : '暂无代码变更'}
      </div>
      <RuntimeActivityPanel
        activityStats={activityStats}
        tokenUsage={tokenUsage}
        warnings={warnings}
        runtimeResults={runtimeResults}
        activityPanelHeight={activityPanelHeight}
        activityPanelMaxHeight={activityPanelMax}
        activityPanelMinHeight={ACTIVITY_PANEL_MIN_HEIGHT}
        onResizeKeyDown={handleActivityPanelResizeKeyDown}
        onResizeStart={beginActivityPanelResize}
      />
    </aside>
  );
}

function RuntimeActivityPanel({
  activityStats,
  tokenUsage,
  warnings,
  runtimeResults,
  activityPanelHeight,
  activityPanelMaxHeight,
  activityPanelMinHeight,
  onResizeKeyDown,
  onResizeStart,
}) {
  const [hoveredStat, setHoveredStat] = useState(null);
  const [hoveredWarning, setHoveredWarning] = useState(null);
  const panelRef = useRef(null);
  const stats = useMemo(() => activityStats || {}, [activityStats]);
  const statItems = useMemo(() => activityStatItems(stats), [stats]);
  const detailEntriesByStat = useMemo(() => Object.fromEntries(
    statItems.map((item) => [item.key, activityStatDetailEntries(stats, item.key)]),
  ), [statItems, stats]);
  const logEntries = useMemo(() => runtimeLogEntries(warnings, runtimeResults), [warnings, runtimeResults]);
  const hoveredWarningEntry = useMemo(
    () => logEntries.find((entry) => entry.id === hoveredWarning?.id) || null,
    [hoveredWarning, logEntries],
  );
  const showStatTooltip = (key, element) => setHoveredStat({ key, anchorRect: elementViewportRect(element) });
  const hideStatTooltip = (key) => setHoveredStat((current) => (current?.key === key ? null : current));
  const showWarningPopover = (id, element) => setHoveredWarning({
    id,
    anchorRect: elementViewportRect(element),
    panelRect: elementViewportRect(panelRef.current),
  });
  const hideWarningPopover = (id) => setHoveredWarning((current) => (current?.id === id ? null : current));

  return (
    <section className="runtime-activity-panel" aria-label="工具使用面板" ref={panelRef}>
      <div
        role="separator"
        className="activity-panel-resizer"
        aria-orientation="horizontal"
        aria-label="调整工具使用面板高度"
        aria-valuemin={activityPanelMinHeight}
        aria-valuemax={activityPanelMaxHeight}
        aria-valuenow={activityPanelHeight}
        title="拖动调整工具使用面板高度，最大为应用高度的 1/2"
        data-testid="activity-panel-resizer"
        tabIndex={0}
        onKeyDown={onResizeKeyDown}
        onPointerDown={(event) => onResizeStart(event, 'pointer')}
        onMouseDown={(event) => {
          if (!window.PointerEvent) onResizeStart(event, 'mouse');
        }}
      />
      <div className="runtime-icons" role="list" aria-label="工具调用统计">
        {statItems.map(({ key, label, icon: Icon, className, value }) => {
          const detailEntries = detailEntriesByStat[key] || [];
          return (
            <span
              key={key}
              className={`runtime-stat ${className}`}
              role="listitem"
              tabIndex={0}
              aria-label={key === 'tool' ? '工具调用总数' : `${label} 调用次数`}
              title={`${label}: ${value}`}
              onMouseEnter={(event) => showStatTooltip(key, event.currentTarget)}
              onMouseLeave={() => hideStatTooltip(key)}
              onFocus={(event) => showStatTooltip(key, event.currentTarget)}
              onBlur={() => hideStatTooltip(key)}
            >
              <Icon size={16} aria-hidden="true" />
              <strong>{value}</strong>
              {hoveredStat?.key === key ? (
                <span
                  className="runtime-stat-tooltip"
                  data-testid="runtime-stat-tooltip"
                  role="tooltip"
                  style={runtimeStatTooltipStyle(hoveredStat.anchorRect)}
                >
                  <span className="runtime-stat-tooltip-title">
                    <b>{label}</b>
                    <strong>{value}</strong>
                  </span>
                  {detailEntries.length > 0 ? (
                    <span className="runtime-stat-tooltip-list">
                      {detailEntries.map((entry) => (
                        <span key={entry.name}>
                          <span>{entry.name}</span>
                          <strong>{entry.count}</strong>
                        </span>
                      ))}
                    </span>
                  ) : (
                    <span className="runtime-stat-tooltip-empty">后端暂无明细</span>
                  )}
                </span>
              ) : null}
            </span>
          );
        })}
        <span className="runtime-context" title={tokenUsage ? `上下文使用率 ${tokenUsage.usedPercent.toFixed(1)}%` : '等待后端同步上下文使用率'}>
          {tokenUsage ? `${tokenUsage.usedPercent.toFixed(1)}% context` : 'context --'}
        </span>
      </div>
      <div className="log-lines" data-testid="warning-log-panel">
        {logEntries.length === 0 ? <p><time>--:--</time> runtime log 等待事件</p> : null}
        {logEntries.map((entry) => (
          <p
            key={entry.id}
            className={`warning-log-line runtime-log-line--${entry.runtimeKind || 'warning'}`}
            tabIndex={0}
            onMouseEnter={(event) => showWarningPopover(entry.id, event.currentTarget)}
            onMouseLeave={() => hideWarningPopover(entry.id)}
            onFocus={(event) => showWarningPopover(entry.id, event.currentTarget)}
            onBlur={() => hideWarningPopover(entry.id)}
          >
            <time>{formatTime(runtimeLogTimestamp(entry))}</time> <b>{runtimeLogLabel(entry)}</b>
            {Number(entry.occurrenceCount) > 1 ? <span> ×{Number(entry.occurrenceCount)}</span> : null}
          </p>
        ))}
      </div>
      {hoveredWarningEntry ? (
        <div
          className="warning-log-popover"
          data-testid="warning-log-popover"
          role="tooltip"
          style={warningLogPopoverStyle(hoveredWarning.anchorRect, hoveredWarning.panelRect)}
        >
          <span className="warning-log-popover-title">
            <time>{formatTime(runtimeLogTimestamp(hoveredWarningEntry))}</time>
            <b>{runtimeLogLabel(hoveredWarningEntry)}</b>
          </span>
          <code>{warningDetailText(hoveredWarningEntry)}</code>
        </div>
      ) : null}
    </section>
  );
}

function formatTime(value) {
  if (!value) return '--:--';
  const text = value.toString().trim();
  // 截断高精度时间戳中的多余小数秒，以兼容 JS new Date() 的 3 位毫秒限制
  const sanitized = text.replace(/(\.\d{3})\d+/g, '$1');
  const date = new Date(sanitized);
  if (!Number.isFinite(date.getTime())) return '--:--';
  return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
}

function PageHeader({ icon: Icon, title, subtitle, actions }) {
  return (
    <header className="page-header">
      <h1><Icon size={25} /> {title}</h1>
      {subtitle ? <p>{subtitle}</p> : null}
      {actions ? <div className="page-actions">{actions}</div> : null}
    </header>
  );
}

function RetryableSyncError({ className = 'danger-text', message, onRetry }) {
  if (!message) return null;
  return (
    <div className={className} role="alert">
      <span>{message}</span>
      <button type="button" className="ghost" onClick={() => { void onRetry(); }}>重试同步</button>
    </div>
  );
}

function PromptPage({ projectPath, store, refreshKey = 0 }) {
  return <PromptPageView projectPath={projectPath} refreshKey={refreshKey} resolveLaunchPreferences={store?.resolveLaunchPreferences} />;
}


function WorkflowPage({ projectPath, store, refreshKey = 0 }) {
  const workflowCwd = optionalSettingsCwd(projectPath);
  const isProjectPending = !workflowCwd;
  const queryClient = useQueryClient();
  useDashboardFocusInvalidation(workflowCwd, 'dags');
  const [activeCategory, setActiveCategory] = useState(DAG_CATEGORIES[0].key);
  const [categoryManuallySelected, setCategoryManuallySelected] = useState(false);
  const [selectedDagKey, setSelectedDagKey] = useState('');
  const [selectedRunKey, setSelectedRunKey] = useState('');
  const [actioning, setActioning] = useState('');
  const [savingNodeKey, setSavingNodeKey] = useState('');
  const [error, setError] = useState('');
  const [workflowSyncFailure, setWorkflowSyncFailure] = useState('');
  const [notice, setNotice] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [scheduleCron, setScheduleCron] = useState('0 8 * * *');
  const selectedDagKeyRef = useRef(selectedDagKey);
  const handledWorkflowRefreshRef = useRef(0);
  const workflowRefreshKey = Number(refreshKey || 0);

  const dagsQuery = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dags'),
    queryFn: () => fetchDagsDashboard(workflowCwd),
    enabled: Boolean(workflowCwd),
  });
  const hasDagsSnapshot = queryHasSnapshot(dagsQuery);
  const items = useMemo(() => (Array.isArray(dagsQuery.data) ? dagsQuery.data : []), [dagsQuery.data]);
  const loading = Boolean(workflowCwd) && dagsQuery.isPending && !hasDagsSnapshot;
  const dagListErrorState = dashboardQueryErrorState(dagsQuery, hasDagsSnapshot);

  const refreshDags = useCallback(async () => {
    if (!workflowCwd) return [];
    const key = dashboardQueryKey(workflowCwd, 'dags');
    try {
      await queryClient.invalidateQueries({ queryKey: key }, { throwOnError: true });
      setWorkflowSyncFailure('');
    } catch (err) {
      setWorkflowSyncFailure(`同步失败，显示的是上次成功的数据：${errorMessage(err)}`);
    }
    return queryClient.getQueryData(key) || [];
  }, [queryClient, workflowCwd]);

  const counts = useMemo(() => categoryCounts(items), [items]);
  const preferredCategory = useMemo(() => firstAvailableCategory(items), [items]);
  const visibleItems = useMemo(
    () => items.filter((item) => dagCategoryOf(item) === activeCategory),
    [activeCategory, items],
  );
  const selectedDag = useMemo(
    () => items.find((item) => item.dagKey === selectedDagKey) || visibleItems[0] || null,
    [items, selectedDagKey, visibleItems],
  );

  useEffect(() => {
    selectedDagKeyRef.current = selectedDagKey;
    setNotice(null);
  }, [selectedDagKey]);

  const clearNotice = useCallback(() => {
    setNotice(null);
  }, []);

  const showTaskNotice = useCallback((message, taskKey = selectedDagKey) => {
    const key = textValue(taskKey);
    if (!message || !key || selectedDagKeyRef.current !== key) return;
    setNotice({ dagKey: key, message });
  }, [selectedDagKey]);

  useEffect(() => {
    if (!categoryManuallySelected && items.length > 0 && visibleItems.length === 0 && activeCategory !== preferredCategory) {
      setActiveCategory(preferredCategory);
    }
  }, [activeCategory, categoryManuallySelected, items.length, preferredCategory, visibleItems.length]);

  useEffect(() => {
    if (visibleItems.length === 0) {
      setSelectedDagKey('');
      return;
    }
    if (!visibleItems.some((item) => item.dagKey === selectedDagKey)) {
      setSelectedDagKey(visibleItems[0].dagKey);
    }
  }, [selectedDagKey, visibleItems]);

  const fetchRunDetail = useCallback(async (runKey) => {
    const key = textValue(runKey);
    if (!key) return null;
    const response = await getDagRun({ runKey: key });
    const run = response?.run ? normalizeDagRun(response.run) : null;
    const runNodes = Array.isArray(response?.nodes) ? response.nodes.map((node, index) => normalizeDagNode(node, index)) : [];
    return { run, nodes: runNodes };
  }, []);

  const dagDetailQuery = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dag-detail', selectedDagKey),
    queryFn: async () => {
      const key = textValue(selectedDagKey);
      const [detailResponse, runsResponse, activeResponse] = await Promise.all([
        getDagDetail({ dagKey: key }),
        getDagRuns({ dagKey: key, limit: DAG_RECENT_RUN_LIMIT }),
        getDagRuns({ dagKey: key, status: 'running', limit: 1 }),
      ]);
      const listDag = items.find((item) => item.dagKey === key);
      const dag = normalizeDashboardDag({ ...objectValue(listDag?.raw), ...objectValue(detailResponse?.dag) });
      const normalizedNodes = Array.isArray(detailResponse?.nodes)
        ? detailResponse.nodes.map((node, index) => normalizeDagNode(node, index))
        : [];
      const nextRuns = Array.isArray(runsResponse?.runs)
        ? runsResponse.runs.map((run, index) => normalizeDagRun(run, index))
        : [];
      const runningRun = Array.isArray(activeResponse?.runs) && activeResponse.runs.length > 0
        ? normalizeDagRun(activeResponse.runs[0])
        : null;
      return { dag, nodes: normalizedNodes, runs: nextRuns, activeRun: runningRun };
    },
    enabled: Boolean(workflowCwd && selectedDagKey),
  });
  const hasDetailSnapshot = queryHasSnapshot(dagDetailQuery);
  const detailLoading = Boolean(selectedDagKey) && dagDetailQuery.isPending && !hasDetailSnapshot && !selectedDag;
  const detailData = dagDetailQuery.data || {};
  const detailDag = detailData.dag || null;
  const nodes = useMemo(() => (Array.isArray(detailData.nodes) ? detailData.nodes : []), [detailData.nodes]);
  const runs = useMemo(() => (Array.isArray(detailData.runs) ? detailData.runs : []), [detailData.runs]);
  const activeRun = detailData.activeRun || null;
  const detailErrorState = dashboardQueryErrorState(dagDetailQuery, hasDetailSnapshot);

  useEffect(() => {
    const nextRunKey = activeRun?.runKey || runs[0]?.runKey || '';
    if (!nextRunKey) {
      if (selectedRunKey) setSelectedRunKey('');
      return;
    }
    if (!selectedRunKey || !runs.some((run) => run.runKey === selectedRunKey)) {
      setSelectedRunKey(nextRunKey);
    }
  }, [activeRun, runs, selectedRunKey]);

  const runDetailQuery = useQuery({
    queryKey: dashboardQueryKey(workflowCwd, 'dag-run', selectedRunKey),
    queryFn: () => fetchRunDetail(selectedRunKey),
    enabled: Boolean(workflowCwd && selectedRunKey),
  });
  const selectedRun = runDetailQuery.data || null;

  const loadRunDetail = useCallback(async (runKey) => {
    const key = textValue(runKey);
    if (!key) {
      setSelectedRunKey('');
      return null;
    }
    setSelectedRunKey(key);
    return queryClient.fetchQuery({
      queryKey: dashboardQueryKey(workflowCwd, 'dag-run', key),
      queryFn: () => fetchRunDetail(key),
    });
  }, [fetchRunDetail, queryClient, workflowCwd]);

  const refreshDetail = useCallback(async (dagKey, preferredRunKey = '') => {
    const key = textValue(dagKey);
    if (!key) return;
    if (preferredRunKey) setSelectedRunKey(textValue(preferredRunKey));
    await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(workflowCwd, 'dag-detail', key) });
    await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(workflowCwd, 'dag-run') });
  }, [queryClient, workflowCwd]);

  const refreshWorkflowSurface = useCallback(async () => {
    if (!workflowCwd) return;
    await refreshDags();
    const activeKey = selectedDagKeyRef.current;
    if (activeKey) {
      await refreshDetail(activeKey, selectedRunKey);
    }
  }, [refreshDags, refreshDetail, selectedRunKey, workflowCwd]);

  useEffect(() => {
    if (workflowRefreshKey <= 0) return;
    if (handledWorkflowRefreshRef.current === workflowRefreshKey) return;
    handledWorkflowRefreshRef.current = workflowRefreshKey;
    void refreshWorkflowSurface();
  }, [refreshWorkflowSurface, workflowRefreshKey]);

  const syncError = workflowSyncFailure || dagListErrorState.cachedSyncError || detailErrorState.cachedSyncError;
  const blockingLoadError = dagListErrorState.blockingError
    ? `加载自动化失败：${dagListErrorState.blockingError}`
    : (detailErrorState.blockingError ? `加载自动化详情失败：${detailErrorState.blockingError}` : '');

  const activeDetailDag = detailDag || selectedDag;
  const dagKey = activeDetailDag?.dagKey || selectedDag?.dagKey || '';
  const baseVersion = dagVersionOf(activeDetailDag);
  const activeRunKey = activeRun?.runKey || (isRunningStatus(selectedRun?.run?.status) ? selectedRun?.run?.runKey : '');
  const finalText = finalOutputText(selectedRun?.run) || finalOutputText(activeRun) || finalOutputText(selectedDag?.latestRun);
  const recentRunPanelLabel = dagStatusLabel(activeRun?.status || runs[0]?.status || activeDetailDag?.latestRun?.status);
  const scheduleActionLabel = isScheduledTrigger(activeDetailDag?.trigger) || activeDetailDag?.cronExpr ? '修改计划' : '创建定时任务';
  const scheduleToggleVisible = isScheduledTrigger(activeDetailDag?.trigger) && Boolean(activeDetailDag?.cronExpr);
  const startDisabledReason = useMemo(() => {
    if (!dagKey) return '未选择自动化';
    if (loading || detailLoading) return '自动化详情加载中';
    if (activeRunKey) return '已有运行正在进行';
    if (!STARTABLE_DAG_STATUSES.has(textValue(activeDetailDag?.status).toLowerCase())) return '当前流程状态不可运行';
    if (!STARTABLE_DAG_TRIGGERS.has(textValue(activeDetailDag?.trigger).toLowerCase())) return '当前触发方式不可运行';
    return '';
  }, [activeDetailDag, activeRunKey, dagKey, detailLoading, loading]);

  const runSelectedDag = useCallback(async () => {
    if (startDisabledReason) return;
    const targetDagKey = dagKey;
    setActioning('start');
    setError('');
    clearNotice();
    try {
      const result = await startDag({
        dagKey: targetDagKey,
        triggerSource: 'manual',
        idempotencyKey: `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      });
      const runKey = runKeyOf(result);
      await refreshDags().catch(() => []);
      await refreshDetail(targetDagKey, runKey).catch(() => {});
      const warning = textValue(result?.warning);
      showTaskNotice(warning ? `已启动，后端提示：${warning}` : '已启动自动化', targetDagKey);
    } catch (err) {
      setError(`启动自动化失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [clearNotice, dagKey, refreshDags, refreshDetail, showTaskNotice, startDisabledReason]);

  const stopSelectedDag = useCallback(async () => {
    if (!dagKey || !activeRunKey) return;
    const targetDagKey = dagKey;
    setActioning('stop');
    setError('');
    clearNotice();
    try {
      await terminateDagRun({ dagKey: targetDagKey, runKey: activeRunKey, reason: 'user_requested' });
      await refreshDags().catch(() => []);
      await refreshDetail(targetDagKey).catch(() => {});
      showTaskNotice('已停止运行', targetDagKey);
    } catch (err) {
      setError(`停止运行失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [activeRunKey, clearNotice, dagKey, refreshDags, refreshDetail, showTaskNotice]);

  const confirmDeleteDAG = useCallback(async () => {
    const target = deleteTarget;
    const targetKey = target?.dagKey || dagKey;
    if (!targetKey) return;
    setActioning('delete');
    setError('');
    clearNotice();
    try {
      await deleteDag({ dagKey: targetKey });
      setDeleteTarget(null);
      const nextItems = await refreshDags().catch(() => items.filter((item) => item.dagKey !== targetKey));
      setSelectedDagKey(nextItems.find((item) => dagCategoryOf(item) === activeCategory)?.dagKey || nextItems[0]?.dagKey || '');
      showTaskNotice(`已删除 ${target?.title || targetKey}`, targetKey);
    } catch (err) {
      setError(`删除自动化失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [activeCategory, clearNotice, dagKey, deleteTarget, items, refreshDags, showTaskNotice]);

  const openScheduleModal = useCallback(() => {
    setScheduleCron(activeDetailDag?.cronExpr || '0 8 * * *');
    setScheduleOpen(true);
  }, [activeDetailDag]);

  const saveSchedule = useCallback(async (nextCronExpr = '') => {
    const cronExpr = textValue(nextCronExpr) || textValue(scheduleCron);
    if (!dagKey || !cronExpr) return;
    if (baseVersion === null) {
      setError('自动化详情不完整，无法保存定时任务');
      return;
    }
    const targetDagKey = dagKey;
    setActioning('schedule');
    setError('');
    clearNotice();
    try {
      await applyDagOps({
        dagKey: targetDagKey,
        baseVersion,
        ops: [{ op: 'update_dag', patch: { trigger: 'scheduled', cron_expr: cronExpr } }],
      });
      setScheduleOpen(false);
      await refreshDags().catch(() => []);
      await refreshDetail(targetDagKey).catch(() => {});
      showTaskNotice('已保存定时任务', targetDagKey);
    } catch (err) {
      setError(`保存定时任务失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [baseVersion, clearNotice, dagKey, refreshDags, refreshDetail, scheduleCron, showTaskNotice]);

  const toggleScheduleEnabled = useCallback(async () => {
    if (!dagKey) return;
    if (baseVersion === null) {
      setError('自动化详情不完整，无法切换自动运行');
      return;
    }
    const targetDagKey = dagKey;
    const enabled = !activeDetailDag?.scheduleEnabled;
    setActioning('schedule-toggle');
    setError('');
    clearNotice();
    try {
      await applyDagOps({
        dagKey: targetDagKey,
        baseVersion,
        ops: [{ op: 'update_dag', patch: { schedule_enabled: enabled } }],
      });
      await refreshDags().catch(() => []);
      await refreshDetail(targetDagKey).catch(() => {});
      showTaskNotice(enabled ? '已启用自动运行' : '已暂停自动运行', targetDagKey);
    } catch (err) {
      setError(`切换自动运行失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [activeDetailDag, baseVersion, clearNotice, dagKey, refreshDags, refreshDetail, showTaskNotice]);

  const saveAgentNode = useCallback(async (form, node) => {
    if (!dagKey || !node?.nodeKey) return;
    if (baseVersion === null) {
      setError('自动化详情不完整，无法保存步骤');
      return;
    }
    const targetDagKey = dagKey;
    setSavingNodeKey(node.nodeKey);
    setError('');
    clearNotice();
    try {
      await applyDagOps({
        dagKey: targetDagKey,
        baseVersion,
        ops: [{
          op: 'update_node',
          node_key: node.nodeKey,
          patch: dagNodePatchFromForm(form, node),
        }],
      });
      await refreshDetail(targetDagKey).catch(() => {});
      showTaskNotice(`已保存步骤 ${node.title}`, targetDagKey);
    } catch (err) {
      setError(`保存步骤失败：${err.message || String(err)}`);
    } finally {
      setSavingNodeKey('');
    }
  }, [baseVersion, clearNotice, dagKey, refreshDetail, showTaskNotice]);

  const startDesignFlow = useCallback(async () => {
    if (!workflowCwd) return;
    setActioning('design');
    setError('');
    clearNotice();
    try {
      if (typeof store?.resolveLaunchPreferences !== 'function') {
        throw new Error('自动化启动配置不可用');
      }
      const launchPreferences = await store.resolveLaunchPreferences(workflowCwd);
      const { config: launchConfig = {}, ...launchPayload } = launchPreferences || {};
      const response = await startThread({
        cwd: workflowCwd,
        ...launchPayload,
        name: 'AI 设计流程',
        agentKey: 'dag_designer',
        promptKey: 'main/dag_designer_zh',
        deferSpawn: true,
        config: {
          ...launchConfig,
          enabledTools: [...DAG_DESIGNER_ENABLED_TOOLS],
          providerNativeSkills: false,
        },
      });
      const threadId = threadIdFromStartResponse(response);
      if (threadId && typeof store?.setActiveThread === 'function') {
        await store.setActiveThread(threadId);
      }
      if (typeof store?.setActivePage === 'function') store.setActivePage('chat');
    } catch (err) {
      setError(`启动 AI 设计流程失败：${err.message || String(err)}`);
    } finally {
      setActioning('');
    }
  }, [clearNotice, store, workflowCwd]);

  const agentNodes = nodes.filter((node) => textValue(node.nodeType).toLowerCase() === 'agent');

  return (
    <section className="workflow-page">
      <PageHeader
        icon={Workflow}
        title="自动化"
        subtitle={workflowCwd ? `当前项目：${workflowCwd}` : '正在连接本地项目...'}
        actions={(
          <button type="button" onClick={() => { void startDesignFlow(); }} disabled={isProjectPending || actioning === 'design'}>
            {actioning === 'design' ? '启动中...' : 'AI 设计流程'}
          </button>
        )}
      />
      {syncError ? (
        <div className="danger-text workflow-sync-alert" role="alert">
          <span>{syncError}</span>
          <button type="button" className="ghost" onClick={() => { void refreshWorkflowSurface(); }}>重试同步</button>
        </div>
      ) : null}
      {error ? <p className="danger-text" role="alert">{error}</p> : null}
      <RetryableSyncError
        className="danger-text workflow-sync-alert"
        message={blockingLoadError}
        onRetry={refreshWorkflowSurface}
      />
      <div className="workflow-grid">
        <aside className="workflow-list">
          <div className="tabs" role="tablist" aria-label="自动化分类">
            {DAG_CATEGORIES.map((category) => (
              <button
                key={category.key}
                type="button"
                role="tab"
                aria-selected={activeCategory === category.key ? 'true' : 'false'}
                className={activeCategory === category.key ? 'active' : ''}
                onClick={() => {
                  setCategoryManuallySelected(true);
                  setActiveCategory(category.key);
                }}
              >
                {category.label} {counts[category.key] || 0}
              </button>
            ))}
          </div>
          {!isProjectPending && loading ? <p className="console-message">正在加载自动化...</p> : null}
          {!isProjectPending && !blockingLoadError && !loading && visibleItems.length === 0 ? <p className="console-message">无任务</p> : null}
          {visibleItems.map((item) => (
            <button
              type="button"
              key={item.id}
              className={item.dagKey === selectedDagKey ? 'active' : ''}
              onClick={() => setSelectedDagKey(item.dagKey)}
            >
              <strong>{item.title}</strong>
              <span>{latestDagRunLabel(item) === '-' ? '暂无运行' : `最近运行：${latestDagRunLabel(item)}`}</span>
              <em>{displayDagStatusLabel(item)} · {schedulePlanLabel(item)} · {latestDagRunLabel(item)}</em>
            </button>
          ))}
        </aside>
        <section className="workflow-detail">
          {!activeDetailDag ? (
            <EmptyState icon={Workflow} title="暂无自动化" text="左侧选择自动化后查看详情。" />
          ) : (
            <>
              <div className="detail-top">
                <h2>{activeDetailDag.title}</h2>
                <button type="button" className="danger" onClick={() => setDeleteTarget(activeDetailDag)} disabled={actioning === 'delete'}>删除</button>
                <button type="button" onClick={openScheduleModal} disabled={baseVersion === null || actioning === 'schedule'}>
                  {scheduleActionLabel}
                </button>
                {scheduleToggleVisible ? (
                  <button type="button" onClick={() => { void toggleScheduleEnabled(); }} disabled={baseVersion === null || actioning === 'schedule-toggle'}>
                    {activeDetailDag.scheduleEnabled ? '暂停自动运行' : '启用自动运行'}
                  </button>
                ) : null}
                {activeRunKey ? (
                  <button type="button" className="danger" onClick={() => { void stopSelectedDag(); }} disabled={actioning === 'stop'}>
                    {actioning === 'stop' ? '停止中...' : '停止运行'}
                  </button>
                ) : null}
                <button
                  type="button"
                  onClick={() => { void runSelectedDag(); }}
                  disabled={Boolean(startDisabledReason) || actioning === 'start'}
                  title={startDisabledReason}
                >
                  {actioning === 'start' ? '启动中...' : '运行'}
                </button>
              </div>
              {detailLoading ? <p className="console-message">正在加载详情...</p> : null}
              {notice?.message && notice.dagKey === selectedDagKey ? <p className="settings-status">{notice.message}</p> : null}
              {startDisabledReason ? <p className="console-message">{startDisabledReason}</p> : null}
              <Panel title="最终结果">{finalText || '当前运行尚未标记最终结果。'}</Panel>
              <div className="stat-grid">
                <Panel title="任务状态">{displayDagStatusLabel(activeDetailDag)}</Panel>
                <Panel title="运行计划">{schedulePlanLabel(activeDetailDag)}</Panel>
                <Panel title="最近运行">{recentRunPanelLabel === '-' ? latestDagRunLabel(activeDetailDag) : recentRunPanelLabel}</Panel>
                <Panel title="最终结果">{finalText ? '已生成' : '-'}</Panel>
              </div>
              <Panel title="运行历史">
                <div className="dag-run-list">
                  {runs.length === 0 ? <p>暂无运行记录</p> : null}
                  {runs.map((run, index) => (
                    <button
                      key={run.id}
                      type="button"
                      className={`run-row ${run.runKey === selectedRunKey ? 'active' : ''}`}
                      onClick={() => { void loadRunDetail(run.runKey); }}
                    >
                      <span>{`第 ${index + 1} 次运行`}</span>
                      <em>{dagStatusLabel(run.status)}</em>
                      <time>{run.startedAt || '时间未记录'}</time>
                    </button>
                  ))}
                </div>
              </Panel>
              <Panel title="执行步骤">
                <div className="dag-node-list">
                  {nodes.length === 0 ? <p>暂无步骤</p> : null}
                  {nodes.map((node) => (
                    <article key={node.id} className="dag-node-row">
                      <strong>{node.title}</strong>
                      <em>{dagStatusLabel(node.status)}</em>
                      {node.threadId ? <button type="button" onClick={() => { void store?.setActiveThread?.(node.threadId); store?.setActivePage?.('chat'); }}>查看对话</button> : null}
                    </article>
                  ))}
                </div>
              </Panel>
              <details className="workflow-advanced">
                <summary>高级设置</summary>
                {agentNodes.length > 0 ? (
                  <DagNodeEditor
                    nodes={agentNodes}
                    savingNodeKey={savingNodeKey}
                    onSave={saveAgentNode}
                  />
                ) : <p className="console-message">暂无可配置步骤</p>}
              </details>
            </>
          )}
        </section>
      </div>
      {scheduleOpen ? (
        <DagScheduleModal
          cron={scheduleCron}
          actionLabel={scheduleActionLabel}
          saving={actioning === 'schedule'}
          onClose={() => setScheduleOpen(false)}
          onSave={saveSchedule}
        />
      ) : null}
      {deleteTarget ? (
        <ConfirmDagDeleteModal
          dag={deleteTarget}
          deleting={actioning === 'delete'}
          onClose={() => setDeleteTarget(null)}
          onConfirm={confirmDeleteDAG}
        />
      ) : null}
    </section>
  );
}

function DagNodeEditor({ nodes, savingNodeKey, onSave }) {
  const [activeNodeKey, setActiveNodeKey] = useState(nodes[0]?.nodeKey || '');
  const activeNode = useMemo(
    () => nodes.find((node) => node.nodeKey === activeNodeKey) || nodes[0] || null,
    [activeNodeKey, nodes],
  );
  const [form, setForm] = useState(() => dagNodeFormFromNode(activeNode));
  const [formNodeKey, setFormNodeKey] = useState(activeNode?.nodeKey || '');

  useEffect(() => {
    if (!activeNode || nodes.some((node) => node.nodeKey === activeNodeKey)) return;
    setActiveNodeKey(nodes[0]?.nodeKey || '');
  }, [activeNode, activeNodeKey, nodes]);

  useEffect(() => {
    const nextNodeKey = activeNode?.nodeKey || '';
    if (nextNodeKey === formNodeKey) return;
    setFormNodeKey(nextNodeKey);
    setForm(dagNodeFormFromNode(activeNode));
  }, [activeNode, formNodeKey]);

  if (!activeNode) return null;
  const update = (key) => (event) => setForm((current) => ({ ...current, [key]: event.target.value }));
  const modelOptions = form.provider ? appendCurrentModelOption(form.provider, form.model) : [];

  return (
    <Panel title="步骤设置">
      <div className="dag-node-editor">
        <label>
          步骤
          <select value={activeNode.nodeKey} onChange={(event) => setActiveNodeKey(event.target.value)}>
            {nodes.map((node) => <option key={node.nodeKey} value={node.nodeKey}>{node.title}</option>)}
          </select>
        </label>
        <label>名称<input value={form.title} onChange={update('title')} aria-label="名称" /></label>
        <label>
          执行引擎
          <select value={form.provider} onChange={update('provider')} aria-label="执行引擎">
            <option value="">默认</option>
            <option value="claude">claude</option>
            <option value="codex">codex</option>
          </select>
        </label>
        <label>
          模型
          <select value={form.model} onChange={update('model')} aria-label="模型">
            <option value="">默认</option>
            {modelOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <label>提示词<input value={form.promptKey} onChange={update('promptKey')} aria-label="提示词" /></label>
        <label>依赖步骤<input value={form.dependsOn} onChange={update('dependsOn')} aria-label="依赖步骤" /></label>
        <label className="wide">指令<textarea value={form.firstTurn} onChange={update('firstTurn')} aria-label="指令" /></label>
        <label>输出文件<input value={form.outputFile} onChange={update('outputFile')} aria-label="输出文件" /></label>
        <div className="dag-node-editor-actions">
          <button type="button" onClick={() => { void onSave(form, activeNode); }} disabled={savingNodeKey === activeNode.nodeKey}>
            {savingNodeKey === activeNode.nodeKey ? '保存中...' : '保存步骤'}
          </button>
        </div>
      </div>
    </Panel>
  );
}

function DagScheduleModal({ cron, actionLabel, saving, onClose, onSave }) {
  const initialSchedule = useMemo(() => scheduleStateFromCron(cron), [cron]);
  const [preset, setPreset] = useState(initialSchedule.preset);
  const [time, setTime] = useState(initialSchedule.time);
  const [weekday, setWeekday] = useState(initialSchedule.weekday);
  const [monthDay, setMonthDay] = useState(initialSchedule.monthDay);
  const [inputError, setInputError] = useState(initialSchedule.warning || '');
  const monthDays = useMemo(() => Array.from({ length: 31 }, (_item, index) => (index + 1).toString()), []);
  const previewText = scheduleLabelFromState({ preset, time, weekday, monthDay });

  useEffect(() => {
    setPreset(initialSchedule.preset);
    setTime(initialSchedule.time);
    setWeekday(initialSchedule.weekday);
    setMonthDay(initialSchedule.monthDay);
    setInputError(initialSchedule.warning || '');
  }, [initialSchedule]);

  const choose = (setter) => (event) => {
    setter(event.target.value);
    setInputError('');
  };

  const confirm = () => {
    const { cronExpr, error } = cronExprFromSchedule(preset, time, weekday, monthDay);
    if (error) {
      setInputError(error);
      return;
    }
    void onSave(cronExpr);
  };

  return (
    <FocusTrapDialog ariaLabel={actionLabel} closeDisabled={saving} onClose={onClose}>
        <header><h2>{actionLabel}</h2><button type="button" className="ghost" onClick={onClose} disabled={saving}>关闭</button></header>
        <div className="dag-node-editor">
          <label>
            运行频率
            <select value={preset} onChange={choose(setPreset)} disabled={saving} aria-label="运行频率">
              <option value="daily">每天</option>
              <option value="weekdays">工作日</option>
              <option value="weekly">每周</option>
              <option value="monthly">每月</option>
            </select>
          </label>
          {preset === 'weekly' ? (
            <label>
              星期几
              <select value={weekday} onChange={choose(setWeekday)} disabled={saving} aria-label="星期几">
                {DAG_WEEKDAY_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
              </select>
            </label>
          ) : null}
          {preset === 'monthly' ? (
            <label>
              每月几号
              <select value={monthDay} onChange={choose(setMonthDay)} disabled={saving} aria-label="每月几号">
                {monthDays.map((day) => <option key={day} value={day}>{day} 日</option>)}
              </select>
            </label>
          ) : null}
          <label>
            运行时间
            <input value={time} type="time" onChange={choose(setTime)} disabled={saving} aria-label="运行时间" />
          </label>
        </div>
        {previewText ? <p className="settings-status">{previewText} 自动运行</p> : null}
        {inputError ? <p className="danger-text" role="alert">{inputError}</p> : null}
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          <button type="button" onClick={confirm} disabled={saving}>{saving ? '保存中...' : actionLabel}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function ConfirmDagDeleteModal({ dag, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除自动化" closeDisabled={deleting} onClose={onClose}>
        <header><h2>删除自动化</h2><button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button></header>
        <p>确定删除自动化 “{dag.title}” 吗？该操作会删除配置和运行关联信息，无法恢复。</p>
        <p className="path">{dag.dagKey}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function SkillsPage({ projectPath, refreshKey = 0, resolveLaunchPreferences }) {
  const projectCwd = optionalSettingsCwd(projectPath);
  const queryClient = useQueryClient();
  const [query, setQuery] = useState('');
  const [scopeFilter, setScopeFilter] = useState('all');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorForm, setEditorForm] = useState(emptySkillForm);
  const [activeSkillPath, setActiveSkillPath] = useState('');
  const [skillFiles, setSkillFiles] = useState([]);
  const [summarySuggestion, setSummarySuggestion] = useState('');
  const [summarySuggesting, setSummarySuggesting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleting, setDeleting] = useState(false);
  const [importScopeOpen, setImportScopeOpen] = useState(false);
  const [importing, setImporting] = useState(false);
  const [resolutionPreview, setResolutionPreview] = useState(null);
  const [resolutionNamePrompt, setResolutionNamePrompt] = useState(null);
  const [resolutionNameInput, setResolutionNameInput] = useState('');
  const [resolutionActioning, setResolutionActioning] = useState('');
  const skillRefreshKey = Number(refreshKey || 0);

  const skillsQuery = useQuery({
    queryKey: dashboardQueryKey(projectCwd, 'skills'),
    queryFn: () => fetchSkillsDashboard(projectCwd),
    enabled: Boolean(projectCwd),
  });
  const skillResolutionsQuery = useQuery({
    queryKey: dashboardQueryKey(projectCwd, 'skill-resolutions'),
    queryFn: () => fetchSkillResolutionsDashboard(projectCwd),
    enabled: Boolean(projectCwd),
  });
  const skillsData = skillsQuery.data;
  const resolutionConflictsData = skillResolutionsQuery.data;
  const refetchSkills = skillsQuery.refetch;
  const refetchSkillResolutions = skillResolutionsQuery.refetch;
  const items = useMemo(() => (Array.isArray(skillsData) ? skillsData : []), [skillsData]);
  const resolutionConflicts = useMemo(() => (
    Array.isArray(resolutionConflictsData) ? resolutionConflictsData : []
  ), [resolutionConflictsData]);
  const hasSkillsSnapshot = Array.isArray(skillsData);
  const isProjectPending = !projectCwd;
  const isInitialSkillsLoading = Boolean(projectCwd) && skillsQuery.isPending && !hasSkillsSnapshot;
  const syncErrorText = skillsQuery.error
    ? errorMessage(skillsQuery.error)
    : (skillResolutionsQuery.error ? `读取技能冲突失败：${errorMessage(skillResolutionsQuery.error)}` : '');
  const showCachedSyncError = Boolean(syncErrorText && hasSkillsSnapshot);
  const showBlockingSyncError = Boolean(syncErrorText && !hasSkillsSnapshot);

  const refreshSkillSurface = useCallback(async () => {
    if (!projectCwd) return;
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd, 'skills') }),
      queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd, 'skill-resolutions') }),
    ]);
  }, [projectCwd, queryClient]);

  const retrySkillSurface = useCallback(async () => {
    if (!projectCwd) return;
    await Promise.all([
      refetchSkills(),
      refetchSkillResolutions(),
    ]);
  }, [projectCwd, refetchSkillResolutions, refetchSkills]);

  const openCreateEditor = () => {
    setEditorForm(emptySkillForm());
    setActiveSkillPath('');
    setSkillFiles([]);
    setSummarySuggestion('');
    setError('');
    setNotice('');
    setEditorOpen(true);
  };

  const openEditSkill = async (skill) => {
    if (!skill?.dir) {
      setError('skills/local/read: path is required');
      return;
    }
    setError('');
    setNotice('');
    setSummarySuggestion('');
    const path = `${skill.dir.replace(/[\\/]+$/g, '')}/SKILL.md`;
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const [rawSkill, rawFiles] = await Promise.all([
        readSkill({ cwd, path }),
        listSkillFiles({ cwd, dir: skill.dir }),
      ]);
      const content = (rawSkill?.skill?.content || '').toString();
      const parsed = parseSkillMarkdown(content, skill.name);
      setEditorForm({
        name: parsed.name || skill.name,
        displayName: parsed.displayName || skill.title || '',
        description: parsed.description || skill.description || '',
        keywords: listToText(parsed.triggerWords.length > 0 ? parsed.triggerWords : skill.tags),
        body: parsed.body,
        scope: skill.scope,
        personalType: skill.personalType,
      });
      setActiveSkillPath(path);
      setSkillFiles(normalizeSkillFileList(rawFiles));
      setEditorOpen(true);
    } catch (err) {
      setError(`读取技能失败：${err.message || String(err)}`);
    }
  };

  const openSkillFile = async (file) => {
    const path = (file?.path || '').toString().trim();
    if (!path) return;
    setError('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const raw = await readSkill({ cwd, path });
      const content = (raw?.skill?.content || '').toString();
      if (isMainSkillFile(path)) {
        const parsed = parseSkillMarkdown(content, editorForm.name);
        setEditorForm((form) => ({
          ...form,
          name: parsed.name || form.name,
          displayName: parsed.displayName || parsed.name || form.displayName,
          description: parsed.description,
          keywords: listToText(parsed.triggerWords),
          body: parsed.body,
        }));
      } else {
        setEditorForm((form) => ({ ...form, body: content }));
      }
      setActiveSkillPath(path);
    } catch (err) {
      setError(`读取子文件失败：${err.message || String(err)}`);
    }
  };

  const suggestSummary = async () => {
    setSummarySuggesting(true);
    setError('');
    setSummarySuggestion('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const launchPreferences = typeof resolveLaunchPreferences === 'function'
        ? await resolveLaunchPreferences(cwd)
        : null;
      const description = await suggestSkillSummary({
        cwd,
        name: editorForm.displayName || editorForm.name,
        description: editorForm.description,
        content: editorForm.body,
        scenario_words: wordListFromText(editorForm.keywords),
        scope: editorForm.scope,
        provider: textValue(launchPreferences?.modelProvider || launchPreferences?.provider),
        model: textValue(launchPreferences?.model),
        codexModelProvider: textValue(launchPreferences?.config?.codexModelProvider),
      });
      setSummarySuggestion(normalizeSummarySuggestion(description));
    } catch (err) {
      setError(`生成简介失败：${err.message || String(err)}`);
    } finally {
      setSummarySuggesting(false);
    }
  };

  const saveEditor = async () => {
    setSaving(true);
    setError('');
    setNotice('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const isMain = !activeSkillPath || isMainSkillFile(activeSkillPath);
      const displayName = editorForm.displayName.trim();
      const name = editorForm.name.trim() || skillNameFromDisplayName(displayName);
      if (isMain && !displayName) throw new Error('请先填写技能名称');
      if (isMain && !name) throw new Error('技能名称必须包含中文、英文或数字');
      const normalizedForm = isMain ? { ...editorForm, name, displayName } : editorForm;
      const payload = {
        cwd,
        path: isMain ? (activeSkillPath || name) : activeSkillPath,
        content: isMain ? buildSkillMarkdown(normalizedForm) : editorForm.body,
        scope: editorForm.scope,
        personal_type: editorForm.scope === 'personal' ? (editorForm.personalType || 'user') : '',
      };
      await writeSkill(payload);
      setEditorOpen(false);
      await refreshSkillSurface();
      setNotice('已保存');
    } catch (err) {
      setError(`保存失败：${err.message || String(err)}`);
    } finally {
      setSaving(false);
    }
  };

  const onDeleteSkill = (skill) => {
    setDeleteTarget(skill);
  };

  const confirmDeleteSkill = async () => {
    const skill = deleteTarget;
    const skillName = (skill?.name || '').toString().trim();
    if (!skillName) {
      setError('skills/local/delete: name is required');
      return;
    }

    setDeleting(true);
    setError('');
    setNotice('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      await deleteSkill({
        cwd,
        name: skillName,
        scope: skill.scope,
        personal_type: skill.personalType,
      });
      setDeleteTarget(null);
      await refreshSkillSurface();
      setNotice(`已删除 ${skill.title}`);
    } catch (err) {
      setError(err.message || String(err));
    } finally {
      setDeleting(false);
    }
  };

  const confirmImportScope = async (scope) => {
    setImporting(true);
    setError('');
    setNotice('');
    try {
      const cwd = normalizeSettingsCwd(projectPath);
      const paths = await selectProjectDirs();
      setImportScopeOpen(false);
      if (!Array.isArray(paths) || paths.length === 0) {
        setNotice('未选择目录');
        return;
      }
      await importSkillDirectories({
        cwd,
        paths,
        scope,
        personal_type: scope === 'personal' ? 'imported' : '',
      });
      await refreshSkillSurface();
      setNotice('导入完成');
    } catch (err) {
      setError(`导入目录失败：${err.message || String(err)}`);
    } finally {
      setImporting(false);
    }
  };

  const runResolutionAction = async (conflict, actionOrEntry, entry = null, newName = '') => {
    const conflictID = (conflict?.conflict_id || conflict?.conflictId || '').toString().trim();
    const actionEntry = typeof actionOrEntry === 'string' ? { action: actionOrEntry } : actionOrEntry || {};
    const action = (actionEntry.action || '').toString().trim();
    if (!conflictID || !action) return false;
    if (resolutionActionUnsupported(action)) {
      setNotice(`暂不支持该技能冲突操作：${resolutionActionLabel(action)}`);
      return false;
    }
    const providerEntry = resolutionActionEntryTarget(actionEntry, entry || resolutionProviderEntries(conflict)[0] || {});
    const applyKey = resolutionApplyKey(conflict, action, providerEntry);
    const trimmedNewName = (newName || '').toString().trim();
    if (requiresResolutionNewName(action) && !trimmedNewName) {
      setResolutionPreview(null);
      setResolutionNamePrompt({
        conflict,
        action,
        entry: providerEntry,
        applyKey,
        autoApply: resolutionActionAutoAppliesForConflict(action, conflict),
      });
      setResolutionNameInput(defaultResolutionNewName(conflict, action));
      setNotice('请输入新技能名称后继续。');
      return false;
    }
    const payload = {
      cwd: normalizeSettingsCwd(projectPath),
      conflict_id: conflictID,
      action,
      name: conflict?.name || conflict?.skill_name || '',
      scope: conflict?.scope || '',
      personal_type: conflict?.personal_type || conflict?.personalType || '',
      provider: providerEntry?.provider || conflict?.provider || '',
      source_provider: providerEntry?.provider || conflict?.source_provider || conflict?.provider || '',
      source_path_id: providerEntry?.source_path_id || providerEntry?.sourcePathId || conflict?.source_path_id || '',
      ...resolutionSameNamePayloadFields(conflict, action, actionEntry),
    };
    if (trimmedNewName) payload.new_name = trimmedNewName;
    setResolutionActioning(applyKey);
    setError('');
    try {
      const preview = await previewSkillResolution(payload);
      const items = Array.isArray(preview?.items) ? preview.items : [];
      if (resolutionActionAutoAppliesForConflict(action, conflict)) {
        const proof = items[0];
        if (!proof?.preview_id || !proof?.preview_hash) throw new Error('缺少处理预览凭据');
        await applySkillResolution({
          ...payload,
          provider: proof.provider || payload.provider,
          source_provider: proof.source_provider || payload.source_provider,
          source_path_id: proof.source_path_id || payload.source_path_id,
          preview_id: proof.preview_id,
          preview_hash: proof.preview_hash,
        });
        setResolutionPreview(null);
        setResolutionNamePrompt(null);
        setResolutionNameInput('');
        await refreshSkillSurface();
        setNotice('已处理技能冲突');
        return true;
      }
      setResolutionPreview({
        ...preview,
        action,
        payload,
        items,
        requiresApply: resolutionRequiresApply(action),
      });
      setNotice(isResolutionViewAction(action) ? '已生成处理预览' : '已生成处理预览，请确认应用。');
      return true;
    } catch (err) {
      setError(`处理技能冲突失败：${err.message || String(err)}`);
      return false;
    } finally {
      setResolutionActioning('');
    }
  };

  const confirmResolutionNewName = async () => {
    const prompt = resolutionNamePrompt;
    if (!prompt) return;
    const newName = resolutionNameInput.trim();
    if (!newName) {
      setError('请输入新技能名称。');
      return;
    }
    const ok = await runResolutionAction(prompt.conflict, prompt.action, prompt.entry, newName);
    if (ok) {
      setResolutionNamePrompt(null);
      setResolutionNameInput('');
    }
  };

  const confirmResolutionPreview = async () => {
    const preview = resolutionPreview;
    const proof = Array.isArray(preview?.items) ? preview.items[0] : null;
    if (!preview?.requiresApply || !proof?.preview_id || !proof?.preview_hash) return;
    setResolutionActioning('confirm');
    try {
      await applySkillResolution({
        ...preview.payload,
        provider: proof.provider || preview.payload.provider,
        source_provider: proof.source_provider || preview.payload.source_provider,
        source_path_id: proof.source_path_id || preview.payload.source_path_id,
        preview_id: proof.preview_id,
        preview_hash: proof.preview_hash,
      });
      setResolutionPreview(null);
      setResolutionNamePrompt(null);
      setResolutionNameInput('');
      await refreshSkillSurface();
      setNotice('已处理技能冲突');
    } catch (err) {
      setError(`应用技能冲突处理失败：${err.message || String(err)}`);
    } finally {
      setResolutionActioning('');
    }
  };

  useEffect(() => {
    setError('');
    setNotice('');
    setResolutionPreview(null);
    setResolutionNamePrompt(null);
    setResolutionNameInput('');
  }, [projectCwd]);

  useEffect(() => {
    if (skillRefreshKey <= 0 || !projectCwd) return;
    void refreshSkillSurface();
  }, [projectCwd, refreshSkillSurface, skillRefreshKey]);

  useEffect(() => {
    if (!projectCwd) return undefined;
    const refreshWhenVisible = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void refreshSkillSurface();
    };
    const handleVisibilityChange = () => {
      if (typeof document === 'undefined' || document.visibilityState === 'visible') {
        refreshWhenVisible();
      }
    };
    window.addEventListener('focus', refreshWhenVisible);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.removeEventListener('focus', refreshWhenVisible);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [projectCwd, refreshSkillSurface]);

  useEffect(() => {
    if (resolutionConflicts.length > 0) return;
    setResolutionPreview(null);
    setResolutionNamePrompt(null);
    setResolutionNameInput('');
  }, [resolutionConflicts.length]);

  const counts = useMemo(() => items.reduce((acc, item) => {
    acc.all += 1;
    if (item.scope === 'personal') acc.personal += 1;
    else acc.project += 1;
    return acc;
  }, { all: 0, personal: 0, project: 0 }), [items]);

  const filteredItems = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return items.filter((item) => {
      if (scopeFilter !== 'all' && item.scope !== scopeFilter) return false;
      if (!keyword) return true;
      return [
        item.name,
        item.title,
        item.description,
        item.summary,
        item.dir,
        ...item.tags,
      ].join(' ').toLowerCase().includes(keyword);
    });
  }, [items, query, scopeFilter]);

  const scopeOptions = [
    ['personal', `私人使用 ${counts.personal}`],
    ['project', `项目共享 ${counts.project}`],
    ['all', `全部 ${counts.all}`],
  ];

  return (
    <section className="console-page">
      <PageHeader
        icon={Sparkles}
        title="技能管理"
      />
      <div className="subhead">技能列表</div>
      <div className="skills-toolbar">
        <button type="button" onClick={() => setImportScopeOpen(true)} disabled={importing}>批量导入技能目录</button>
        <button type="button" className="ghost" onClick={openCreateEditor}>新建技能</button>
        <label><Search size={18} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索技能名称、简介、关键词..." /></label>
      </div>
      <div className="skill-filter">
        {scopeOptions.map(([value, label]) => (
          <button
            key={value}
            type="button"
            className={scopeFilter === value ? 'active' : ''}
            onClick={() => setScopeFilter(value)}
          >
            {label}
          </button>
        ))}
      </div>
      {isProjectPending ? <p className="console-message">正在连接本地项目...</p> : null}
      {isInitialSkillsLoading ? <p className="console-message">加载技能中...</p> : null}
      {notice ? <p className="settings-status">{notice}</p> : null}
      {showCachedSyncError ? (
        <div className="danger-text skills-sync-alert" role="alert">
          <span>同步失败，显示的是上次成功的数据：{syncErrorText}</span>
          <button type="button" className="ghost" onClick={() => { void retrySkillSurface(); }}>重试同步</button>
        </div>
      ) : null}
      {showBlockingSyncError ? (
        <RetryableSyncError
          className="danger-text skills-sync-alert"
          message={syncErrorText}
          onRetry={retrySkillSurface}
        />
      ) : null}
      {error ? <p className="danger-text" role="alert">{error}</p> : null}
      {resolutionConflicts.length > 0 ? (
        <section className="skills-resolution-panel">
          <strong>发现 {resolutionConflicts.length} 个技能冲突，需要处理后再使用。</strong>
          {resolutionConflicts.map((conflict, index) => {
            const conflictID = (conflict.conflict_id || conflict.conflictId || index).toString();
            const providerEntries = resolutionProviderEntries(conflict);
            const actionEntries = resolutionActionEntries(conflict);
            const promptConflictID = (resolutionNamePrompt?.conflict?.conflict_id || resolutionNamePrompt?.conflict?.conflictId || '').toString();
            const promptApplies = resolutionNamePrompt && promptConflictID === (conflict.conflict_id || conflict.conflictId || '').toString();
            return (
              <article key={conflictID} className="skills-resolution-item">
                <header>
                  <h3>{conflict.name || conflict.skill_name || '未命名技能'} · {resolutionKindLabel(conflict.kind)}</h3>
                  <span>{scopeLabel(scopeForSkill(conflict))}</span>
                </header>
                {providerEntries.map((providerEntry, sourceIndex) => (
                  <div className="skills-resolution-actions" key={`${conflictID}:${sourceIndex}:${resolutionProviderEntryLabel(providerEntry)}`}>
                    {providerEntries.length > 1 ? <span className="skills-resolution-source">{resolutionProviderEntryLabel(providerEntry)}</span> : null}
                    {actionEntries.map((actionEntry, actionIndex) => {
                      const action = (actionEntry.action || actionEntry).toString();
                      const targetEntry = resolutionActionEntryTarget(actionEntry, providerEntry);
                      const applyKey = resolutionApplyKey(conflict, action, targetEntry);
                      const label = resolutionActionEntryLabel(actionEntry);
                      const help = resolutionActionEntryHelp(actionEntry);
                      return (
                        <button
                          key={`${applyKey}:${actionIndex}`}
                          type="button"
                          title={help}
                          onClick={() => { void runResolutionAction(conflict, actionEntry, providerEntry); }}
                          disabled={resolutionActioning === applyKey}
                        >
                          {resolutionActioning === applyKey ? '处理中...' : label}
                        </button>
                      );
                    })}
                  </div>
                ))}
                {promptApplies ? (
                  <div className="skills-resolution-name-field">
                    <label>
                      新技能名称
                      <input
                        value={resolutionNameInput}
                        onChange={(event) => setResolutionNameInput(event.target.value)}
                        aria-label="新技能名称"
                      />
                    </label>
                    <button
                      type="button"
                      onClick={() => { void confirmResolutionNewName(); }}
                      disabled={resolutionActioning === resolutionNamePrompt.applyKey}
                    >
                      {resolutionActioning === resolutionNamePrompt.applyKey
                        ? (resolutionNamePrompt.autoApply ? '处理中...' : '生成中...')
                        : (resolutionNamePrompt.autoApply ? '确认处理' : '生成预览')}
                    </button>
                    <button
                      type="button"
                      className="ghost"
                      onClick={() => {
                        setResolutionNamePrompt(null);
                        setResolutionNameInput('');
                      }}
                    >
                      取消
                    </button>
                  </div>
                ) : null}
              </article>
            );
          })}
          {resolutionPreview ? (
            <article className="skills-resolution-preview">
              <header>
                <h3>{resolutionActionLabel(resolutionPreview.action)}</h3>
                {resolutionPreview.requiresApply ? (
                  <button type="button" onClick={() => { void confirmResolutionPreview(); }} disabled={resolutionActioning === 'confirm'}>
                    {resolutionActioning === 'confirm' ? '应用中...' : '确认应用'}
                  </button>
                ) : null}
                <button type="button" className="ghost" onClick={() => setResolutionPreview(null)}>取消</button>
              </header>
              {(resolutionPreview.items || []).map((item, index) => (
                <div key={item.preview_id || index} className="skills-resolution-preview-item">
                  {previewItemPaths(item).map((pathItem) => (
                    <p key={`${pathItem.label}:${pathItem.value}`}><span>{pathItem.label}</span><code>{pathItem.value}</code></p>
                  ))}
                </div>
              ))}
            </article>
          ) : null}
        </section>
      ) : null}
      {!isProjectPending && !isInitialSkillsLoading && !showBlockingSyncError && filteredItems.length === 0 ? <p className="console-message">暂无技能</p> : null}
      {filteredItems.length > 0 ? (
        <div className="skill-grid">
          {filteredItems.map((skill) => <SkillCard key={skill.id} skill={skill} onEdit={openEditSkill} onDelete={onDeleteSkill} />)}
        </div>
      ) : null}
      {editorOpen ? (
        <SkillEditorModal
          form={editorForm}
          setForm={setEditorForm}
          activeSkillPath={activeSkillPath}
          files={skillFiles}
          summarySuggestion={summarySuggestion}
          summarySuggesting={summarySuggesting}
          saving={saving}
          onSuggestSummary={suggestSummary}
          onApplySummary={() => {
            setEditorForm((form) => ({ ...form, description: summarySuggestion }));
            setSummarySuggestion('');
          }}
          onOpenFile={openSkillFile}
          onClose={() => setEditorOpen(false)}
          onSave={saveEditor}
        />
      ) : null}
      {deleteTarget ? (
        <ConfirmSkillDeleteModal
          skill={deleteTarget}
          deleting={deleting}
          onClose={() => setDeleteTarget(null)}
          onConfirm={confirmDeleteSkill}
        />
      ) : null}
      {importScopeOpen ? (
        <ImportScopeModal
          importing={importing}
          onClose={() => setImportScopeOpen(false)}
          onConfirm={confirmImportScope}
        />
      ) : null}
    </section>
  );
}

function SkillCard({ skill, onEdit, onDelete }) {
  const tags = skill.tags.slice(0, 4);
  const extraTagCount = skill.tags.length - tags.length;
  const descriptionText = (skill.description || '').toString().trim();
  const summaryText = (skill.summary || '').toString().trim();
  const description = descriptionText || summaryText || '暂无描述';
  const shouldShowSummary = Boolean(summaryText && summaryText !== description);

  return (
    <article className="skill-card">
      <header><h3>{skill.title}</h3><span>{scopeLabel(skill.scope)}</span></header>
      <p className="path">{skill.dir || '未提供路径'}</p>
      <p>{description}</p>
      {shouldShowSummary ? <div className="quote">{summaryText}</div> : null}
      <small>关键词</small>
      <div className="tags">
        {tags.length > 0 ? tags.map((tag) => <span key={tag}>{tag}</span>) : <span>暂无关键词</span>}
        {extraTagCount > 0 ? <span>+{extraTagCount}</span> : null}
      </div>
      <footer>
        <button type="button" onClick={() => { void onEdit(skill); }} disabled={!skill.dir}>编辑详情</button>
        <button type="button" className="text-danger" onClick={() => { void onDelete(skill); }} disabled={!skill.name}>删除</button>
      </footer>
    </article>
  );
}

function SkillEditorModal({
  form,
  setForm,
  activeSkillPath,
  files,
  summarySuggestion,
  summarySuggesting,
  saving,
  onSuggestSummary,
  onApplySummary,
  onOpenFile,
  onClose,
  onSave,
}) {
  const isMain = !activeSkillPath || isMainSkillFile(activeSkillPath);
  const modalTitle = activeSkillPath ? '编辑技能' : '新建技能';
  const update = (key) => (event) => setForm((current) => ({ ...current, [key]: event.target.value }));
  const updateDisplayName = (event) => {
    const value = event.target.value;
    setForm((current) => ({
      ...current,
      displayName: value,
      name: activeSkillPath ? current.name : skillNameFromDisplayName(value),
    }));
  };
  const [bodyEditing, setBodyEditing] = useState(!activeSkillPath);
  useEffect(() => {
    setBodyEditing(!activeSkillPath);
  }, [activeSkillPath]);
  return (
    <FocusTrapDialog ariaLabel={modalTitle} className="modal-box skills-editor-modal" closeDisabled={saving} onClose={onClose}>
        <header className="skills-editor-modal-head">
          <div>
            <h2>{modalTitle}</h2>
            <p>你可以修改简介和技能内容。</p>
          </div>
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>关闭</button>
        </header>
        <div className="form-grid">
          <label className="wide">技能名称<input value={form.displayName} onChange={updateDisplayName} disabled={!isMain} /></label>
          <div className="skills-field wide">
            <div className="skills-editor-label-row">
              <label htmlFor="skills-description-input">技能简介</label>
              <button type="button" className="ghost" onClick={() => { void onSuggestSummary(); }} disabled={!isMain || summarySuggesting || (!form.name.trim() && !form.body.trim())}>
                {summarySuggesting ? '生成中' : '帮我生成'}
              </button>
            </div>
            <input id="skills-description-input" value={form.description} onChange={update('description')} disabled={!isMain} aria-label="技能简介" />
            {summarySuggestion ? (
              <div className="skills-inline-tip skills-summary-suggestion" data-testid="skills-summary-suggestion">
                <span>建议：{summarySuggestion}</span>
                <button type="button" onClick={onApplySummary}>采用</button>
              </div>
            ) : null}
            <div className="skills-inline-tip">建议写成“当你需要……时使用”。</div>
          </div>
          <div className="skills-field">
            <span>使用范围</span>
            <div className="skills-scope-segmented" role="group" aria-label="使用范围">
              <button type="button" className={form.scope === 'project' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'project' }))}>项目共享</button>
              <button type="button" className={form.scope === 'personal' ? 'active' : ''} disabled={!isMain} onClick={() => setForm((current) => ({ ...current, scope: 'personal' }))}>私人使用</button>
            </div>
          </div>
          <label>关键词<input value={form.keywords} onChange={update('keywords')} disabled={!isMain} aria-label="关键词" /></label>
        </div>
        {files.some((file) => !file.isMain) ? (
          <div className="skills-subfiles-wrap">
            <span>附加内容</span>
            <div className="skills-subfiles">
            {files.map((file) => (
              <button
                key={file.path}
                type="button"
                className={file.path === activeSkillPath ? 'active' : ''}
                onClick={() => { void onOpenFile(file); }}
              >
                {file.name}{file.isMain ? ' · 主要文件' : ''}
              </button>
            ))}
            </div>
            <div className="skills-inline-tip">这里是这个技能附带的示例、模板或脚本。</div>
          </div>
        ) : null}
        <div className="skills-body-field">
          <div className="skills-body-head">
            <span>{isMain ? '技能内容' : '关联文件内容'}</span>
            {bodyEditing ? (
              <button type="button" className="ghost" onClick={() => setBodyEditing(false)}>预览正文</button>
            ) : (
              <button type="button" onClick={() => setBodyEditing(true)}>编辑正文</button>
            )}
          </div>
          {bodyEditing ? (
            <textarea value={form.body} onChange={update('body')} aria-label={isMain ? '技能内容' : '关联文件内容'} />
          ) : (
            <div className="skills-body-preview" data-testid="skills-editor-body-preview">
              <SkillMarkdownPreview content={form.body} />
            </div>
          )}
          <div className="skills-inline-tip">点击“编辑正文”展开编辑；切回“预览正文”查看效果。</div>
        </div>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          <button type="button" onClick={() => { void onSave(); }} disabled={saving}>{saving ? '保存中...' : '保存技能'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function ConfirmSkillDeleteModal({ skill, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除技能" closeDisabled={deleting} onClose={onClose}>
        <header><h2>删除技能</h2><button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button></header>
        <p>确定删除技能 “{skill.name}” 吗？该操作会删除技能目录及其资源文件，无法恢复。</p>
        <p className="path">{skill.dir || '-'}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function ImportScopeModal({ importing, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="导入技能" closeDisabled={importing} onClose={onClose}>
        <header><h2>导入技能</h2><button type="button" className="ghost" onClick={onClose} disabled={importing}>关闭</button></header>
        <p>这些技能导入后给谁使用？</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={importing}>取消</button>
          <button type="button" onClick={() => { void onConfirm('personal'); }} disabled={importing}>私人使用</button>
          <button type="button" onClick={() => { void onConfirm('project'); }} disabled={importing}>项目共享</button>
        </footer>
    </FocusTrapDialog>
  );
}

function FilesPage({ projectPath, store }) {
  const exportDefaultPath = optionalSettingsCwd(projectPath);
  const queryClient = useQueryClient();
  const sharedFilesQueryKey = useMemo(() => dashboardGlobalQueryKey('shared-files'), []);
  useDashboardQueryFocusInvalidation(sharedFilesQueryKey);
  const sharedFilesQuery = useQuery({
    queryKey: sharedFilesQueryKey,
    queryFn: fetchSharedFilesDashboard,
  });
  const hasSharedFilesSnapshot = queryHasSnapshot(sharedFilesQuery);
  const sharedFilesData = sharedFilesQuery.data || {
    files: [],
    finalOutputRefs: [],
    retention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
  };
  const files = sharedFilesData.files;
  const finalOutputRefs = sharedFilesData.finalOutputRefs;
  const retention = sharedFilesData.retention;
  const loading = sharedFilesQuery.isPending && !hasSharedFilesSnapshot;
  const { cachedSyncError: syncError, blockingError } = dashboardQueryErrorState(sharedFilesQuery, hasSharedFilesSnapshot);
  const error = blockingError ? `加载共享文件失败：${blockingError}` : '';
  const [notice, setNotice] = useState(null);
  const [searchText, setSearchText] = useState('');
  const [sortMode, setSortMode] = useState('updated-desc');
  const [category, setCategory] = useState('all');
  const [selectedFile, setSelectedFile] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [busyPath, setBusyPath] = useState('');
  const [exportingPath, setExportingPath] = useState('');
  const [deletingPath, setDeletingPath] = useState('');
  const [copied, setCopied] = useState(false);
  const sharedFilesRevision = Number(store?.sharedFilesRevision || 0);

  const finalOutputByPath = useMemo(() => (
    new Map(finalOutputRefs.filter((ref) => files.some((file) => file.path === ref.path)).map((ref) => [ref.path, ref]))
  ), [files, finalOutputRefs]);
  const retentionByPath = useMemo(() => (
    new Map(retention.items.map((item) => [item.path, item]))
  ), [retention.items]);
  const finalCount = files.filter((file) => finalOutputByPath.has(file.path)).length;
  const workCount = Math.max(0, files.length - finalCount);
  const activeSortLabel = SHARED_FILE_SORTS.find((item) => item.key === sortMode)?.label || '最新更新';
  const categoryCountsByKey = useMemo(() => ({
    all: files.length,
    final: finalCount,
    work: workCount,
  }), [files.length, finalCount, workCount]);

  const refreshFiles = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: sharedFilesQueryKey });
  }, [queryClient, sharedFilesQueryKey]);

  useEffect(() => {
    setNotice(null);
  }, []);

  useEffect(() => {
    if (sharedFilesRevision <= 0) return;
    void refreshFiles();
  }, [refreshFiles, sharedFilesRevision]);

  const visibleFiles = useMemo(() => {
    const filtered = sortSharedFiles(files.filter((file) => sharedFileMatches(file, searchText)), sortMode);
    if (category === 'final') return filtered.filter((file) => sharedFileCategoryOf(file, finalOutputByPath) === 'final');
    if (category === 'work') return filtered.filter((file) => sharedFileCategoryOf(file, finalOutputByPath) === 'work');
    return filtered;
  }, [category, files, finalOutputByPath, searchText, sortMode]);

  const protectionFor = useCallback((file) => {
    const retentionItem = retentionByPath.get(file.path);
    if (retentionItem?.protected) return retentionItem;
    const ref = finalOutputByPath.get(file.path);
    if (ref) return { path: file.path, protected: true, reason: 'final_output', finalOutput: ref };
    return null;
  }, [finalOutputByPath, retentionByPath]);

  const loadFileDetail = useCallback(async (file) => {
    const path = textValue(file?.path);
    if (!path) throw new Error('shared file path is required');
    setBusyPath(path);
    try {
      const detail = await readSharedFile({ path });
      return normalizeSharedFile({
        path: detail?.path || path,
        content: detail?.content || file?.content || '',
        updatedBy: detail?.updatedBy || file?.updatedBy || '',
        updatedAt: detail?.updatedAt || file?.updatedAt || '',
      }, 0);
    } finally {
      setBusyPath('');
    }
  }, []);

  const openFile = useCallback(async (file) => {
    setNotice(null);
    setCopied(false);
    try {
      setSelectedFile(await loadFileDetail(file));
    } catch (err) {
      setNotice({ level: 'error', message: `读取文件失败：${err.message || String(err)}` });
    }
  }, [loadFileDetail]);

  const exportFile = useCallback(async (file) => {
    const path = textValue(file?.path);
    if (!path || exportingPath) return;
    setNotice(null);
    setExportingPath(path);
    try {
      const detail = await loadFileDetail(file);
      const savedPath = await saveTextFile({
        defaultPath: exportDefaultPath,
        defaultFilename: sharedFileExportName(detail.path),
        content: detail.content,
      });
      setNotice({ level: 'info', message: savedPath ? `已保存到：${savedPath}` : '已取消保存。' });
    } catch (err) {
      setNotice({ level: 'error', message: `导出失败：${err.message || String(err)}` });
    } finally {
      setExportingPath('');
    }
  }, [exportDefaultPath, exportingPath, loadFileDetail]);

  const askDelete = useCallback((file) => {
    const protection = protectionFor(file);
    if (protection) {
      setNotice({ level: 'error', message: `最终产物不能直接删除：${file.path}` });
      return;
    }
    setDeleteTarget(file);
  }, [protectionFor]);

  const confirmDelete = useCallback(async () => {
    const target = deleteTarget;
    if (!target?.path || deletingPath) return;
    setNotice(null);
    setDeletingPath(target.path);
    try {
      await deleteSharedFile({ path: target.path });
      if (selectedFile?.path === target.path) setSelectedFile(null);
      setDeleteTarget(null);
      setNotice({ level: 'info', message: `已删除文件：${target.path}` });
      await refreshFiles();
    } catch (err) {
      setNotice({ level: 'error', message: `删除失败：${err.message || String(err)}` });
    } finally {
      setDeletingPath('');
    }
  }, [deleteTarget, deletingPath, refreshFiles, selectedFile]);

  const continueWithFile = useCallback((file) => {
    if (typeof store?.continueWithSharedFile === 'function') {
      store.continueWithSharedFile(file.path);
    }
  }, [store]);

  const copySelectedContent = useCallback(async () => {
    const text = selectedFile?.content || '';
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
    } catch (err) {
      setNotice({ level: 'error', message: `复制失败：${err.message || String(err)}` });
    }
  }, [selectedFile]);

  return (
    <section className="console-page shared-files-page">
      <PageHeader
        icon={FolderOpen}
        title="文件产物"
        subtitle={`${activeSortLabel} · 全部${files.length} 最终产物${finalCount} 工作文件${workCount}`}
        actions={(
          <>
            <label className="shared-files-search">
              <Search size={15} />
              <input
                aria-label="搜索共享文件"
                placeholder="搜索文件名 / 内容"
                value={searchText}
                onChange={(event) => setSearchText(event.target.value)}
              />
            </label>
            <select aria-label="共享文件排序" value={sortMode} onChange={(event) => setSortMode(event.target.value)}>
              {SHARED_FILE_SORTS.map((item) => <option key={item.key} value={item.key}>{item.label}</option>)}
            </select>
          </>
        )}
      />
      <div className="file-intro">
        <FolderOpen size={29} />
        <h2>共享文件 · Agent 协作中转站</h2>
        <p>Agent 在运行过程中产生的所有数据产物都保存在这里。</p>
      </div>
      <div className="shared-files-tabs" role="tablist" aria-label="文件产物分类">
        {SHARED_FILE_CATEGORIES.map((item) => {
          const count = categoryCountsByKey[item.key] || 0;
          return (
            <button
              key={item.key}
              type="button"
              role="tab"
              aria-selected={category === item.key}
              className={category === item.key ? 'active' : ''}
              onClick={() => setCategory(item.key)}
            >
              {item.label} {count}
            </button>
          );
        })}
      </div>
      {notice ? <p className={notice.level === 'error' ? 'danger-text' : 'settings-status'}>{notice.message}</p> : null}
      {syncError ? (
        <div className="danger-text shared-files-sync-alert" role="alert">
          <span>{syncError}</span>
          <button type="button" className="ghost" onClick={() => { void refreshFiles(); }}>重试同步</button>
        </div>
      ) : null}
      <RetryableSyncError className="danger-text shared-files-sync-alert" message={error} onRetry={refreshFiles} />
      {!error && loading && files.length === 0 ? <p className="console-message">正在加载共享文件...</p> : null}
      {!error && !loading && files.length === 0 ? (
        <div className="empty-state">
          <span><File size={24} /></span>
          <h2>还没有文件产物</h2>
          <p>Agent 生成报告、草稿或数据文件后，会显示在这里。</p>
        </div>
      ) : null}
      {!error && files.length > 0 && visibleFiles.length === 0 ? (
        <div className="empty-state">
          <span><Search size={24} /></span>
          <h2>没有匹配的文件</h2>
          <p>清空搜索或切换分类后再试。</p>
        </div>
      ) : null}
      {!error && visibleFiles.length > 0 ? (
        <div className="file-list" data-testid="shared-files-list">
          {visibleFiles.map((file) => (
            <SharedFileRow
              key={file.path}
              file={file}
              finalOutputRef={finalOutputByPath.get(file.path)}
              protectedFile={Boolean(protectionFor(file))}
              busy={busyPath === file.path}
              exporting={exportingPath === file.path}
              deleting={deletingPath === file.path}
              onOpen={openFile}
              onExport={exportFile}
              onDelete={askDelete}
              onContinue={continueWithFile}
            />
          ))}
        </div>
      ) : null}
      {selectedFile ? (
        <SharedFileViewer
          file={selectedFile}
          copied={copied}
          exporting={exportingPath === selectedFile.path}
          onClose={() => setSelectedFile(null)}
          onCopy={copySelectedContent}
          onExport={() => { void exportFile(selectedFile); }}
        />
      ) : null}
      {deleteTarget ? (
        <ConfirmSharedFileDeleteModal
          file={deleteTarget}
          deleting={deletingPath === deleteTarget.path}
          onClose={() => setDeleteTarget(null)}
          onConfirm={confirmDelete}
        />
      ) : null}
    </section>
  );
}

function SharedFileRow({
  file,
  finalOutputRef,
  protectedFile,
  busy,
  exporting,
  deleting,
  onOpen,
  onExport,
  onDelete,
  onContinue,
}) {
  const path = splitSharedFilePath(file.path);
  const role = finalOutputRef ? '最终产物' : '工作文件';
  return (
    <article className={`file-row${finalOutputRef ? ' is-final-output' : ''}`}>
      <header>
        <h3>{path.base}</h3>
        <span>{role}</span>
      </header>
      <p>{role} {sharedFileTimestamp(file.updatedAt)} {formatBytes(sharedFileContent(file).length)}</p>
      <code>{file.path}</code>
      {finalOutputRef ? (
        <small>Run {finalOutputRef.runKey || '-'} · DAG {finalOutputRef.dagKey || '-'} · Node {finalOutputRef.sourceNodeKey || '-'}</small>
      ) : null}
      <pre>{sharedFileSummary(file)}</pre>
      <footer>
        <button type="button" onClick={() => { void onOpen(file); }} disabled={busy}>
          <Eye size={14} /> {busy ? '加载中...' : '打开'}
        </button>
        <button type="button" onClick={() => { void onExport(file); }} disabled={busy || exporting}>
          <Download size={14} /> {exporting ? '导出中...' : '导出'}
        </button>
        <button
          type="button"
          className={protectedFile ? 'ghost' : 'text-danger'}
          onClick={() => onDelete(file)}
          disabled={protectedFile || deleting}
          title={protectedFile ? '最终产物由任务结果引用，不能直接删除。' : ''}
        >
          <Trash2 size={14} /> {protectedFile ? '不可删除' : deleting ? '删除中...' : '删除'}
        </button>
        <button type="button" className="ghost" onClick={() => onContinue(file)}>
          <MessageCircle size={14} /> 用此文件继续对话
        </button>
      </footer>
    </article>
  );
}

function SharedFileViewer({ file, copied, exporting, onClose, onCopy, onExport }) {
  return (
    <FocusTrapDialog ariaLabel="文件预览" className="modal-box shared-file-viewer-modal" onClose={onClose}>
        <header>
          <div>
            <h2>文件预览</h2>
            <p className="path">{file.path}</p>
          </div>
          <div className="modal-actions">
            <button type="button" onClick={onExport} disabled={exporting}><Download size={14} /> {exporting ? '导出中...' : '导出'}</button>
            <button type="button" onClick={() => { void onCopy(); }} disabled={!file.content}><Copy size={14} /> {copied ? '已复制' : '复制内容'}</button>
            <button type="button" className="ghost" onClick={onClose}><X size={14} /> 关闭</button>
          </div>
        </header>
        <dl className="shared-file-viewer-meta">
          <dt>来源</dt><dd>{file.updatedBy || '-'}</dd>
          <dt>更新时间</dt><dd>{sharedFileTimestamp(file.updatedAt)}</dd>
        </dl>
        <pre className="shared-file-content-preview">{sharedFilePreview(file)}</pre>
    </FocusTrapDialog>
  );
}

function ConfirmSharedFileDeleteModal({ file, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除文件" closeDisabled={deleting} onClose={onClose}>
        <header>
          <h2>删除文件</h2>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button>
        </header>
        <p>文件删除后无法恢复。删除前请确认这份内容不再需要。</p>
        <p className="path">{file.path}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>
            {deleting ? '删除中...' : '确认删除'}
          </button>
        </footer>
    </FocusTrapDialog>
  );
}

function MemoryPage({ projectPath, onSimilarCountChange, resolveLaunchPreferences }) {
  const memoryCwd = optionalSettingsCwd(projectPath);
  const isProjectPending = !memoryCwd;
  const queryClient = useQueryClient();
  const memoryQuery = useQuery({
    queryKey: dashboardQueryKey(memoryCwd, 'memory'),
    queryFn: () => fetchMemoryDashboard(memoryCwd),
    enabled: Boolean(memoryCwd),
  });
  const hasMemorySnapshot = queryHasSnapshot(memoryQuery);
  const snapshot = memoryQuery.data || { overview: {}, entries: [] };
  const loading = Boolean(memoryCwd) && memoryQuery.isPending && !hasMemorySnapshot;
  const { cachedSyncError: syncError, blockingError: error } = dashboardQueryErrorState(memoryQuery, hasMemorySnapshot);
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const [searchText, setSearchText] = useState('');
  const [activeCategory, setActiveCategory] = useState('preference');
  const [createMenuOpen, setCreateMenuOpen] = useState(false);
  const [editor, setEditor] = useState({ open: false, mode: 'create', form: defaultMemoryForm('project') });
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [mergeTarget, setMergeTarget] = useState(null);
  const [similarExpanded, setSimilarExpanded] = useState(false);
  const [busyKey, setBusyKey] = useState('');
  const [saving, setSaving] = useState(false);
  const [deletingKey, setDeletingKey] = useState('');
  const [autoToggling, setAutoToggling] = useState(false);
  const [mergingAll, setMergingAll] = useState(false);
  const [ignoringKey, setIgnoringKey] = useState('');
  const [mergingKey, setMergingKey] = useState('');
  const [consolidationJob, setConsolidationJob] = useState(null);

  const refreshMemory = useCallback(async () => {
    if (!memoryCwd) return;
    await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(memoryCwd, 'memory') });
  }, [memoryCwd, queryClient]);

  useEffect(() => {
    setNotice({ level: 'info', message: '' });
  }, [memoryCwd]);

  const showNotice = useCallback((level, message) => {
    setNotice({ level: level || 'info', message: memoryNoticeText(message) });
  }, []);

  const applyConsolidationResult = useCallback(async (cwd, result) => {
    const summary = memoryConsolidationResultMessage(result);
    if (!Number(result?.failed) && !Number(result?.skipped)) {
      queryClient.setQueryData(dashboardQueryKey(cwd, 'memory'), clearMemorySimilarGroups);
    }
    showNotice(summary.level, summary.message);
    await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(cwd, 'memory') });
  }, [queryClient, showNotice]);

  useEffect(() => {
    if (!consolidationJob?.jobId || !consolidationJob?.cwd) return undefined;
    let cancelled = false;

    (async () => {
      try {
        const result = await waitForMemoryConsolidationJob(consolidationJob.cwd, consolidationJob.jobId);
        if (cancelled) return;
        await applyConsolidationResult(consolidationJob.cwd, result);
      } catch (err) {
        if (cancelled) return;
        const message = errorMessage(err);
        const level = message.includes('仍在进行') ? 'warning' : 'error';
        showNotice(level, level === 'warning' ? message : `智能整合失败：${message}`);
      } finally {
        if (!cancelled) setConsolidationJob(null);
      }
    })();

    return () => { cancelled = true; };
  }, [applyConsolidationResult, consolidationJob, showNotice]);

  const preferenceEntries = useMemo(
    () => snapshot.entries.filter((entry) => entry.category === 'preference'),
    [snapshot.entries],
  );
  const projectEntries = useMemo(
    () => snapshot.entries.filter((entry) => entry.category === 'project'),
    [snapshot.entries],
  );
  const entriesByCategory = useMemo(() => ({
    preference: preferenceEntries,
    project: projectEntries,
    all: snapshot.entries,
  }), [preferenceEntries, projectEntries, snapshot.entries]);
  const categoryCounts = useMemo(() => ({
    preference: preferenceEntries.length,
    project: projectEntries.length,
    all: snapshot.entries.length,
  }), [preferenceEntries.length, projectEntries.length, snapshot.entries.length]);
  const visibleEntries = useMemo(() => (
    sortMemoryEntries((entriesByCategory[activeCategory] || []).filter((entry) => memoryMatches(entry, searchText)))
  ), [activeCategory, entriesByCategory, searchText]);
  const health = useMemo(() => memoryHealth(snapshot.overview, categoryCounts), [snapshot.overview, categoryCounts]);
  const autoDreamRuntime = snapshot.overview?.autoDreamEnabled === true;
  const autoDreamIntent = normalizeAutoDreamIntent(snapshot.overview?.autoDreamIntent);
  const autoDreamEnabled = autoDreamIntent === null ? autoDreamRuntime : autoDreamIntent;
  const autoDreamPendingRestart = autoDreamIntent !== null && autoDreamIntent !== autoDreamRuntime;
  const prefPercent = health ? memoryHealthPercent(health.preferenceCount, health.maxPerCategory) : 0;
  const projPercent = health ? memoryHealthPercent(health.projectCount, health.maxPerCategory) : 0;
  const similarGroups = health?.similarGroups || [];

  useEffect(() => {
    if (typeof onSimilarCountChange === 'function') {
      onSimilarCountChange(similarGroups.length);
    }
  }, [onSimilarCountChange, similarGroups.length]);

  const updateEditorForm = useCallback((patch) => {
    setEditor((current) => ({ ...current, form: { ...current.form, ...patch } }));
  }, []);

  const openCreate = useCallback((type) => {
    if (isProjectPending) return;
    const form = defaultMemoryForm(type, memoryTargetForType(type));
    setEditor({ open: true, mode: 'create', form });
    setCreateMenuOpen(false);
  }, [isProjectPending]);

  const openEdit = useCallback(async (entry) => {
    if (!memoryCwd) return;
    const key = `${entry.target}:${entry.path}`;
    setBusyKey(key);
    try {
      const detail = await getMemoryEntry({ cwd: memoryCwd, target: entry.target, path: entry.path });
      setEditor({
        open: true,
        mode: 'edit',
        form: {
          target: firstText(detail?.target, entry.target),
          existingPath: firstText(detail?.path, entry.path),
          name: firstText(detail?.name, entry.name),
          description: firstText(detail?.description, entry.description),
          title: firstText(detail?.title, entry.title),
          type: firstText(detail?.type, entry.type),
          content: firstText(detail?.content, entry.preview, memoryTemplateForType(entry.type)),
        },
      });
    } catch (err) {
      showNotice('error', `加载失败：${errorMessage(err)}`);
    } finally {
      setBusyKey('');
    }
  }, [memoryCwd, showNotice]);

  const closeEditor = useCallback(() => {
    if (saving) return;
    setEditor((current) => ({ ...current, open: false }));
  }, [saving]);

  const saveEditor = useCallback(async () => {
    if (saving) return;
    if (!memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    const form = editor.form;
    const description = textValue(form.description);
    const content = textValue(form.content);
    if (!description) { showNotice('error', '请先填写描述'); return; }
    if (!content) { showNotice('error', '内容不能为空'); return; }
    setSaving(true);
    try {
      const type = textValue(form.type) || 'project';
      await upsertMemoryEntry({
        cwd: memoryCwd,
        target: form.existingPath ? form.target : memoryTargetForType(type),
        existingPath: form.existingPath,
        name: memoryAutoName({ ...form, type }),
        description,
        title: textValue(form.title),
        type,
        content,
      });
      setEditor((current) => ({ ...current, open: false }));
      showNotice('info', '已保存');
      await refreshMemory();
    } catch (err) {
      showNotice('error', `保存失败：${errorMessage(err)}`);
    } finally {
      setSaving(false);
    }
  }, [editor.form, memoryCwd, refreshMemory, saving, showNotice]);

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget || deletingKey) return;
    if (!memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    const key = `${deleteTarget.target}:${deleteTarget.path}`;
    setDeletingKey(key);
    try {
      await deleteMemoryEntry({ cwd: memoryCwd, target: deleteTarget.target, path: deleteTarget.path });
      showNotice('info', `已删除：${memoryEntryTitle(deleteTarget)}`);
      setDeleteTarget(null);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `删除失败：${errorMessage(err)}`);
    } finally {
      setDeletingKey('');
    }
  }, [deleteTarget, deletingKey, memoryCwd, refreshMemory, showNotice]);

  const toggleAutoDream = useCallback(async () => {
    if (autoToggling || isProjectPending) return;
    const next = !autoDreamEnabled;
    setAutoToggling(true);
    try {
      await setMemoryAutoDreamIntent({ enabled: next });
      showNotice('warning', `自动沉淀已切换为${next ? '开启' : '关闭'}，重启 agent-terminal 后生效`);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `切换自动沉淀失败：${errorMessage(err)}`);
    } finally {
      setAutoToggling(false);
    }
  }, [autoDreamEnabled, autoToggling, isProjectPending, refreshMemory, showNotice]);

  const confirmMerge = useCallback(async () => {
    if (!mergeTarget || mergingKey) return;
    if (!memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    const key = memoryPairKey(mergeTarget);
    setMergingKey(key);
    try {
      await mergeMemoryEntries({
        cwd: memoryCwd,
        targetA: mergeTarget.targetA,
        pathA: mergeTarget.pathA,
        targetB: mergeTarget.targetB,
        pathB: mergeTarget.pathB,
      });
      showNotice('info', `已整合「${mergeTarget.nameA || mergeTarget.pathA}」与「${mergeTarget.nameB || mergeTarget.pathB}」`);
      setMergeTarget(null);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `整合失败：${errorMessage(err)}`);
    } finally {
      setMergingKey('');
    }
  }, [memoryCwd, mergeTarget, mergingKey, refreshMemory, showNotice]);

  const mergeAllGroups = useCallback(async () => {
    if (!similarGroups.length || mergingAll || consolidationJob) return;
    if (!memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    setMergingAll(true);
    try {
      const launchPreferences = typeof resolveLaunchPreferences === 'function'
        ? await resolveLaunchPreferences(memoryCwd)
        : null;
      const started = await startConsolidateMemorySimilarities({
        cwd: memoryCwd,
        provider: textValue(launchPreferences?.modelProvider || launchPreferences?.provider),
        model: textValue(launchPreferences?.model),
        codexModelProvider: textValue(launchPreferences?.config?.codexModelProvider),
      });
      const jobID = textValue(started?.jobId);
      if (started?.status === 'failed') throw memoryConsolidationJobFailed(started);
      if (started?.status !== 'succeeded' && !jobID) throw new Error('智能整合未能启动，请稍后重试');
      if (started?.status === 'succeeded') {
        await applyConsolidationResult(memoryCwd, started.result || {});
      } else {
        setConsolidationJob({ cwd: memoryCwd, jobId: jobID });
        showNotice('info', '智能整合已在后台进行，完成后会自动更新');
      }
    } catch (err) {
      showNotice('error', `智能整合失败：${errorMessage(err)}`);
    } finally {
      setMergingAll(false);
    }
  }, [applyConsolidationResult, consolidationJob, memoryCwd, mergingAll, resolveLaunchPreferences, showNotice, similarGroups.length]);

  const ignoreGroup = useCallback(async (group) => {
    const key = memoryPairKey(group);
    if (ignoringKey) return;
    if (!memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    setIgnoringKey(key);
    try {
      await ignoreMemorySimilarity({
        cwd: memoryCwd,
        targetA: group.targetA,
        pathA: group.pathA,
        targetB: group.targetB,
        pathB: group.pathB,
      });
      showNotice('info', `已忽略「${group.nameA || group.pathA}」与「${group.nameB || group.pathB}」`);
      await refreshMemory();
    } catch (err) {
      showNotice('error', `忽略失败：${errorMessage(err)}`);
    } finally {
      setIgnoringKey('');
    }
  }, [ignoringKey, memoryCwd, refreshMemory, showNotice]);

  return (
    <section className="memory-page">
      <PageHeader
        icon={MemoryStick}
        title="记忆中心"
        actions={(
          <>
            <label>
              <Search size={17} />
              <input
                aria-label="搜索记忆"
                placeholder="搜索记忆标题 / 内容"
                value={searchText}
                onChange={(event) => setSearchText(event.target.value)}
              />
            </label>
            <div className="memory-create">
              <button type="button" className="light" aria-label="+ 新建 ▾" onClick={() => setCreateMenuOpen((open) => !open)} disabled={isProjectPending}>
                <Plus size={15} /> 新建 ▾
              </button>
              {createMenuOpen ? (
                <div className="memory-create-menu">
                  <button type="button" onClick={() => openCreate('feedback')}>新建偏好</button>
                  <button type="button" onClick={() => openCreate('project')}>新建项目</button>
                </div>
              ) : null}
            </div>
          </>
        )}
      />

      <div className="memory-stats">
        <Panel title="总览">
          <strong className="big">{categoryCounts.all}</strong>
          <p><span className="orange-dot" />{categoryCounts.preference} 偏好 <span />{categoryCounts.project} 项目</p>
        </Panel>
        {health ? (
          <Panel title="健康度">
            <p>偏好 <meter value={health.preferenceCount} max={health.maxPerCategory} /> {health.preferenceCount} / {health.maxPerCategory}</p>
            <div className={`memory-health-track ${memoryHealthClass(prefPercent)}`}><span style={{ width: `${prefPercent}%` }} /></div>
            <p>项目 <meter value={health.projectCount} max={health.maxPerCategory} /> {health.projectCount} / {health.maxPerCategory}</p>
            <div className={`memory-health-track ${memoryHealthClass(projPercent)}`}><span style={{ width: `${projPercent}%` }} /></div>
            <p><span className="green-dot" /> 综合良好</p>
          </Panel>
        ) : null}
        <Panel title="自动沉淀">
          <p><span className={autoDreamEnabled ? 'green-dot' : 'orange-dot'} /> {autoDreamEnabled ? '已开启' : '已关闭'}</p>
          <small>对话结束后自动整理重要内容</small>
          <button type="button" onClick={() => { void toggleAutoDream(); }} disabled={autoToggling || isProjectPending}>
            {autoDreamEnabled ? '关闭' : '开启'}
          </button>
          {autoDreamPendingRestart ? <small className="memory-pending">已保存切换，重启 agent-terminal 后生效</small> : null}
        </Panel>
      </div>

      {similarGroups.length ? (
        <div className="similar-alert">
          <AlertTriangle size={20} />
          <span>{similarGroups.length} 组条目内容相似</span>
          <button type="button" onClick={() => { void mergeAllGroups(); }} disabled={mergingAll || Boolean(consolidationJob) || Boolean(mergingKey) || Boolean(ignoringKey)}>
            {mergingAll ? '启动中...' : (consolidationJob ? '后台整合中' : '一键整合全部')}
          </button>
          <button type="button" onClick={() => setSimilarExpanded((expanded) => !expanded)}>{similarExpanded ? '收起' : '展开'}</button>
        </div>
      ) : null}
      {similarExpanded && similarGroups.length ? (
        <div className="memory-similar-list">
          {similarGroups.map((group) => {
            const key = memoryPairKey(group);
            return (
              <div className="memory-similar-item" key={key}>
                <span>「{group.nameA || group.pathA}」与「{group.nameB || group.pathB}」</span>
                <strong>{formatMemoryScore(group.score)}</strong>
                <button type="button" onClick={() => setMergeTarget(group)} disabled={Boolean(mergingKey) || mergingAll || Boolean(consolidationJob) || Boolean(ignoringKey)}>整合</button>
                <button type="button" className="ghost" onClick={() => { void ignoreGroup(group); }} disabled={Boolean(ignoringKey) || mergingAll || Boolean(consolidationJob) || Boolean(mergingKey)}>
                  {ignoringKey === key ? '...' : '忽略'}
                </button>
              </div>
            );
          })}
        </div>
      ) : null}

      {notice.message ? <div className={`memory-notice is-${notice.level}`}>{notice.message}</div> : null}
      {isProjectPending ? <div className="memory-notice is-info">正在连接本地项目...</div> : null}
      {!isProjectPending && loading ? <div className="memory-notice is-info">正在加载记忆中心...</div> : null}
      {syncError ? (
        <div className="memory-notice is-error" role="alert">
          <span>{syncError}</span>
          <button type="button" onClick={() => { void refreshMemory(); }}>重试同步</button>
        </div>
      ) : null}
      {error ? (
        <div className="memory-notice is-error" role="alert">
          <span>{error}</span>
          <button type="button" onClick={() => { void refreshMemory(); }}>重试同步</button>
        </div>
      ) : null}

      <div className="memory-tabs" role="tablist" aria-label="记忆分类">
        {MEMORY_CATEGORIES.map((item) => (
          <button
            key={item.key}
            type="button"
            role="tab"
            className={activeCategory === item.key ? 'active' : ''}
            onClick={() => setActiveCategory(item.key)}
          >
            {item.label} {categoryCounts[item.key] || 0}
          </button>
        ))}
      </div>

      {!error && !isProjectPending && !loading && visibleEntries.length === 0 ? (
        <div className="empty-state memory-empty">
          <span><MemoryStick size={24} /></span>
          <h2>{searchText ? '没有匹配的条目' : '暂无记忆'}</h2>
          <p>{searchText ? '清空搜索或切换分类后再试。' : '点击右上角“新建”按钮开始添加记忆。'}</p>
        </div>
      ) : null}

      {!error && !isProjectPending && visibleEntries.length ? (
        <div className="memory-cards">
          {visibleEntries.map((entry) => (
            <MemoryCard
              key={entry.id}
              entry={entry}
              busy={busyKey === `${entry.target}:${entry.path}`}
              deleting={deletingKey === `${entry.target}:${entry.path}`}
              onEdit={openEdit}
              onDelete={setDeleteTarget}
            />
          ))}
        </div>
      ) : null}

      {editor.open ? (
        <MemoryEditorModal
          editor={editor}
          saving={saving}
          onClose={closeEditor}
          onChange={updateEditorForm}
          onSave={saveEditor}
          onDelete={() => {
            setDeleteTarget({
              target: editor.form.target,
              path: editor.form.existingPath,
              name: editor.form.name,
              title: editor.form.title,
            });
            setEditor((current) => ({ ...current, open: false }));
          }}
        />
      ) : null}
      {deleteTarget ? (
        <MemoryDeleteModal
          entry={deleteTarget}
          deleting={deletingKey === `${deleteTarget.target}:${deleteTarget.path}`}
          onClose={() => setDeleteTarget(null)}
          onConfirm={confirmDelete}
        />
      ) : null}
      {mergeTarget ? (
        <MemoryMergeModal
          group={mergeTarget}
          merging={mergingKey === memoryPairKey(mergeTarget)}
          onClose={() => setMergeTarget(null)}
          onConfirm={confirmMerge}
        />
      ) : null}
    </section>
  );
}

function MemoryCard({ entry, busy, deleting, onEdit, onDelete }) {
  return (
    <article className={`memory-card ${entry.category === 'project' ? 'type-project' : 'type-preference'}`}>
      <header>
        <h3>{memoryEntryTitle(entry)}</h3>
        <span>{entry.tag}</span>
        {entry.source === 'dream' ? <em>梦境</em> : null}
      </header>
      {entry.description ? <p>{entry.description}</p> : null}
      <code>{entry.preview || '暂无预览'}</code>
      <footer>
        <time>{sharedFileTimestamp(entry.updatedAt)}</time>
        <button type="button" onClick={() => { void onEdit(entry); }} disabled={busy}>{busy ? '加载中...' : '编辑'}</button>
        <button type="button" className="danger" onClick={() => onDelete(entry)} disabled={deleting}>{deleting ? '删除中...' : '删除'}</button>
      </footer>
    </article>
  );
}

function MemoryEditorModal({ editor, saving, onClose, onChange, onSave, onDelete }) {
  const form = editor.form;
  const identityLocked = editor.mode === 'edit' && Boolean(form.existingPath);
  return (
    <FocusTrapDialog
      ariaLabel={editor.mode === 'edit' ? '编辑记忆' : '新建记忆'}
      className="modal-box memory-editor-modal"
      closeDisabled={saving}
      onClose={onClose}
    >
        <header>
          <div>
            <h2>{editor.mode === 'edit' ? '编辑记忆' : '新建记忆'}</h2>
            <p>{form.type === 'project' ? '项目记忆' : '偏好记忆'}</p>
          </div>
        </header>
        <div className="memory-form-grid">
          <label>分类
            <select
              value={form.type}
              onChange={(event) => onChange({
                type: event.target.value,
                target: memoryTargetForType(event.target.value),
                content: memoryTemplateForType(event.target.value),
              })}
              disabled={identityLocked}
            >
              {MEMORY_EDITOR_TYPES.map((type) => <option key={type.key} value={type.key}>{type.label}</option>)}
            </select>
          </label>
          <label>描述
            <input value={form.description} onChange={(event) => onChange({ description: event.target.value })} placeholder="一句话描述为什么值得长期保留" />
          </label>
          <label>卡片标题
            <input value={form.title} onChange={(event) => onChange({ title: event.target.value })} placeholder="卡片上显示的短标题" />
          </label>
        </div>
        <label className="memory-content-label">内容
          <textarea rows={12} value={form.content} onChange={(event) => onChange({ content: event.target.value })} />
        </label>
        <div className="memory-editor-actions">
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          {form.existingPath ? <button type="button" className="danger" onClick={onDelete} disabled={saving}>删除</button> : null}
          <button type="button" onClick={() => onChange({ content: memoryTemplateForType(form.type) })} disabled={saving}>套用当前类型模板</button>
          <button type="button" className="light" onClick={() => { void onSave(); }} disabled={saving || !textValue(form.description) || !textValue(form.content)}>
            {saving ? '保存中...' : '保存'}
          </button>
        </div>
    </FocusTrapDialog>
  );
}

function MemoryDeleteModal({ entry, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除记忆" closeDisabled={deleting} onClose={onClose}>
        <header>
          <h2>删除记忆</h2>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button>
        </header>
        <p>删除后无法恢复。如果后续可能重用，建议先编辑备份内容。</p>
        <p className="path">{memoryEntryTitle(entry)}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function MemoryMergeModal({ group, merging, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="整合相似记忆" closeDisabled={merging} onClose={onClose}>
        <header>
          <div>
            <h2>整合相似记忆</h2>
            <p>相似度 {formatMemoryScore(group.score)}</p>
          </div>
          <button type="button" className="ghost" onClick={onClose} disabled={merging}>关闭</button>
        </header>
        <p>合并到：{group.nameA || '保留项'}</p>
        <p>移除：{group.nameB || '重复项'}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={merging}>取消</button>
          <button type="button" className="light" onClick={() => { void onConfirm(); }} disabled={merging}>{merging ? '整合中...' : '确认整合'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function SettingsPage({ projectPath }) {
  const store = useClientStore();
  const cwd = projectPath || store.activeProject || store.cwd;

  // Build Info & Original Form states
  const [buildInfo, setBuildInfo] = useState(null);
  const [form, setForm] = useState({
    stallThresholdSec: String(SETTINGS_DEFAULTS.stallThresholdSec),
    contextWarn: String(SETTINGS_DEFAULTS.contextThresholds[0]),
    contextDanger: String(SETTINGS_DEFAULTS.contextThresholds[1]),
    contextCritical: String(SETTINGS_DEFAULTS.contextThresholds[2]),
    activeProvider: SETTINGS_DEFAULTS.activeProvider,
    codexHome: SETTINGS_DEFAULTS.codexHome,
    codexInstanceKey: SETTINGS_DEFAULTS.codexInstanceKey,
    codexModelProvider: SETTINGS_DEFAULTS.codexModelProvider,
    providerModel: SETTINGS_DEFAULTS.providerModel,
    providerEffort: SETTINGS_DEFAULTS.providerEffort,
    sandboxPolicy: SETTINGS_DEFAULTS.sandboxPolicy,
    writableRoots: SETTINGS_DEFAULTS.writableRoots,
    networkAccess: SETTINGS_DEFAULTS.networkAccess,
  });
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');

  // Provider Settings State (for summary & approval)
  const [summaryMode, setSummaryMode] = useState('detailed');
  const [approvalMode, setApprovalMode] = useState('on-request');
  const [providerNotice, setProviderNotice] = useState({ level: 'info', message: '' });
  const [providerSaving, setProviderSaving] = useState(false);

  // Prompt Settings State
  const [lspPromptHint, setLspPromptHint] = useState('');
  const [lspPromptEffectiveHint, setLspPromptEffectiveHint] = useState('');
  const [lspPromptDefaultHint, setLspPromptDefaultHint] = useState('');
  const [lspPromptUsingDefault, setLspPromptUsingDefault] = useState(true);
  const [lspPromptLoading, setLspPromptLoading] = useState(false);
  const [lspPromptSaving, setLspPromptSaving] = useState(false);
  const [lspPromptNotice, setLspPromptNotice] = useState({ level: 'info', message: '' });
  const [showInjectedPromptInChat, setShowInjectedPromptInChat] = useState(false);
  const [showInjectedPromptSaving, setShowInjectedPromptSaving] = useState(false);
  const [currentScopeCwd, setCurrentScopeCwd] = useState('');

  // Model Built-in capabilities State
  const [builtinTools, setBuiltinTools] = useState([]);
  const [builtinToolsLoading, setBuiltinToolsLoading] = useState(false);
  const [builtinSavingIds, setBuiltinSavingIds] = useState({});
  const [builtinExpandedGroups, setBuiltinExpandedGroups] = useState({});
  const [builtinToolsNotice, setBuiltinToolsNotice] = useState({ level: 'info', message: '' });

  // Boolean helper
  const parseBoolPreference = useCallback((value) => {
    if (typeof value === 'boolean') return value;
    if (typeof value === 'number') return value !== 0;
    if (typeof value === 'string') {
      const normalized = value.trim().toLowerCase();
      if (['1', 'true', 'yes', 'on'].includes(normalized)) return true;
      if (['0', 'false', 'no', 'off'].includes(normalized)) return false;
    }
    return false;
  }, []);

  const refreshBuildInfo = useCallback(async () => {
    setError('');
    try {
      const info = await getBuildInfo();
      if (!info || typeof info !== 'object') throw new Error('build info response must be an object');
      setBuildInfo(info);
      setStatus('构建信息已刷新');
    } catch (err) {
      setError(err.message || String(err));
    }
  }, []);

  const loadPreferences = useCallback(async () => {
    setError('');
    if (!cwd) return;
    try {
      const [stallValue, contextValue, activeProviderValue] = await Promise.all([
        getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
        getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
        getPreference({ cwd, key: SETTINGS_KEYS.activeProvider }),
      ]);
      const activeProvider = normalizeProviderName(activeProviderValue);
      const providerPrefix = `settings.provider.${activeProvider}`;
      const [
        codexHome,
        codexInstanceKey,
        codexModelProvider,
        providerModel,
        providerEffort,
        sandbox,
        summaryValue,
        approvalValue,
      ] = await Promise.all([
        getPreference({ cwd, key: providerSettingKey('codex', 'codexHome') }),
        getPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey') }),
        getPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider') }),
        getPreference({ cwd, key: `${providerPrefix}.model` }),
        getPreference({ cwd, key: `${providerPrefix}.effort` }),
        getPreference({ cwd, key: `${providerPrefix}.sandbox` }),
        getPreference({ cwd, key: 'settings.provider.codex.summary' }),
        getPreference({ cwd, key: 'settings.provider.codex.approvalPolicy' }),
      ]);
      const contextThresholds = normalizeContextThresholds(contextValue);
      setForm({
        stallThresholdSec: String(numberSetting(stallValue, SETTINGS_DEFAULTS.stallThresholdSec)),
        contextWarn: String(contextThresholds[0]),
        contextDanger: String(contextThresholds[1]),
        contextCritical: String(contextThresholds[2]),
        activeProvider,
        codexHome: stringSetting(codexHome, SETTINGS_DEFAULTS.codexHome),
        codexInstanceKey: stringSetting(codexInstanceKey, SETTINGS_DEFAULTS.codexInstanceKey),
        codexModelProvider: stringSetting(codexModelProvider, SETTINGS_DEFAULTS.codexModelProvider),
        providerModel: stringSetting(providerModel, SETTINGS_DEFAULTS.providerModel),
        providerEffort: stringSetting(providerEffort, SETTINGS_DEFAULTS.providerEffort),
        sandboxPolicy: sandboxPolicyFromPreference(sandbox),
        writableRoots: writableRootsFromPreference(sandbox),
        networkAccess: Boolean(sandbox && typeof sandbox === 'object' && sandbox.networkAccess),
      });
      setSummaryMode(summaryValue || 'detailed');
      setApprovalMode(approvalValue || 'on-request');
      setProviderNotice({ level: 'info', message: '' });
    } catch (err) {
      setError(err.message || String(err));
    }
  }, [cwd]);

  const updateForm = (key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => ({ ...current, [key]: value }));
  };

  const saveRuntimeSettings = async () => {
    setError('');
    setStatus('');
    try {
      const { stallThresholdSec, contextThresholds } = validateRuntimeThresholds(form);
      await setPreference({ cwd, key: SETTINGS_KEYS.stallThreshold, value: stallThresholdSec });
      await setPreference({ cwd, key: SETTINGS_KEYS.contextThresholds, value: contextThresholds });
      setStatus('已保存超时与上下文使用率设置');
    } catch (err) {
      setError(err.message || String(err));
    }
  };

  const saveProviderSettings = async () => {
    setError('');
    setStatus('');
    try {
      const provider = normalizeProviderName(form.activeProvider);
      await setPreference({ cwd, key: SETTINGS_KEYS.activeProvider, value: provider });
      await setPreference({ cwd, key: providerSettingKey(provider, 'model'), value: form.providerModel.trim() });
      await setPreference({ cwd, key: providerSettingKey(provider, 'effort'), value: form.providerEffort.trim() });
      await setPreference({
        cwd,
        key: providerSettingKey(provider, 'sandbox'),
        value: sandboxPreferenceValue(form.sandboxPolicy, form.writableRoots, form.networkAccess),
      });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexHome'), value: form.codexHome.trim() });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey'), value: form.codexInstanceKey.trim() });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider'), value: form.codexModelProvider.trim() });
      setStatus('Provider 设置已保存');
    } catch (err) {
      setError(err.message || String(err));
    }
  };

  // Provider preferences loaders/savers (summary & approvalPolicy)
  const loadProviderPreferences = useCallback(async () => {
    if (!cwd) return;
    try {
      const summaryValue = await getPreference({ cwd, key: 'settings.provider.codex.summary' });
      const approvalValue = await getPreference({ cwd, key: 'settings.provider.codex.approvalPolicy' });
      setSummaryMode(summaryValue || 'detailed');
      setApprovalMode(approvalValue || 'on-request');
      setProviderNotice({ level: 'info', message: '' });
    } catch (error) {
      setProviderNotice({ level: 'error', message: `加载 Preferences 失败: ${error.message}` });
    }
  }, [cwd]);

  const saveProviderPreferences = useCallback(async () => {
    if (!cwd || providerSaving) return;
    setProviderSaving(true);
    try {
      await setPreference({ cwd, key: 'settings.provider.codex.summary', value: summaryMode });
      await setPreference({ cwd, key: 'settings.provider.codex.approvalPolicy', value: approvalMode });
      setProviderNotice({ level: 'info', message: `已保存：${summaryMode} / ${approvalMode}` });
    } catch (error) {
      setProviderNotice({ level: 'error', message: `保存失败: ${error.message}` });
    } finally {
      setProviderSaving(false);
    }
  }, [cwd, summaryMode, approvalMode, providerSaving]);

  // Prompt hints loaders/savers
  const loadLspPromptHint = useCallback(async () => {
    if (!cwd) return;
    setLspPromptLoading(true);
    try {
      const res = await callBackend('config/lspPromptHint/read', { cwd });
      const hint = (res?.hint || '').toString();
      const defaultHint = (res?.defaultHint || '').toString();
      const overrideHint = (res?.overrideHint || '').toString();
      const usingDefault = Boolean(res?.usingDefault);
      setLspPromptHint(overrideHint);
      setLspPromptEffectiveHint(hint);
      setLspPromptDefaultHint(defaultHint);
      setLspPromptUsingDefault(usingDefault || overrideHint.trim() === '');
      setLspPromptNotice({ level: 'info', message: '' });
    } catch (error) {
      setLspPromptNotice({ level: 'error', message: `加载失败：${error?.message || error}` });
    } finally {
      setLspPromptLoading(false);
    }
  }, [cwd]);

  const loadCurrentScopeCwd = useCallback(async () => {
    try {
      const cfg = await callBackend('config/read', {});
      setCurrentScopeCwd((cfg?.cwd || '').toString().trim());
    } catch {
      setCurrentScopeCwd('');
    }
  }, []);

  const loadInjectedPromptVisibility = useCallback(async () => {
    if (!cwd) return;
    try {
      const value = await getPreference({ cwd, key: 'settings.showInjectedPromptInChat' });
      setShowInjectedPromptInChat(parseBoolPreference(value));
    } catch (error) {
      setLspPromptNotice({ level: 'error', message: `加载聊天注入显示开关失败：${error?.message || error}` });
    }
  }, [cwd, parseBoolPreference]);

  const saveLspPromptHint = useCallback(async () => {
    if (!cwd || lspPromptSaving) return;
    setLspPromptSaving(true);
    try {
      const res = await callBackend('config/lspPromptHint/write', { cwd, hint: lspPromptHint });
      setLspPromptEffectiveHint((res?.hint || '').toString());
      setLspPromptDefaultHint((res?.defaultHint || lspPromptDefaultHint || '').toString());
      setLspPromptHint((res?.overrideHint || '').toString());
      const usingDefault = Boolean(res?.usingDefault);
      setLspPromptUsingDefault(usingDefault);
      if (usingDefault) {
        setLspPromptNotice({ level: 'info', message: '已恢复默认提示词' });
      } else {
        setLspPromptNotice({ level: 'info', message: '提示词已保存' });
      }
    } catch (error) {
      setLspPromptNotice({ level: 'error', message: `保存失败：${error?.message || error}` });
    } finally {
      setLspPromptSaving(false);
    }
  }, [cwd, lspPromptHint, lspPromptDefaultHint, lspPromptSaving]);

  const resetLspPromptHint = useCallback(async () => {
    if (!cwd || lspPromptSaving) return;
    setLspPromptSaving(true);
    try {
      const res = await callBackend('config/lspPromptHint/write', { cwd, hint: '' });
      setLspPromptEffectiveHint((res?.hint || '').toString());
      setLspPromptDefaultHint((res?.defaultHint || lspPromptDefaultHint || '').toString());
      setLspPromptHint('');
      setLspPromptUsingDefault(true);
      setLspPromptNotice({ level: 'info', message: '已恢复默认提示词' });
    } catch (error) {
      setLspPromptNotice({ level: 'error', message: `恢复失败：${error?.message || error}` });
    } finally {
      setLspPromptSaving(false);
    }
  }, [cwd, lspPromptDefaultHint, lspPromptSaving]);

  const handleInjectedPromptVisibilityChange = useCallback(async (event) => {
    if (!cwd || showInjectedPromptSaving) return;
    const next = event.target.checked;
    setShowInjectedPromptInChat(next);
    setShowInjectedPromptSaving(true);
    try {
      await setPreference({ cwd, key: 'settings.showInjectedPromptInChat', value: next });
      setLspPromptNotice({
        level: 'info',
        message: next ? '聊天区已改为显示自动注入内容' : '聊天区已改为隐藏自动注入内容',
      });
    } catch (error) {
      setLspPromptNotice({ level: 'error', message: `保存聊天注入显示开关失败：${error?.message || error}` });
      await loadInjectedPromptVisibility();
    } finally {
      setShowInjectedPromptSaving(false);
    }
  }, [cwd, showInjectedPromptSaving, loadInjectedPromptVisibility]);

  const copyEffectivePromptHint = useCallback(async () => {
    const text = (lspPromptEffectiveHint || lspPromptDefaultHint || '').trim() || '暂无可用提示词';
    if (!text || text === '暂无可用提示词') {
      setLspPromptNotice({ level: 'error', message: '暂无可复制内容' });
      return;
    }
    try {
      await navigator.clipboard.writeText(text);
      setLspPromptNotice({ level: 'info', message: '已复制生效提示词' });
    } catch (error) {
      setLspPromptNotice({ level: 'error', message: `复制失败：${error?.message || error}` });
    }
  }, [lspPromptEffectiveHint, lspPromptDefaultHint]);

  // Model Built-in capabilities loaders/savers
  const applyToolsPayload = useCallback((payload) => {
    const list = Array.isArray(payload?.tools) ? payload.tools : [];
    setBuiltinTools(list.map((item) => ({
      id: (item.id || '').toString(),
      label: (item.label || item.id || '').toString(),
      description: (item.description || '').toString(),
      enabled: Boolean(item.enabled),
      provider: (item.provider || 'claude').toString(),
      replacedBy: item.replacedBy ? (item.replacedBy || '').toString() : undefined,
      filterMode: (item.filterMode || '').toString() || undefined,
      enforcement: (item.enforcement || '').toString() || undefined,
    })));
  }, []);

  const loadBuiltinTools = useCallback(async () => {
    if (!cwd) return;
    setBuiltinToolsLoading(true);
    try {
      const res = await callBackend('config/builtinTools/read', { cwd });
      applyToolsPayload(res);
      setBuiltinToolsNotice({ level: 'info', message: '' });
    } catch (error) {
      setBuiltinToolsNotice({ level: 'error', message: `加载失败：${error?.message || error}` });
    } finally {
      setBuiltinToolsLoading(false);
    }
  }, [cwd, applyToolsPayload]);

  const toggleBuiltinTool = useCallback(async (tool) => {
    if (!cwd || tool.replacedBy) return;
    const id = tool.id;
    if (!id) return;
    if (builtinSavingIds[id]) return;
    setBuiltinSavingIds((prev) => ({ ...prev, [id]: true }));
    const nextEnabled = !tool.enabled;

    setBuiltinTools((prev) =>
      prev.map((item) => (item.id === id ? { ...item, enabled: nextEnabled } : item))
    );

    try {
      const res = await callBackend('config/builtinTools/write', { cwd, id, enabled: nextEnabled });
      applyToolsPayload(res);
      setBuiltinToolsNotice({ level: 'info', message: `${tool.label || id} 已${nextEnabled ? '启用' : '禁用'}` });
    } catch (error) {
      setBuiltinTools((prev) =>
        prev.map((item) => (item.id === id ? { ...item, enabled: !nextEnabled } : item))
      );
      setBuiltinToolsNotice({ level: 'error', message: `保存失败：${error?.message || error}` });
    } finally {
      setBuiltinSavingIds((prev) => ({ ...prev, [id]: false }));
    }
  }, [cwd, builtinSavingIds, applyToolsPayload]);

  // Collapsible accordion group controls
  const toggleGroupExpanded = useCallback((key) => {
    setBuiltinExpandedGroups((prev) => ({
      ...prev,
      [key]: !prev[key],
    }));
  }, []);

  // Sync state
  useEffect(() => {
    refreshBuildInfo().catch(() => { });
  }, [refreshBuildInfo]);

  useEffect(() => {
    loadPreferences().catch(() => { });
    loadLspPromptHint();
    loadCurrentScopeCwd();
    loadInjectedPromptVisibility();
    loadBuiltinTools();
  }, [cwd, loadPreferences, loadLspPromptHint, loadCurrentScopeCwd, loadInjectedPromptVisibility, loadBuiltinTools]);

  // Derived Prompt variables
  const lspPromptDisplayHint = (lspPromptEffectiveHint || lspPromptDefaultHint || '').trim() || '暂无可用提示词';
  const lspPromptLineCount = lspPromptDisplayHint === '暂无可用提示词' ? 0 : lspPromptDisplayHint.split('\n').length;
  const lspPromptCharCount = lspPromptDisplayHint === '暂无可用提示词' ? 0 : lspPromptDisplayHint.length;

  // Derived Built-in capabilities groups
  const enforcementBucket = useCallback((tool) => {
    const enforcement = (tool.enforcement || '').toString().trim();
    if (enforcement) return enforcement;
    if (tool.filterMode === 'hard') return 'native-hard';
    return 'soft-audit';
  }, []);

  const groups = useMemo(() => {
    const disabled = builtinTools.filter((t) => !t.enabled || t.replacedBy);
    const nativeHard = disabled.filter((t) => enforcementBucket(t) === 'native-hard');
    const effectHard = disabled.filter((t) => enforcementBucket(t) === 'effect-hard');
    const softAudit = disabled.filter((t) => enforcementBucket(t) === 'soft-audit');
    const notFiltered = builtinTools.filter((t) => t.enabled && !t.replacedBy);

    const result = [];
    const pushGroup = (key, label, items, note) => {
      if (items.length === 0) return;
      result.push({
        key,
        label: `${label}（${items.length}）`,
        tools: items,
        note,
        disabledCount: items.length,
        canToggle: true,
      });
    };

    pushGroup('native-hard', '启动前已关闭', nativeHard, '模型启动前就看不到这些能力。');
    pushGroup('effect-hard', '已限制为只读', effectHard, 'Codex 暂不支持单独关闭这类能力，已限制为只读，避免它直接改文件或执行命令。');
    pushGroup('soft-audit', '仅提醒使用项目工具', softAudit, 'Codex 暂不支持可靠关闭这类能力，只能提示模型优先使用本项目工具；这不是强制拦截。');

    if (notFiltered.length > 0) {
      result.push({
        key: 'unfiltered',
        label: `保持可用（${notFiltered.length}）`,
        tools: notFiltered,
        disabledCount: 0,
        canToggle: true,
      });
    }
    return result;
  }, [builtinTools, enforcementBucket]);

  const filteredCount = useMemo(() => builtinTools.filter((t) => t.replacedBy || !t.enabled).length, [builtinTools]);
  const totalToolCount = builtinTools.length;

  const toolStatusLabel = useCallback((tool) => {
    if (tool.replacedBy) return '已由项目工具接管';
    if (tool.enabled) return '保持可用';
    const enforcement = enforcementBucket(tool);
    if (enforcement === 'native-hard') return '启动前已关闭';
    if (enforcement === 'effect-hard') return '已限制为只读';
    if (enforcement === 'soft-audit') return '仅提醒使用项目工具';
    return '已管控';
  }, [enforcementBucket]);

  const toolMetaText = useCallback((tool) => {
    const parts = [];
    const description = (tool.description || '').trim();
    if (description) parts.push(description);
    const provider = PROVIDER_LABELS[tool.provider] || tool.provider || '';
    if (provider) parts.push(provider);
    parts.push(toolStatusLabel(tool));
    return parts.join(' · ');
  }, [toolStatusLabel]);

  const groupSummary = useCallback((group) => {
    if (group.key === 'unfiltered') return `可用 ${group.tools.length} 项`;
    return `已管控 ${group.disabledCount} 项`;
  }, []);

  const formatLogTime = (value) => {
    if (!value) return '--:--:--';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '--:--:--';
    return date.toLocaleTimeString('zh-CN', { hour12: false });
  };

  const logList = store.logEntries ? store.logEntries.slice(0, 14) : [];

  return (
    <section className="settings-page" data-testid="settings-page">
      <PageHeader icon={Settings} title="设置" actions={<button className="btn btn-secondary" type="button" data-testid="settings-refresh-build-button" onClick={() => void refreshBuildInfo()}>刷新构建信息</button>} />
      {status ? <p className="settings-page-notice settings-status" role="status">{status}</p> : null}
      {error ? <p className="settings-page-notice danger-text" role="alert">{error}</p> : null}

      <div className="panel-body" data-testid="settings-panel-body">
        <Panel title="ABOUT">
          <dl>
            <dt>版本</dt><dd>Agent Orchestrator {buildInfo?.version || 'unknown'}</dd>
            <dt>运行时</dt><dd>{buildInfo?.runtime || 'unknown'}</dd>
            <dt>构建时间</dt><dd>{buildInfo?.buildTime || 'unknown'}</dd>
            <dt>Commit</dt><dd>{buildInfo?.commit || 'unknown'}</dd>
            <dt>当前项目</dt><dd>{cwd}</dd>
          </dl>
        </Panel>

        <Panel title="TURN TRACKER">
          <div className="form-line">
            <label>统一超时阈值<input aria-label="统一超时阈值" data-testid="settings-stall-threshold-input" type="number" min="30" value={form.stallThresholdSec} onChange={updateForm('stallThresholdSec')} /> 秒</label>
            <button className="btn btn-primary" type="button" data-testid="settings-stall-threshold-save-button" onClick={() => void saveRuntimeSettings()}>保存超时阈值</button>
          </div>
        </Panel>

        <Panel title="CONTEXT USAGE ALERT" data-testid="settings-ctx-thresholds-card">
          <div className="form-line">
            <label>Warn 阈值<input aria-label="Warn 阈值" type="number" min="1" max="100" value={form.contextWarn} onChange={updateForm('contextWarn')} /></label>
            <label>Danger 阈值<input aria-label="Danger 阈值" type="number" min="1" max="100" value={form.contextDanger} onChange={updateForm('contextDanger')} /></label>
            <label>Critical 阈值<input aria-label="Critical 阈值" type="number" min="1" max="100" value={form.contextCritical} onChange={updateForm('contextCritical')} /></label>
            <button className="btn btn-primary" type="button" data-testid="settings-ctx-thresholds-save-button" onClick={() => void saveRuntimeSettings()}>保存运行阈值</button>
          </div>
        </Panel>

        <Panel title="PROVIDER">
          <div className="form-grid">
            <label>Active Provider<select value={form.activeProvider} onChange={updateForm('activeProvider')}><option value="codex">Codex</option><option value="claude">Claude</option></select></label>
            <label>Provider Model<input value={form.providerModel} onChange={updateForm('providerModel')} /></label>
            <label>Provider Effort<input value={form.providerEffort} onChange={updateForm('providerEffort')} /></label>
            <label>Codex Home<input aria-label="Codex Home" value={form.codexHome} onChange={updateForm('codexHome')} /></label>
            <label>Instance Key<input aria-label="Instance Key" value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label>
            <label>Model Provider<input aria-label="Model Provider" value={form.codexModelProvider} onChange={updateForm('codexModelProvider')} /></label>
            <label>Sandbox Policy<select aria-label="Sandbox Policy" value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
            <label className="checkbox-line"><input type="checkbox" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label>
            <label className="wide">Writable Roots<textarea value={form.writableRoots} onChange={updateForm('writableRoots')} placeholder="每行一个绝对路径" /></label>
          </div>
          <div className="settings-actions"><button className="btn btn-primary" type="button" onClick={() => void saveProviderSettings()}>保存 Provider 设置</button></div>
        </Panel>

        {/* Inference Summary & Approval Policy Card */}
        <div className="section-header">PROPERTIES</div>
        <div className="data-card-vue" data-testid="settings-provider-sandbox-card">
          <div className="settings-stall-row settings-provider-control-row">
            <label className="settings-stall-label" htmlFor="provider-summary-mode-select">推理摘要 (Summary)</label>
            <select
              id="provider-summary-mode-select"
              className="settings-stall-input settings-provider-select"
              data-testid="provider-summary-mode-select"
              value={summaryMode}
              onChange={(e) => setSummaryMode(e.target.value)}
            >
              <option value="detailed">detailed（详细摘要，推荐）</option>
              <option value="auto">auto（自动）</option>
              <option value="concise">concise（简洁）</option>
              <option value="none">none（关闭）</option>
            </select>
          </div>
          <div className="settings-stall-row settings-provider-control-row">
            <label className="settings-stall-label" htmlFor="provider-approval-mode-select">审批策略 (ApprovalPolicy)</label>
            <select
              id="provider-approval-mode-select"
              className="settings-stall-input settings-provider-select"
              data-testid="provider-approval-mode-select"
              value={approvalMode}
              onChange={(e) => setApprovalMode(e.target.value)}
            >
              <option value="on-request">on-request（按需，默认）</option>
              <option value="untrusted">untrusted（始终询问）</option>
              <option value="on-failure">on-failure（失败后询问）</option>
              <option value="never">never（全部放行）</option>
            </select>
          </div>
          {providerNotice.message && (
            <div className={`settings-prompt-notice settings-provider-notice is-${providerNotice.level}`} role={providerNotice.level === 'error' ? 'alert' : 'status'}>
              {providerNotice.message}
            </div>
          )}
          <div className="settings-action-row settings-action-inline settings-provider-actions">
            <button className="btn btn-secondary btn-toolbar-sm" onClick={loadProviderPreferences} disabled={providerSaving}>刷新</button>
            <button className="btn btn-primary btn-toolbar-sm" data-testid="provider-sandbox-save-button" onClick={saveProviderPreferences} disabled={providerSaving}>
              {providerSaving ? '保存中...' : '保存'}
            </button>
          </div>
        </div>

        {/* PROMPT Section */}
        <div className="section-header">PROMPT</div>
        <div className="data-card-vue settings-prompt-card" data-testid="settings-lsp-prompt-card">
          <div className="data-row-vue">
            <strong>自动注入提示词 (LSP / Playwright / json-render)</strong>
            <span>{lspPromptLoading ? '加载中...' : (lspPromptUsingDefault ? '默认注入' : '自定义覆盖')}</span>
          </div>
          <div className="settings-prompt-desc">下方“生效内容”是后端每轮实际注入文本：“覆盖编辑”用于调试，留空保存可恢复默认。</div>
          <div className="settings-prompt-meta" data-testid="settings-lsp-effective-cwd">
            当前作用 CWD: {currentScopeCwd || '未知'}
          </div>
          <label className="settings-prompt-toggle" data-testid="settings-show-injected-toggle">
            <div className="settings-prompt-toggle-copy">
              <span className="settings-prompt-toggle-title">聊天区显示自动注入内容（调试）</span>
              <span className="settings-prompt-toggle-desc">开启后将保留首发消息里的“已注入 ...”段。</span>
            </div>
            <input
              type="checkbox"
              className="settings-prompt-toggle-input"
              data-testid="settings-show-injected-toggle-input"
              checked={showInjectedPromptInChat}
              onChange={handleInjectedPromptVisibilityChange}
              disabled={lspPromptLoading || showInjectedPromptSaving}
            />
          </label>
          <div className="settings-prompt-meta">生效行数 {lspPromptLineCount} · 字符 {lspPromptCharCount}</div>
          <label className="settings-prompt-label" htmlFor="settings-lsp-effective-output">当前生效内容（只读）</label>
          <textarea
            id="settings-lsp-effective-output"
            className="settings-prompt-textarea settings-prompt-textarea-readonly"
            data-testid="settings-lsp-effective-output"
            rows={12}
            value={lspPromptDisplayHint}
            readOnly
          />
          <label className="settings-prompt-label" htmlFor="settings-lsp-prompt-input">自定义覆盖（可编辑，空=默认）</label>
          <textarea
            id="settings-lsp-prompt-input"
            className="settings-prompt-textarea"
            data-testid="settings-lsp-prompt-input"
            rows={8}
            value={lspPromptHint}
            onChange={(e) => setLspPromptHint(e.target.value)}
            placeholder={lspPromptDefaultHint || '请输入提示词'}
            disabled={lspPromptLoading || lspPromptSaving}
          />
          {lspPromptNotice.message && (
            <div className={`settings-prompt-notice is-${lspPromptNotice.level}`} data-testid="settings-lsp-prompt-notice" role={lspPromptNotice.level === 'error' ? 'alert' : 'status'}>
              {lspPromptNotice.message}
            </div>
          )}
          <div className="settings-action-row settings-action-inline">
            <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-refresh-button" onClick={loadLspPromptHint} disabled={lspPromptSaving}>刷新</button>
            <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-copy-button" onClick={copyEffectivePromptHint} disabled={lspPromptLoading || lspPromptSaving}>复制生效提示词</button>
            <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-reset-button" onClick={resetLspPromptHint} disabled={lspPromptLoading || lspPromptSaving}>恢复默认</button>
            <button className="btn btn-primary btn-toolbar-sm" data-testid="settings-lsp-save-button" onClick={saveLspPromptHint} disabled={lspPromptLoading || lspPromptSaving}>
              {lspPromptSaving ? '保存中...' : '保存提示词'}
            </button>
          </div>
        </div>

        {/* Model Built-in capabilities Section */}
        <div className="section-header">模型内置能力</div>
        <div className="data-card-vue" data-testid="settings-builtin-tools-card">
          <div className="data-row-vue">
            <strong>内置能力开关</strong>
            <span data-testid="settings-builtin-tools-summary">
              {builtinToolsLoading ? '加载中...' : `已管控 ${filteredCount} / ${totalToolCount}`}
            </span>
          </div>
          <div className="settings-prompt-desc">
            默认管控与本项目文件、命令、编排、计划、权限、插件管理重复，或会绕过项目治理的能力。
          </div>
          {builtinTools.length === 0 && !builtinToolsLoading ? (
            <div className="settings-log-empty" data-testid="settings-builtin-tools-empty">暂无可配置的内置工具</div>
          ) : (
            <div className="settings-builtin-tool-groups" data-testid="settings-builtin-tools-groups">
              {groups.map((group) => {
                const isOpen = isOpenGroup(group.key);
                return (
                  <section
                    key={group.key}
                    className="settings-builtin-tool-group"
                    data-testid={`settings-builtin-tool-group-${group.key}`}
                  >
                    <button
                      type="button"
                      className="settings-builtin-tool-group-head"
                      data-testid={`settings-builtin-tool-group-head-${group.key}`}
                      aria-expanded={isOpen ? 'true' : 'false'}
                      onClick={() => toggleGroupExpanded(group.key)}
                    >
                      <span className={`settings-builtin-tool-group-chevron ${isOpen ? 'is-open' : ''}`}>▸</span>
                      <span className="settings-builtin-tool-group-name">{group.label}</span>
                      <span className="settings-builtin-tool-group-summary">{groupSummary(group)}</span>
                    </button>
                    {isOpen && (
                      <div className="settings-builtin-tool-group-body">
                        {group.note && (
                          <p className="settings-builtin-tool-group-note" data-testid={`settings-builtin-tool-group-note-${group.key}`}>
                            {group.note}
                          </p>
                        )}
                        {group.tools.map((tool) => (
                          <label
                            key={tool.id}
                            className={`settings-prompt-toggle ${(!tool.enabled || tool.replacedBy) ? 'is-disabled-tool' : ''}`}
                            data-testid={`settings-builtin-tool-${tool.id}`}
                          >
                            <div className="settings-prompt-toggle-copy">
                              <span className="settings-prompt-toggle-title">{tool.label}</span>
                              <span className="settings-prompt-toggle-desc">{toolMetaText(tool)}</span>
                            </div>
                            <input
                              type="checkbox"
                              className="settings-prompt-toggle-input"
                              data-testid={`settings-builtin-tool-input-${tool.id}`}
                              checked={!tool.enabled || !!tool.replacedBy}
                              disabled={!!tool.replacedBy || Boolean(builtinSavingIds[tool.id])}
                              onChange={() => toggleBuiltinTool(tool)}
                            />
                          </label>
                        ))}
                      </div>
                    )}
                  </section>
                );
              })}
            </div>
          )}
          {builtinToolsNotice.message && (
            <div className={`settings-prompt-notice is-${builtinToolsNotice.level}`} data-testid="settings-builtin-tools-notice" role={builtinToolsNotice.level === 'error' ? 'alert' : 'status'}>
              {builtinToolsNotice.message}
            </div>
          )}
        </div>

        {/* UI LOG Section */}
        <div className="section-header">UI LOG</div>
        <div className="data-card-vue settings-log-card" data-testid="settings-log-card">
          <div className="data-row-vue">
            <strong>日志级别</strong>
            <span>{store.logLevel}</span>
          </div>
          <div className="settings-stall-row settings-log-control-row">
            <label className="settings-stall-label" htmlFor="settings-log-level-select">日志级别</label>
            <select
              id="settings-log-level-select"
              className="settings-stall-input settings-log-level-select"
              data-testid="settings-log-level-select"
              value={store.logLevel}
              onChange={(e) => store.setLogLevel(e.target.value)}
            >
              <option value="debug">debug（最详细）</option>
              <option value="info">info（默认）</option>
              <option value="warn">warn</option>
              <option value="error">error（仅错误）</option>
            </select>
            <span className="settings-stall-unit">立即生效（跨 tab 同步）</span>
          </div>
          <div className="settings-action-row settings-log-action-row">
            <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-log-refresh-button">刷新日志</button>
          </div>
          {logList.length === 0 ? (
            <div className="settings-log-empty" data-testid="settings-log-empty">暂无日志</div>
          ) : (
            <div className="settings-log-list" data-testid="settings-log-list">
              {logList.map((entry) => (
                <div key={entry.seq || entry.id} className="settings-log-item">
                  <span className="settings-log-time">{formatLogTime(entry.ts)}</span>
                  <span className={`settings-log-level is-${entry.level}`}>{entry.level}</span>
                  <span className="settings-log-event">{entry.scope}.{entry.event}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </section>
  );

  function isOpenGroup(key) {
    return Boolean(builtinExpandedGroups[key]);
  }
}
function Panel({ title, children }) {
  return (
    <section className="panel">
      <h3>{title}</h3>
      <div>{children}</div>
    </section>
  );
}

function EmptyState({ icon: Icon, title, text }) {
  return (
    <div className="empty-state">
      <span><Icon size={34} /></span>
      <h2>{title}</h2>
      <p>{text}</p>
    </div>
  );
}

export default App;

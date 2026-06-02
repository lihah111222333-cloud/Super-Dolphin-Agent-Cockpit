import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Archive, ArrowLeft, Bot, Boxes, Brain, CheckCircle2, ChevronDown, CircleStop, Clock3, Code2, Copy, Eye, File, FileText, Folder, GitBranch, Link2, PanelTopOpen, Pencil, Pin, Plus, RefreshCw, Send, Settings, Sparkles, Terminal, Trash2, UserRound, Workflow, Wrench, X } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { onFilesDropped, copyTextToClipboard } from '../../shared/api/backendApi.js';
import { appendCurrentModelOption, canonicalizeModelValue, modelOptionFor, normalizeConfigText, normalizeProviderKey, textValue } from '../shared/pageShared.js';

const COMPOSER_DROP_TARGET_IDS = new Set(['chat-input-bar', 'composer-input', 'chatInput']);

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

const TIMELINE_INITIAL_MATERIALIZED_MESSAGES = 80;

const TIMELINE_MATERIALIZATION_INCREMENT = 80;

const TIMELINE_SCROLL_LOAD_THRESHOLD = 32;

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
  return (
    Object.entries(merged)
    .map(([name, count]) => ({ name, count }))
    .filter((entry) => entry.count > 0)
    .sort((left, right) => right.count - left.count || left.name.localeCompare(right.name))
  );
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

const PATCH_UPDATE_FILE_PREFIX = '*** Update File:';

const PATCH_ADD_FILE_PREFIX = '*** Add File:';

const PATCH_DELETE_FILE_PREFIX = '*** Delete File:';

const PATCH_MOVE_TO_PREFIX = '*** Move to:';

const PATCH_BOUNDARY_PREFIXES = ['*** Begin Patch', '*** End Patch', '*** End of File'];

const DIFF_HEADER_PREFIXES = ['index ', 'new file', 'deleted file', '@@'];

const UNIFIED_DIFF_METADATA_PREFIXES = [
  'diff --git',
  'index ',
  '--- ',
  '+++ ',
  '*** Begin Patch',
  PATCH_UPDATE_FILE_PREFIX,
  PATCH_ADD_FILE_PREFIX,
  PATCH_DELETE_FILE_PREFIX,
  PATCH_MOVE_TO_PREFIX,
  '*** End Patch',
  '*** End of File',
];

function startsWithAny(value, prefixes) {
  return prefixes.some((prefix) => value.startsWith(prefix));
}

function emptyDiffSummary() {
  return { fileCount: 0, additions: 0, deletions: 0, changedLines: 0, files: [] };
}

function createDiffSummaryState() {
  return { files: [], current: null, pendingFileHeader: null };
}

function startDiffSummaryFile(state, filename) {
  state.current = {
    filename: filename || `file-${state.files.length + 1}`,
    additions: 0,
    deletions: 0,
    lines: [],
  };
  state.files.push(state.current);
}

function ensureDiffSummaryFile(state) {
  if (!state.current) startDiffSummaryFile(state);
}

function appendDiffSummaryLine(state, line) {
  ensureDiffSummaryFile(state);
  state.current.lines.push(line);
}

function diffPatchFilePrefix(line) {
  if (line.startsWith(PATCH_UPDATE_FILE_PREFIX)) return PATCH_UPDATE_FILE_PREFIX;
  if (line.startsWith(PATCH_ADD_FILE_PREFIX)) return PATCH_ADD_FILE_PREFIX;
  if (line.startsWith(PATCH_DELETE_FILE_PREFIX)) return PATCH_DELETE_FILE_PREFIX;
  return '';
}

function handleDiffGitHeader(state, line) {
  const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
  state.pendingFileHeader = null;
  startDiffSummaryFile(state, match?.[2] || match?.[1] || `file-${state.files.length + 1}`);
  state.current.lines.push(line);
}

function handlePatchFileHeader(state, line, prefix) {
  state.pendingFileHeader = null;
  startDiffSummaryFile(
    state,
    parseDiffFilename(line, prefix) || state.current?.filename || `file-${state.files.length + 1}`,
  );
  state.current.lines.push(line);
}

function handleOldDiffHeader(state, line) {
  state.pendingFileHeader = {
    oldFilename: parseDiffFilename(line, '---'),
    beginsNewFile: Boolean(state.current && (state.current.additions > 0 || state.current.deletions > 0)),
    line,
  };
  if (state.current && !state.pendingFileHeader.beginsNewFile) state.current.lines.push(line);
}

function handleNewDiffHeader(state, line) {
  const filename = parseDiffFilename(line, '+++');
  const fallback = state.current?.filename || `file-${state.files.length + 1}`;
  const headerFilename = filename || state.pendingFileHeader?.oldFilename || fallback;
  if (!state.current || state.pendingFileHeader?.beginsNewFile) startDiffSummaryFile(state, headerFilename);
  else state.current.filename = headerFilename || state.current.filename;
  if (state.pendingFileHeader?.line && !state.current.lines.includes(state.pendingFileHeader.line)) {
    state.current.lines.push(state.pendingFileHeader.line);
  }
  state.current.lines.push(line);
  state.pendingFileHeader = null;
}

function handleChangedDiffLine(state, line) {
  ensureDiffSummaryFile(state);
  if (line.startsWith('+')) state.current.additions += 1;
  if (line.startsWith('-')) state.current.deletions += 1;
  state.current.lines.push(line);
}

function applyPatchBoundaryLine(state, line) {
  if (!startsWithAny(line, PATCH_BOUNDARY_PREFIXES)) return false;
  state.pendingFileHeader = null;
  return true;
}

function applyPatchFileHeaderLine(state, line) {
  const patchPrefix = diffPatchFilePrefix(line);
  if (!patchPrefix) return false;
  handlePatchFileHeader(state, line, patchPrefix);
  return true;
}

function applyPatchMoveLine(state, line) {
  if (!line.startsWith(PATCH_MOVE_TO_PREFIX)) return false;
  const filename = parseDiffFilename(line, PATCH_MOVE_TO_PREFIX);
  if (state.current && filename) state.current.filename = filename;
  appendDiffSummaryLine(state, line);
  return true;
}

function applyDiffFileHeaderLine(state, line) {
  if (line.startsWith('diff --git')) handleDiffGitHeader(state, line);
  else if (line.startsWith('--- ')) handleOldDiffHeader(state, line);
  else if (line.startsWith('+++ ')) handleNewDiffHeader(state, line);
  else return false;
  return true;
}

function applyDiffMetaLine(state, line) {
  if (!startsWithAny(line, DIFF_HEADER_PREFIXES)) return false;
  appendDiffSummaryLine(state, line);
  return true;
}

function applyDiffContentLine(state, line) {
  const changed = (line.startsWith('+') && !line.startsWith('+++')) || (line.startsWith('-') && !line.startsWith('---'));
  if (!changed) return false;
  handleChangedDiffLine(state, line);
  return true;
}

const DIFF_SUMMARY_LINE_HANDLERS = [
  applyDiffFileHeaderLine,
  applyPatchBoundaryLine,
  applyPatchFileHeaderLine,
  applyPatchMoveLine,
  applyDiffMetaLine,
  applyDiffContentLine,
];

function applyDiffSummaryLine(state, line) {
  const handled = DIFF_SUMMARY_LINE_HANDLERS.some((handler) => handler(state, line));
  if (!handled && state.current) state.current.lines.push(line);
  return handled;
}

function buildDiffSummary(files) {
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

function summarizeUnifiedDiff(diffText) {
  if (!diffText || typeof diffText !== 'string') return emptyDiffSummary();
  const state = createDiffSummaryState();
  for (const line of diffText.split('\n')) applyDiffSummaryLine(state, line);
  return buildDiffSummary(state.files);
}

function isUnifiedDiffMetadataLine(line) {
  return startsWithAny(line, UNIFIED_DIFF_METADATA_PREFIXES);
}

function diffLineEntry({ index, type, oldNo = '', newNo = '', prefix = '', content }) {
  return { key: `${index}:${type}`, type, oldNo, newNo, prefix, content };
}

function parseHunkLineEntry(state, line, index) {
  const match = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
  state.oldLine = match ? Number(match[1]) : null;
  state.newLine = match ? Number(match[2]) : null;
  return diffLineEntry({ index, type: 'hunk', content: line });
}

function parseChangedDiffLineEntry(state, line, index) {
  if (line.startsWith('+') && !line.startsWith('+++')) {
    const entry = diffLineEntry({ index, type: 'add', newNo: state.newLine ?? '', prefix: '+', content: line.slice(1) });
    if (state.newLine !== null) state.newLine += 1;
    return entry;
  }
  if (line.startsWith('-') && !line.startsWith('---')) {
    const entry = diffLineEntry({ index, type: 'del', oldNo: state.oldLine ?? '', prefix: '-', content: line.slice(1) });
    if (state.oldLine !== null) state.oldLine += 1;
    return entry;
  }
  return null;
}

function parseContextDiffLineEntry(state, line, index) {
  const entry = diffLineEntry({
    index,
    type: 'context',
    oldNo: state.oldLine ?? '',
    newNo: state.newLine ?? '',
    content: line.slice(1),
  });
  if (state.oldLine !== null) state.oldLine += 1;
  if (state.newLine !== null) state.newLine += 1;
  return entry;
}

function parseUnifiedDiffLineEntry(state, line, index) {
  if (isUnifiedDiffMetadataLine(line)) return [];
  if (line.startsWith('@@')) return parseHunkLineEntry(state, line, index);
  const changed = parseChangedDiffLineEntry(state, line, index);
  if (changed) return changed;
  if (line.startsWith(' ')) return parseContextDiffLineEntry(state, line, index);
  return diffLineEntry({ index, type: 'meta', content: line });
}

function parseUnifiedDiffLineEntries(fileText) {
  const state = { oldLine: null, newLine: null };
  return String(fileText || '').split('\n').flatMap((line, index) => parseUnifiedDiffLineEntry(state, line, index));
}

function warningDetailText(entry) {
  if (entry?.runtimeKind === 'result' && entry?.fields && typeof entry.fields === 'object') {
    return JSON.stringify(entry.fields, null, 2);
  }
  return entry?.detail || JSON.stringify(entry?.fields ?? {}, null, 2);
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

function runtimeLogInlineLabel(entry) {
  const label = runtimeLogLabel(entry);
  if (entry?.runtimeKind === 'result') {
    return label.split(' · ', 1)[0] || label;
  }
  return label;
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

const TURN_STATE_INFO = Object.freeze({
  idle: Object.freeze({ label: '空闲', tone: 'connected', busy: false }),
  starting: Object.freeze({ label: '启动中', tone: 'active', busy: true }),
  preparing: Object.freeze({ label: '准备中', tone: 'active', busy: true }),
  thinking: Object.freeze({ label: '思考中', tone: 'active', busy: true }),
  running: Object.freeze({ label: '运行中', tone: 'active', busy: true }),
  editing: Object.freeze({ label: '编辑中', tone: 'active', busy: true }),
  waiting: Object.freeze({ label: '等待确认', tone: 'warning', busy: true }),
  syncing: Object.freeze({ label: '同步中', tone: 'active', busy: true }),
  responding: Object.freeze({ label: '回复中', tone: 'active', busy: true }),
  force_completing: Object.freeze({ label: '强制完成中', tone: 'active', busy: true }),
  interrupting: Object.freeze({ label: '中断中', tone: 'warning', busy: true }),
  interrupted: Object.freeze({ label: '已中断', tone: 'warning', busy: false }),
  completed: Object.freeze({ label: '已完成', tone: 'done', busy: false }),
  error: Object.freeze({ label: '异常', tone: 'error', busy: false }),
  failed: Object.freeze({ label: '失败', tone: 'error', busy: false }),
  stalled: Object.freeze({ label: '停滞', tone: 'error', busy: false }),
  stopped: Object.freeze({ label: '已停止', tone: 'idle', busy: false }),
  archived: Object.freeze({ label: '已归档', tone: 'idle', busy: false }),
});

const LEGACY_TURN_STATE_ALIASES = Object.freeze({
  工作中: 'running',
  发送中: 'preparing',
  pending: 'starting',
  recovering: 'syncing',
  错误: 'error',
  失败: 'failed',
  空闲: 'idle',
  等待指示: 'idle',
});

function knownProviderKey(value) {
  const normalized = (value || '').toString().trim().toLowerCase();
  return normalized === 'claude' || normalized === 'codex' ? normalized : '';
}

function threadProviderLabel(provider) {
  return knownProviderKey(provider) || 'unknown';
}

function threadCardStatusLabel(thread, running) {
  const status = (thread?.status || '').toString().trim();
  const normalized = status.toLowerCase();
  const mapped = TURN_STATE_INFO[normalizeTurnState(status)];
  if (!status || normalized === 'idle' || status === '空闲' || status === '等待指示') return '';
  if (mapped?.label) return mapped.label;
  if (running) return '工作中';
  return status;
}

function threadStatusBusy(status) {
  const mapped = TURN_STATE_INFO[normalizeTurnState(status)];
  if (mapped) return mapped.busy;
  const normalized = (status || '').toString().trim().toLowerCase();
  return normalized === '工作中';
}

function threadStatusDotState(status) {
  const normalized = normalizeTurnState(status);
  if (!normalized) return 'idle';
  if (['failed', 'error', 'stalled'].includes(normalized)) return 'error';
  if (['running', 'force_completing'].includes(normalized)) return 'running';
  if (['preparing', 'starting', 'thinking'].includes(normalized)) return 'thinking';
  if (['waiting', 'interrupting', 'interrupted'].includes(normalized)) return 'waiting';
  if (['syncing', 'responding', 'editing'].includes(normalized)) return normalized;
  if (['completed', 'idle', 'stopped', 'archived'].includes(normalized)) return 'idle';
  return 'idle';
}

function threadStatusDotTitle(status, statusLabel) {
  const normalized = normalizeTurnState(status);
  return statusLabel || TURN_STATE_INFO[normalized]?.label || '空闲';
}

function normalizedThreadIdentity(value) {
  return (value || '').toString().trim();
}

function isInternalThreadIdentifier(value) {
  const text = normalizedThreadIdentity(value);
  if (!text) return false;
  return /^agent_[a-z0-9_-]+$/i.test(text) || /^thread[-_][a-z0-9_-]+$/i.test(text);
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

function firstNormalizedIdentity(values = []) {
  for (const value of values) {
    const id = normalizedThreadIdentity(value);
    if (id) return id;
  }
  return '';
}

function activityEntryThreadIdentifier(entry = {}) {
  const fields = entry.fields || {};
  const patch = fields._threadPatch || fields._thread_patch || {};
  return firstNormalizedIdentity([
    entry.threadId,
    entry.thread_id,
    entry.agentId,
    entry.agent_id,
    fields.threadId,
    fields.thread_id,
    fields.agentId,
    fields.agent_id,
    patch.threadId,
    patch.thread_id,
    patch.agentId,
    patch.agent_id,
  ]);
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
  return (
    normalizedThreadIdentity(value)
    .replace(/\uFFFD+/g, '')
    .replace(/\|+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  );
}

function isInternalThreadDisplayText(value, activeThreadId, activeThread) {
  const text = normalizedThreadIdentity(value);
  if (!text) return false;
  const unprefixed = text.replace(/^线程\s+/u, '').trim();
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  return (
    ids.has(text)
    || ids.has(unprefixed)
    || isInternalThreadIdentifier(text)
    || isInternalThreadIdentifier(unprefixed)
  );
}

function displayThreadName(thread, fallback = '新对话') {
  const ids = activeThreadIdentifiers(thread?.id, thread);
  for (const value of [thread?.name, thread?.title, thread?.displayName, thread?.display_name]) {
    const text = normalizedThreadIdentity(value);
    if (!text) continue;
    if (ids.has(text) || isInternalThreadIdentifier(text)) continue;
    return text;
  }
  return fallback;
}

function workStatusDetailsForThread({ activeThreadId, activeThread, statusEntry }) {
  const details = cleanWorkStatusDetails(firstStatusText(statusEntry?.statusDetails, activeThread?.lastMessage));
  if (details && !isInternalThreadDisplayText(details, activeThreadId, activeThread)) return details;
  return '当前会话已连接';
}

function workStatusForThread({ sending, loading, activeThreadId, activeThread, statusEntry }) {
  if (!activeThreadId) {
    return { label: '待启动', details: '发送首条消息后创建线程', tone: 'idle', busy: false };
  }
  if (loading) {
    return { label: '加载中', details: '正在同步当前会话', tone: 'active', busy: true };
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
    details: workStatusDetailsForThread({ activeThreadId, activeThread, statusEntry }),
    tone: mapped?.tone || 'connected',
    busy: mapped?.busy ?? Boolean(sending),
  };
}

function hasAssistantReplyAfterLastUser(messages = []) {
  let lastUserIndex = -1;
  for (let index = 0; index < messages.length; index += 1) {
    if ((messages[index]?.role || '').toString().trim().toLowerCase() === 'user') {
      lastUserIndex = index;
    }
  }
  return messages.some((message, index) => (
    index > lastUserIndex &&
    (message?.role || '').toString().trim().toLowerCase() === 'assistant' &&
    !isReasoningMessage(message) &&
    Boolean((message?.text || '').toString().trim())
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

function isClaudeOpusFamilyModel(model) {
  const normalized = normalizeConfigText(model).toLowerCase();
  return normalized === 'best' || normalized.includes('opus');
}

function effortOptionFor(provider, value) {
  const normalized = normalizeConfigText(value);
  const options = EFFORT_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || EFFORT_OPTIONS_BY_PROVIDER.codex;
  return options.find((item) => item.value === normalized) || (normalized ? { value: normalized, label: normalized } : null);
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

function runUIAction(action) {
  try {
    const result = typeof action === 'function' ? action() : action;
    if (result && typeof result.catch === 'function') {
      void result.catch(() => {});
    }
  }
  catch (error) {
    void error;
  }
}

function useChatThreadData(store, activeThreadId) {
  const activeThread = activeThreadForStore(store);
  const timelineBlocked = Boolean(activeThreadId && store.threadStateLoadingByThread?.[activeThreadId]);
  const cachedTimeline = threadScopedMapValue(store.timelinesByThread, activeThreadId, activeThread, []) || [];
  const timelineReady = Boolean(
    activeThreadId &&
    store.threadTimelineReadyByThread?.[activeThreadId] &&
    (!timelineBlocked || cachedTimeline.length > 0),
  );
  const timelineContentBlocked = timelineBlocked && !timelineReady;
  return {
    activeThread,
    activeTurn: threadScopedMapValue(store.activeTurnByThread, activeThreadId, activeThread, null),
    activityStats: threadScopedMapValue(store.activityStatsByThread, activeThreadId, activeThread, null),
    diffText: threadScopedMapValue(store.diffTextByThread, activeThreadId, activeThread, '') || '',
    messages: timelineContentBlocked ? [] : cachedTimeline,
    runtimeResults: scopedActivityEntries(store.runtimeResultEntries, activeThreadId, activeThread, { includeUnscoped: true }),
    statusEntry: activeThreadId ? store.statuses?.[activeThreadId] : null,
    timelineBlocked,
    timelineContentBlocked,
    tokenUsage: threadScopedMapValue(store.tokenUsageByThread, activeThreadId, activeThread, null),
    warnings: scopedActivityEntries(store.warningEntries, activeThreadId, activeThread, { includeUnscoped: true }),
  };
}

function useViewportWidth() {
  const [viewportWidth, setViewportWidth] = useState(currentViewportWidth);
  useEffect(() => {
    const onResize = () => setViewportWidth(currentViewportWidth());
    window.addEventListener('resize', onResize);
    return () => window.removeEventListener('resize', onResize);
  }, []);
  return viewportWidth;
}

function useThreadRailLayout(viewportWidth) {
  const [threadRailWidth, setThreadRailWidth] = useState(() => threadRailTargetWidth());
  const resizedRef = useRef(false);
  const maxWidth = threadRailTargetWidth(viewportWidth);
  const width = clampWidth(threadRailWidth, THREAD_RAIL_MIN_WIDTH, maxWidth);
  useEffect(() => {
    setThreadRailWidth((currentWidth) => {
      const targetWidth = threadRailTargetWidth(viewportWidth);
      if (!resizedRef.current) return targetWidth;
      return clampWidth(currentWidth, THREAD_RAIL_MIN_WIDTH, targetWidth);
    });
  }, [viewportWidth]);
  const beginResize = (event) => {
    event.preventDefault();
    resizedRef.current = true;
    const startX = event.clientX;
    const startWidth = width;
    const move = (moveEvent) => {
      setThreadRailWidth(clampWidth(startWidth + (moveEvent.clientX - startX), THREAD_RAIL_MIN_WIDTH, maxWidth));
    };
    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
  };
  const handleKeyDown = (event) => {
    const nextWidth = resizerNextWidth(event, width, maxWidth, THREAD_RAIL_MIN_WIDTH, 'rail');
    if (nextWidth === null) return;
    event.preventDefault();
    resizedRef.current = true;
    setThreadRailWidth(nextWidth);
  };
  return { beginResize, handleKeyDown, maxWidth, width };
}

function resizerNextWidth(event, currentWidth, maxWidth, minWidth, mode) {
  if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return null;
  if (event.key === 'Home') return minWidth;
  if (event.key === 'End') return maxWidth;
  const direction = mode === 'right' ? 1 : -1;
  const deltaByKey = {
    ArrowLeft: RESIZER_KEY_STEP * direction,
    ArrowRight: -RESIZER_KEY_STEP * direction,
  };
  const delta = deltaByKey[event.key];
  return delta === undefined ? null : clampWidth(currentWidth + delta, minWidth, maxWidth);
}

function useChatInterruptShortcut(store, activeThreadId) {
  useEffect(() => {
    const onKeyDown = (event) => {
      if (event.defaultPrevented || event.key !== 'Escape' || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
      if (shouldIgnoreGlobalEscape(event.target)) return;
      if (!store.hasActiveThreadActions?.()) return;
      event.preventDefault();
      runUIAction(() => store.interruptActiveThread?.());
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [store, activeThreadId]);
}

function useRuntimeSidePanelLayout({ activeThreadId, railWidth, store, viewportWidth }) {
  const [open, setOpen] = useState(false);
  const resizedRef = useRef(false);
  const layoutRef = useRef(null);
  const maxWidth = rightPanelMaxWidth(viewportWidth, railWidth);
  const width = clampWidth(store.rightPanelWidth, 0, maxWidth);
  useRuntimePanelWidthSync({ maxWidth, open, resizedRef, setOpen, store, viewportWidth });
  useRuntimeDiffSync({ activeThreadId, open, store });
  const beginResize = (event) => {
    resizedRef.current = true;
    beginRightPanelDrag({ event, layoutRef, maxWidth, railWidth, setOpen, store, width });
  };
  const handleKeyDown = (event) => {
    const nextWidth = resizerNextWidth(event, width, maxWidth, 0, 'right');
    if (nextWidth === null) return;
    event.preventDefault();
    resizedRef.current = true;
    if (nextWidth <= RIGHT_PANEL_CLOSE_THRESHOLD) {
      store.setRightPanelWidth?.(0);
      setOpen(false);
      return;
    }
    store.setRightPanelWidth?.(nextWidth);
  };
  const toggle = () => toggleRuntimePanel({ maxWidth, open, resizedRef, setOpen, store, viewportWidth });
  return { beginResize, handleKeyDown, layoutRef, maxWidth, open, toggle, width };
}

function useRuntimePanelWidthSync({ maxWidth, open, resizedRef, setOpen, store, viewportWidth }) {
  useEffect(() => {
    if (!open) return;
    const targetWidth = resizedRef.current
      ? clampWidth(store.rightPanelWidth, 0, maxWidth)
      : clampWidth(rightPanelDefaultWidth(viewportWidth), 0, maxWidth);
    if (targetWidth <= 0) {
      store.setRightPanelWidth?.(0);
      setOpen(false);
      return;
    }
    if (targetWidth !== store.rightPanelWidth) store.setRightPanelWidth?.(targetWidth);
  }, [maxWidth, open, resizedRef, setOpen, store, viewportWidth]);
}

function useRuntimeDiffSync({ activeThreadId, open, store }) {
  useEffect(() => {
    if (!open || !activeThreadId) return;
    if (store.threadDiffReadyByThread?.[activeThreadId]) return;
    if (store.threadStateLoadingByThread?.[activeThreadId]) return;
    runUIAction(() => store.syncThreadState?.(activeThreadId, {
      includeArchived: true,
      includeDiff: true,
      loadMessages: false,
      preserveActiveThreadId: true,
    }));
  }, [activeThreadId, open, store]);
}

function toggleRuntimePanel({ maxWidth, open, resizedRef, setOpen, store, viewportWidth }) {
  const next = !open;
  if (next) {
    resizedRef.current = false;
    store.setRightPanelWidth?.(clampWidth(rightPanelDefaultWidth(viewportWidth), 0, maxWidth));
  }
  setOpen(next);
}

function beginRightPanelDrag({ event, layoutRef, maxWidth, railWidth, setOpen, store, width }) {
  event.preventDefault();
  event.currentTarget?.setPointerCapture?.(event.pointerId);
  const drag = rightPanelDragState({ event, layoutRef, maxWidth, railWidth, setOpen, store, width });
  window.addEventListener('pointermove', drag.move);
  window.addEventListener('pointerup', drag.finish);
  window.addEventListener('pointercancel', drag.finish);
  window.addEventListener('blur', drag.finish);
}

function rightPanelDragState({ event, layoutRef, maxWidth, railWidth, setOpen, store, width }) {
  const startX = event.clientX;
  const startWidth = width;
  const layoutColumnsForWidth = (nextWidth) => `${railWidth}px ${SPLITTER_WIDTH}px minmax(0, 1fr) ${SPLITTER_WIDTH}px ${nextWidth}px`;
  const state = { latestWidth: startWidth, stopped: false };
  const applyDragWidth = (nextWidth) => {
    if (layoutRef.current) layoutRef.current.style.gridTemplateColumns = layoutColumnsForWidth(nextWidth);
  };
  const finish = () => finishRightPanelDrag({ event, setOpen, state, store, drag });
  const move = (moveEvent) => moveRightPanelDrag({ applyDragWidth, finish, maxWidth, moveEvent, startWidth, startX, state });
  const drag = { finish, move };
  return drag;
}

function moveRightPanelDrag({ applyDragWidth, finish, maxWidth, moveEvent, startWidth, startX, state }) {
  if (Number(moveEvent.buttons) === 0) {
    finish();
    return;
  }
  const rawNext = startWidth - (moveEvent.clientX - startX);
  if (rawNext <= RIGHT_PANEL_CLOSE_THRESHOLD) {
    state.latestWidth = 0;
    applyDragWidth(0);
    finish();
    return;
  }
  state.latestWidth = clampWidth(rawNext, 0, maxWidth);
  applyDragWidth(state.latestWidth);
}

function finishRightPanelDrag({ event, setOpen, state, store, drag }) {
  if (state.stopped) return;
  state.stopped = true;
  window.removeEventListener('pointermove', drag.move);
  window.removeEventListener('pointerup', drag.finish);
  window.removeEventListener('pointercancel', drag.finish);
  window.removeEventListener('blur', drag.finish);
  event.currentTarget?.releasePointerCapture?.(event.pointerId);
  if (state.latestWidth <= RIGHT_PANEL_CLOSE_THRESHOLD) {
    store.setRightPanelWidth?.(0);
    setOpen(false);
    return;
  }
  store.setRightPanelWidth?.(state.latestWidth);
}

function ChatPage({ store, projectPath }) {
  const activeThreadId = store.activeThreadId;
  const modelThreadId = composerConfigThreadId(store, activeThreadId);
  const threadData = useChatThreadData(store, activeThreadId);
  const canUseProjectActions = canUseProjectActionsForStore(store);
  const viewportWidth = useViewportWidth();
  const rail = useThreadRailLayout(viewportWidth);
  const {
    beginResize: beginRuntimeResize,
    handleKeyDown: handleRuntimeResizeKeyDown,
    layoutRef: chatLayoutRef,
    maxWidth: runtimeMaxWidth,
    open: rightPanelOpen,
    toggle: toggleRightPanel,
    width: rightPanelWidth,
  } = useRuntimeSidePanelLayout({ activeThreadId, railWidth: rail.width, store, viewportWidth });
  useChatInterruptShortcut(store, activeThreadId);
  const layoutColumns = rightPanelOpen
    ? `${rail.width}px ${SPLITTER_WIDTH}px minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightPanelWidth}px`
    : `${rail.width}px ${SPLITTER_WIDTH}px minmax(0, 1fr)`;

  return (
    <section className="chat-page" data-testid="chat-page">
      <TopCommandBar
        store={store}
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        toggleRightPanel={toggleRightPanel}
      />
      <div ref={chatLayoutRef} className="chat-layout" data-testid="chat-layout" style={{ gridTemplateColumns: layoutColumns }}>
        <ThreadRail store={store} />
        <ThreadRailResizer rail={rail} />
        <Conversation
          messages={threadData.messages}
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
          tokenUsage={threadData.tokenUsage}
          activeThreadId={activeThreadId}
          activeThread={threadData.activeThread}
          statusEntry={threadData.statusEntry}
          activeTurn={threadData.activeTurn}
          modelThreadId={modelThreadId}
          timelineBlocked={threadData.timelineBlocked}
          timelineContentBlocked={threadData.timelineContentBlocked}
          canUseProjectActions={canUseProjectActions}
        />
        <RuntimePanelSlot
          beginResize={beginRuntimeResize}
          handleKeyDown={handleRuntimeResizeKeyDown}
          maxWidth={runtimeMaxWidth}
          open={rightPanelOpen}
          threadData={threadData}
          width={rightPanelWidth}
        />
      </div>
    </section>
  );
}

function ThreadRailResizer({ rail }) {
  return (
    <div
      role="separator"
      className="splitter splitter--left"
      aria-orientation="vertical"
      aria-label="调整会话栏宽度"
      aria-valuemin={THREAD_RAIL_MIN_WIDTH}
      aria-valuemax={rail.maxWidth}
      aria-valuenow={rail.width}
      data-testid="thread-rail-resizer"
      tabIndex={0}
      onKeyDown={rail.handleKeyDown}
      onPointerDown={rail.beginResize}
    />
  );
}

function RuntimePanelSlot({ beginResize, handleKeyDown, maxWidth, open, threadData, width }) {
  if (!open) return null;
  return (
    <>
      <div
        role="separator"
        className="splitter splitter--right"
        aria-orientation="vertical"
        aria-label="调整侧边栏宽度"
        aria-valuemin={0}
        aria-valuemax={maxWidth}
        aria-valuenow={width}
        data-testid="right-panel-resizer"
        tabIndex={0}
        onKeyDown={handleKeyDown}
        onPointerDown={beginResize}
      />
      <RuntimePanel
        diffText={threadData.diffText}
        tokenUsage={threadData.tokenUsage}
        activityStats={threadData.activityStats}
        warnings={threadData.warnings}
        runtimeResults={threadData.runtimeResults}
      />
    </>
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
  }, [open, wrapRef]);

  const selectProject = (value) => {
    setOpen(false);
    return store.setActiveProjectPath?.(value);
  };

  const addProject = () => {
    setOpen(false);
    return store.addProjectFromPicker?.();
  };

  const removeProject = (event, value) => {
    event.stopPropagation();
    return store.removeProjectPath?.(value);
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
        <ProjectDropdown
          options={options}
          selectedValue={selected.value}
          onSelect={selectProject}
          onRemove={removeProject}
          onAdd={addProject}
        />
      ) : null}
    </div>
  );
}

function ProjectDropdown({ options, selectedValue, onSelect, onRemove, onAdd }) {
  return (
    <div className="project-dropdown" role="menu" aria-label="项目列表">
      {options.map((item) => (
        <div key={item.value} className={`project-dropdown-row ${item.value === selectedValue ? 'selected' : ''}`} role="none" title={item.full}>
          <button
            type="button"
            className="project-dropdown-item"
            role="menuitem"
            onClick={() => runUIAction(() => onSelect(item.value))}
          >
            <span className="project-option-check" aria-hidden="true">{item.value === selectedValue ? '✓' : ''}</span>
            <span className="project-dropdown-label">{item.label}</span>
          </button>
          {item.value !== '.' ? (
            <button
              type="button"
              className="project-dropdown-remove"
              aria-label={`移除此项目 ${item.label}`}
              title="移除此项目"
              onClick={(event) => runUIAction(() => onRemove(event, item.value))}
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
        onClick={() => runUIAction(onAdd)}
      >
        <Plus size={13} />
        <span>添加项目</span>
      </button>
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
        runUIAction(() => store.toggleProviderMode());
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
  const canInterruptThread = Boolean(store.hasInterruptibleThreadAction?.());
  const bootstrapFailureMessage = store.bootstrapStatus === 'failed' && textValue(store.error)
    ? `连接后端失败：${textValue(store.error)}`
    : '';
  let feedback = null;
  if (store.actionNotice?.message) {
    feedback = store.actionNotice;
  } else if (bootstrapFailureMessage) {
    feedback = { message: bootstrapFailureMessage, tone: 'error' };
  }
  return (
    <div className="top-command" data-testid="chat-toolbar">
      <ProjectSelector store={store} projectPath={projectPath} />
      <button
        type="button"
        className="icon-btn"
        aria-label="新窗口（独立进程）"
        title="新窗口（独立进程）"
        onClick={() => runUIAction(() => store.openNewWindow?.())}
      >
        <PanelTopOpen size={15} />
      </button>
      {canUseThreadActions ? (
        <button type="button" className="icon-btn" aria-label="复制当前线程" title="复制当前线程" onClick={() => runUIAction(() => store.copyActiveThreadInfo())}><Copy size={15} /></button>
      ) : null}
      {canInterruptThread ? (
        <button type="button" className="icon-btn" aria-label="停止" title="中断当前执行" onClick={() => runUIAction(() => store.interruptActiveThread())}><CircleStop size={15} /></button>
      ) : null}
      <button
        type="button"
        className="icon-btn"
        aria-label={canUseThreadActions ? '进程恢复' : '请先选择会话'}
        title={canUseThreadActions ? '手动杀进程并恢复连接' : '请先选择会话'}
        disabled={!canUseThreadActions}
        onClick={() => runUIAction(() => store.recoverActiveThread())}
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
      <button
        type="button"
        className={`icon-btn sidebar-toggle ${rightPanelOpen ? 'active' : ''}`}
        aria-label={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        title={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        aria-pressed={rightPanelOpen}
        onClick={toggleRightPanel}
      >
        {rightPanelOpen ? <X size={15} /> : <Eye size={15} />}
        <span className="sidebar-toggle-label">侧边栏</span>
      </button>
    </div>
  );
}

function ThreadRail({ store }) {
  const [showArchivedThreads, setShowArchivedThreads] = useState(false);
  const [confirmCleanMode, setConfirmCleanMode] = useState(false);
  const [hoveredPinThreadId, setHoveredPinThreadId] = useState('');
  const rename = useThreadRenameController(store);
  const activeThreads = store.threads.filter((thread) => !thread.archived);
  const archivedThreads = store.threads.filter((thread) => thread.archived);
  const threads = showArchivedThreads ? archivedThreads : activeThreads;
  const chatListLoading = Boolean(store.chatSurfaceLoadingCwd);
  const visibleThreads = visibleThreadRows(threads, store);
  const staleThreadIds = showArchivedThreads
    ? visibleThreads.filter((thread) => thread.staleReason).map((thread) => thread.id)
    : [];
  const toggleArchiveLabel = showArchivedThreads ? '返回会话列表' : '打开归档列表';
  let emptyThreadText = '暂无会话，点击「新建对话」开始草稿';
  if (chatListLoading && !showArchivedThreads) {
    emptyThreadText = '正在加载会话列表…';
  } else if (showArchivedThreads) {
    emptyThreadText = '暂无归档会话';
  }
  const toggleArchiveList = () => {
    setShowArchivedThreads((value) => {
      const next = !value;
      if (!next) setConfirmCleanMode(false);
      return next;
    });
  };
  return (
    <aside className="thread-rail" data-testid="thread-rail" aria-label={showArchivedThreads ? '归档列表' : '会话列表'}>
      <ThreadRailTools
        count={visibleThreads.length}
        confirmCleanMode={confirmCleanMode}
        showArchivedThreads={showArchivedThreads}
        staleThreadIds={staleThreadIds}
        toggleArchiveLabel={toggleArchiveLabel}
        onNewThread={store.newThread}
        onCleanConfirm={() => {
          setConfirmCleanMode(false);
          runUIAction(() => store.deleteStaleThreads(staleThreadIds));
        }}
        onCleanMode={() => setConfirmCleanMode(true)}
        onCancelClean={() => setConfirmCleanMode(false)}
        onToggleArchive={toggleArchiveList}
      />
      <div className="thread-list">
        {visibleThreads.length === 0 ? (
          <p className="thread-empty">
            {emptyThreadText}
          </p>
        ) : null}
        {visibleThreads.map((thread) => (
          <ThreadCard
            key={thread.id}
            thread={thread}
            store={store}
            active={(store.pendingActiveThreadId || store.activeThreadId) === thread.id}
            editing={rename.editingThreadId === thread.id}
            editingName={rename.editingName}
            hoveredPinThreadId={hoveredPinThreadId}
            renaming={rename.renamingThreadId === thread.id}
            onBeginRename={rename.beginRename}
            onCancelRename={rename.cancelRename}
            onRenameBlur={rename.handleRenameBlur}
            onSetEditingName={rename.setEditingName}
            onSetHoveredPinThreadId={setHoveredPinThreadId}
            onSubmitRename={rename.submitRename}
          />
        ))}
      </div>
    </aside>
  );
}

function useThreadRenameController(store) {
  const [editingThreadId, setEditingThreadId] = useState('');
  const [editingName, setEditingName] = useState('');
  const [renamingThreadId, setRenamingThreadId] = useState('');

  const beginRename = (thread) => {
    setEditingThreadId(thread.id);
    setEditingName(displayThreadName(thread, ''));
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
      const saved = await store.renameThread(thread.id, nextName);
      if (saved) {
        setEditingThreadId('');
        setEditingName('');
      }
    }
    finally {
      setRenamingThreadId('');
    }
  };
  const handleRenameBlur = (event, thread) => {
    const saveFor = event.relatedTarget?.dataset?.renameSaveButtonFor || '';
    if (saveFor === thread.id) return;
    cancelRename();
  };

  return { beginRename, cancelRename, editingName, editingThreadId, handleRenameBlur, renamingThreadId, setEditingName, submitRename };
}

function visibleThreadRows(threads, store) {
  const rows = threads
    .map((thread, index) => ({
      ...thread,
      staleReason: archivedStaleReason(thread),
      listIndex: index,
      pinnedAt: Number(store.pinnedThreadAtById?.[thread.id] || thread.pinnedAt || 0),
      activityAt: threadSortTimestamp(store.activityThreadAtById?.[thread.id] || thread.updatedAt),
    }))
    .sort(sortThreadRows);
  return rows;
}

function sortThreadRows(left, right) {
  const leftPinned = left.pinnedAt > 0;
  const rightPinned = right.pinnedAt > 0;
  if (leftPinned !== rightPinned) return leftPinned ? -1 : 1;
  if (leftPinned && rightPinned && left.pinnedAt !== right.pinnedAt) return right.pinnedAt - left.pinnedAt;
  if (!leftPinned && !rightPinned && left.activityAt !== right.activityAt) return right.activityAt - left.activityAt;
  return left.listIndex - right.listIndex;
}

function ThreadRailTools({
  count,
  confirmCleanMode,
  showArchivedThreads,
  staleThreadIds,
  toggleArchiveLabel,
  onNewThread,
  onCleanConfirm,
  onCleanMode,
  onCancelClean,
  onToggleArchive,
}) {
  return (
    <div className="thread-tools">
      <button type="button" className="round thread-new-primary" aria-label="新建对话" title="新对话：发送第一条消息时才会创建会话" onClick={onNewThread}>
        <Pencil size={17} />
      </button>
      <span className="count thread-count" role="img" aria-label={`${count} 个 Agent`} title={`${count} 个 Agent`}>
        <Bot size={14} />
        <strong>{count}</strong>
      </span>
      {showArchivedThreads && staleThreadIds.length > 0 && !confirmCleanMode ? (
        <button type="button" className="round thread-clean" aria-label="清理无用对话" title="清理无用对话" onClick={onCleanMode}>
          <Trash2 size={15} />
        </button>
      ) : null}
      {showArchivedThreads && confirmCleanMode ? (
        <>
          <button type="button" className="thread-clean-confirm" onClick={onCleanConfirm}>确认</button>
          <button type="button" className="thread-clean-cancel" onClick={onCancelClean}>取消</button>
        </>
      ) : null}
      <button
        type="button"
        className={`round thread-archive-toggle ${showArchivedThreads ? 'active' : ''}`}
        aria-label={toggleArchiveLabel}
        title={toggleArchiveLabel}
        onClick={onToggleArchive}
      >
        {showArchivedThreads ? <ArrowLeft size={15} /> : <Archive size={15} />}
      </button>
    </div>
  );
}

function ThreadCard({
  thread,
  store,
  active,
  editing,
  editingName,
  hoveredPinThreadId,
  renaming,
  onBeginRename,
  onCancelRename,
  onRenameBlur,
  onSetEditingName,
  onSetHoveredPinThreadId,
  onSubmitRename,
}) {
  const archiveLabel = thread.archived ? '恢复会话' : '归档会话';
  return (
    <div className={`thread-card ${active ? 'active' : ''}`}>
      {editing ? (
        <ThreadRenameCardContent
          thread={thread}
          editingName={editingName}
          renaming={renaming}
          onCancelRename={onCancelRename}
          onRenameBlur={onRenameBlur}
          onSetEditingName={onSetEditingName}
          onSubmitRename={onSubmitRename}
        />
      ) : (
        <ThreadDisplayCardContent
          thread={thread}
          store={store}
          hoveredPinThreadId={hoveredPinThreadId}
          onBeginRename={onBeginRename}
          onSetHoveredPinThreadId={onSetHoveredPinThreadId}
        />
      )}
      <button
        type="button"
        className={`thread-archive ${thread.archived ? 'active' : ''}`}
        aria-label={archiveLabel}
        title={archiveLabel}
        disabled={Boolean(store.threadArchiveLoadingByThread?.[thread.id])}
        onClick={() => runUIAction(() => store.archiveThread(thread.id, !thread.archived))}
      >
        <Archive size={15} />
      </button>
    </div>
  );
}

function ThreadRenameCardContent({ thread, editingName, renaming, onCancelRename, onRenameBlur, onSetEditingName, onSubmitRename }) {
  return (
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
          disabled={renaming}
          autoFocus
          onFocus={(event) => event.currentTarget.select()}
          onChange={(event) => onSetEditingName(event.target.value)}
          onClick={(event) => event.stopPropagation()}
          onBlur={(event) => onRenameBlur(event, thread)}
          onKeyDown={(event) => handleThreadRenameKeyDown(event, thread, onSubmitRename, onCancelRename)}
        />
        <button
          type="button"
          className="thread-rename-save"
          aria-label="保存别名"
          data-rename-save-button-for={thread.id}
          disabled={renaming}
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => runUIAction(() => onSubmitRename(thread))}
        >
          保存
        </button>
      </div>
    </>
  );
}

function handleThreadRenameKeyDown(event, thread, onSubmitRename, onCancelRename) {
  if (event.key === 'Enter') {
    event.preventDefault();
    runUIAction(() => onSubmitRename(thread));
  }
  if (event.key === 'Escape') {
    event.preventDefault();
    onCancelRename();
  }
}

function ThreadDisplayCardContent({ thread, store, hoveredPinThreadId, onBeginRename, onSetHoveredPinThreadId }) {
  const running = threadStatusBusy(thread.status);
  const threadLabel = displayThreadName(thread);
  const statusLabel = threadCardStatusLabel(thread, running);
  const statusDotState = threadStatusDotState(thread.status);
  const statusDotTitle = threadStatusDotTitle(thread.status, statusLabel);
  return (
    <>
      <ThreadPinButton
        thread={thread}
        hoveredPinThreadId={hoveredPinThreadId}
        onSetHoveredPinThreadId={onSetHoveredPinThreadId}
        onToggle={() => store.toggleThreadPin(thread.id)}
      />
      <button type="button" className="thread-main" onClick={() => runUIAction(() => store.setActiveThread(thread.id))}>
        <span
          className="thread-name"
          title={threadLabel}
          onClick={(event) => {
            event.stopPropagation();
            onBeginRename(thread);
          }}
        >
          {threadLabel}
        </span>
        <b>{threadProviderLabel(thread.provider)}</b>
        <ThreadStatusLine
          thread={thread}
          statusDotState={statusDotState}
          statusDotTitle={statusDotTitle}
          statusLabel={statusLabel}
        />
      </button>
    </>
  );
}

function ThreadPinButton({ thread, hoveredPinThreadId, onSetHoveredPinThreadId, onToggle }) {
  const pinned = thread.pinnedAt > 0 || thread.pinned;
  const pinLabel = pinned ? '取消置顶对话' : '置顶对话';
  const clearHover = () => onSetHoveredPinThreadId((current) => (current === thread.id ? '' : current));
  return (
    <button
      type="button"
      className={`thread-pin ${pinned ? 'active' : ''}`}
      aria-label={pinLabel}
      title={pinLabel}
      aria-pressed={pinned}
      onClick={() => runUIAction(onToggle)}
      onMouseEnter={() => onSetHoveredPinThreadId(thread.id)}
      onMouseLeave={clearHover}
      onFocus={() => onSetHoveredPinThreadId(thread.id)}
      onBlur={clearHover}
    >
      <Pin size={20} strokeWidth={2.2} />
      {hoveredPinThreadId === thread.id ? (
        <span className="thread-pin-tooltip" data-testid="thread-pin-tooltip" role="tooltip">
          {pinLabel}
        </span>
      ) : null}
    </button>
  );
}

function ThreadStatusLine({ thread, statusDotState, statusDotTitle, statusLabel }) {
  return (
    <span className="thread-status-row" data-thread-status={statusDotState}>
      <span
        className={`thread-status-dot thread-status-dot--${statusDotState}`}
        title={statusDotTitle}
        aria-hidden="true"
      />
      {statusLabel ? <span className="thread-status-label">{statusLabel}</span> : null}
      {thread.staleReason ? (
        <span className="thread-stale-badge" data-stale-reason={thread.staleReason}>
          {thread.staleReason === 'expired' ? '超7天' : '空对话'}
        </span>
      ) : null}
    </span>
  );
}

function firstConfigText(...values) {
  for (const value of values) {
    const text = normalizeConfigText(value);
    if (text) return text;
  }
  return '';
}

function activeThreadComposerConfig(store, activeThreadId) {
  return activeThreadId ? store.threadConfigByThread?.[activeThreadId] : null;
}

function modelSnapshotValue(canOverrideThread, activeThreadConfig, providerValue, defaultValue, key) {
  if (canOverrideThread) {
    return firstConfigText(activeThreadConfig?.override?.[key], activeThreadConfig?.effective?.[key], defaultValue);
  }
  return firstConfigText(providerValue, defaultValue);
}

function modelSelectorSnapshot(store, activeThreadId) {
  const activeThreadConfig = activeThreadComposerConfig(store, activeThreadId);
  const providerKey = normalizeProviderKey(firstConfigText(activeThreadConfig?.provider, store.providerConfig?.provider, store.provider));
  const providerDefaults = MODEL_DEFAULTS_BY_PROVIDER[providerKey] || MODEL_DEFAULTS_BY_PROVIDER.codex;
  const canOverrideThread = Boolean(activeThreadId && activeThreadConfig?.supportsThreadOverride);
  const activeModel = modelSnapshotValue(canOverrideThread, activeThreadConfig, store.providerConfig?.model, providerDefaults.model, 'model');
  const activeEffort = modelSnapshotValue(canOverrideThread, activeThreadConfig, store.providerConfig?.effort, providerDefaults.effort, 'effort');
  return {
    activeEffort,
    activeModel,
    activeThreadConfig,
    canOverrideThread,
    draftEffort: canOverrideThread ? normalizeConfigText(activeThreadConfig?.override?.effort) : activeEffort,
    draftModel: canOverrideThread ? normalizeConfigText(activeThreadConfig?.override?.model) : activeModel,
    providerKey,
  };
}

function modelSelectorTitle(disabled, canOverrideThread) {
  if (disabled) return '请先连接后端并选择项目';
  return canOverrideThread ? '线程执行配置' : '全局模型配置';
}

function nextModelDraft(providerKey, draft, patch, activeModel) {
  const next = { ...draft, ...patch };
  const nextEffort = normalizeConfigText(next.effort).toLowerCase();
  if (providerKey === 'claude' && nextEffort === 'max' && !isClaudeOpusFamilyModel(next.model || activeModel)) {
    return { ...next, effort: 'high' };
  }
  return next;
}

function loadedModelDraft(loaded, activeModel, activeEffort) {
  const loadedCanOverride = Boolean(loaded?.supportsThreadOverride);
  return {
    model: loadedCanOverride ? normalizeConfigText(loaded.override?.model) : activeModel,
    effort: loadedCanOverride ? normalizeConfigText(loaded.override?.effort) : activeEffort,
  };
}

function modelSelectorDerivedState({ activeEffort, activeModel, activeThreadConfig, canOverrideThread, disabled, draft, providerKey, store, activeThreadId }) {
  const selectedModel = canonicalizeModelValue(providerKey, draft.model || activeModel);
  const selectedEffort = draft.effort || activeEffort;
  return {
    canOverrideThread,
    disabled,
    effortOptions: appendCurrentEffortOption(providerKey, selectedEffort, selectedModel),
    inheritEffortLabel: activeEffort ? `默认（当前：${effortOptionFor(providerKey, activeEffort)?.label || activeEffort}）` : '默认',
    inheritModelLabel: activeModel ? `默认（当前：${modelOptionFor(providerKey, activeModel)?.label || activeModel}）` : '默认',
    inherited: canOverrideThread && !activeThreadConfig?.override?.model && !activeThreadConfig?.override?.effort,
    label: composerModelLabel(providerKey, activeModel, activeEffort),
    modelOptions: appendCurrentModelOption(providerKey, selectedModel),
    selectEffortValue: canOverrideThread ? draft.effort : draft.effort || activeEffort,
    selectModelValue: canOverrideThread
      ? canonicalizeModelValue(providerKey, draft.model)
      : canonicalizeModelValue(providerKey, draft.model || activeModel),
    selectorBusy: Boolean(store.threadConfigSaving || (activeThreadId && store.threadConfigLoadingByThread?.[activeThreadId])),
    selectorTitle: modelSelectorTitle(disabled, canOverrideThread),
  };
}

function useModelSelectorController({ store, activeThreadId, disabled, wrapRef }) {
  const [open, setOpen] = useState(false);
  const snapshot = modelSelectorSnapshot(store, activeThreadId);
  const { activeEffort, activeModel, activeThreadConfig, canOverrideThread, draftEffort, draftModel, providerKey } = snapshot;
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
  }, [open, wrapRef]);

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
    setDraft(loadedModelDraft(loaded, activeModel, activeEffort));
  };

  const saveModelConfig = async (patch) => {
    const next = nextModelDraft(providerKey, draft, patch, activeModel);
    setDraft(next);
    await store.saveComposerModelConfig?.({ threadId: activeThreadId, model: next.model, effort: next.effort });
  };

  const restoreInheritance = async () => {
    const restored = await store.restoreComposerModelInheritance?.({ threadId: activeThreadId });
    if (restored) setOpen(false);
  };

  return {
    ...modelSelectorDerivedState({ activeEffort, activeModel, activeThreadConfig, canOverrideThread, disabled, draft, providerKey, store, activeThreadId }),
    open,
    openSelector,
    restoreInheritance,
    saveModelConfig,
  };
}

function ModelSelector({ store, activeThreadId, disabled = false }) {
  const wrapRef = useRef(null);
  const controller = useModelSelectorController({ store, activeThreadId, disabled, wrapRef });

  return (
    <div className="composer-model-wrap" ref={wrapRef}>
      <ModelSelectorButton controller={controller} />
      {controller.open ? <ModelSelectorDropdown controller={controller} /> : null}
    </div>
  );
}

function ModelSelectorButton({ controller }) {
  return (
    <button
      type="button"
      className="composer-model"
      aria-label="选择模型"
      aria-expanded={controller.open}
      aria-haspopup="dialog"
      aria-busy={controller.selectorBusy}
      title={controller.selectorTitle}
      disabled={controller.disabled}
      onClick={() => runUIAction(controller.openSelector)}
    >
      {controller.label}
      <ChevronDown size={12} />
    </button>
  );
}

function ModelSelectorDropdown({ controller }) {
  const optionDisabled = controller.disabled || controller.selectorBusy;
  return (
    <div className="model-dropdown" role="dialog" aria-label="模型配置">
      <label>
        <span>模型</span>
        <select aria-label="模型" value={controller.selectModelValue} disabled={optionDisabled} onChange={(event) => runUIAction(() => controller.saveModelConfig({ model: event.target.value }))}>
          {controller.canOverrideThread ? <option value="">{controller.inheritModelLabel}</option> : null}
          {controller.modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
      </label>
      <label>
        <span>强度</span>
        <select aria-label="推理强度" value={controller.selectEffortValue} disabled={optionDisabled} onChange={(event) => runUIAction(() => controller.saveModelConfig({ effort: event.target.value }))}>
          {controller.canOverrideThread ? <option value="">{controller.inheritEffortLabel}</option> : null}
          {controller.effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
      </label>
      {controller.canOverrideThread && !controller.inherited ? (
        <button type="button" className="model-inherit" disabled={optionDisabled} onClick={() => runUIAction(controller.restoreInheritance)}>
          继承全局
        </button>
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
  return (
    Array.from(transfer.items || [])
    .filter((item) => item?.kind === 'file')
    .map((item) => item.getAsFile?.())
    .filter(Boolean)
  );
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
  return (
    line
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((cell) => cell.trim())
  );
}

function isMarkdownTableDivider(line) {
  const cells = markdownTableCells(line);
  return cells.length > 0 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function parsedMarkdownUrl(value) {
  try {
    return new URL(value, window.location?.origin || 'http://localhost');
  }
  catch {
    return null;
  }
}

function markdownImageUrl(value, protocol) {
  const allowed = new Set(['http:', 'https:', 'data:', 'file:']);
  return allowed.has(protocol) ? value : '';
}

function markdownLinkUrl(parsed, protocol) {
  const allowed = new Set(['http:', 'https:', 'mailto:', 'file:']);
  return allowed.has(protocol) ? parsed.href : '';
}

function safeMarkdownUrl(rawUrl, options = {}) {
  const value = (rawUrl || '').toString().trim();
  if (!value) return '';
  const localSrc = options.image ? imagePreviewSource(value) : '';
  if (localSrc) return localSrc;
  const parsed = parsedMarkdownUrl(value);
  if (!parsed) return '';
  const protocol = parsed.protocol.toLowerCase();
  if (options.image) return markdownImageUrl(value, protocol);
  return markdownLinkUrl(parsed, protocol);
}

const IMAGE_PATH_RE = /\.(?:png|jpe?g|webp|gif|svg)(?:[?#].*)?$/i;

const INLINE_IMAGE_PATH_RE = /(?:file:\/\/\/?[^\s`<>()"']+|~?\/(?!\/)[^\s`<>()"']+|\.{1,2}\/[^\s`<>()"']+|[A-Za-z]:[\\/][^\s`<>()"']+)\.(?:png|jpe?g|webp|gif|svg)(?:[?#][^\s`<>()"']*)?/gi;

function basenameFromPath(path) {
  const value = (path || '').toString().trim().split(/[?#]/, 1)[0];
  if (!value) return '';
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function fileURLToPath(value) {
  try {
    const url = new URL(value);
    if (url.protocol.toLowerCase() !== 'file:') return '';
    return decodeURIComponent(url.pathname || '');
  }
  catch {
    return '';
  }
}

function isGeneratedImagePath(value) {
  const path = (value || '').toString().trim();
  if (!path || !IMAGE_PATH_RE.test(path)) return false;
  return /(?:^|[/\\])\.codex[/\\]generated_images[/\\]/i.test(path);
}

function imagePreviewSource(rawValue) {
  const value = (rawValue || '').toString().trim();
  if (!value || !IMAGE_PATH_RE.test(value)) return '';
  if (/^data:image\//i.test(value) || /^https?:\/\//i.test(value)) return value;
  const localPath = /^file:\/\//i.test(value) ? fileURLToPath(value) : value;
  if (isGeneratedImagePath(localPath)) {
    return `/generated-image?path=${encodeURIComponent(localPath)}`;
  }
  if (/^[A-Za-z]:[\\/]/.test(localPath)) {
    return `file:///${localPath.replace(/\\/g, '/')}`;
  }
  if (/^(?:\/|~\/|\.{1,2}\/)/.test(localPath)) {
    return `file://${localPath}`;
  }
  return '';
}

function renderImagePreview(rawSource, altText, key) {
  const src = imagePreviewSource(rawSource);
  if (!src) return null;
  const label = (altText || '').toString().trim() || basenameFromPath(rawSource) || '图片预览';
  return <MarkdownImagePreview key={key} src={src} label={label} />;
}

function LightboxShell({ label, href, onClose, children }) {
  const displayLabel = (label || '').toString().trim() || '预览';
  return createPortal(
    <div className="image-lightbox" role="dialog" aria-modal="true" aria-label={`图片预览：${displayLabel}`}>
      <button type="button" className="image-lightbox-backdrop" aria-label="关闭图片预览" onClick={onClose} />
      <section className="image-lightbox-panel">
        <header>
          <strong>{displayLabel}</strong>
          <div>
            {href ? <a href={href} target="_blank" rel="noreferrer">外部打开</a> : null}
            <button type="button" aria-label="关闭图片预览" onClick={onClose}><X size={16} /></button>
          </div>
        </header>
        {children}
      </section>
    </div>,
    document.body,
  );
}

function MarkdownImagePreview({ src, label }) {
  const [failed, setFailed] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const displayLabel = (label || '').toString().trim() || '图片预览';
  useEffect(() => {
    if (!expanded) return undefined;
    const onKeyDown = (event) => {
      if (event.key === 'Escape') setExpanded(false);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [expanded]);

  if (failed) {
    return (
      <span className="message-image-fallback" role="note" title={src}>
        <span>图片无法加载</span>
        <code>{displayLabel}</code>
      </span>
    );
  }

  const lightbox = expanded ? (
    <LightboxShell label={displayLabel} href={src} onClose={() => setExpanded(false)}>
      <img src={src} alt={displayLabel} />
    </LightboxShell>
  ) : null;

  return (
    <>
      <button
        type="button"
        className="message-image-preview"
        aria-label={`放大图片 ${displayLabel}`}
        onClick={() => setExpanded(true)}
      >
        <img
          src={src}
          alt={displayLabel}
          loading="lazy"
          decoding="async"
          onError={() => setFailed(true)}
        />
        <span>点击放大</span>
      </button>
      {lightbox}
    </>
  );
}

function svgDataUrl(svg) {
  const value = (svg || '').toString();
  if (!value) return '';
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(value)}`;
}

function normalizeSvgAttributeValue(value) {
  return (
    Array.from((value || '').toString().trim())
    .filter((char) => {
      const charCode = char.charCodeAt(0);
      return charCode > 0x1f && charCode !== 0x7f && !/\s/.test(char);
    })
    .join('')
    .toLowerCase()
  );
}

function isDangerousSvgAttributeValue(value) {
  const normalized = normalizeSvgAttributeValue(value);
  if (
    normalized.startsWith('javascript:') ||
    normalized.startsWith('vbscript:') ||
    normalized.startsWith('data:text/html')
  ) {
    return true;
  }
  return (
    /url\((['"]?)(?:javascript:|vbscript:|data:text\/html)/.test(normalized) ||
    normalized.includes('expression(')
  );
}

function sanitizeMermaidSvg(svg) {
  const value = (svg || '').toString();
  if (!value) return '';
  if (typeof DOMParser === 'undefined' || typeof XMLSerializer === 'undefined') {
    throw new Error('当前环境不支持 SVG 清理');
  }

  const documentNode = new DOMParser().parseFromString(value, 'image/svg+xml');
  if (documentNode.querySelector('parsererror')) {
    throw new Error('Mermaid SVG 解析失败');
  }

  documentNode.querySelectorAll('script, foreignObject, iframe, object, embed').forEach((node) => {
    node.remove();
  });

  documentNode.querySelectorAll('*').forEach((node) => {
    Array.from(node.attributes).forEach((attribute) => {
      const name = attribute.name.toLowerCase();
      if (
        name.startsWith('on') ||
        isDangerousSvgAttributeValue(attribute.value)
      ) {
        node.removeAttribute(attribute.name);
      }
    });
  });

  return new XMLSerializer().serializeToString(documentNode.documentElement);
}

function trimTrailingImagePathPunctuation(value) {
  let path = (value || '').toString();
  let suffix = '';
  while (/[.,;:!?，。；：！？、]$/.test(path)) {
    suffix = `${path.at(-1)}${suffix}`;
    path = path.slice(0, -1);
  }
  return { path, suffix };
}

function renderPlainTextWithImagePreviews(text, keyPrefix) {
  const source = (text || '').toString();
  const parts = [];
  let lastIndex = 0;
  let matchIndex = 0;
  for (const match of source.matchAll(INLINE_IMAGE_PATH_RE)) {
    const token = match[0];
    const start = match.index ?? 0;
    const { path, suffix } = trimTrailingImagePathPunctuation(token);
    const image = renderImagePreview(path, basenameFromPath(path), `${keyPrefix}-image-${matchIndex}`);
    if (!image) continue;
    if (start > lastIndex) parts.push(source.slice(lastIndex, start));
    parts.push(image);
    if (suffix) parts.push(suffix);
    lastIndex = start + token.length;
    matchIndex += 1;
  }
  if (lastIndex < source.length) parts.push(source.slice(lastIndex));
  return parts.length > 0 ? parts : [source];
}

function inlineMarkdownPattern() {
  const tokenPattern = '!\\[[^\\]]*]\\([^)]+\\)|\\[[^\\]]+]\\([^)]+\\)|`[^`]+`|\\*\\*[^*]+\\*\\*|__[^_]+__|~~[^~]+~~|\\*[^*]+\\*|_[^_]+_';
  return new RegExp(`(${INLINE_IMAGE_PATH_RE.source})|(${tokenPattern})`, 'gi');
}

function appendInlineTextSegment(parts, source, start, end, keyPrefix) {
  if (end <= start) return;
  parts.push(...renderPlainTextWithImagePreviews(source.slice(start, end), keyPrefix));
}

function renderMarkdownImageToken(token, key) {
  const parsed = token.match(/^!\[([^\]]*)]\(([^)]+)\)$/);
  const src = safeMarkdownUrl(parsed?.[2], { image: true });
  if (!src) return token;
  return <MarkdownImagePreview key={key} src={src} label={parsed?.[1] || basenameFromPath(parsed?.[2])} />;
}

function renderMarkdownLinkToken(token, key) {
  const parsed = token.match(/^\[([^\]]+)]\(([^)]+)\)$/);
  const href = safeMarkdownUrl(parsed?.[2]);
  return href ? <a key={key} href={href} target="_blank" rel="noreferrer">{parsed?.[1]}</a> : parsed?.[1] || token;
}

function renderInlineCodeToken(token, key) {
  const codeText = token.slice(1, -1).trim();
  const image = renderImagePreview(codeText, basenameFromPath(codeText), key);
  return image || <code key={key}>{token.slice(1, -1)}</code>;
}

function renderStyledInlineToken(token, key) {
  if (token.startsWith('~~')) return <del key={key}>{token.slice(2, -2)}</del>;
  if (token.startsWith('*') && !token.startsWith('**')) return <em key={key}>{token.slice(1, -1)}</em>;
  if (token.startsWith('_') && !token.startsWith('__')) return <em key={key}>{token.slice(1, -1)}</em>;
  return <strong key={key}>{token.slice(2, -2)}</strong>;
}

function renderInlineMarkdownToken(token, key) {
  const inlineImage = renderImagePreview(token, basenameFromPath(token), key);
  if (inlineImage) return inlineImage;
  if (token.startsWith('![')) return renderMarkdownImageToken(token, key);
  if (token.startsWith('[')) return renderMarkdownLinkToken(token, key);
  if (token.startsWith('`')) return renderInlineCodeToken(token, key);
  return renderStyledInlineToken(token, key);
}

function renderInlineMarkdown(text, keyPrefix) {
  const source = (text || '').toString();
  const parts = [];
  let lastIndex = 0;
  let matchIndex = 0;
  for (const match of source.matchAll(inlineMarkdownPattern())) {
    appendInlineTextSegment(parts, source, lastIndex, match.index, `${keyPrefix}-text-${matchIndex}`);
    const token = match[0];
    parts.push(renderInlineMarkdownToken(token, `${keyPrefix}-inline-${matchIndex}`));
    lastIndex = match.index + token.length;
    matchIndex += 1;
  }
  appendInlineTextSegment(parts, source, lastIndex, source.length, `${keyPrefix}-text-tail`);
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

const CODE_FENCE_LANGUAGE_PREFIXES = Object.freeze([
  'mermaid',
  'javascript',
  'typescript',
  'plaintext',
  'markdown',
  'jsonc',
  'json',
  'bash',
  'shell',
  'text',
  'diff',
  'patch',
  'yaml',
  'toml',
  'html',
  'css',
  'tsx',
  'jsx',
  'zsh',
  'sh',
  'txt',
  'sql',
  'log',
  'xml',
  'go',
  'py',
  'md',
].sort((left, right) => right.length - left.length));

let mermaidModulePromise = null;

function loadMermaidModule() {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import('mermaid').then((module) => {
      const mermaid = module.default || module;
      return Promise.resolve(mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: 'base',
        themeVariables: {
          fontFamily: 'ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
        },
      })).then(() => mermaid);
    });
  }
  return mermaidModulePromise;
}

function isMermaidLanguage(language) {
  const value = (language || '').toString().trim().toLowerCase();
  return value === 'mermaid' || value === 'mmd';
}

function isMermaidSource(source) {
  const firstLine = normalizeMessageText(source).trim().split('\n')[0]?.trim().toLowerCase() || '';
  return /^(flowchart|graph|sequencediagram|classdiagram|statediagram|statediagram-v2|erdiagram|journey|gantt|pie|mindmap|timeline|gitgraph|quadrantchart|requirementdiagram)\b/.test(firstLine);
}

function MermaidDiagram({ source }) {
  const reactId = useId();
  const [state, setState] = useState({ status: 'loading', svg: '', error: '' });
  const [expanded, setExpanded] = useState(false);
  const diagram = normalizeMessageText(source).trim();

  useEffect(() => {
    let cancelled = false;
    if (!diagram) {
      setState({ status: 'error', svg: '', error: 'Mermaid 图表内容为空' });
      return () => { cancelled = true; };
    }
    setState({ status: 'loading', svg: '', error: '' });
    loadMermaidModule()
      .then((mermaid) => mermaid.render(`mermaid-${reactId.replace(/[^a-zA-Z0-9_-]/g, '')}`, diagram))
      .then((result) => {
        const svg = sanitizeMermaidSvg(result?.svg);
        if (!cancelled) setState({ status: 'ready', svg, error: '' });
      })
      .catch((error) => {
        if (!cancelled) setState({ status: 'error', svg: '', error: error?.message || String(error) });
      });
    return () => { cancelled = true; };
  }, [diagram, reactId]);

  useEffect(() => {
    if (!expanded) return undefined;
    const onKeyDown = (event) => {
      if (event.key === 'Escape') setExpanded(false);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [expanded]);

  if (state.status === 'ready' && state.svg) {
    const href = svgDataUrl(state.svg);
    return (
      <figure className="mermaid-diagram" aria-label="Mermaid 图表">
        <button
          type="button"
          className="mermaid-diagram-preview"
          aria-label="放大 Mermaid 图表"
          onClick={() => setExpanded(true)}
        >
          <img src={href} alt="Mermaid 图表" loading="lazy" decoding="async" />
          <span>点击放大</span>
        </button>
        {expanded ? (
          <LightboxShell label="Mermaid 图表" href={href} onClose={() => setExpanded(false)}>
            <div className="mermaid-lightbox-svg">
              <img src={href} alt="Mermaid 图表" />
            </div>
          </LightboxShell>
        ) : null}
      </figure>
    );
  }

  return (
    <figure className={`mermaid-diagram mermaid-diagram--${state.status}`} aria-label="Mermaid 图表">
      <figcaption>{state.status === 'loading' ? '正在渲染 Mermaid 图表...' : `Mermaid 渲染失败：${state.error}`}</figcaption>
      <pre><code>{diagram}</code></pre>
    </figure>
  );
}

function CodeBlock({ language = '', code = '' }) {
  if (isMermaidLanguage(language) || isMermaidSource(code)) {
    return <MermaidDiagram source={code} />;
  }
  return <pre><code>{code}</code></pre>;
}

function splitMarkdownFenceLine(line) {
  const markerIndex = line.indexOf('```');
  if (markerIndex < 0) return null;
  const prefix = line.slice(0, markerIndex);
  const afterMarker = line.slice(markerIndex + 3).replace(/^\s+/, '');
  if (!afterMarker) return { prefix, language: '', firstCodeLine: '' };

  const tokenMatch = afterMarker.match(/^([A-Za-z][\w-]*)(?:\s+(.*))?$/);
  if (tokenMatch && tokenMatch[2] !== undefined) {
    return { prefix, language: tokenMatch[1].toLowerCase(), firstCodeLine: tokenMatch[2] };
  }

  const lower = afterMarker.toLowerCase();
  const language = CODE_FENCE_LANGUAGE_PREFIXES.find((item) => lower.startsWith(item));
  if (language && afterMarker.length > language.length) {
    return { prefix, language, firstCodeLine: afterMarker.slice(language.length) };
  }

  return { prefix, language: afterMarker.toLowerCase(), firstCodeLine: '' };
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
  }
  catch {
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

function markdownBlockContext(lines) {
  const nodes = [];
  return {
    lines,
    nodes,
    nextKey: (kind) => `${kind}-${nodes.length}`,
  };
}

function consumeBlankMarkdownLine(context, index) {
  return context.lines[index].trim() ? null : { index: index + 1 };
}

function consumeMarkdownSeparator(context, index) {
  const trimmed = context.lines[index].trim();
  if (!/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) return null;
  context.nodes.push(<hr key={context.nextKey('separator')} />);
  return { index: index + 1 };
}

function readMarkdownCodeLines(lines, index, firstCodeLine) {
  const codeLines = firstCodeLine ? [firstCodeLine] : [];
  let cursor = index + 1;
  while (cursor < lines.length) {
    const closingIndex = lines[cursor].indexOf('```');
    if (closingIndex >= 0) {
      const beforeClose = lines[cursor].slice(0, closingIndex);
      if (beforeClose) codeLines.push(beforeClose);
      return { codeLines, index: cursor + 1 };
    }
    codeLines.push(lines[cursor]);
    cursor += 1;
  }
  return { codeLines, index: cursor };
}

function consumeMarkdownFence(context, index) {
  const fence = splitMarkdownFenceLine(context.lines[index]);
  if (!fence) return null;
  if (fence.prefix.trim()) {
    context.nodes.push(renderMarkdownParagraph([fence.prefix.trimEnd()], context.nextKey('paragraph')));
  }
  const key = context.nextKey('code');
  const code = readMarkdownCodeLines(context.lines, index, fence.firstCodeLine);
  context.nodes.push(<CodeBlock key={key} language={fence.language} code={code.codeLines.join('\n')} />);
  return { index: code.index };
}

function consumeMarkdownHeading(context, index) {
  const heading = context.lines[index].trim().match(/^(#{1,6})\s+(.+)$/);
  if (!heading) return null;
  const level = Math.min(6, heading[1].length);
  const HeadingTag = `h${level}`;
  context.nodes.push(
    <HeadingTag key={context.nextKey('heading')}>
      {renderInlineMarkdown(heading[2], `heading-${context.nodes.length}`)}
    </HeadingTag>,
  );
  return { index: index + 1 };
}

function markdownTableStarts(lines, index) {
  return (
    index + 1 < lines.length
    && lines[index].trim().includes('|')
    && isMarkdownTableDivider(lines[index + 1])
  );
}

function readMarkdownTableRows(lines, index) {
  const rows = [];
  let cursor = index;
  while (cursor < lines.length && lines[cursor].trim().includes('|')) {
    rows.push(markdownTableCells(lines[cursor]));
    cursor += 1;
  }
  return { rows, index: cursor };
}

function renderMarkdownTable(headers, rows, key) {
  return (
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
    </table>
  );
}

function consumeMarkdownTable(context, index) {
  if (!markdownTableStarts(context.lines, index)) return null;
  const key = context.nextKey('table');
  const headers = markdownTableCells(context.lines[index]);
  const body = readMarkdownTableRows(context.lines, index + 2);
  context.nodes.push(renderMarkdownTable(headers, body.rows, key));
  return { index: body.index };
}

function consumeMarkdownQuote(context, index) {
  if (!context.lines[index].trim().startsWith('>')) return null;
  const key = context.nextKey('quote');
  const quoteLines = [];
  let cursor = index;
  while (cursor < context.lines.length && context.lines[cursor].trim().startsWith('>')) {
    quoteLines.push(context.lines[cursor].trim().replace(/^>\s?/, ''));
    cursor += 1;
  }
  context.nodes.push(<blockquote key={key}>{renderMarkdownParagraph(quoteLines, `${key}-p`)}</blockquote>);
  return { index: cursor };
}

function readMarkdownTaskItems(lines, index) {
  const items = [];
  let cursor = index;
  while (cursor < lines.length) {
    const itemMatch = lines[cursor].trim().match(/^[-*]\s+\[([ xX])]\s+(.+)$/);
    if (!itemMatch) break;
    items.push({ checked: itemMatch[1].toLowerCase() === 'x', text: itemMatch[2] });
    cursor += 1;
  }
  return { items, index: cursor };
}

function consumeMarkdownTaskList(context, index) {
  if (!context.lines[index].trim().match(/^[-*]\s+\[([ xX])]\s+(.+)$/)) return null;
  const key = context.nextKey('task-list');
  const result = readMarkdownTaskItems(context.lines, index);
  context.nodes.push(
    <ul key={key} className="task-list">
      {result.items.map((item, itemIndex) => (
        <li key={`${key}-${itemIndex}`}>
          <input type="checkbox" checked={item.checked} disabled readOnly />
          <span>{renderInlineMarkdown(item.text, `${key}-${itemIndex}`)}</span>
        </li>
      ))}
    </ul>,
  );
  return { index: result.index };
}

function readMarkdownListItems(lines, index, ordered) {
  const items = [];
  let cursor = index;
  const itemPattern = ordered ? /^\d+\.\s+(.+)$/ : /^[-*]\s+(.+)$/;
  while (cursor < lines.length) {
    const itemMatch = lines[cursor].trim().match(itemPattern);
    if (!itemMatch) break;
    items.push(itemMatch[1]);
    cursor += 1;
  }
  return { items, index: cursor };
}

function consumeMarkdownList(context, index) {
  const trimmed = context.lines[index].trim();
  const unordered = trimmed.match(/^[-*]\s+(.+)$/);
  const ordered = trimmed.match(/^\d+\.\s+(.+)$/);
  if (!unordered && !ordered) return null;
  const key = context.nextKey('list');
  const ListTag = ordered ? 'ol' : 'ul';
  const result = readMarkdownListItems(context.lines, index, Boolean(ordered));
  context.nodes.push(
    <ListTag key={key}>
      {result.items.map((item, itemIndex) => <li key={`${key}-${itemIndex}`}>{renderInlineMarkdown(item, `${key}-${itemIndex}`)}</li>)}
    </ListTag>,
  );
  return { index: result.index };
}

function startsMarkdownBlock(lines, index) {
  const next = lines[index];
  const trimmed = next.trim();
  if (!trimmed) return true;
  if (next.includes('```') || trimmed.startsWith('>')) return true;
  if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) return true;
  if (/^(#{1,6})\s+(.+)$/.test(trimmed)) return true;
  if (/^[-*]\s+(.+)$/.test(trimmed) || /^\d+\.\s+(.+)$/.test(trimmed)) return true;
  return markdownTableStarts(lines, index);
}

function consumeMarkdownParagraphBlock(context, index) {
  const paragraph = [context.lines[index]];
  let cursor = index + 1;
  while (cursor < context.lines.length && !startsMarkdownBlock(context.lines, cursor)) {
    paragraph.push(context.lines[cursor]);
    cursor += 1;
  }
  context.nodes.push(renderMarkdownParagraph(paragraph, context.nextKey('paragraph')));
  return { index: cursor };
}

const MARKDOWN_BLOCK_CONSUMERS = [
  consumeBlankMarkdownLine,
  consumeMarkdownSeparator,
  consumeMarkdownFence,
  consumeMarkdownHeading,
  consumeMarkdownTable,
  consumeMarkdownQuote,
  consumeMarkdownTaskList,
  consumeMarkdownList,
  consumeMarkdownParagraphBlock,
];

function consumeMarkdownBlock(context, index) {
  for (const consumer of MARKDOWN_BLOCK_CONSUMERS) {
    const result = consumer(context, index);
    if (result) return result.index;
  }
  throw new Error('markdown block consumer pipeline is incomplete');
}

function renderMarkdownBlocks(lines) {
  const context = markdownBlockContext(lines);
  let index = 0;
  while (index < lines.length) index = consumeMarkdownBlock(context, index);
  return context.nodes;
}

function MarkdownMessage({ text }) {
  const nodes = renderMarkdownBlocks(normalizeMessageText(text).split('\n'));
  return <div className="message-markdown">{nodes.length > 0 ? nodes : <p />}</div>;
}

function MessageContent({ text }) {
  const output = detectMessageOutput(text);
  if (output.kind === 'markdown') return <MarkdownMessage text={output.text} />;
  return <StructuredMessage kind={output.kind} text={output.text} />;
}

function isReasoningMessage(message) {
  const kind = (message?.kind || '').toString().trim().toLowerCase();
  return kind === 'thinking' || kind === 'reasoning' || kind === 'tool' || kind === 'command' || kind === 'process' || kind === 'plan';
}

function reasoningTitle(message) {
  const kind = (message?.kind || '').toString().trim().toLowerCase();
  const title = (message?.title || '').toString().trim();
  if (title) return title;
  if (kind === 'plan') return '执行计划';
  if (kind === 'tool') return '调用工具';
  if (kind === 'command') return '执行命令';
  return 'AI 思考';
}

function reasoningKindMeta(message = {}) {
  const kind = (message?.kind || '').toString().trim().toLowerCase();
  if (kind === 'tool') return { label: '工具', tone: 'tool', Icon: Wrench };
  if (kind === 'command') return { label: '命令', tone: 'command', Icon: Terminal };
  if (kind === 'plan') return { label: '计划', tone: 'plan', Icon: CheckCircle2 };
  if (kind === 'process') return { label: '流程', tone: 'process', Icon: Sparkles };
  return { label: '思考', tone: 'thinking', Icon: Brain };
}

function reasoningStatusText(message = {}, done = true) {
  const status = (message?.status || '').toString().trim().toLowerCase();
  if (!done) return '执行中';
  if (status === 'failed' || status === 'error') return '失败';
  if (status === 'skipped' || status === 'cancelled' || status === 'canceled') return '已跳过';
  return '完成';
}

function reasoningStepDescription(message = {}) {
  const body = (message?.text || '').toString().trim();
  if (body) return body;
  const meta = reasoningKindMeta(message);
  if (meta.tone === 'plan') return '正在罗列执行计划并同步进度。';
  if (meta.tone === 'tool') return '正在调用工具并等待返回结果。';
  if (meta.tone === 'command') return '正在执行命令并读取输出。';
  if (meta.tone === 'process') return '正在推进任务流程并同步上下文。';
  return 'AI 正在分析上下文、选择工具并整理回答。';
}

function parsePlanItems(text) {
  const statusMarkers = {
    '✅': true,
    '☑': true,
    '✓': true,
    '✔': true,
    '🔄': false,
    '⏳': false,
    '○': false,
    '◯': false,
    '☐': false,
    '❌': false,
  };
  return (
    normalizeMessageText(text)
    .split('\n')
    .map((line) => line.trim())
    .map((line) => {
      const match = line.match(/^([✅☑✓✔🔄⏳○◯☐❌])?\s*(?:[-*]|\d+[.)])\s*(?:\[([ xX])\]\s*)?(.+)$/u);
      if (!match) return null;
      const label = (match[3] || '').trim();
      if (!label || /^plan$/i.test(label)) return null;
      return {
        text: label,
        done: match[1] ? statusMarkers[match[1]] === true : (match[2] || '').toLowerCase() === 'x',
      };
    })
    .filter(Boolean)
  );
}

function ExecutionPlan({ message }) {
  const items = parsePlanItems(message?.text);
  const completed = items.filter((item) => item.done).length;
  const summary = items.length > 0 ? `已完成 ${completed}/${items.length} 项任务` : '正在整理执行计划';
  return (
    <section className="execution-plan" aria-label="AI 执行计划">
      <header>
        <span>{reasoningTitle(message)}</span>
        <b>{summary}</b>
      </header>
      {items.length > 0 ? (
        <ol className="execution-plan-list">
          {items.map((item, index) => (
            <li key={`${item.text}-${index}`} data-plan-status={item.done ? 'done' : 'pending'}>
              <span className="execution-plan-check" aria-hidden="true">{item.done ? '✓' : ''}</span>
              <span>{item.text}</span>
            </li>
          ))}
        </ol>
      ) : (
        <MessageContent text={reasoningStepDescription(message)} />
      )}
    </section>
  );
}

function MessageAvatar({ role = 'assistant' }) {
  const isUser = role === 'user';
  const Icon = isUser ? UserRound : Bot;
  return (
    <div className={`avatar avatar--${isUser ? 'user' : 'assistant'}`} aria-hidden="true">
      <Icon size={18} strokeWidth={2.2} />
    </div>
  );
}

function AssistantMessageActions({ text }) {
  const [copyState, setCopyState] = useState('idle');
  const resetTimerRef = useRef(null);
  useEffect(() => () => {
    if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
  }, []);
  const copyableText = (text || '').toString();
  const canCopy = copyableText.trim().length > 0;
  const scheduleReset = (delay) => {
    if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
    resetTimerRef.current = window.setTimeout(() => {
      resetTimerRef.current = null;
      setCopyState('idle');
    }, delay);
  };
  const copyOutput = async () => {
    if (!canCopy) return;
    try {
      await copyTextToClipboard(copyableText);
      setCopyState('copied');
      scheduleReset(1800);
    }
    catch {
      setCopyState('failed');
      scheduleReset(2200);
    }
  };
  if (!canCopy) return null;
  const copied = copyState === 'copied';
  const failed = copyState === 'failed';
  let copyLabel = '复制';
  if (copied) {
    copyLabel = '已复制';
  } else if (failed) {
    copyLabel = '复制失败';
  }
  return (
    <div className="message-actions" aria-label="AI 输出操作">
      <button
        type="button"
        className={`message-copy${copied ? ' is-copied' : ''}${failed ? ' is-failed' : ''}`}
        aria-label="复制 AI 输出"
        title="复制 AI 输出"
        onClick={() => { void copyOutput(); }}
      >
        {copied ? <CheckCircle2 size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
        <span>{copyLabel}</span>
      </button>
    </div>
  );
}

function positiveTimestampNumber(value) {
  if (!Number.isFinite(value) || value <= 0) return 0;
  return value < 1_000_000_000_000 ? value * 1000 : value;
}

function numericTextTimestampMs(text) {
  if (!/^\d+(?:\.\d+)?$/.test(text)) return 0;
  return positiveTimestampNumber(Number(text));
}

function parsedDateTimestampMs(text) {
  const parsed = Date.parse(text);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function timestampMs(value) {
  if (typeof value === 'number') return positiveTimestampNumber(value);
  const text = (value || '').toString().trim();
  return numericTextTimestampMs(text) || parsedDateTimestampMs(text);
}

function durationLabelFromMs(ms, options = {}) {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  if (totalSeconds <= 0 && !options.showZero) return '';
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes <= 0) return `${seconds}s`;
  return `${minutes}m ${seconds}s`;
}

function useElapsedLabel(startValue, endValue, active) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return undefined;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active]);
  const start = timestampMs(startValue);
  if (!start) return '';
  const completed = timestampMs(endValue);
  if (!active && !completed) return '';
  const end = completed || now;
  if (end < start) return '';
  return durationLabelFromMs(end - start, { showZero: active });
}

function ReasoningTrace({ message, active = false }) {
  const done = !active && message?.done !== false;
  const hookElapsed = useElapsedLabel(message?.time, message?.completedAt, !done);
  const elapsed = (done && typeof message?.elapsedMs === 'number' && message.elapsedMs > 0)
    ? durationLabelFromMs(message.elapsedMs)
    : hookElapsed;
  const statusLabel = done ? `已处理${elapsed ? ` ${elapsed}` : ''}` : `正在思考${elapsed ? ` ${elapsed}` : ''}`;
  const meta = reasoningKindMeta(message);
  const StatusIcon = done ? CheckCircle2 : Clock3;
  const StepIcon = meta.Icon;
  return (
    <article className={`reasoning-message${done ? '' : ' is-active'}`} aria-label="AI 思考记录">
      <details className="reasoning-trace">
        <summary>
          <span className="reasoning-trace-status">
            <StatusIcon size={15} aria-hidden="true" />
            {statusLabel}
          </span>
          <em>{reasoningTitle(message)}</em>
        </summary>
        <div className="reasoning-step-list">
          <section className={`reasoning-step reasoning-step--${meta.tone}`} aria-label={`${meta.label}步骤`}>
            <div className="reasoning-step-icon">
              <StepIcon size={15} aria-hidden="true" />
            </div>
            <div className="reasoning-step-body">
              <header>
                <span>{meta.label}</span>
                <strong>{reasoningTitle(message)}</strong>
                <b aria-label={`执行状态：${reasoningStatusText(message, done)}`}>{reasoningStatusText(message, done)}</b>
              </header>
              {meta.tone === 'plan' ? <ExecutionPlan message={message} /> : <MessageContent text={reasoningStepDescription(message)} />}
            </div>
          </section>
        </div>
      </details>
    </article>
  );
}

function syntheticReasoningMessage({ activeTurn, sending }) {
  if (!activeTurn && !sending) return null;
  return {
    id: `thinking-${activeTurn?.id || 'sending'}`,
    role: 'assistant',
    kind: 'thinking',
    title: '正在处理请求',
    text: '',
    time: activeTurn?.startedAt || new Date().toISOString(),
    done: false,
  };
}

function useComposerInteractions({
  attachments,
  attachPaths,
  attachDroppedFiles,
  attachPastedImages,
  removeAttachment,
  projectActionBlocked,
  canUseProjectActions,
}) {
  const [previewAttachment, setPreviewAttachment] = useState(null);
  const [dropActive, setDropActive] = useState(false);
  const isComposingRef = useRef(false);
  const activePreview = previewAttachment && attachments.some((item) => attachmentKey(item) === attachmentKey(previewAttachment))
    ? previewAttachment
    : null;

  const previewAttachmentItem = (item) => {
    setPreviewAttachment(item);
  };
  const removeAttachmentItem = (item) => {
    removeAttachment(attachmentKey(item));
    if (activePreview && attachmentKey(activePreview) === attachmentKey(item)) {
      setPreviewAttachment(null);
    }
  };
  const handlers = useComposerTransferHandlers({
    attachDroppedFiles,
    attachPaths,
    attachPastedImages,
    canUseProjectActions,
    projectActionBlocked,
    setDropActive,
  });

  return {
    activePreview,
    dropActive,
    handleCompositionEnd: () => { isComposingRef.current = false; },
    handleCompositionStart: () => { isComposingRef.current = true; },
    isComposing: () => isComposingRef.current,
    previewAttachmentItem,
    removeAttachmentItem,
    setPreviewAttachment,
    ...handlers,
  };
}

function useComposerTransferHandlers({ attachDroppedFiles, attachPaths, attachPastedImages, canUseProjectActions, projectActionBlocked, setDropActive }) {
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
  }, [attachPaths, canUseProjectActions, setDropActive]);
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
  return { handleDragEnter, handleDragLeave, handleDragOver, handleDrop, handlePaste };
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
  const hasComposerInput = Boolean(textValue(draft) || attachments.length > 0);
  const canSend = canUseProjectActions && !sending && hasComposerInput;
  const projectActionBlocked = !canUseProjectActions;
  const projectActionBlockedTitle = '请先连接后端并选择项目';
  const composer = useComposerInteractions({
    attachments,
    attachPaths,
    attachDroppedFiles,
    attachPastedImages,
    removeAttachment,
    projectActionBlocked,
    canUseProjectActions,
  });

  const handleKeyDown = useComposerSendKeyHandler({ canSend, composer, sendMessage });

  return (
    <footer
      id="chat-input-bar"
      className={`${composerClass}${composer.dropActive ? ' drop-active' : ''}`}
      data-testid="composer-dock"
      data-file-drop-target=""
      onDragEnter={composer.handleDragEnter}
      onDragOver={composer.handleDragOver}
      onDragLeave={composer.handleDragLeave}
      onDrop={(event) => runUIAction(() => composer.handleDrop(event))}
    >
      <div className="composer-card">
        {composer.dropActive ? <div className="composer-drop-hint" aria-live="polite">松开即可添加附件</div> : null}
        <ComposerAttachments attachments={attachments} onPreview={composer.previewAttachmentItem} onRemove={composer.removeAttachmentItem} />
        <ComposerTextarea
          composer={composer}
          draft={draft}
          handleKeyDown={handleKeyDown}
          setDraft={setDraft}
        />
        <ComposerMeta
          canSend={canSend}
          canUseProjectActions={canUseProjectActions}
          modelThreadId={modelThreadId}
          permission={permission}
          projectActionBlocked={projectActionBlocked}
          projectActionBlockedTitle={projectActionBlockedTitle}
          selectFiles={selectFiles}
          sendMessage={sendMessage}
          setPermission={setPermission}
          showProviderToggle={showProviderToggle}
          store={store}
        />
      </div>
      <ComposerPreviewModal composer={composer} />
    </footer>
  );
}

function ComposerTextarea({ composer, draft, handleKeyDown, setDraft }) {
  return (
    <textarea
      id="composer-input"
      data-testid="composer-input"
      data-file-drop-target=""
      rows={3}
      value={draft}
      onChange={(event) => setDraft(event.target.value)}
      onPaste={(event) => { runUIAction(() => composer.handlePaste(event)); }}
      onCompositionStart={composer.handleCompositionStart}
      onCompositionEnd={composer.handleCompositionEnd}
      onKeyDown={handleKeyDown}
      placeholder="输入给 Agent 的内容，Enter 发送，Shift+Enter 换行"
    />
  );
}

function ComposerPreviewModal({ composer }) {
  if (!composer.activePreview) return null;
  return (
    <AttachmentPreviewModal
      attachment={composer.activePreview}
      onClose={() => composer.setPreviewAttachment(null)}
      onRemove={() => composer.removeAttachmentItem(composer.activePreview)}
    />
  );
}

function useComposerSendKeyHandler({ canSend, composer, sendMessage }) {
  return (event) => {
    if (event.key !== 'Enter' || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return;
    const keyCode = Number(event.keyCode || event.which || 0);
    const imeLikely = event.isComposing || composer.isComposing() || keyCode === 229 || event.key === 'Process' || event.key === 'Unidentified';
    if (imeLikely) return;
    event.preventDefault();
    if (!canSend) return;
    runUIAction(() => sendMessage());
  };
}

function ComposerAttachments({ attachments, onPreview, onRemove }) {
  if (attachments.length === 0) return null;
  return (
    <div className="attachments">
      {attachments.map((item) => (
        <span key={attachmentKey(item)} className={`attachment-pill${item.kind === 'image' ? ' attachment-pill--image' : ''}`}>
          <button type="button" className="attachment-preview" aria-label={`预览附件 ${item.name || item.path}`} onClick={() => onPreview(item)}>
            {item.kind === 'image' && item.previewUrl ? <img src={item.previewUrl} alt="" /> : <File size={14} />}
            <span>{item.name || item.path}</span>
          </button>
          <button type="button" className="attachment-remove" aria-label={`移除附件 ${item.name || item.path}`} onClick={() => onRemove(item)}>
            <X size={12} />
          </button>
        </span>
      ))}
    </div>
  );
}

function ComposerMeta({
  canSend,
  canUseProjectActions,
  modelThreadId,
  permission,
  projectActionBlocked,
  projectActionBlockedTitle,
  selectFiles,
  sendMessage,
  setPermission,
  showProviderToggle,
  store,
}) {
  return (
    <div className="composer-meta">
      <button
        type="button"
        className="composer-attach"
        aria-label="添加文件"
        title={projectActionBlocked ? projectActionBlockedTitle : '添加文件'}
        disabled={projectActionBlocked}
        onClick={() => {
          if (!projectActionBlocked) runUIAction(() => selectFiles());
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
        <button type="button" className="send" aria-label="发送消息" disabled={!canSend} onClick={() => { if (canSend) runUIAction(() => sendMessage()); }}>
          <Send size={18} />
        </button>
      </div>
    </div>
  );
}

function Conversation(props) {
  const {
    messages,
    sending,
    projectPath,
    tokenUsage,
    activeThreadId,
    activeThread,
    statusEntry,
    activeTurn,
    timelineBlocked,
    timelineContentBlocked = false,
  } = props;
  const introMode = !activeThreadId && !timelineBlocked && messages.length === 0;
  const hasActiveReasoning = messages.some((message) => isReasoningMessage(message) && message.done === false);
  const pendingReasoning = !introMode && !timelineBlocked && !hasActiveReasoning && !hasAssistantReplyAfterLastUser(messages)
    ? syntheticReasoningMessage({ activeTurn, sending })
    : null;
  const composer = <ConversationComposer {...props} floating={introMode} showProviderToggle={!activeThreadId} />;
  return (
    <section className={`conversation${introMode ? ' conversation--intro' : ''}`}>
      <ConversationTimeline
        composer={composer}
        introMode={introMode}
        messages={messages}
        pendingReasoning={pendingReasoning}
        projectPath={projectPath}
        activeThreadId={activeThreadId}
        timelineContentBlocked={timelineContentBlocked}
      />
      {!introMode ? (
        <WorkStatus
          sending={sending}
          loading={timelineContentBlocked}
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

function ConversationComposer({
  floating,
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
  showProviderToggle,
  canUseProjectActions,
}) {
  return (
    <ComposerDock
      floating={floating}
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
}

function useMaterializedTimelineWindow({ activeThreadId, introMode, messages, timelineContentBlocked }) {
  const materializationKey = `${activeThreadId || ''}:${introMode ? 'intro' : 'thread'}:${timelineContentBlocked ? 'blocked' : 'ready'}`;
  const [materialization, setMaterialization] = useState(() => ({
    count: TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
    key: materializationKey,
  }));
  const messageCount = messages.length;
  const materializedCount = materialization.key === materializationKey
    ? materialization.count
    : TIMELINE_INITIAL_MATERIALIZED_MESSAGES;

  useEffect(() => {
    setMaterialization((current) => {
      if (current.key === materializationKey) return current;
      return { count: TIMELINE_INITIAL_MATERIALIZED_MESSAGES, key: materializationKey };
    });
  }, [materializationKey]);

  useEffect(() => {
    setMaterialization((current) => {
      if (current.key !== materializationKey) return current;
      return {
        ...current,
        count: Math.min(
          Math.max(TIMELINE_INITIAL_MATERIALIZED_MESSAGES, current.count),
          Math.max(TIMELINE_INITIAL_MATERIALIZED_MESSAGES, messageCount),
        ),
      };
    });
  }, [materializationKey, messageCount]);

  const visibleStart = Math.max(0, messageCount - materializedCount);
  const visibleMessages = useMemo(() => messages.slice(visibleStart), [messages, visibleStart]);
  const hiddenOlderCount = visibleStart;
  const revealOlder = useCallback(() => {
    setMaterialization((current) => {
      const currentCount = current.key === materializationKey
        ? current.count
        : TIMELINE_INITIAL_MATERIALIZED_MESSAGES;
      return {
        count: Math.min(
          messageCount,
          Math.max(TIMELINE_INITIAL_MATERIALIZED_MESSAGES, currentCount) + TIMELINE_MATERIALIZATION_INCREMENT,
        ),
        key: materializationKey,
      };
    });
  }, [materializationKey, messageCount]);

  return {
    hiddenOlderCount,
    revealOlder,
    visibleMessages,
  };
}

function ConversationTimeline({ composer, introMode, messages, pendingReasoning, projectPath, activeThreadId, timelineContentBlocked }) {
  const {
    hiddenOlderCount,
    revealOlder,
    visibleMessages,
  } = useMaterializedTimelineWindow({ activeThreadId, introMode, messages, timelineContentBlocked });
  const handleScroll = useCallback((event) => {
    if (hiddenOlderCount <= 0 || timelineContentBlocked) return;
    if (event.currentTarget.scrollTop <= TIMELINE_SCROLL_LOAD_THRESHOLD) revealOlder();
  }, [hiddenOlderCount, revealOlder, timelineContentBlocked]);

  return (
    <div className="timeline" data-testid="chat-timeline" onScroll={handleScroll}>
      {introMode ? <IntroChatStage composer={composer} projectPath={projectPath} /> : null}
      {!introMode && !timelineContentBlocked && hiddenOlderCount > 0 ? (
        <TimelineOlderMessagesMarker hiddenCount={hiddenOlderCount} onReveal={revealOlder} />
      ) : null}
      {!introMode && !timelineContentBlocked ? visibleMessages.map((message) => <TimelineMessage key={message.id} message={message} />) : null}
      {!introMode && timelineContentBlocked ? <TimelineLoadingPlaceholder /> : null}
      {pendingReasoning ? <ReasoningTrace key={pendingReasoning.id} message={pendingReasoning} active /> : null}
    </div>
  );
}

function TimelineOlderMessagesMarker({ hiddenCount, onReveal }) {
  return (
    <div className="timeline-placeholder" data-testid="timeline-older-marker">
      <button type="button" className="ghost" onClick={onReveal}>
        显示更早的消息（{hiddenCount} 条）
      </button>
    </div>
  );
}

function IntroChatStage({ composer, projectPath }) {
  return (
    <div className="intro-chat-stage">
      <div className="empty-chat">
        <h2>我们应该在 {projectDisplayName(projectPath)} 中构建什么？</h2>
        <p>{projectPath}</p>
      </div>
      {composer}
    </div>
  );
}

function TimelineMessage({ message }) {
  if (isReasoningMessage(message)) return <ReasoningTrace message={message} active={message.done === false} />;
  return (
    <article className={`message ${message.role}`}>
      <MessageAvatar role={message.role} />
      <div className="bubble">
        <header><span>{message.role === 'user' ? '你' : 'AI'}</span><time>{formatTime(message.time)}</time></header>
        <MessageContent text={message.text} />
        {message.role === 'assistant' ? <AssistantMessageActions text={message.text} /> : null}
      </div>
    </article>
  );
}

function TimelineLoadingPlaceholder() {
  return (
    <div className="timeline-placeholder" data-testid="timeline-loading-placeholder" aria-live="polite">
      <span className="timeline-placeholder-line" />
      <span className="timeline-placeholder-line timeline-placeholder-line--short" />
      <p>正在同步会话历史</p>
    </div>
  );
}

function WorkStatus({ sending, loading, activeThreadId, activeThread, statusEntry, tokenUsage }) {
  const status = workStatusForThread({ sending, loading, activeThreadId, activeThread, statusEntry });
  const className = `work-status work-status--${status.tone}${status.busy ? ' is-busy' : ''}`;
  const tokenText = tokenUsage ? `${tokenUsage.usedTokens} / ${tokenUsage.contextWindowTokens} tokens` : 'token usage 等待后端同步';
  return (
    <div className={className}>
      <span className="spinner" aria-hidden="true" />
      <span className="work-status-label">{status.label}</span>
      <em>{status.details}</em>
      <code title={tokenText}>{tokenText}</code>
    </div>
  );
}

function activityPanelNextKeyboardHeight(event, currentHeight, maxHeight) {
  const keyActions = {
    ArrowUp: currentHeight + RESIZER_KEY_STEP,
    PageUp: currentHeight + RESIZER_KEY_STEP,
    ArrowDown: currentHeight - RESIZER_KEY_STEP,
    PageDown: currentHeight - RESIZER_KEY_STEP,
    Home: ACTIVITY_PANEL_MIN_HEIGHT,
    End: maxHeight,
  };
  return keyActions[event.key] ?? null;
}

function useRuntimePanelLayout() {
  const [viewportHeight, setViewportHeight] = useState(currentViewportHeight);
  const [activityPanelHeight, setActivityPanelHeight] = useState(() => clampActivityPanelHeight(ACTIVITY_PANEL_DEFAULT_HEIGHT));
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
    const nextHeight = activityPanelNextKeyboardHeight(event, activityPanelHeight, activityPanelMax);
    if (nextHeight === null) return;
    event.preventDefault();
    setActivityPanelHeight(clampActivityPanelHeight(nextHeight, viewportHeight));
  };
  return {
    activityPanelHeight,
    activityPanelMax,
    beginActivityPanelResize,
    handleActivityPanelResizeKeyDown,
    viewportHeight,
  };
}

function RuntimePanel({ diffText, tokenUsage, activityStats, warnings, runtimeResults }) {
  const [collapsedDiffFiles, setCollapsedDiffFiles] = useState(() => new Set());
  const diffSummary = useMemo(() => summarizeUnifiedDiff(diffText), [diffText]);
  const runtimeLayout = useRuntimePanelLayout();
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
      style={runtimePanelHeightVars(runtimeLayout.activityPanelHeight, runtimeLayout.viewportHeight)}
    >
      <RuntimeToolbar diffSummary={diffSummary} />
      <RuntimeDiffView diffText={diffText} diffSummary={diffSummary} collapsedFiles={collapsedDiffFiles} onToggleFile={toggleDiffFile} />
      <RuntimeActivityPanel
        activityStats={activityStats}
        tokenUsage={tokenUsage}
        warnings={warnings}
        runtimeResults={runtimeResults}
        activityPanelHeight={runtimeLayout.activityPanelHeight}
        activityPanelMaxHeight={runtimeLayout.activityPanelMax}
        activityPanelMinHeight={ACTIVITY_PANEL_MIN_HEIGHT}
        onResizeKeyDown={runtimeLayout.handleActivityPanelResizeKeyDown}
        onResizeStart={runtimeLayout.beginActivityPanelResize}
      />
    </aside>
  );
}

function RuntimeToolbar({ diffSummary }) {
  return (
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
  );
}

function RuntimeDiffView({ diffText, diffSummary, collapsedFiles, onToggleFile }) {
  if (!diffText) return <div className="diff-empty">暂无代码变更</div>;
  return (
    <div className="diff-empty">
      <div className="diff-view" data-testid="diff-view">
        {diffSummary.files.map((file, index) => (
          <RuntimeDiffFile
            key={`${file.filename}:${index}`}
            file={file}
            index={index}
            collapsed={collapsedFiles.has(`${file.filename}:${index}`)}
            onToggle={() => onToggleFile(`${file.filename}:${index}`)}
          />
        ))}
      </div>
    </div>
  );
}

function RuntimeDiffFile({ file, index, collapsed, onToggle }) {
  return (
    <section className={`diff-file-group${collapsed ? ' is-collapsed' : ''}`}>
      <div className="diff-file-header">
        <button type="button" className="diff-file-toggle" aria-expanded={!collapsed} aria-controls={`runtime-diff-file-${index}`} onClick={onToggle}>
          <span className="diff-file-title">
            <ChevronDown className="diff-file-caret" size={14} aria-hidden="true" />
            <span className="diff-file-name">{file.filename}</span>
          </span>
          <span className="diff-file-stats" aria-hidden="true"><b className="good">+{file.additions}</b><b className="bad">-{file.deletions}</b></span>
        </button>
      </div>
      {!collapsed ? <RuntimeDiffLines file={file} index={index} /> : null}
    </section>
  );
}

function RuntimeDiffLines({ file, index }) {
  return (
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
  const [activeStat, setActiveStat] = useState(null);
  const [activeWarning, setActiveWarning] = useState(null);
  const panelRef = useRef(null);
  const stats = useMemo(() => activityStats || {}, [activityStats]);
  const statItems = useMemo(() => activityStatItems(stats), [stats]);
  const detailEntriesByStat = useMemo(() => Object.fromEntries(
    statItems.map((item) => [item.key, activityStatDetailEntries(stats, item.key)]),
  ), [statItems, stats]);
  const logEntries = useMemo(() => runtimeLogEntries(warnings, runtimeResults), [warnings, runtimeResults]);
  const activeWarningEntry = useMemo(
    () => logEntries.find((entry) => entry.id === activeWarning?.id) || null,
    [activeWarning, logEntries],
  );
  const activeStatItem = useMemo(
    () => statItems.find((item) => item.key === activeStat?.key) || null,
    [activeStat, statItems],
  );
  const activeStatDetailEntries = activeStat ? detailEntriesByStat[activeStat.key] || [] : [];
  const hideStatTooltip = useCallback(() => setActiveStat(null), []);
  const hideWarningPopover = useCallback(() => setActiveWarning(null), []);
  const toggleStatTooltip = (key, element) => {
    setActiveWarning(null);
    setActiveStat((current) => (
      current?.key === key ? null : { key, anchorRect: elementViewportRect(element) }
    ));
  };
  const toggleWarningPopover = (id, element) => {
    setActiveStat(null);
    setActiveWarning((current) => (
      current?.id === id ? null : {
        id,
        anchorRect: elementViewportRect(element),
        panelRect: elementViewportRect(panelRef.current),
      }
    ));
  };
  const handleStatKeyDown = (event, key) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggleStatTooltip(key, event.currentTarget);
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      hideStatTooltip();
    }
  };
  const handleWarningKeyDown = (event, id) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      toggleWarningPopover(id, event.currentTarget);
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      hideWarningPopover();
    }
  };

  useEffect(() => {
    if (!activeStat && !activeWarning) return undefined;
    const handleDocumentPointerDown = (event) => {
      if (panelRef.current?.contains(event.target)) return;
      hideStatTooltip();
      hideWarningPopover();
    };
    const handleDocumentKeyDown = (event) => {
      if (event.key !== 'Escape') return;
      hideStatTooltip();
      hideWarningPopover();
    };
    document.addEventListener('pointerdown', handleDocumentPointerDown);
    document.addEventListener('keydown', handleDocumentKeyDown);
    return () => {
      document.removeEventListener('pointerdown', handleDocumentPointerDown);
      document.removeEventListener('keydown', handleDocumentKeyDown);
    };
  }, [activeStat, activeWarning, hideStatTooltip, hideWarningPopover]);

  return (
    <section
      className="runtime-activity-panel"
      aria-label="工具使用面板"
      ref={panelRef}
      onPointerDown={(event) => {
        if (event.target.closest('.runtime-stat, .runtime-stat-tooltip, .warning-log-line, .warning-log-popover')) return;
        hideStatTooltip();
        hideWarningPopover();
      }}
      onClick={(event) => {
        if (event.target.closest('.runtime-stat, .runtime-stat-tooltip, .warning-log-line, .warning-log-popover')) return;
        hideStatTooltip();
        hideWarningPopover();
      }}
    >
      <RuntimeActivityResizer
        activityPanelHeight={activityPanelHeight}
        activityPanelMaxHeight={activityPanelMaxHeight}
        activityPanelMinHeight={activityPanelMinHeight}
        onResizeKeyDown={onResizeKeyDown}
        onResizeStart={onResizeStart}
      />
      <RuntimeStatList
        activeStat={activeStat}
        onStatKeyDown={handleStatKeyDown}
        onToggleStat={toggleStatTooltip}
        statItems={statItems}
        tokenUsage={tokenUsage}
      />
      <RuntimeStatTooltip
        activeStat={activeStat}
        detailEntries={activeStatDetailEntries}
        item={activeStatItem}
      />
      <RuntimeLogLines
        activeWarning={activeWarning}
        entries={logEntries}
        onWarningKeyDown={handleWarningKeyDown}
        onToggleWarning={toggleWarningPopover}
      />
      <RuntimeWarningPopover entry={activeWarningEntry} hoverState={activeWarning} />
    </section>
  );
}

function RuntimeActivityResizer({ activityPanelHeight, activityPanelMaxHeight, activityPanelMinHeight, onResizeKeyDown, onResizeStart }) {
  return (
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
  );
}

function RuntimeStatList({ activeStat, onStatKeyDown, onToggleStat, statItems, tokenUsage }) {
  return (
    <div className="runtime-icons" role="list" aria-label="工具调用统计">
      {statItems.map((item) => (
        <RuntimeStatItem
          key={item.key}
          item={item}
          activeStat={activeStat}
          onKeyDown={onStatKeyDown}
          onToggle={onToggleStat}
        />
      ))}
      <span className="runtime-context" title={tokenUsage ? `上下文使用率 ${tokenUsage.usedPercent.toFixed(1)}%` : '等待后端同步上下文使用率'}>
        {tokenUsage ? `${tokenUsage.usedPercent.toFixed(1)}% context` : 'context --'}
      </span>
    </div>
  );
}

function RuntimeStatItem({ activeStat, item, onKeyDown, onToggle }) {
  const { key, label, icon: Icon, className, value } = item;
  return (
    <span
      className={`runtime-stat ${className}`}
      role="listitem"
      tabIndex={0}
      aria-expanded={activeStat?.key === key}
      aria-haspopup="dialog"
      aria-label={key === 'tool' ? '工具调用总数' : `${label} 调用次数`}
      title={`${label}: ${value}`}
      onClick={(event) => {
        event.stopPropagation();
        onToggle(key, event.currentTarget);
      }}
      onKeyDown={(event) => onKeyDown(event, key)}
    >
      <Icon size={16} aria-hidden="true" />
      <strong>{value}</strong>
    </span>
  );
}

function RuntimeStatTooltip({ activeStat, detailEntries, item }) {
  if (!activeStat || !item) return null;
  return (
    <span className="runtime-stat-tooltip" data-testid="runtime-stat-tooltip" role="tooltip" style={runtimeStatTooltipStyle(activeStat.anchorRect)}>
      <span className="runtime-stat-tooltip-title"><b>{item.label}</b><strong>{item.value}</strong></span>
      {detailEntries.length > 0 ? (
        <span className="runtime-stat-tooltip-list">
          {detailEntries.map((entry) => (
            <span key={entry.name} className="runtime-stat-tooltip-row">
              <span className="runtime-stat-tooltip-name" title={entry.name}>{entry.name}</span>
              <strong>{entry.count}</strong>
            </span>
          ))}
        </span>
      ) : <span className="runtime-stat-tooltip-empty">后端暂无明细</span>}
    </span>
  );
}

function RuntimeLogLines({ activeWarning, entries, onWarningKeyDown, onToggleWarning }) {
  return (
    <div className="log-lines" data-testid="warning-log-panel">
      {entries.length === 0 ? <p><time>--:--</time> runtime log 等待事件</p> : null}
      {entries.map((entry) => (
        <p
          key={entry.id}
          className={`warning-log-line runtime-log-line--${entry.runtimeKind || 'warning'}`}
          tabIndex={0}
          aria-expanded={activeWarning?.id === entry.id}
          aria-haspopup="dialog"
          onClick={(event) => {
            event.stopPropagation();
            onToggleWarning(entry.id, event.currentTarget);
          }}
          onKeyDown={(event) => onWarningKeyDown(event, entry.id)}
        >
          <time>{formatTime(runtimeLogTimestamp(entry))}</time> <b>{runtimeLogInlineLabel(entry)}</b>
          {Number(entry.occurrenceCount) > 1 ? <span> ×{Number(entry.occurrenceCount)}</span> : null}
        </p>
      ))}
    </div>
  );
}

function RuntimeWarningPopover({ entry, hoverState }) {
  if (!entry) return null;
  return (
    <div className="warning-log-popover" data-testid="warning-log-popover" role="tooltip" style={warningLogPopoverStyle(hoverState.anchorRect, hoverState.panelRect)}>
      <span className="warning-log-popover-title">
        <time>{formatTime(runtimeLogTimestamp(entry))}</time>
        <b>{runtimeLogInlineLabel(entry)}</b>
      </span>
      <code>{warningDetailText(entry)}</code>
    </div>
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

export { ChatPage };

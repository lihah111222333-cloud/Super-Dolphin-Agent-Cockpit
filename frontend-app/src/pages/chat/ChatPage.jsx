import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Brain, CheckCircle2, ChevronDown, CircleStop, Copy, File, FileText, Folder, GitBranch, Plus, Send, Sparkles, Terminal, Trash2, Wrench, X } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { onFilesDropped, copyTextToClipboard, locateCodeFile, openCodeFile, saveCodeFile } from '../../shared/api/backendApi.js';
import { appendCurrentModelOption, canonicalizeModelValue, modelOptionFor, normalizeConfigText, normalizeProviderKey, textValue } from '../shared/pageShared.js';
import { ComposerAttachments } from './components/ComposerAttachments.jsx';
import { composerAttachmentKey } from './components/composerAttachmentKey.js';
import { RuntimeActivityPanel } from './components/RuntimeActivityPanel.jsx';
import { RuntimeDiffView } from './components/RuntimeDiffView.jsx';
import { RuntimeToolbar } from './components/RuntimeToolbar.jsx';
import { ThreadCardActions } from './components/ThreadCardActions.jsx';
import { ThreadDisplayCardContent as ThreadDisplayCardContentView } from './components/ThreadDisplayCardContent.jsx';
import { ThreadRailTools } from './components/ThreadRailTools.jsx';
import { useTimelineMaterialization } from './hooks/useTimelineMaterialization.js';

const CONVERSATION_DROP_TARGET_ID = 'conversation-drop-zone';
const CLIPBOARD_FILE_PATH_TYPES = Object.freeze(['x-special/gnome-copied-files', 'text/uri-list', 'text/plain']);
const DROP_FILE_PATH_TYPES = new Set(['x-special/gnome-copied-files', 'text/uri-list']);
const NATIVE_FILE_DROP_TARGET_IDS = new Set(['composer-input', 'chat-input-bar', CONVERSATION_DROP_TARGET_ID]);
const NATIVE_FILE_DROP_TARGET_ATTRIBUTE = 'data-file-drop-target';
const NATIVE_FILE_DROP_TARGET_CLASSES = new Set([
  'composer',
  'composer--docked',
  'composer--floating',
  'composer-card',
  'composer-drop-hint',
  'conversation',
  'conversation--intro',
  'timeline',
  'timeline-shell',
]);

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

const ACTIVITY_PANEL_MIN_HEIGHT = ACTIVITY_ICON_ROW_HEIGHT;

const ACTIVITY_PANEL_DEFAULT_HEIGHT = ACTIVITY_ICON_ROW_HEIGHT;

const TIMELINE_SCROLL_LOAD_THRESHOLD = 32;

const TIMELINE_BOTTOM_STICKY_THRESHOLD = 48;

const CONTEXT_USAGE_FORK_THRESHOLD = 90;

const STREAMING_REVEAL_SHORT_TEXT_CHARS = 16;

const STREAMING_REVEAL_CATCHUP_FRAMES = 80;

const STREAMING_REVEAL_MAX_CHARS_PER_FRAME = 8;

const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)';

const APPROVAL_TERMINAL_STATUSES = new Set(['approved', 'rejected', 'denied', 'resolved', 'completed', 'complete', 'done', 'success', 'succeeded']);

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

function scrollTimelineElementToBottom(timeline, smooth = false) {
  if (!timeline) return;
  if (smooth && typeof timeline.scrollTo === 'function') {
    timeline.scrollTo({ top: timeline.scrollHeight, behavior: 'smooth' });
  } else {
    timeline.scrollTop = timeline.scrollHeight;
  }
}

function isTimelineNearBottom(timeline) {
  if (!timeline) return true;
  const scrollHeight = Number(timeline.scrollHeight) || 0;
  const clientHeight = Number(timeline.clientHeight) || 0;
  const scrollTop = Number(timeline.scrollTop) || 0;
  if (scrollHeight <= clientHeight) return true;
  return scrollHeight - clientHeight - scrollTop <= TIMELINE_BOTTOM_STICKY_THRESHOLD;
}

function requestTimelineBottomScroll(scrollToBottom) {
  if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') {
    scrollToBottom();
    return;
  }
  window.requestAnimationFrame(scrollToBottom);
}

function prefersReducedMotion() {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  return Boolean(window.matchMedia(REDUCED_MOTION_QUERY).matches);
}

function streamingRevealStepSize(remaining) {
  if (remaining <= STREAMING_REVEAL_SHORT_TEXT_CHARS) return 1;
  return Math.max(
    2,
    Math.min(STREAMING_REVEAL_MAX_CHARS_PER_FRAME, Math.ceil(remaining / STREAMING_REVEAL_CATCHUP_FRAMES)),
  );
}

function cancelAnimationFrameRef(frameRef) {
  if (!frameRef.current) return;
  if (typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
    window.cancelAnimationFrame(frameRef.current);
  }
  frameRef.current = 0;
}

function useReducedMotionPreference(enabled) {
  const [reduced, setReduced] = useState(() => (enabled ? prefersReducedMotion() : false));
  useEffect(() => {
    if (!enabled) {
      if (reduced) setReduced(false);
      return undefined;
    }
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      if (reduced) setReduced(false);
      return undefined;
    }
    const query = window.matchMedia(REDUCED_MOTION_QUERY);
    const update = () => setReduced(Boolean(query.matches));
    update();
    query.addEventListener?.('change', update);
    return () => query.removeEventListener?.('change', update);
  }, [enabled, reduced]);
  return enabled && reduced;
}

function useSmoothStreamingText(text, { enabled = false, streamKey = '' } = {}) {
  const targetText = (text || '').toString();
  const [state, setState] = useState(() => ({ streamKey, visibleText: targetText }));
  const frameRef = useRef(0);
  const targetTextRef = useRef(targetText);

  useEffect(() => {
    targetTextRef.current = targetText;
  }, [targetText]);

  const reducedMotion = useReducedMotionPreference(enabled);
  const streamKeyChanged = state.streamKey !== streamKey;
  const passthrough = !enabled || reducedMotion || streamKeyChanged;
  const visibleText = passthrough ? targetText : state.visibleText;

  useEffect(() => () => cancelAnimationFrameRef(frameRef), []);

  useEffect(() => {
    setState({ streamKey, visibleText: targetText });
  }, [streamKey]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!enabled || reducedMotion) {
      cancelAnimationFrameRef(frameRef);
      return undefined;
    }

    let active = true;

    const tick = () => {
      if (!active) return;

      setState((current) => {
        if (current.streamKey !== streamKey) return current;
        const latestTarget = targetTextRef.current;
        const currentText = current.visibleText;

        if (!latestTarget.startsWith(currentText) || currentText.length > latestTarget.length) {
          return { streamKey, visibleText: latestTarget };
        }

        const remaining = latestTarget.length - currentText.length;
        if (remaining <= 0) return current;

        return {
          streamKey,
          visibleText: latestTarget.slice(0, currentText.length + streamingRevealStepSize(remaining)),
        };
      });

      frameRef.current = window.requestAnimationFrame(tick);
    };

    frameRef.current = window.requestAnimationFrame(tick);

    return () => {
      active = false;
      cancelAnimationFrameRef(frameRef);
    };
  }, [enabled, reducedMotion, streamKey]);

  return visibleText;
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

function runtimeCodeScopePayload(filePath, projectPath, projects, position = null) {
  const payload = { filePath };
  const line = Number(position?.line);
  const column = Number(position?.column);
  if (Number.isFinite(line) && line > 0) payload.line = Math.floor(line);
  if (position && Number.isFinite(column) && column >= 0) payload.column = Math.floor(column);
  const project = normalizeProjectPath(projectPath);
  if (project) payload.project = project;
  const projectList = [];
  for (const rawProject of Array.isArray(projects) ? projects : []) {
    const normalizedProject = normalizeProjectPath(rawProject);
    if (normalizedProject) projectList.push(normalizedProject);
  }
  if (projectList.length > 0) payload.projects = projectList;
  return payload;
}

function normalizeCodeOpenSnippet(snippet) {
  if (typeof snippet === 'string') return snippet;
  if (!Array.isArray(snippet)) return '';
  return snippet.map((line) => {
    if (typeof line === 'string') return line;
    if (line && typeof line === 'object') return (line.text ?? '').toString();
    return '';
  }).join('\n');
}

function normalizeCodePreviewText(value) {
  return (value || '').toString().replace(/\r\n?/g, '\n');
}

function countCodePreviewLines(text) {
  const normalized = normalizeCodePreviewText(text);
  if (!normalized) return 0;
  const lineBreaks = normalized.match(/\n/g)?.length || 0;
  return normalized.endsWith('\n') ? lineBreaks : lineBreaks + 1;
}

function codePreviewFormatBytes(value) {
  const size = Number(value);
  if (!Number.isFinite(size) || size <= 0) return '';
  if (size >= 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(2)} MB`;
  if (size >= 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${Math.floor(size)} B`;
}

function codeOpenDisplayPath(result, fallback = '') {
  return (result?.relative || result?.filePath || result?.path || fallback || '').toString().trim();
}

function isCodePreviewMarkdownPath(path) {
  return /\.(md|markdown)$/i.test((path || '').toString().trim());
}

function isCodePreviewImagePath(path) {
  return /\.(png|jpe?g|gif|webp|svg|ico)$/i.test((path || '').toString().trim());
}

function codePreviewFileUrl(path) {
  const raw = (path || '').toString().trim();
  if (!raw) return '';
  if (/^(?:file|https?):\/\//i.test(raw) || /^data:image\//i.test(raw)) return raw;
  if (/^[A-Za-z]:[\\/]/.test(raw)) return `file:///${raw.replace(/\\/g, '/')}`;
  return `file://${raw}`;
}

function codePreviewLanguage(result, relative, previewKind) {
  const language = (result?.language || '').toString().trim().toLowerCase();
  if (language) return language === 'text' ? 'plaintext' : language;
  if (previewKind === 'markdown') return 'markdown';
  if (/\.json$/i.test(relative)) return 'json';
  if (/\.(ya?ml)$/i.test(relative)) return 'yaml';
  return 'plaintext';
}

function codePreviewLineRange(result, content) {
  const startRaw = Number(result?.startLine);
  const startLine = Number.isFinite(startRaw) && startRaw > 0 ? Math.floor(startRaw) : 1;
  const endRaw = Number(result?.endLine);
  const fallbackEnd = startLine + Math.max(0, countCodePreviewLines(content) - 1);
  const endLine = Number.isFinite(endRaw) && endRaw >= startLine ? Math.floor(endRaw) : fallbackEnd;
  const totalRaw = Number(result?.totalLines);
  const totalLines = Number.isFinite(totalRaw) && totalRaw > 0 ? Math.floor(totalRaw) : Math.max(endLine, countCodePreviewLines(content));
  return { startLine, endLine, totalLines };
}

function codePreviewMeta(preview) {
  const parts = [];
  if (preview?.image) {
    if (preview.mediaType) parts.push(preview.mediaType);
    const size = codePreviewFormatBytes(preview.sizeBytes);
    if (size) parts.push(size);
    return parts.join(' · ');
  }
  const startLine = Number(preview?.startLine);
  const endLine = Number(preview?.endLine);
  const totalLines = Number(preview?.totalLines);
  if (Number.isFinite(startLine) && startLine > 0 && Number.isFinite(endLine) && endLine >= startLine) {
    parts.push(startLine === endLine ? `第 ${startLine} 行` : `第 ${startLine}-${endLine} 行`);
  }
  if (Number.isFinite(totalLines) && totalLines > 0) parts.push(`共 ${totalLines} 行`);
  return parts.join(' · ');
}

function codePreviewStateFromOpenResult(result, requestedPath, fallbackRelative = '') {
  const filePath = (result?.filePath || result?.path || requestedPath || '').toString();
  const relative = codeOpenDisplayPath(result, fallbackRelative || requestedPath);
  const mediaType = (result?.mediaType || '').toString().trim().toLowerCase();
  const image = Boolean(result?.image) || mediaType.startsWith('image/') || isCodePreviewImagePath(relative || filePath);
  if (image) {
    const previewUrl = (result?.previewURL || result?.previewUrl || '').toString().trim();
    const thumbnailUrl = (result?.thumbnailURL || result?.thumbnailUrl || '').toString().trim();
    const imageSrc = thumbnailUrl || previewUrl || codePreviewFileUrl(filePath);
    const imageFullSrc = previewUrl || imageSrc;
    return {
      open: true,
      loading: false,
      saving: false,
      filePath,
      relative,
      content: '',
      draft: '',
      error: '',
      status: '',
      previewKind: 'image',
      language: '',
      editable: false,
      editing: false,
      image: true,
      imageSrc,
      imageFullSrc,
      mediaType: mediaType || 'image/*',
      sizeBytes: Number.isFinite(Number(result?.sizeBytes)) ? Math.floor(Number(result.sizeBytes)) : 0,
      startLine: 0,
      endLine: 0,
      totalLines: 0,
    };
  }
  const content = normalizeCodePreviewText(normalizeCodeOpenSnippet(result?.snippet));
  const explicitKind = (result?.previewKind || '').toString().trim().toLowerCase();
  const previewKind = explicitKind === 'markdown' || isCodePreviewMarkdownPath(relative) || mediaType === 'text/markdown'
    ? 'markdown'
    : 'text';
  const { startLine, endLine, totalLines } = codePreviewLineRange(result, content);
  return {
    open: true,
    loading: false,
    saving: false,
    filePath,
    relative,
    content,
    draft: content,
    error: '',
    status: '',
    previewKind,
    language: codePreviewLanguage(result, relative, previewKind),
    editable: Boolean(filePath),
    editing: previewKind !== 'markdown',
    image: false,
    imageSrc: '',
    imageFullSrc: '',
    mediaType: '',
    sizeBytes: Number.isFinite(Number(result?.sizeBytes)) ? Math.floor(Number(result.sizeBytes)) : 0,
    startLine,
    endLine,
    totalLines,
  };
}

function emptyCodePreviewState() {
  return {
    open: false,
    loading: false,
    saving: false,
    filePath: '',
    relative: '',
    content: '',
    draft: '',
    error: '',
    status: '',
    previewKind: 'text',
    language: 'plaintext',
    editable: false,
    editing: true,
    image: false,
    imageSrc: '',
    imageFullSrc: '',
    mediaType: '',
    sizeBytes: 0,
    startLine: 0,
    endLine: 0,
    totalLines: 0,
  };
}

function codeActionError(error, fallback) {
  return (error?.message || fallback).toString();
}

function normalizeCodeLocateOptions(result) {
  const options = [];
  const seen = new Set();
  const add = (value) => {
    const text = (value || '').toString().trim();
    if (!text || seen.has(text)) return;
    seen.add(text);
    options.push(text);
  };
  if (Array.isArray(result?.paths)) {
    result.paths.forEach(add);
  }
  if (Array.isArray(result?.matches)) {
    result.matches.forEach((match) => {
      if (typeof match === 'string') {
        add(match);
        return;
      }
      add(match?.path || match?.filePath || match?.relative);
    });
  }
  return options;
}

function emptyPathChoiceState() {
  return {
    open: false,
    file: null,
    options: [],
    truncated: false,
  };
}

function fileRefPosition(payload = {}) {
  const line = Number(payload.line ?? payload.lineStart);
  const column = Number(payload.column);
  return {
    line: Number.isFinite(line) && line > 0 ? Math.floor(line) : 1,
    column: Number.isFinite(column) && column >= 0 ? Math.floor(column) : 0,
  };
}

function useCodePreviewController({ projectPath, projects }) {
  const [codePreview, setCodePreview] = useState(emptyCodePreviewState);
  const [pathChoice, setPathChoice] = useState(emptyPathChoiceState);

  const openCodePreviewForPath = useCallback(async (filePath, fallbackRelative = '', position = null) => {
    const displayPath = (fallbackRelative || filePath || '').toString();
    setCodePreview({
      ...emptyCodePreviewState(),
      open: true,
      loading: true,
      filePath,
      relative: displayPath,
    });
    try {
      const result = await openCodeFile(runtimeCodeScopePayload(filePath, projectPath, projects, position));
      setCodePreview(codePreviewStateFromOpenResult(result, filePath, displayPath));
    } catch (error) {
      setCodePreview((current) => ({
        ...current,
        loading: false,
        error: codeActionError(error, '打开失败'),
      }));
    }
  }, [projectPath, projects]);

  const openFileRef = useCallback(async (payload = {}) => {
    const filePath = (payload.path || payload.filePath || '').toString().trim();
    if (!filePath) return;
    const position = fileRefPosition(payload);
    setCodePreview({
      ...emptyCodePreviewState(),
      open: true,
      loading: true,
      filePath,
      relative: filePath,
    });
    try {
      const locateResult = await locateCodeFile(runtimeCodeScopePayload(filePath, projectPath, projects, position));
      const options = normalizeCodeLocateOptions(locateResult);
      if (options.length > 1) {
        setCodePreview(emptyCodePreviewState());
        setPathChoice({ open: true, file: { filename: filePath, position }, options, truncated: Boolean(locateResult?.truncated) });
        return;
      }
      await openCodePreviewForPath(options[0] || filePath, filePath, position);
    } catch (error) {
      setCodePreview((current) => ({
        ...current,
        loading: false,
        error: codeActionError(error, '定位失败'),
      }));
    }
  }, [openCodePreviewForPath, projectPath, projects]);

  const openChosenPath = useCallback(async (filePath) => {
    const fallback = pathChoice.file?.filename || filePath;
    const position = pathChoice.file?.position || null;
    setPathChoice(emptyPathChoiceState());
    await openCodePreviewForPath(filePath, fallback, position);
  }, [openCodePreviewForPath, pathChoice.file]);

  const savePreviewChanges = useCallback(async () => {
    if (!codePreview.filePath || codePreview.saving) return;
    setCodePreview((current) => ({ ...current, saving: true, error: '', status: '' }));
    try {
      const result = await saveCodeFile({
        ...runtimeCodeScopePayload(codePreview.filePath, projectPath, projects),
        content: codePreview.draft,
      });
      const relative = codeOpenDisplayPath(result, codePreview.relative || codePreview.filePath);
      setCodePreview((current) => ({
        ...current,
        saving: false,
        filePath: (result?.filePath || current.filePath).toString(),
        relative,
        content: current.draft,
        editing: current.previewKind === 'markdown' ? false : current.editing,
        totalLines: Number.isFinite(Number(result?.totalLines)) ? Math.floor(Number(result.totalLines)) : countCodePreviewLines(current.draft),
        status: `已保存 ${relative}`,
      }));
    } catch (error) {
      setCodePreview((current) => ({
        ...current,
        saving: false,
        error: codeActionError(error, '保存失败'),
      }));
    }
  }, [codePreview.draft, codePreview.filePath, codePreview.relative, codePreview.saving, projectPath, projects]);

  const dialogs = (
    <>
      {codePreview.open ? (
        <CodePreviewDialog
          preview={codePreview}
          onBeginEdit={() => setCodePreview((current) => ({ ...current, editing: true, error: '', status: '' }))}
          onCancelEdit={() => setCodePreview((current) => ({ ...current, editing: false, draft: current.content, error: '', status: '' }))}
          onChangeDraft={(draft) => setCodePreview((current) => ({ ...current, draft, error: '' }))}
          onClose={() => setCodePreview(emptyCodePreviewState())}
          onDirtyClose={() => setCodePreview((current) => ({ ...current, error: '请先保存或放弃预览更改' }))}
          onSave={savePreviewChanges}
        />
      ) : null}
      {pathChoice.open ? (
        <PathChoiceDialog
          choice={pathChoice}
          onClose={() => setPathChoice(emptyPathChoiceState())}
          onSelect={(filePath) => { void openChosenPath(filePath); }}
        />
      ) : null}
    </>
  );

  return { dialogs, openFileRef };
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

function runtimeProjectPath(activeProject, fallbackProject) {
  const normalized = normalizeProjectPath(activeProject);
  if (normalized && normalized !== '.' && normalized !== '未选择项目') return normalized;
  return normalizeProjectPath(fallbackProject);
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
  return Boolean(element.closest('dialog, [role="dialog"], [role="menu"], [role="listbox"], [data-escape-scope="local"]'));
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

  const items = [];
  for (const value of values) {
    if (value === '.') continue;
    const segments = value.split(/[\\/]/).filter(Boolean);
    const depth = Math.min(2, segments.length);
    items.push({
      value,
      label: segments.slice(-depth).join('/') || value,
      full: value,
      segments,
      depth,
    });
  }
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
  create: 'idle',
  created: 'idle',
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
  const normalizedState = normalizeTurnState(status);
  const mapped = TURN_STATE_INFO[normalizedState];
  if (!status || normalizedState === 'idle' || normalized === 'idle' || status === '空闲' || status === '等待指示') return '';
  if (mapped?.label) return mapped.label;
  if (running) return '工作中';
  return '';
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
  const ids = [
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
  ];
  const result = new Set();
  for (const id of ids) {
    const normalized = normalizedThreadIdentity(id);
    if (normalized) result.add(normalized);
  }
  return result;
}

function threadScopedMapValue(map = {}, activeThreadId, activeThread, fallback = null) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  for (const id of ids) {
    if (Object.prototype.hasOwnProperty.call(map || {}, id)) return map[id];
  }
  return fallback;
}

function threadScopedBooleanValue(map = {}, activeThreadId, activeThread, fallback = false) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  let found = false;
  for (const id of ids) {
    if (!Object.prototype.hasOwnProperty.call(map || {}, id)) continue;
    found = true;
    if (map[id]) return true;
  }
  return found ? false : fallback;
}

const GENERIC_TIMELINE_COMMAND_TITLES = new Set(['command', 'execute command', 'running command', '执行命令', '命令', '终端命令']);

function timelineItemKind(item = {}) {
  return (item.kind || item.type || item.eventType || item.event_type || '').toString().trim().toLowerCase();
}

function timelineItemTextValue(item = {}) {
  return (item.text || item.content || item.message || item.output || item.result || item.error || '').toString().trim();
}

function hasRenderableTimelineCommand(item = {}) {
  if ((item.command || '').toString().trim()) return true;
  if (timelineItemTextValue(item)) return true;
  const title = (item.title || '').toString().trim();
  return Boolean(title.startsWith('$ ') && !GENERIC_TIMELINE_COMMAND_TITLES.has(title.toLowerCase()));
}

function isRenderableThreadScopedTimelineItem(item = {}) {
  if (timelineItemKind(item) !== 'command') return true;
  if ((item.tool || item.toolName || item.tool_name || '').toString().trim()) return false;
  return hasRenderableTimelineCommand(item);
}

function timelineItemOrderTime(item = {}) {
  return timestampMs(item.time || item.ts || item.createdAt || item.created_at || item.completedAt || item.completed_at);
}

function mergeThreadScopedTimelineItems(items = []) {
  const merged = [];
  const indexById = new Map();

  for (const item of items) {
    const id = (item?.id || '').toString().trim();
    if (!id) {
      merged.push(item);
      continue;
    }
    const existingIndex = indexById.get(id);
    if (existingIndex === undefined) {
      indexById.set(id, merged.length);
      merged.push(item);
      continue;
    }
    merged[existingIndex] = { ...merged[existingIndex], ...item };
  }

  return merged
    .map((item, index) => ({ item, index, orderTime: timelineItemOrderTime(item) }))
    .sort((left, right) => {
      if (left.orderTime && right.orderTime && left.orderTime !== right.orderTime) {
        return left.orderTime - right.orderTime;
      }
      if (left.orderTime && !right.orderTime) return -1;
      if (!left.orderTime && right.orderTime) return 1;
      return left.index - right.index;
    })
    .map(({ item }) => item);
}

function threadScopedTimelineValue(map = {}, activeThreadId, activeThread, fallback = []) {
  const ids = activeThreadIdentifiers(activeThreadId, activeThread);
  const items = [];
  for (const id of ids) {
    if (!Object.prototype.hasOwnProperty.call(map || {}, id)) continue;
    const value = map[id];
    if (Array.isArray(value)) items.push(...value.filter(isRenderableThreadScopedTimelineItem));
  }
  return items.length > 0 ? mergeThreadScopedTimelineItems(items) : fallback;
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

function workStatusForThread({ sending, loading, activeThreadId, activeThread, statusEntry }) {
  if (!activeThreadId) {
    return { busy: false };
  }
  if (loading) {
    return { busy: true };
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
  return {
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

function hasReasoningMessageAfterLastUser(messages = []) {
  let lastUserIndex = -1;
  for (let index = 0; index < messages.length; index += 1) {
    if ((messages[index]?.role || '').toString().trim().toLowerCase() === 'user') {
      lastUserIndex = index;
    }
  }
  return messages.some((message, index) => (
    index > lastUserIndex &&
    isReasoningMessage(message)
  ));
}

function timelineMessageAutoScrollKey(message) {
  if (!message) return '';
  const done = Object.prototype.hasOwnProperty.call(message, 'done') ? String(message.done) : '';
  return [
    message.id || '',
    message.role || message.kind || '',
    message.status || '',
    done,
    timelineItemTextValue(message),
  ].map((value) => value.toString()).join('\u0001');
}

function shouldAutoScrollForTimelineMessage(message) {
  if (!message) return false;
  const role = (message.role || '').toString().trim().toLowerCase();
  return role === 'assistant' || isReasoningMessage(message) || isApprovalMessage(message);
}

function timelineAutoScrollKey({ activeThreadId, introMode, messages, pendingReasoning, timelineContentBlocked }) {
  if (introMode || timelineContentBlocked) return '';
  const lastMessage = messages[messages.length - 1] || null;
  if (!shouldAutoScrollForTimelineMessage(lastMessage) && !shouldAutoScrollForTimelineMessage(pendingReasoning)) return '';
  return [
    activeThreadId || '',
    shouldAutoScrollForTimelineMessage(lastMessage) ? timelineMessageAutoScrollKey(lastMessage) : '',
    timelineMessageAutoScrollKey(pendingReasoning),
  ].join('\u0002');
}

/*
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
*/

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

function composerTextFromCitation(payload = {}) {
  const kind = (payload.kind || '').toString().trim();
  const raw = (payload.raw || '').toString().trim();
  if (kind === 'task') return (payload.prompt || payload.title || raw || '').toString().trim();
  if (kind === 'automation-update') {
    const title = (payload.title || '').toString().trim();
    const prompt = (payload.prompt || '').toString().trim();
    const message = (payload.message || raw || '').toString().trim();
    if (title && prompt) return `Automation update (${title}):\n${prompt}`;
    if (prompt) return `Automation update:\n${prompt}`;
    if (message) return `Automation update:\n${message}`;
    return title ? `Automation update (${title})` : '';
  }
  if (kind === 'code-comment') {
    const title = (payload.title || '').toString().trim();
    const message = (payload.message || raw || '').toString().trim();
    const path = (payload.path || '').toString().trim();
    const header = title || path ? `Code comment${path ? ` (${path})` : ''}${title ? `: ${title}` : ''}` : 'Code comment';
    return message ? `${header}\n${message}` : (header === 'Code comment' ? '' : header);
  }
  return '';
}

function appendComposerCitation(store, payload) {
  const nextText = composerTextFromCitation(payload);
  if (!nextText || typeof store?.setDraft !== 'function') return false;
  const current = (store.draft || '').toString().trim();
  store.setDraft(current ? `${current}\n\n${nextText}` : nextText);
  return true;
}

function handleTimelineCitationAction(payload, { store, openFileRef }) {
  const kind = (payload?.kind || '').toString().trim();
  if (!kind) return;
  if (kind === 'conversation') {
    const nextThreadId = (payload?.conversationId || '').toString().trim();
    if (nextThreadId) store?.selectThread?.(nextThreadId);
    return;
  }
  if (kind === 'skill') {
    const path = (payload?.path || '').toString().trim();
    if (path) void openFileRef({ path, line: 1, column: 0, raw: payload.raw || path });
    return;
  }
  if (kind === 'image') {
    const path = (payload?.path || '').toString().trim();
    if (path) void openFileRef({ path, line: 1, column: 0, raw: payload.raw || path });
    return;
  }
  if (kind === 'code-comment') {
    appendComposerCitation(store, payload);
    const path = (payload?.path || '').toString().trim();
    if (path) void openFileRef({ path, line: Number(payload.lineStart) || 1, column: 0, raw: payload.raw || path });
    return;
  }
  appendComposerCitation(store, payload);
}

function useChatThreadData(store, activeThreadId) {
  const activeThread = activeThreadForStore(store);
  const timelineBlocked = Boolean(activeThreadId && threadScopedBooleanValue(store.threadStateLoadingByThread, activeThreadId, activeThread, false));
  const cachedTimeline = threadScopedTimelineValue(store.timelinesByThread, activeThreadId, activeThread, []);
  const timelineReadyFlag = threadScopedBooleanValue(store.threadTimelineReadyByThread, activeThreadId, activeThread, false);
  const timelineReady = Boolean(
    activeThreadId &&
    timelineReadyFlag &&
    (!timelineBlocked || cachedTimeline.length > 0),
  );
  const timelineContentBlocked = timelineBlocked && !timelineReady;
  return {
    activeThread,
    activeTurn: threadScopedMapValue(store.activeTurnByThread, activeThreadId, activeThread, null),
    activityStats: threadScopedMapValue(store.activityStatsByThread, activeThreadId, activeThread, null),
    diffText: threadScopedMapValue(store.diffTextByThread, activeThreadId, activeThread, '') || '',
    messagePagination: threadScopedMapValue(store.threadMessagePaginationByThread, activeThreadId, activeThread, null),
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
    let frameId = null;
    const onResize = () => {
      if (frameId) return;
      frameId = window.requestAnimationFrame(() => {
        frameId = null;
        setViewportWidth(currentViewportWidth());
      });
    };
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      if (frameId) window.cancelAnimationFrame(frameId);
    };
  }, []);
  return viewportWidth;
}

function useThreadRailLayout({ viewportWidth, rightPanelOpen, store, layoutRef }) {
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
    event.currentTarget?.setPointerCapture?.(event.pointerId);

    const startX = event.clientX;
    const startWidth = width;
    let latestWidth = startWidth;

    const layoutColumnsForWidth = (nextWidth) => {
      const rightWidth = clampWidth(store.rightPanelWidth, 0, rightPanelMaxWidth(viewportWidth, nextWidth));
      return rightPanelOpen
        ? `${nextWidth}px ${SPLITTER_WIDTH}px minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightWidth}px`
        : `${nextWidth}px ${SPLITTER_WIDTH}px minmax(0, 1fr)`;
    };

    const move = (moveEvent) => {
      if (Number(moveEvent.buttons) === 0) {
        stop();
        return;
      }
      const rawNext = startWidth + (moveEvent.clientX - startX);
      latestWidth = clampWidth(rawNext, THREAD_RAIL_MIN_WIDTH, maxWidth);
      if (layoutRef.current) {
        layoutRef.current.style.gridTemplateColumns = layoutColumnsForWidth(latestWidth);
      }
    };

    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
      window.removeEventListener('pointercancel', stop);
      window.removeEventListener('blur', stop);
      event.currentTarget?.releasePointerCapture?.(event.pointerId);

      setThreadRailWidth(latestWidth);
    };

    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
    window.addEventListener('pointercancel', stop);
    window.addEventListener('blur', stop);
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

function useRuntimeSidePanelLayout({ activeThreadId, railWidth, store, viewportWidth, open, setOpen, layoutRef }) {
  const resizedRef = useRef(false);
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
  return { beginResize, handleKeyDown, maxWidth, open, toggle, width };
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

function useActiveChatThreadSync(store, activeThreadId) {
  const timelineReady = Boolean(activeThreadId && store.threadTimelineReadyByThread?.[activeThreadId]);
  const loading = Boolean(activeThreadId && store.threadStateLoadingByThread?.[activeThreadId]);
  useEffect(() => {
    if (!activeThreadId || timelineReady || loading) return;
    runUIAction(() => store.syncThreadState?.(activeThreadId, {
      includeArchived: true,
      includeDiff: true,
      preserveActiveThreadId: true,
    }));
  }, [activeThreadId, loading, store, timelineReady]);
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

function ChatPage({ store, projectPath, rightPanelOpen = false, setRightPanelOpen = () => {} }) {
  const activeThreadId = store.activeThreadId;
  const modelThreadId = composerConfigThreadId(store, activeThreadId);
  const threadData = useChatThreadData(store, activeThreadId);
  const canUseProjectActions = canUseProjectActionsForStore(store);
  const runtimeProject = runtimeProjectPath(store.activeProject, projectPath);
  const codePreview = useCodePreviewController({ projectPath: runtimeProject, projects: store.projects });
  const messageActions = useMemo(() => ({
    onFileRef: codePreview.openFileRef,
    onCitation: (payload) => handleTimelineCitationAction(payload, { store, openFileRef: codePreview.openFileRef }),
    onApproval: (message, approved) => store.respondApproval?.(message, approved),
  }), [codePreview.openFileRef, store]);
  const viewportWidth = useViewportWidth();
  const chatLayoutRef = useRef(null);
  const rail = useThreadRailLayout({
    viewportWidth,
    rightPanelOpen,
    store,
    layoutRef: chatLayoutRef,
  });
  const {
    beginResize: beginRuntimeResize,
    handleKeyDown: handleRuntimeResizeKeyDown,
    maxWidth: runtimeMaxWidth,
    width: rightPanelWidth,
  } = useRuntimeSidePanelLayout({
    activeThreadId,
    railWidth: rail.width,
    store,
    viewportWidth,
    open: rightPanelOpen,
    setOpen: setRightPanelOpen,
    layoutRef: chatLayoutRef,
  });
  useActiveChatThreadSync(store, activeThreadId);
  useChatInterruptShortcut(store, activeThreadId);
  const layoutColumns = rightPanelOpen
    ? `${rail.width}px ${SPLITTER_WIDTH}px minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightPanelWidth}px`
    : `${rail.width}px ${SPLITTER_WIDTH}px minmax(0, 1fr)`;

  return (
    <section className="chat-page" data-testid="chat-page">
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
          removeAttachment={store.removeAttachment}
          sending={store.sending}
          store={store}
          projectPath={projectPath}
          tokenUsage={threadData.tokenUsage}
          activeThreadId={activeThreadId}
          activeThread={threadData.activeThread}
          statusEntry={threadData.statusEntry}
          activeTurn={threadData.activeTurn}
          modelThreadId={modelThreadId}
          messagePagination={threadData.messagePagination}
          loadOlderThreadMessages={store.loadOlderThreadMessages}
          timelineBlocked={threadData.timelineBlocked}
          timelineContentBlocked={threadData.timelineContentBlocked}
          canUseProjectActions={canUseProjectActions}
          messageActions={messageActions}
        />
        <RuntimePanelSlot
          beginResize={beginRuntimeResize}
          handleKeyDown={handleRuntimeResizeKeyDown}
          maxWidth={runtimeMaxWidth}
          open={rightPanelOpen}
          projectPath={runtimeProject}
          projects={store.projects}
          threadData={threadData}
          width={rightPanelWidth}
        />
      </div>
      {codePreview.dialogs}
    </section>
  );
}

function ThreadRailResizer({ rail }) {
  return (
    <button
      type="button"
      className="splitter splitter--left"
      role="separator"
      aria-label="调整会话栏宽度"
      aria-orientation="vertical"
      aria-valuemin={THREAD_RAIL_MIN_WIDTH}
      aria-valuemax={rail.maxWidth}
      aria-valuenow={rail.width}
      title="调整会话栏宽度"
      data-testid="thread-rail-resizer"
      onKeyDown={rail.handleKeyDown}
      onPointerDown={rail.beginResize}
    >
      <span className="sr-only">调整会话栏宽度，当前 {rail.width} 像素</span>
    </button>
  );
}

function RuntimePanelSlot({ beginResize, handleKeyDown, maxWidth, open, projectPath, projects, threadData, width }) {
  if (!open) return null;
  return (
    <>
      <button
        type="button"
        className="splitter splitter--right"
        role="separator"
        aria-label="调整侧边栏宽度"
        aria-orientation="vertical"
        aria-valuemin={RIGHT_PANEL_CLOSE_THRESHOLD}
        aria-valuemax={maxWidth}
        aria-valuenow={width}
        title="调整侧边栏宽度"
        data-testid="right-panel-resizer"
        onKeyDown={handleKeyDown}
        onPointerDown={beginResize}
      >
        <span className="sr-only">调整侧边栏宽度，当前 {width} 像素</span>
      </button>
      <RuntimePanel
        diffText={threadData.diffText}
        tokenUsage={threadData.tokenUsage}
        activityStats={threadData.activityStats}
        warnings={threadData.warnings}
        runtimeResults={threadData.runtimeResults}
        projectPath={projectPath}
        projects={projects}
      />
    </>
  );
}

export function ProjectSelector({ store, projectPath }) {
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

/*
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
*/


function ThreadRail({ store }) {
  const [showArchivedThreads, setShowArchivedThreads] = useState(false);
  const [confirmCleanMode, setConfirmCleanMode] = useState(false);
  const [deletingThreadId, setDeletingThreadId] = useState('');
  const [hoveredArchiveThreadId, setHoveredArchiveThreadId] = useState('');
  const [hoveredPinThreadId, setHoveredPinThreadId] = useState('');
  const rename = useThreadRenameController(store);
  const activeThreads = store.threads.filter((thread) => !thread.archived);
  const archivedThreads = store.threads.filter((thread) => thread.archived);
  const threads = showArchivedThreads ? archivedThreads : activeThreads;
  const chatListLoading = Boolean(store.chatSurfaceLoadingCwd);
  const visibleThreads = visibleThreadRows(threads, store);
  const staleThreadIds = [];
  if (showArchivedThreads) {
    for (const thread of visibleThreads) {
      if (thread.staleReason) staleThreadIds.push(thread.id);
    }
  }
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
      if (!next) {
        setConfirmCleanMode(false);
        setDeletingThreadId('');
      }
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
            hoveredArchiveThreadId={hoveredArchiveThreadId}
            hoveredPinThreadId={hoveredPinThreadId}
            renaming={rename.renamingThreadId === thread.id}
            onBeginRename={rename.beginRename}
            onCancelRename={rename.cancelRename}
            onRenameBlur={rename.handleRenameBlur}
            onSetEditingName={rename.setEditingName}
            onSetHoveredArchiveThreadId={setHoveredArchiveThreadId}
            onSetHoveredPinThreadId={setHoveredPinThreadId}
            onSubmitRename={rename.submitRename}
            deleting={deletingThreadId === thread.id}
            onBeginDelete={() => setDeletingThreadId(thread.id)}
            onCancelDelete={() => setDeletingThreadId('')}
            onConfirmDelete={() => {
              setDeletingThreadId('');
              runUIAction(() => store.deleteStaleThreads([thread.id]));
            }}
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

function ThreadCard({
  thread,
  store,
  active,
  editing,
  editingName,
  hoveredArchiveThreadId,
  hoveredPinThreadId,
  renaming,
  onBeginRename,
  onCancelRename,
  onRenameBlur,
  onSetEditingName,
  onSetHoveredArchiveThreadId,
  onSetHoveredPinThreadId,
  onSubmitRename,
  deleting,
  onBeginDelete,
  onCancelDelete,
  onConfirmDelete,
}) {
  const archiveLabel = thread.archived ? '恢复会话' : '归档会话';
  const threadLabel = displayThreadName(thread);
  if (deleting) {
    return (
      <div className={`thread-card ${active ? 'active' : ''} thread-card--deleting`}>
        <div className="thread-delete-confirm-label">确定删除该会话？</div>
        <div className="thread-delete-confirm-actions">
          <button type="button" className="thread-delete-confirm-btn confirm" onClick={onConfirmDelete}>确认</button>
          <button type="button" className="thread-delete-confirm-btn cancel" onClick={onCancelDelete}>取消</button>
        </div>
      </div>
    );
  }
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
        />
      )}
      <ThreadCardActions
        thread={thread}
        threadLabel={threadLabel}
        editing={editing}
        archiveLabel={archiveLabel}
        hoveredArchiveThreadId={hoveredArchiveThreadId}
        hoveredPinThreadId={hoveredPinThreadId}
        loading={Boolean(store.threadArchiveLoadingByThread?.[thread.id])}
        onBeginRename={() => onBeginRename(thread)}
        onSetHoveredArchiveThreadId={onSetHoveredArchiveThreadId}
        onSetHoveredPinThreadId={onSetHoveredPinThreadId}
        onToggleArchive={() => runUIAction(() => store.archiveThread(thread.id, !thread.archived))}
        onTogglePin={() => runUIAction(() => store.toggleThreadPin(thread.id))}
        onBeginDelete={onBeginDelete}
      />
    </div>
  );
}

function ThreadRenameCardContent({ thread, editingName, renaming, onCancelRename, onRenameBlur, onSetEditingName, onSubmitRename }) {
  const inputRef = useRef(null);
  useEffect(() => {
    const input = inputRef.current;
    if (!input || renaming) return;
    input.focus({ preventScroll: true });
    input.select();
  }, [renaming]);

  return (
    <div className="thread-main thread-main--editing">
      <input
        ref={inputRef}
        className="thread-name-input"
        aria-label="会话别名"
        value={editingName}
        maxLength={64}
        disabled={renaming}
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

function ThreadDisplayCardContent({ thread, store }) {
  const running = threadStatusBusy(thread.status);
  const threadLabel = displayThreadName(thread);
  const statusLabel = threadCardStatusLabel(thread, running);
  const statusDotState = threadStatusDotState(thread.status);
  const statusDotTitle = threadStatusDotTitle(thread.status, statusLabel);
  return (
    <ThreadDisplayCardContentView
      providerLabel={threadProviderLabel(thread.provider)}
      staleReason={thread.staleReason}
      statusDotState={statusDotState}
      statusDotTitle={statusDotTitle}
      statusLabel={statusLabel}
      threadLabel={threadLabel}
      onSelect={() => runUIAction(() => store.setActiveThread(thread.id))}
    />
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
  const closedDraft = { model: draftModel, effort: draftEffort };
  const selectorOpen = open && !disabled;
  useEffect(() => { if (disabled && open) setOpen(false); }, [disabled, open]);
  const selectorDraft = selectorOpen ? draft : closedDraft;

  useEffect(() => {
    if (!selectorOpen) return undefined;
    const onPointerDown = (event) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target)) setOpen(false);
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () => document.removeEventListener('pointerdown', onPointerDown, true);
  }, [selectorOpen, wrapRef]);

  const openSelector = async () => {
    if (disabled) return;
    const nextOpen = !selectorOpen;
    setDraft({ model: draftModel, effort: draftEffort });
    setOpen(nextOpen);
    if (!nextOpen || !activeThreadId) return;
    let cancelled = false;
    const loaded = await store.loadThreadConfig?.(activeThreadId);
    if (cancelled || !loaded) return;
    setDraft(loadedModelDraft(loaded, activeModel, activeEffort));
    return () => { cancelled = true; };
  };

  const saveModelConfig = async (patch) => {
    const next = nextModelDraft(providerKey, selectorDraft, patch, activeModel);
    setDraft(next);
    await store.saveComposerModelConfig?.({ threadId: activeThreadId, model: next.model, effort: next.effort });
  };

  const restoreInheritance = async () => {
    const restored = await store.restoreComposerModelInheritance?.({ threadId: activeThreadId });
    if (restored) setOpen(false);
  };

  return {
    ...modelSelectorDerivedState({ activeEffort, activeModel, activeThreadConfig, canOverrideThread, disabled, draft: selectorDraft, providerKey, store, activeThreadId }),
    open: selectorOpen,
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
    <dialog className="model-dropdown" open aria-label="模型配置">
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
    </dialog>
  );
}

function hasFilesTransfer(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return false;
  if (transfer.files && transfer.files.length > 0) return true;
  const types = Array.from(transfer.types || []).map((type) => textValue(type));
  if (types.includes('Files')) return true;
  return types.some((type) => DROP_FILE_PATH_TYPES.has(type));
}

function collectTransferFiles(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return [];
  const files = Array.from(transfer.files || []).filter(Boolean);
  if (files.length > 0) return files;
  const collected = [];
  for (const item of Array.from(transfer.items || [])) {
    if (item?.kind !== 'file') continue;
    const file = item.getAsFile?.();
    if (file) collected.push(file);
  }
  return collected;
}

function decodeClipboardFileUri(value) {
  const raw = textValue(value).trim();
  if (!/^file:/i.test(raw)) return '';
  try {
    const url = new URL(raw);
    if (url.protocol !== 'file:') return '';
    const hostname = textValue(url.hostname);
    let pathname = decodeURIComponent(url.pathname || '');
    if (/^\/[a-zA-Z]:[\\/]/.test(pathname)) pathname = pathname.slice(1);
    if (hostname && hostname !== 'localhost') return `//${hostname}${pathname}`;
    return pathname;
  }
  catch {
    try {
      return decodeURIComponent(raw.replace(/^file:\/+/i, '/'));
    }
    catch {
      return raw.replace(/^file:\/+/i, '/');
    }
  }
}

function normalizeClipboardPathLine(line) {
  let value = textValue(line).trim();
  if (
    value.length >= 2 &&
    ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))
  ) {
    value = value.slice(1, -1).trim();
  }
  if (!value || value.startsWith('#')) return '';
  if (value === 'copy' || value === 'cut') return '';
  if (/^file:/i.test(value)) return decodeClipboardFileUri(value);
  if (/^[a-zA-Z]:[\\/]/.test(value) || value.startsWith('/') || value.startsWith('\\\\')) return value;
  return '';
}

function clipboardPathsFromText(text) {
  const paths = [];
  const seen = new Set();
  for (const line of textValue(text).split(/\r?\n/)) {
    const path = normalizeClipboardPathLine(line);
    if (!path || seen.has(path)) continue;
    seen.add(path);
    paths.push(path);
  }
  return paths;
}

function extractFilePathsFromTransferData(transferData) {
  if (!transferData || typeof transferData.getData !== 'function') return [];
  const types = new Set(Array.from(transferData.types || []).map((type) => textValue(type)));
  const paths = [];
  const seen = new Set();
  for (const type of CLIPBOARD_FILE_PATH_TYPES) {
    if (types.size > 0 && !types.has(type)) continue;
    let data;
    try {
      data = transferData.getData(type);
    }
    catch {
      continue;
    }
    for (const path of clipboardPathsFromText(data)) {
      if (seen.has(path)) continue;
      seen.add(path);
      paths.push(path);
    }
  }
  return paths;
}

function extractTransferFilePaths(event) {
  return extractFilePathsFromTransferData(event?.dataTransfer);
}

function extractClipboardFilePaths(event) {
  return extractFilePathsFromTransferData(event?.clipboardData);
}

function extractClipboardFiles(event) {
  const clipboard = event?.clipboardData;
  if (!clipboard) return [];
  const files = [];
  const seen = new Set();
  const add = (file) => {
    if (!file || seen.has(file)) return;
    seen.add(file);
    files.push(file);
  };
  Array.from(clipboard.files || []).forEach(add);
  Array.from(clipboard.items || []).forEach((item) => {
    if (item?.kind !== 'file') return;
    add(item.getAsFile?.());
  });
  return files;
}

function nativeDropFiles(event, options) {
  if (!event || typeof event !== 'object') return [];
  const candidates = [event, event.data, event.payload, event.data?.payload];
  const payload = candidates.find((item) => item && typeof item === 'object' && Array.isArray(item.files));
  if (!nativeDropTargetAcceptsFiles(payload?.details, options)) return [];
  return payload?.files || [];
}

function nativeDropClassTokens(value) {
  if (!value) return [];
  const raw = Array.isArray(value)
    ? value
    : (typeof value === 'string' ? value.split(/\s+/) : []);
  return raw.map((item) => textValue(item)).filter(Boolean);
}

function nativeDropHasAcceptedClass(value) {
  return nativeDropClassTokens(value).some((className) => NATIVE_FILE_DROP_TARGET_CLASSES.has(className));
}

function nativeDropHasTargetEvidence(details, attributes) {
  if (textValue(details?.id) || nativeDropClassTokens(details?.classList).length > 0) return true;
  if (!attributes) return false;
  return Boolean(textValue(attributes.id)
    || nativeDropClassTokens(attributes.class).length > 0
    || Object.keys(attributes).length > 0);
}

function nativeDropTargetAcceptsFiles(details, options = {}) {
  if (!details || typeof details !== 'object') return Boolean(options.acceptEmptyDetails);
  const id = textValue(details.id);
  if (NATIVE_FILE_DROP_TARGET_IDS.has(id)) return true;
  if (nativeDropHasAcceptedClass(details.classList)) return true;

  const attributes = details.attributes && typeof details.attributes === 'object' ? details.attributes : null;
  if (!attributes) return Boolean(options.acceptEmptyDetails && !nativeDropHasTargetEvidence(details, attributes));
  const attributeId = textValue(attributes.id);
  return NATIVE_FILE_DROP_TARGET_IDS.has(attributeId)
    || Object.prototype.hasOwnProperty.call(attributes, NATIVE_FILE_DROP_TARGET_ATTRIBUTE)
    || nativeDropHasAcceptedClass(attributes.class)
    || Boolean(options.acceptEmptyDetails && !nativeDropHasTargetEvidence(details, attributes));
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

const CODEX_DIRECTIVE_RE = /:(codex-file-citation|codex-terminal-citation|codex-image-citation|task-stub|automation-update|code-comment)(?:\[([^\]\n]*)])?(?:\{([^{}\n]*)})?/g;

const CODEX_DIRECTIVE_TOKEN_RE = /^:(codex-file-citation|codex-terminal-citation|codex-image-citation|task-stub|automation-update|code-comment)(?:\[([^\]\n]*)])?(?:\{([^{}\n]*)})?$/;

const DIRECTIVE_ATTR_RE = /([A-Za-z_][\w-]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'{}]+))/g;

function basenameFromPath(path) {
  const value = (path || '').toString().trim().split(/[?#]/, 1)[0];
  if (!value) return '';
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function skillNameFromPath(path) {
  const value = (path || '').toString().trim().split(/[?#]/, 1)[0].replace(/\\/g, '/');
  const segments = value.split('/').filter(Boolean);
  if (segments.length >= 2 && /^SKILL\.md$/i.test(segments[segments.length - 1])) return segments[segments.length - 2] || '';
  return basenameFromPath(value).replace(/\.md$/i, '');
}

function parseDirectiveAttrs(rawAttrs) {
  const attrs = {};
  const source = (rawAttrs || '').toString();
  for (const match of source.matchAll(DIRECTIVE_ATTR_RE)) {
    const key = (match[1] || '').toString().trim();
    if (!key) continue;
    attrs[key] = (match[2] ?? match[3] ?? match[4] ?? '').toString();
  }
  return attrs;
}

function positiveInt(value, fallback = 0) {
  const parsed = Number.parseInt((value || '').toString(), 10);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}

function lineRangeLabel(startLine, endLine) {
  const start = positiveInt(startLine, 0);
  const end = positiveInt(endLine, 0);
  if (start > 0 && end > 0 && end !== start) return `lines ${start}-${end}`;
  if (start > 0) return `line ${start}`;
  if (end > 0) return `line ${end}`;
  return '';
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

function LightboxShell({ label, onClose, children }) {
  const displayLabel = (label || '').toString().trim() || '预览';
  return createPortal(
    <dialog className="image-lightbox" open aria-label={`图片预览：${displayLabel}`}>
      <button type="button" className="image-lightbox-backdrop" aria-label="关闭图片预览" onClick={onClose} />
      <section className="image-lightbox-panel">
        <header>
          <strong>{displayLabel}</strong>
          <div>
            <button type="button" aria-label="关闭图片预览" onClick={onClose}><X size={16} /></button>
          </div>
        </header>
        {children}
      </section>
    </dialog>,
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
    <LightboxShell label={displayLabel} onClose={() => setExpanded(false)}>
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
  const tokenPattern = `${CODEX_DIRECTIVE_RE.source}|!\\[[^\\]]*]\\([^)]+\\)|\\[[^\\]]+]\\([^)]+\\)|\`[^\`]+\`|\\*\\*[^*]+\\*\\*|__[^_]+__|~~[^~]+~~|\\*[^*]+\\*|_[^_]+_`;
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

function citationLinkPayload(label, rawHref) {
  const href = (rawHref || '').toString().trim();
  if (!href) return null;
  const skillMatch = href.match(/^app:\/\/([^/?#]+)/i);
  if (skillMatch) {
    return {
      kind: 'skill',
      skillId: (skillMatch[1] || '').toString(),
      skillName: (label || skillMatch[1] || '').toString().trim(),
      path: '',
      conversationId: '',
      raw: (label || '').toString(),
    };
  }
  const conversationMatch = href.match(/^agent:\/\/([^/?#]+)/i);
  if (conversationMatch) {
    return {
      kind: 'conversation',
      skillId: '',
      skillName: '',
      path: '',
      conversationId: (conversationMatch[1] || '').toString(),
      raw: (label || '').toString(),
    };
  }
  if (/(^|[/\\])SKILL\.md(?:[?#].*)?$/i.test(href)) {
    return {
      kind: 'skill',
      skillId: '',
      skillName: skillNameFromPath(href),
      path: href,
      conversationId: '',
      raw: (label || '').toString(),
    };
  }
  return null;
}

function renderCitationChip({ className = '', icon = '', label, payload, key, title = '', actions }) {
  const displayLabel = (label || payload?.raw || payload?.title || payload?.kind || '引用').toString().trim();
  return (
    <button
      type="button"
      key={key}
      className={`chat-md-citation ${className}`.trim()}
      title={title || displayLabel}
      onClick={() => actions?.onCitation?.(payload)}
    >
      {icon ? <span className="chat-md-citation__icon" aria-hidden="true">{icon}</span> : null}
      <span className="chat-md-citation__body">
        <span className="chat-md-citation__label">{displayLabel}</span>
      </span>
    </button>
  );
}

function renderFileCitationButton({ key, path, line, endLine, raw, actions }) {
  const filePath = (path || '').toString().trim();
  if (!filePath) return raw || '';
  const location = lineRangeLabel(line, endLine);
  const display = (raw || '').toString().trim() || `${basenameFromPath(filePath)}${location ? ` (${location})` : ''}`;
  const payload = { path: filePath, line: positiveInt(line, positiveInt(endLine, 1)), column: 0, raw: display };
  return (
    <button
      type="button"
      key={key}
      className="chat-md-file-ref chat-md-file-citation"
      aria-label={`打开文件引用 ${filePath}`}
      title={location ? `${filePath} (${location})` : filePath}
      onClick={() => actions?.onFileRef?.(payload)}
    >
      {display}
    </button>
  );
}

function renderDirectiveToken(token, key, actions = {}) {
  const match = token.match(CODEX_DIRECTIVE_TOKEN_RE);
  if (!match) return null;
  const [, name, labelValue = '', rawAttrs = ''] = match;
  const attrs = parseDirectiveAttrs(rawAttrs);
  const label = (labelValue || '').toString().trim();
  if (name === 'codex-file-citation') {
    return renderFileCitationButton({
      key,
      path: attrs.path || label,
      line: attrs.line_range_start,
      endLine: attrs.line_range_end,
      raw: label,
      actions,
    });
  }
  if (name === 'codex-terminal-citation') {
    const lineStart = positiveInt(attrs.line_range_start, 0);
    const lineEnd = positiveInt(attrs.line_range_end, 0);
    return renderCitationChip({
      key,
      className: 'chat-md-terminal-citation',
      icon: '⌘',
      label: label || 'Terminal output',
      title: attrs.terminal_chunk_id || label,
      payload: { kind: 'terminal', chunkId: attrs.terminal_chunk_id || '', lineStart, lineEnd, raw: label || 'Terminal output' },
      actions,
    });
  }
  if (name === 'codex-image-citation') {
    return renderCitationChip({
      key,
      className: 'chat-md-image-citation',
      icon: 'IMG',
      label: label || 'Image citation',
      title: attrs.asset_pointer || label,
      payload: { kind: 'image', assetPointer: attrs.asset_pointer || '', imageSrc: attrs.image_src || '', path: attrs.path || '', raw: label || 'Image citation' },
      actions,
    });
  }
  if (name === 'task-stub') {
    const title = (attrs.title || label || 'Task').toString().trim();
    return renderCitationChip({
      key,
      className: 'chat-md-task-stub',
      icon: '✦',
      label: title,
      payload: { kind: 'task', title, prompt: label, raw: title },
      actions,
    });
  }
  if (name === 'automation-update') {
    const title = (attrs.name || label || 'Automation').toString().trim();
    const prompt = (attrs.prompt || label || '').toString().trim();
    return renderCitationChip({
      key,
      className: 'chat-md-automation-update',
      icon: '⚙',
      label: title,
      payload: { kind: 'automation-update', title, message: label, prompt, path: '', lineStart: 0, lineEnd: 0, raw: title },
      actions,
    });
  }
  if (name === 'code-comment') {
    const path = (attrs.path || '').toString().trim();
    const startLine = positiveInt(attrs.line_range_start, 0);
    const endLine = positiveInt(attrs.line_range_end, startLine);
    const title = (attrs.title || 'Code comment').toString().trim();
    const location = path ? lineRangeLabel(startLine, endLine) : '';
    const display = path ? `${title} · ${basenameFromPath(path)}${location ? ` (${location})` : ''}` : title;
    return renderCitationChip({
      key,
      className: 'chat-md-code-comment',
      icon: '💬',
      label: display,
      title: label || title,
      payload: { kind: 'code-comment', title, message: label, prompt: '', path, lineStart: startLine, lineEnd: endLine, raw: display },
      actions,
    });
  }
  return null;
}

function renderMarkdownLinkToken(token, key, actions = {}) {
  const parsed = token.match(/^\[([^\]]+)]\(([^)]+)\)$/);
  const citationPayload = citationLinkPayload(parsed?.[1], parsed?.[2]);
  if (citationPayload) {
    const label = citationPayload.kind === 'skill' && citationPayload.path && parsed?.[1] === parsed?.[2]
      ? citationPayload.skillName
      : parsed?.[1];
    return renderCitationChip({
      key,
      className: citationPayload.kind === 'conversation' ? 'chat-md-conversation-chip' : 'chat-md-skill-chip',
      icon: citationPayload.kind === 'conversation' ? '↗' : '◆',
      label,
      title: parsed?.[2] || label,
      payload: { ...citationPayload, raw: label || citationPayload.raw },
      actions,
    });
  }
  const href = safeMarkdownUrl(parsed?.[2]);
  if (!href) return parsed?.[1] || token;
  const handleClick = (e) => {
    e.preventDefault();
    if (window.wails?.Browser?.OpenURL) {
      window.wails.Browser.OpenURL(href);
    } else {
      window.open(href, '_blank', 'noreferrer');
    }
  };
  return <a key={key} href={href} onClick={handleClick} rel="noreferrer">{parsed?.[1]}</a>;
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

function renderInlineMarkdownToken(token, key, actions = {}) {
  const inlineImage = renderImagePreview(token, basenameFromPath(token), key);
  if (inlineImage) return inlineImage;
  const directive = renderDirectiveToken(token, key, actions);
  if (directive) return directive;
  if (token.startsWith('![')) return renderMarkdownImageToken(token, key);
  if (token.startsWith('[')) return renderMarkdownLinkToken(token, key, actions);
  if (token.startsWith('`')) return renderInlineCodeToken(token, key);
  return renderStyledInlineToken(token, key);
}

function renderInlineMarkdown(text, keyPrefix, actions = {}) {
  const source = (text || '').toString();
  const parts = [];
  let lastIndex = 0;
  let matchIndex = 0;
  for (const match of source.matchAll(inlineMarkdownPattern())) {
    appendInlineTextSegment(parts, source, lastIndex, match.index, `${keyPrefix}-text-${matchIndex}`);
    const token = match[0];
    parts.push(renderInlineMarkdownToken(token, `${keyPrefix}-inline-${matchIndex}`, actions));
    lastIndex = match.index + token.length;
    matchIndex += 1;
  }
  appendInlineTextSegment(parts, source, lastIndex, source.length, `${keyPrefix}-text-tail`);
  return parts.length > 0 ? parts : source;
}

const EMPTY_MARKDOWN_ACTIONS = Object.freeze({});

function InlineMarkdown({ text, inlineKey, actions = EMPTY_MARKDOWN_ACTIONS }) {
  const nodes = renderInlineMarkdown(text, inlineKey, actions);
  return <>{nodes}</>;
}

function MarkdownParagraph({ lines, paragraphKey, actions = EMPTY_MARKDOWN_ACTIONS }) {
  const nodes = [];
  const seenLines = new Map();
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const seenCount = seenLines.get(line) || 0;
    seenLines.set(line, seenCount + 1);
    const lineKey = `${paragraphKey}-line-${line}${seenCount > 0 ? `-${seenCount}` : ''}`;
    if (index > 0) nodes.push(<br key={`${paragraphKey}-br-${lineKey}`} />);
    nodes.push(
      <InlineMarkdown
        key={`${paragraphKey}-inline-${lineKey}`}
        text={line}
        inlineKey={lineKey}
        actions={actions}
      />,
    );
  }
  return (
    <p>
      {nodes}
    </p>
  );
}

const CODE_FENCE_LANGUAGE_PREFIXES = Object.freeze([
  'mermaid',
  'javascript',
  'typescript',
  'powershell',
  'plaintext',
  'markdown',
  'dockerfile',
  'makefile',
  'terminal',
  'console',
  'python',
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
  'yml',
  'zsh',
  'sh',
  'txt',
  'sql',
  'log',
  'xml',
  'env',
  'ini',
  'ps1',
  'php',
  'cpp',
  'c++',
  'rust',
  'ruby',
  'go',
  'py',
  'rs',
  'rb',
  'c',
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

function mermaidInitialState(diagram) {
  return diagram
    ? { status: 'loading', svg: '', error: '' }
    : { status: 'error', svg: '', error: 'Mermaid 图表内容为空' };
}

function MermaidDiagram({ source }) {
  const reactId = useId();
  const diagram = normalizeMessageText(source).trim();
  const [state, setState] = useState(() => mermaidInitialState(diagram));
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    if (!diagram) return undefined;
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
          <LightboxShell label="Mermaid 图表" onClose={() => setExpanded(false)}>
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
    return <MermaidDiagram key={code} source={code} />;
  }
  return <pre><code>{code}</code></pre>;
}

function fenceMarkerMatch(line) {
  const backtickIndex = line.indexOf('```');
  const tildeIndex = line.indexOf('~~~');
  if (backtickIndex < 0 && tildeIndex < 0) return null;
  const markerIndex = backtickIndex < 0
    ? tildeIndex
    : (tildeIndex < 0 ? backtickIndex : Math.min(backtickIndex, tildeIndex));
  const markerChar = line[markerIndex];
  let fenceLength = 0;
  while (line[markerIndex + fenceLength] === markerChar) fenceLength += 1;
  if (fenceLength < 3) return null;
  return { markerIndex, markerChar, fenceLength };
}

function normalizeFenceLanguageToken(token) {
  const value = (token || '').toString().trim().toLowerCase();
  if (!value) return '';
  const classMatch = value.match(/^\{\.?([a-z][\w+-]*)/);
  if (classMatch) return classMatch[1].replace(/^language-/, '');
  return value.replace(/^language-/, '').replace(/^\./, '');
}

function fenceInfoRestIsMetadata(rest) {
  const value = (rest || '').toString().trim();
  if (!value) return false;
  return (
    /^[{[(]/.test(value) ||
    /^(?:title|filename|file|caption|linenos?|highlight|hl_lines|showlinenumbers|numberlines)\b/i.test(value) ||
    /^[\w-]+\s*=/.test(value)
  );
}

function parseFenceInfo(rawInfo) {
  const info = (rawInfo || '').toString().trim();
  if (!info) return { language: '', firstCodeLine: '' };

  const tokenMatch = info.match(/^([A-Za-z][\w+-]*)(?:\s+(.+))?$/);
  if (tokenMatch) {
    const rest = tokenMatch[2] || '';
    return {
      language: normalizeFenceLanguageToken(tokenMatch[1]),
      firstCodeLine: fenceInfoRestIsMetadata(rest) ? '' : rest,
    };
  }

  const classMatch = info.match(/^\{\.?([A-Za-z][\w+-]*)(?:\s+[^}]*)?}$/);
  if (classMatch) {
    return { language: normalizeFenceLanguageToken(classMatch[1]), firstCodeLine: '' };
  }

  const lower = info.toLowerCase();
  const language = CODE_FENCE_LANGUAGE_PREFIXES.find((item) => lower.startsWith(item));
  if (language && info.length > language.length) {
    const suffix = info.slice(language.length);
    return {
      language,
      firstCodeLine: fenceInfoRestIsMetadata(suffix) ? '' : suffix,
    };
  }

  return { language: normalizeFenceLanguageToken(info), firstCodeLine: '' };
}

function splitMarkdownFenceLine(line) {
  const marker = fenceMarkerMatch(line);
  if (!marker) return null;
  const prefix = line.slice(0, marker.markerIndex);
  const rawInfo = line.slice(marker.markerIndex + marker.fenceLength).replace(/^\s+/, '');
  return {
    prefix,
    markerChar: marker.markerChar,
    fenceLength: marker.fenceLength,
    ...parseFenceInfo(rawInfo),
  };
}

function normalizeMessageText(text) {
  return (text || '').toString().replace(/\r\n/g, '\n');
}

function isIndentedMarkdownCodeLine(line) {
  return /^(?: {4}|\t)/.test(line || '');
}

function unindentMarkdownCodeLine(line) {
  return (line || '').toString().replace(/^(?: {4}|\t)/, '');
}

function isTerminalPromptLine(line) {
  return /^\s{0,3}(?:(?:[$❯➜λ])|(?:PS [^>]*>)|(?:[A-Za-z]:[\\/][^>]*>)|(?:[\w.-]+@[\w.-]+:[^\s$#>]*[$#]))\s+\S/.test(line || '');
}

function isInsideInlineCode(source, offset) {
  let open = false;
  for (let index = 0; index < offset; index += 1) {
    if (source[index] !== '`') continue;
    let runLength = 1;
    while (source[index + runLength] === '`') runLength += 1;
    if (runLength === 1) open = !open;
    index += runLength - 1;
  }
  return open;
}

function markdownHeadingMatch(line) {
  const trimmed = line.trim();
  const standard = trimmed.match(/^(#{1,6})\s+(.+)$/);
  if (standard) return standard;
  const compact = trimmed.match(/^(#{2,6})([A-Za-z0-9_].*)$/);
  if (compact) return [compact[0], compact[1], compact[2]];
  return null;
}

function unorderedMarkdownListItemText(line) {
  const trimmed = line.trim();
  const standard = trimmed.match(/^[-*]\s+(.+)$/);
  if (standard) return standard[1];
  const compact = trimmed.match(/^[-*]((?:[A-Z][A-Za-z0-9_-]{1,40}|[\u4e00-\u9fff][\u4e00-\u9fffA-Za-z0-9_-]{0,20})[:：].+)$/);
  return compact?.[1] || '';
}

function startsSoftMarkdownHeading(source, index) {
  if (index <= 0 || source[index] !== '#' || isInsideInlineCode(source, index)) return false;
  let cursor = index;
  while (source[cursor] === '#') cursor += 1;
  const level = cursor - index;
  if (level < 2 || level > 6 || !source[cursor]) return false;
  const hasSpace = /\s/.test(source[cursor]);
  if (hasSpace) {
    return /[\s。！？!?；;：:，,.)）\]}]/.test(source[index - 1]);
  }
  if (!/^[A-Za-z0-9_]/.test(source[cursor])) return false;
  return /[\s。！？!?；;：:，,.)）\]}]/.test(source[index - 1]);
}

function compactHeadingPrefixBeforeList(value) {
  return /^#{2,6}[^:：\s]*$/.test(value.trim());
}

function startsSoftMarkdownList(source, index, segmentStart) {
  if (index <= 0 || source[index] !== '-' || isInsideInlineCode(source, index)) return false;
  if (compactHeadingPrefixBeforeList(source.slice(segmentStart, index))) return false;
  if (!unorderedMarkdownListItemText(source.slice(index))) return false;
  if (/^-\s+/.test(source.slice(index))) {
    return /[\s。！？!?；;：:，,.)）\]}]/.test(source[index - 1]);
  }
  return !/[\\/]/.test(source[index - 1]);
}

function splitMarkdownSoftBlocks(line) {
  const source = (line || '').toString();
  if (!source || fenceMarkerMatch(source)) return [source];
  const boundaries = [];
  let segmentStart = 0;
  for (let index = 1; index < source.length; index += 1) {
    if (startsSoftMarkdownHeading(source, index)) {
      boundaries.push(index);
      segmentStart = index;
      continue;
    }
    if (startsSoftMarkdownList(source, index, segmentStart)) {
      boundaries.push(index);
      segmentStart = index;
    }
  }
  if (boundaries.length === 0) return [source];
  const chunks = [];
  let start = 0;
  boundaries.forEach((boundary) => {
    const chunk = source.slice(start, boundary).trimEnd();
    if (chunk) chunks.push(chunk);
    start = boundary;
  });
  const tail = source.slice(start).trimStart();
  if (tail) chunks.push(tail);
  return chunks.length > 0 ? chunks : [source];
}

function markdownInputLines(text) {
  return normalizeMessageText(text).split('\n').flatMap(splitMarkdownSoftBlocks);
}

function standaloneCodeFence(text) {
  const lines = normalizeMessageText(text).trim().split('\n');
  if (lines.length < 1) return null;
  const opening = splitMarkdownFenceLine(lines[0]);
  if (!opening || opening.prefix.trim()) return null;

  // Find if there is a closing fence in the lines
  let closingIndex = -1;
  for (let i = 1; i < lines.length; i++) {
    if (markdownClosingFence(lines[i], opening)) {
      closingIndex = i;
      break;
    }
  }

  if (closingIndex !== -1) {
    // If there is a closing fence, it must be the last line to be a standalone code fence
    if (closingIndex !== lines.length - 1) {
      return null; // Closed in the middle, has trailing text -> not standalone
    }
    // Complete code fence
    const bodyLines = lines.slice(1, closingIndex);
    if (opening.firstCodeLine) bodyLines.unshift(opening.firstCodeLine);
    return {
      language: opening.language,
      body: bodyLines.join('\n'),
    };
  }

  // No closing fence found -> it is an incomplete/streaming code fence!
  const bodyLines = lines.slice(1);
  if (opening.firstCodeLine) bodyLines.unshift(opening.firstCodeLine);
  return {
    language: opening.language,
    body: bodyLines.join('\n'),
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
  const lines = [];
  for (const line of payload.body.split('\n')) {
    const trimmed = line.trimEnd();
    if (trimmed) lines.push(trimmed);
  }
  if (lines.length === 0) return false;
  if (['log', 'logs', 'console', 'terminal'].includes(payload.language)) return true;

  const levelLines = lines.filter((line) => /^(\[[A-Z]+]|\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}|(?:TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\b)/.test(line));
  const stackTrace = lines.some((line) => /^(?:\w+\s*)?Error:/.test(line))
    && lines.some((line) => /^\s*at\s+.+:\d+:\d+\)?$/.test(line));
  const terminalPrompt = isTerminalPromptLine(lines[0]);
  return stackTrace || levelLines.length > 0 || terminalPrompt;
}

function isConfigOutput(text) {
  const payload = candidatePayload(text);
  const lines = [];
  for (const line of payload.body.split('\n')) {
    const trimmed = line.trim();
    if (trimmed) lines.push(trimmed);
  }
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

function markdownBlockContext(lines, actions = {}) {
  const nodes = [];
  return {
    actions,
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

function markdownClosingFence(line, openingFence) {
  const value = (line || '').toString();
  const indentMatch = value.match(/^ {0,3}/);
  const markerStart = indentMatch?.[0].length || 0;
  const rest = value.slice(markerStart);
  const marker = openingFence.markerChar.repeat(openingFence.fenceLength);
  if (!rest.startsWith(marker)) return null;
  let cursor = openingFence.fenceLength;
  while (rest[cursor] === openingFence.markerChar) cursor += 1;
  return rest.slice(cursor).trim() ? null : { markerStart, markerLength: cursor };
}

function readMarkdownCodeLines(lines, index, fence) {
  const codeLines = fence.firstCodeLine ? [fence.firstCodeLine] : [];
  let cursor = index + 1;
  while (cursor < lines.length) {
    const closing = markdownClosingFence(lines[cursor], fence);
    if (closing) {
      const beforeClose = lines[cursor].slice(0, closing.markerStart);
      if (beforeClose.trim()) codeLines.push(beforeClose);
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
    const paragraphKey = context.nextKey('paragraph');
    context.nodes.push(<MarkdownParagraph key={paragraphKey} lines={[fence.prefix.trimEnd()]} paragraphKey={paragraphKey} actions={context.actions} />);
  }
  const key = context.nextKey('code');
  const code = readMarkdownCodeLines(context.lines, index, fence);
  context.nodes.push(<CodeBlock key={key} language={fence.language} code={code.codeLines.join('\n')} />);
  return { index: code.index };
}

function readIndentedMarkdownCodeLines(lines, index) {
  const codeLines = [];
  let cursor = index;
  while (cursor < lines.length) {
    if (!lines[cursor].trim()) {
      codeLines.push('');
      cursor += 1;
      continue;
    }
    if (!isIndentedMarkdownCodeLine(lines[cursor])) break;
    codeLines.push(unindentMarkdownCodeLine(lines[cursor]));
    cursor += 1;
  }
  while (codeLines.length > 0 && codeLines.at(-1) === '') codeLines.pop();
  return { codeLines, index: cursor };
}

function consumeIndentedMarkdownCode(context, index) {
  if (!isIndentedMarkdownCodeLine(context.lines[index])) return null;
  const result = readIndentedMarkdownCodeLines(context.lines, index);
  if (result.codeLines.length === 0) return null;
  context.nodes.push(<CodeBlock key={context.nextKey('code')} code={result.codeLines.join('\n')} />);
  return { index: result.index };
}

function readTerminalTranscriptLines(lines, index) {
  const codeLines = [];
  let cursor = index;
  while (cursor < lines.length) {
    if (!lines[cursor].trim()) break;
    codeLines.push(lines[cursor]);
    cursor += 1;
  }
  return { codeLines, index: cursor };
}

function consumeTerminalTranscript(context, index) {
  if (!isTerminalPromptLine(context.lines[index])) return null;
  const result = readTerminalTranscriptLines(context.lines, index);
  context.nodes.push(<CodeBlock key={context.nextKey('terminal')} language="terminal" code={result.codeLines.join('\n')} />);
  return { index: result.index };
}

function consumeMarkdownHeading(context, index) {
  const heading = markdownHeadingMatch(context.lines[index]);
  if (!heading) return null;
  const level = Math.min(6, heading[1].length);
  const HeadingTag = `h${level}`;
  context.nodes.push(
    <HeadingTag key={context.nextKey('heading')}>
      <InlineMarkdown text={heading[2]} inlineKey={`heading-${context.nodes.length}`} actions={context.actions} />
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

function MarkdownTableHeaderCell({ cell, cellKey, actions = EMPTY_MARKDOWN_ACTIONS }) {
  return (
    <th>
      <InlineMarkdown text={cell} inlineKey={cellKey} actions={actions} />
    </th>
  );
}

function MarkdownTableCell({ value, cellKey, actions = EMPTY_MARKDOWN_ACTIONS }) {
  return (
    <td>
      <InlineMarkdown text={value} inlineKey={cellKey} actions={actions} />
    </td>
  );
}

function renderMarkdownTable(headers, rows, key, actions = {}) {
  return (
    <table key={key}>
      <thead>
        <tr>
          {headers.map((cell, cellIndex) => (
            <MarkdownTableHeaderCell key={`${key}-h-${cellIndex}`} cell={cell} cellKey={`${key}-h-${cellIndex}`} actions={actions} />
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row, rowIndex) => (
          <tr key={`${key}-r-${rowIndex}`}>
            {headers.map((_, cellIndex) => (
              <MarkdownTableCell
                key={`${key}-r-${rowIndex}-${cellIndex}`}
                value={row[cellIndex] || ''}
                cellKey={`${key}-r-${rowIndex}-${cellIndex}`}
                actions={actions}
              />
            ))}
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
  context.nodes.push(renderMarkdownTable(headers, body.rows, key, context.actions));
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
  context.nodes.push(
    <blockquote key={key}>
      <MarkdownParagraph lines={quoteLines} paragraphKey={`${key}-p`} actions={context.actions} />
    </blockquote>,
  );
  return { index: cursor };
}

function readMarkdownTaskItems(lines, index) {
  const items = [];
  let cursor = index;
  while (cursor < lines.length) {
    const itemMatch = lines[cursor].trim().match(/^[-*]\s*\[([ xX])]\s+(.+)$/);
    if (!itemMatch) break;
    items.push({ checked: itemMatch[1].toLowerCase() === 'x', text: itemMatch[2] });
    cursor += 1;
  }
  return { items, index: cursor };
}

function consumeMarkdownTaskList(context, index) {
  if (!context.lines[index].trim().match(/^[-*]\s*\[([ xX])]\s+(.+)$/)) return null;
  const key = context.nextKey('task-list');
  const result = readMarkdownTaskItems(context.lines, index);
  context.nodes.push(
    <ul key={key} className="task-list">
      {result.items.map((item, itemIndex) => (
        <li key={`${key}-${itemIndex}`}>
          <input type="checkbox" checked={item.checked} disabled readOnly aria-label={item.text} />
          <span><InlineMarkdown text={item.text} inlineKey={`${key}-${itemIndex}`} actions={context.actions} /></span>
        </li>
      ))}
    </ul>,
  );
  return { index: result.index };
}

function readMarkdownListItems(lines, index, ordered) {
  const items = [];
  let cursor = index;
  while (cursor < lines.length) {
    if (ordered) {
      const itemMatch = lines[cursor].trim().match(/^\d+\.\s+(.+)$/);
      if (!itemMatch) break;
      items.push(itemMatch[1]);
    }
    else {
      const itemText = unorderedMarkdownListItemText(lines[cursor]);
      if (!itemText) break;
      items.push(itemText);
    }
    cursor += 1;
  }
  return { items, index: cursor };
}

function consumeMarkdownList(context, index) {
  const trimmed = context.lines[index].trim();
  const unordered = unorderedMarkdownListItemText(trimmed);
  const ordered = trimmed.match(/^\d+\.\s+(.+)$/);
  if (!unordered && !ordered) return null;
  const key = context.nextKey('list');
  const ListTag = ordered ? 'ol' : 'ul';
  const result = readMarkdownListItems(context.lines, index, Boolean(ordered));
  context.nodes.push(
    <ListTag key={key}>
      {result.items.map((item, itemIndex) => (
        <li key={`${key}-${itemIndex}`}>
          <InlineMarkdown text={item} inlineKey={`${key}-${itemIndex}`} actions={context.actions} />
        </li>
      ))}
    </ListTag>,
  );
  return { index: result.index };
}

function startsMarkdownBlock(lines, index) {
  const next = lines[index];
  const trimmed = next.trim();
  if (!trimmed) return true;
  if (fenceMarkerMatch(next) || isIndentedMarkdownCodeLine(next) || isTerminalPromptLine(next) || trimmed.startsWith('>')) return true;
  if (/^(-{3,}|\*{3,}|_{3,})$/.test(trimmed)) return true;
  if (markdownHeadingMatch(trimmed)) return true;
  if (unorderedMarkdownListItemText(trimmed) || /^\d+\.\s+(.+)$/.test(trimmed)) return true;
  return markdownTableStarts(lines, index);
}

function consumeMarkdownParagraphBlock(context, index) {
  const paragraph = [context.lines[index]];
  let cursor = index + 1;
  while (cursor < context.lines.length && !startsMarkdownBlock(context.lines, cursor)) {
    paragraph.push(context.lines[cursor]);
    cursor += 1;
  }
  const paragraphKey = context.nextKey('paragraph');
  context.nodes.push(<MarkdownParagraph key={paragraphKey} lines={paragraph} paragraphKey={paragraphKey} actions={context.actions} />);
  return { index: cursor };
}

const MARKDOWN_BLOCK_CONSUMERS = [
  consumeBlankMarkdownLine,
  consumeMarkdownSeparator,
  consumeMarkdownFence,
  consumeIndentedMarkdownCode,
  consumeTerminalTranscript,
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

function renderMarkdownBlocks(lines, actions = {}, cache = null) {
  const context = markdownBlockContext(lines, actions);
  let index = 0;
  const checkpoints = [];

  if (cache && cache.lines && cache.nodes && cache.checkpoints) {
    let matchingCount = 0;
    const maxMatch = Math.min(lines.length, cache.lines.length);
    while (matchingCount < maxMatch && lines[matchingCount] === cache.lines[matchingCount]) {
      matchingCount++;
    }

    let splitIndex = -1;
    for (let i = matchingCount - 1; i >= 0; i--) {
      if (cache.checkpoints[i] !== undefined) {
        splitIndex = i;
        break;
      }
    }

    if (splitIndex >= 0) {
      index = splitIndex;
      const reuseCount = cache.checkpoints[splitIndex];
      for (let i = 0; i < reuseCount; i++) {
        context.nodes.push(cache.nodes[i]);
      }
      for (let i = 0; i <= splitIndex; i++) {
        if (cache.checkpoints[i] !== undefined) {
          checkpoints[i] = cache.checkpoints[i];
        }
      }
    }
  }

  while (index < lines.length) {
    checkpoints[index] = context.nodes.length;
    index = consumeMarkdownBlock(context, index);
  }

  if (cache) {
    cache.lines = lines;
    cache.nodes = context.nodes;
    cache.checkpoints = checkpoints;
  }

  return context.nodes;
}

const MarkdownBlocks = React.memo(
  function MarkdownBlocks({ lines, actions = EMPTY_MARKDOWN_ACTIONS, fallback = null }) {
    const cache = useMemo(() => ({ lines: [], nodes: [], checkpoints: [], actions }), [actions]);
    const nodes = renderMarkdownBlocks(lines, actions, cache);
    return <>{nodes.length > 0 ? nodes : fallback}</>;
  },
  (prevProps, nextProps) => {
    if (prevProps.fallback !== nextProps.fallback) return false;
    if (prevProps.actions !== nextProps.actions) return false;
    const prevLines = prevProps.lines;
    const nextLines = nextProps.lines;
    if (prevLines === nextLines) return true;
    if (!prevLines || !nextLines) return false;
    if (prevLines.length !== nextLines.length) return false;
    for (let i = 0; i < prevLines.length; i++) {
      if (prevLines[i] !== nextLines[i]) return false;
    }
    return true;
  }
);

function MarkdownMessage({ text, actions }) {
  return (
    <div className="message-markdown">
      <MarkdownBlocks lines={markdownInputLines(text)} actions={actions} fallback={<p />} />
    </div>
  );
}

function MessageContent({ text, actions }) {
  const output = detectMessageOutput(text);
  if (output.kind === 'markdown') return <MarkdownMessage text={output.text} actions={actions} />;
  return <StructuredMessage kind={output.kind} text={output.text} />;
}

function isReasoningMessage(message) {
  const kind = (message?.kind || '').toString().trim().toLowerCase();
  return kind === 'thinking' || kind === 'reasoning' || kind === 'tool' || kind === 'command' || kind === 'process' || kind === 'plan';
}

function isApprovalMessage(message) {
  return (message?.kind || '').toString().trim().toLowerCase() === 'approval';
}

function approvalRequestId(message) {
  const raw = Number(message?.requestId || message?.request_id);
  const requestId = Number.isFinite(raw) ? Math.trunc(raw) : 0;
  return requestId > 0 ? requestId : 0;
}

function isApprovalTerminal(message) {
  const status = (message?.status || '').toString().trim().toLowerCase();
  return Boolean(status && APPROVAL_TERMINAL_STATUSES.has(status));
}

function approvalHintText({ requestId, busy, resolved, terminal }) {
  if (requestId <= 0) return '审批请求缺少编号';
  if (busy) return '正在提交审批结果';
  if (resolved || terminal) return '审批结果已提交';
  return '等待审批';
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
  const items = [];
  for (const rawLine of normalizeMessageText(text).split('\n')) {
    const line = rawLine.trim();
    const match = line.match(/^([✅☑✓✔🔄⏳○◯☐❌])?\s*(?:[-*]|\d+[.)])\s*(?:\[([ xX])\]\s*)?(.+)$/u);
    if (!match) continue;
    const label = (match[3] || '').trim();
    if (!label || /^plan$/i.test(label)) continue;
    items.push({
      text: label,
      done: match[1] ? statusMarkers[match[1]] === true : (match[2] || '').toLowerCase() === 'x',
    });
  }
  return items;
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
  const [firstStart, setFirstStart] = useState(null);

  useEffect(() => {
    if (active) {
      if (!firstStart && startValue) {
        setFirstStart(timestampMs(startValue));
      }
    } else {
      if (firstStart !== null) {
        setFirstStart(null);
      }
    }
  }, [active, startValue, firstStart]);

  useEffect(() => {
    if (!active) return undefined;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [active]);

  const start = active ? (firstStart || timestampMs(startValue)) : timestampMs(startValue);
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
  const title = reasoningTitle(message);
  const elapsedSuffix = elapsed ? ` ${elapsed}` : '';
  const statusLabel = done 
    ? `已处理 ${title}${elapsedSuffix}` 
    : ((message?.kind || '').toString().trim().toLowerCase() === 'thinking' 
        ? `正在思考${elapsedSuffix}` 
        : `正在运行 ${title}${elapsedSuffix}`);
  const meta = reasoningKindMeta(message);
  return (
    <article className={`reasoning-message${done ? '' : ' is-active'} no-avatar`} aria-label="AI 思考记录">
      <details className="reasoning-trace">
        <summary>
          <span className="reasoning-trace-status">
            {statusLabel}
          </span>
        </summary>
        <div className="reasoning-step-list">
          <section className={`reasoning-step reasoning-step--${meta.tone}`} aria-label={`${meta.label}步骤`}>
            <div className="reasoning-step-body">
              {meta.tone === 'plan' ? <ExecutionPlan message={message} /> : <MessageContent text={reasoningStepDescription(message)} />}
            </div>
          </section>
        </div>
      </details>
    </article>
  );
}

function syntheticReasoningMessage({ activeTurn, sending, isBusy, fallbackStartTime }) {
  if (!activeTurn && !sending && !isBusy) return null;
  const turnId = activeTurn?.id;
  const id = turnId ? `thinking:${turnId}` : 'thinking-sending';
  const defaultStartTime = fallbackStartTime || new Date().toISOString();
  return {
    id,
    role: 'assistant',
    kind: 'thinking',
    title: '正在处理请求',
    text: '',
    time: activeTurn?.startedAt || defaultStartTime,
    done: false,
  };
}

function useComposerInteractions({
  attachments,
  attachPaths,
  attachDroppedFiles,
  removeAttachment,
  projectActionBlocked,
  canUseProjectActions,
}) {
  const [previewAttachment, setPreviewAttachment] = useState(null);
  const [dropActive, setDropActive] = useState(false);
  const dropDepthRef = useRef(0);
  const isComposingRef = useRef(false);
  const activePreview = previewAttachment && attachments.some((item) => composerAttachmentKey(item) === composerAttachmentKey(previewAttachment))
    ? previewAttachment
    : null;

  const previewAttachmentItem = (item) => {
    setPreviewAttachment(item);
  };
  const removeAttachmentItem = (item) => {
    removeAttachment(composerAttachmentKey(item));
    if (activePreview && composerAttachmentKey(activePreview) === composerAttachmentKey(item)) {
      setPreviewAttachment(null);
    }
  };
  const handlers = useComposerTransferHandlers({
    attachDroppedFiles,
    attachPaths,
    canUseProjectActions,
    dropDepthRef,
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

function useComposerTransferHandlers({ attachDroppedFiles, attachPaths, canUseProjectActions, dropDepthRef, projectActionBlocked, setDropActive }) {
  const resetDropState = useCallback(() => {
    dropDepthRef.current = 0;
    setDropActive(false);
  }, [dropDepthRef, setDropActive]);

  useEffect(() => {
    if (typeof attachPaths !== 'function') return undefined;
    return onFilesDropped((event) => {
      const files = nativeDropFiles(event, { acceptEmptyDetails: dropDepthRef.current > 0 });
      if (files.length === 0) return;
      if (!canUseProjectActions) return;
      attachPaths(files);
      resetDropState();
    });
  }, [attachPaths, canUseProjectActions, dropDepthRef, resetDropState]);
  const handlePaste = async (event) => {
    const paths = extractClipboardFilePaths(event);
    if (paths.length > 0) {
      event.preventDefault();
      if (projectActionBlocked) return;
      if (typeof attachPaths === 'function') attachPaths(paths);
      return;
    }
    const files = extractClipboardFiles(event);
    if (files.length === 0) return;
    event.preventDefault();
    if (projectActionBlocked) return;
    await attachDroppedFiles(files);
  };
  const handleDragEnter = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    if (projectActionBlocked) return;
    dropDepthRef.current += 1;
    setDropActive(true);
  };
  const handleDragOver = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    if (projectActionBlocked) return;
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    setDropActive(true);
  };
  const handleDragLeave = (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    dropDepthRef.current = Math.max(dropDepthRef.current - 1, 0);
    if (dropDepthRef.current === 0) setDropActive(false);
  };
  const handleDrop = async (event) => {
    if (!hasFilesTransfer(event)) return;
    event.preventDefault();
    event.stopPropagation();
    resetDropState();
    if (projectActionBlocked) return;
    const files = collectTransferFiles(event);
    const paths = extractTransferFilePaths(event);
    if (files.length > 0) {
      const attachedCount = await attachDroppedFiles(files);
      if (attachedCount > 0 && paths.length === 0) return;
    }
    if (paths.length > 0 && typeof attachPaths === 'function') attachPaths(paths);
  };
  return { handleDragEnter, handleDragLeave, handleDragOver, handleDrop, handlePaste };
}

function useComposerDropTarget(ref, composer) {
  useEffect(() => {
    const target = ref.current;
    if (!target) return undefined;

    const handleDragEnter = (event) => composer.handleDragEnter(event);
    const handleDragOver = (event) => composer.handleDragOver(event);
    const handleDragLeave = (event) => composer.handleDragLeave(event);
    const handleDrop = (event) => runUIAction(() => composer.handleDrop(event));

    target.addEventListener('dragenter', handleDragEnter);
    target.addEventListener('dragover', handleDragOver);
    target.addEventListener('dragleave', handleDragLeave);
    target.addEventListener('drop', handleDrop);
    return () => {
      target.removeEventListener('dragenter', handleDragEnter);
      target.removeEventListener('dragover', handleDragOver);
      target.removeEventListener('dragleave', handleDragLeave);
      target.removeEventListener('drop', handleDrop);
    };
  }, [composer, ref]);
}

function ComposerDock({
  floating = false,
  draft,
  setDraft,
  sendMessage,
  attachments,
  selectFiles,
  sending,
  store,
  modelThreadId,
  showProviderToggle = true,
  composer,
  canUseProjectActions = true,
}) {
  const composerClass = `composer ${floating ? 'composer--floating' : 'composer--docked'}`;
  const hasComposerInput = Boolean(textValue(draft) || attachments.length > 0);
  const canInterrupt = canUseProjectActions && Boolean(store?.hasInterruptibleThreadAction?.(modelThreadId));
  const canSend = canUseProjectActions && !sending && !canInterrupt && hasComposerInput;
  const projectActionBlocked = !canUseProjectActions;
  const projectActionBlockedTitle = '请先连接后端并选择项目';
  const dockRef = useRef(null);
  useComposerDropTarget(dockRef, composer);

  const handleKeyDown = useComposerSendKeyHandler({ canSend, composer, sendMessage });

  return (
    <footer
      ref={dockRef}
      id="chat-input-bar"
      className={`${composerClass}${composer.dropActive ? ' drop-active' : ''}`}
      data-testid="composer-dock"
      data-file-drop-target=""
    >
      <div className="composer-card">
        {composer.dropActive ? <div className="composer-drop-hint" aria-live="polite">松开即可添加附件</div> : null}
        <ForkDraftCard store={store} />
        <ComposerAttachments attachments={attachments} onPreview={composer.previewAttachmentItem} onRemove={composer.removeAttachmentItem} />
        <ComposerTextarea
          composer={composer}
          draft={draft}
          handleKeyDown={handleKeyDown}
          setDraft={setDraft}
        />
        <ComposerMeta
          canInterrupt={canInterrupt}
          canSend={canSend}
          canUseProjectActions={canUseProjectActions}
          modelThreadId={modelThreadId}
          projectActionBlocked={projectActionBlocked}
          projectActionBlockedTitle={projectActionBlockedTitle}
          selectFiles={selectFiles}
          sendMessage={sendMessage}
          showProviderToggle={showProviderToggle}
          store={store}
        />
      </div>
      <ComposerPreviewModal composer={composer} />
    </footer>
  );
}

function ComposerTextarea({ composer, draft, handleKeyDown, setDraft }) {
  const textareaRef = useRef(null);
  useComposerDropTarget(textareaRef, composer);
  return (
    <textarea
      ref={textareaRef}
      id="composer-input"
      data-testid="composer-input"
      data-file-drop-target=""
      aria-label="输入给 Agent 的内容"
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

function ForkDraftCard({ store }) {
  const draft = store.forkDraft;
  if (!draft?.open) return null;
  const selected = new Set(draft.sharedFilePaths || []);
  const files = Array.isArray(draft.availableSharedFiles) ? draft.availableSharedFiles : [];
  return (
    <section className="fork-draft-card" data-testid="fork-draft-card" aria-label="继承对话草稿">
      <header>
        <div>
          <p>继承对话</p>
          <strong>{draft.sourceTitle || '继承自当前会话'}</strong>
        </div>
        <button
          type="button"
          className="fork-draft-close"
          aria-label="关闭继承对话草稿"
          disabled={draft.submitting}
          onClick={() => runUIAction(() => store.closeForkDraft?.())}
        >
          <X size={14} />
        </button>
      </header>
      {draft.error ? <div className="fork-draft-error" role="alert">{draft.error}</div> : null}
      <div className="fork-draft-files" aria-live="polite">
        {draft.loadingSharedFiles ? <span className="fork-draft-muted">正在加载共享文件...</span> : null}
        {!draft.loadingSharedFiles && files.length === 0 ? <span className="fork-draft-muted">暂无可选共享文件</span> : null}
        {files.map((file) => (
          <label key={file.path} className="fork-draft-file">
            <input
              type="checkbox"
              aria-label={`选择共享文件 ${file.path}`}
              checked={selected.has(file.path)}
              disabled={draft.submitting}
              onChange={() => runUIAction(() => store.toggleForkDraftSharedFile?.(file.path))}
            />
            <FileText size={14} />
            <span>{file.path}</span>
          </label>
        ))}
      </div>
      <div className="fork-draft-actions">
        <button type="button" disabled={draft.submitting} onClick={() => runUIAction(() => store.closeForkDraft?.())}>取消</button>
        <button type="button" className="fork-draft-submit" disabled={draft.submitting} onClick={() => runUIAction(() => store.submitForkThread?.())}>
          {draft.submitting ? '创建中...' : '创建继承对话'}
        </button>
      </div>
    </section>
  );
}

function ComposerMeta({
  canInterrupt,
  canSend,
  canUseProjectActions,
  modelThreadId,
  projectActionBlocked,
  projectActionBlockedTitle,
  selectFiles,
  sendMessage,
  showProviderToggle: _,
  store,
}) {
  const canForkThread = canUseProjectActions && Boolean(store.hasActiveThreadActions?.());
  const forkBlockedTitle = projectActionBlocked ? projectActionBlockedTitle : '当前没有可继承的会话';
  const primaryActionLabel = canInterrupt ? '中断当前执行' : '发送消息';
  const primaryActionTitle = canInterrupt ? '中断当前执行' : undefined;
  const primaryActionClass = `send${canInterrupt ? ' send--interrupt' : ''}`;
  const primaryActionDisabled = canInterrupt ? false : !canSend;
  const onPrimaryAction = () => {
    if (canInterrupt) {
      runUIAction(() => store.interruptActiveThread?.());
      return;
    }
    if (canSend) runUIAction(() => sendMessage());
  };
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
      <button
        type="button"
        className="composer-attach composer-fork"
        aria-label="继承当前对话"
        title={canForkThread ? '继承当前对话' : forkBlockedTitle}
        disabled={!canForkThread}
        onClick={() => {
          if (canForkThread) runUIAction(() => store.openForkDraft?.());
        }}
      >
        <GitBranch size={16} />
      </button>
      <div className="composer-actions">

        <ModelSelector store={store} activeThreadId={modelThreadId} disabled={projectActionBlocked} />
        <button type="button" className={primaryActionClass} aria-label={primaryActionLabel} title={primaryActionTitle} disabled={primaryActionDisabled} onClick={onPrimaryAction}>
          {canInterrupt ? <CircleStop size={18} /> : <Send size={18} />}
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
    messagePagination,
    loadOlderThreadMessages,
    timelineBlocked,
    timelineContentBlocked = false,
    messageActions,
    store,
    attachments = [],
    attachPaths,
    attachDroppedFiles,
    removeAttachment,
    canUseProjectActions = true,
    sendMessage,
  } = props;
  const [justSent, setJustSent] = useState(false);
  useEffect(() => {
    if (sending) {
      setJustSent(true);
    } else if (justSent) {
      if (activeTurn) {
        setJustSent(false);
      } else {
        const timer = setTimeout(() => {
          setJustSent(false);
        }, 5000);
        return () => clearTimeout(timer);
      }
    }
  }, [sending, activeTurn, justSent]);

  const threadStatus = workStatusForThread({
    sending,
    loading: timelineContentBlocked,
    activeThreadId,
    activeThread,
    statusEntry,
  });
  const isBusy = threadStatus.busy;
  const introMode = !activeThreadId && !timelineBlocked && messages.length === 0;
  const hasProcessingAfterLastUser = hasReasoningMessageAfterLastUser(messages);
  const lastUserMessage = [...messages].reverse().find((msg) => (msg.role || '').toLowerCase() === 'user');
  const fallbackStartTime = lastUserMessage?.time;
  const pendingReasoning = !introMode && !timelineBlocked && !hasProcessingAfterLastUser && !hasAssistantReplyAfterLastUser(messages)
    ? syntheticReasoningMessage({ activeTurn, sending: sending || justSent, isBusy, fallbackStartTime })
    : null;
  const composerController = useComposerInteractions({
    attachments,
    attachPaths,
    attachDroppedFiles,
    removeAttachment,
    projectActionBlocked: !canUseProjectActions,
    canUseProjectActions,
  });
  const timelineRef = useRef(null);
  const shouldStickToBottomRef = useRef(true);
  const lastTimelineAutoScrollKeyRef = useRef('');
  const timelineContentBlockedRef = useRef(timelineContentBlocked);
  const isInitialThreadRenderRef = useRef(true);
  const updateTimelineStickiness = useCallback((timeline) => {
    shouldStickToBottomRef.current = isTimelineNearBottom(timeline);
  }, []);
  const scrollTimelineToBottomInstant = useCallback(() => {
    shouldStickToBottomRef.current = true;
    scrollTimelineElementToBottom(timelineRef.current, false);
  }, []);
  const scrollTimelineToBottomSmooth = useCallback(() => {
    shouldStickToBottomRef.current = true;
    scrollTimelineElementToBottom(timelineRef.current, true);
  }, []);
  const scrollTimelineToBottomIfSticky = useCallback((smooth = false) => {
    if (timelineRef.current && shouldStickToBottomRef.current) {
      scrollTimelineElementToBottom(timelineRef.current, smooth);
    }
  }, []);
  const sendMessageAndScrollToBottom = useCallback(() => {
    const result = sendMessage();
    shouldStickToBottomRef.current = true;
    requestTimelineBottomScroll(scrollTimelineToBottomSmooth);
    return result;
  }, [scrollTimelineToBottomSmooth, sendMessage]);
  const autoScrollKey = timelineAutoScrollKey({
    activeThreadId,
    introMode,
    messages,
    pendingReasoning,
    timelineContentBlocked,
  });
  useEffect(() => {
    timelineContentBlockedRef.current = timelineContentBlocked;
  }, [timelineContentBlocked]);
  useEffect(() => {
    shouldStickToBottomRef.current = true;
    lastTimelineAutoScrollKeyRef.current = '';
    isInitialThreadRenderRef.current = true;
    const el = timelineRef.current;
    if (el) {
      el.scrollTop = 0;
    }
  }, [activeThreadId]);
  useEffect(() => {
    if (!timelineContentBlocked && activeThreadId) {
      scrollTimelineToBottomInstant();
      isInitialThreadRenderRef.current = false;
      const timer = setTimeout(() => {
        scrollTimelineToBottomInstant();
      }, 50);
      return () => clearTimeout(timer);
    }
  }, [activeThreadId, timelineContentBlocked, scrollTimelineToBottomInstant]);
  useEffect(() => {
    if (!autoScrollKey) {
      lastTimelineAutoScrollKeyRef.current = autoScrollKey;
      return;
    }
    if (lastTimelineAutoScrollKeyRef.current === autoScrollKey) return;
    lastTimelineAutoScrollKeyRef.current = autoScrollKey;
    if (!shouldStickToBottomRef.current) return;
    requestTimelineBottomScroll(scrollTimelineToBottomInstant);
  }, [autoScrollKey, scrollTimelineToBottomInstant]);
  useEffect(() => {
    const el = timelineRef.current;
    if (!el) return;
    const observer = new MutationObserver(() => {
      if (!isInitialThreadRenderRef.current && !timelineContentBlockedRef.current && shouldStickToBottomRef.current) {
        scrollTimelineElementToBottom(el, false);
      }
    });
    observer.observe(el, {
      childList: true,
      subtree: true,
      characterData: true,
    });
    return () => {
      observer.disconnect();
    };
  }, [activeThreadId]);

  useEffect(() => {
    const el = timelineRef.current;
    if (!el) return;
    const handleLoad = () => {
      if (!isInitialThreadRenderRef.current && !timelineContentBlockedRef.current && shouldStickToBottomRef.current) {
        scrollTimelineElementToBottom(el, false);
      }
    };
    el.addEventListener('load', handleLoad, true);
    return () => {
      el.removeEventListener('load', handleLoad, true);
    };
  }, [activeThreadId]);
  const composer = (
    <ConversationComposer
      {...props}
      composer={composerController}
      floating={introMode}
      sendMessage={sendMessageAndScrollToBottom}
      showProviderToggle={!activeThreadId}
    />
  );
  const conversationClass = `conversation${introMode ? ' conversation--intro' : ''}${composerController.dropActive ? ' drop-active' : ''}`;
  return (
    <section
      id={CONVERSATION_DROP_TARGET_ID}
      className={conversationClass}
      data-testid={CONVERSATION_DROP_TARGET_ID}
      data-file-drop-target=""
      onDragEnter={composerController.handleDragEnter}
      onDragOver={composerController.handleDragOver}
      onDragLeave={composerController.handleDragLeave}
      onDrop={(event) => runUIAction(() => composerController.handleDrop(event))}
    >
      <ContextUsageBanner activeThreadId={activeThreadId} store={store} tokenUsage={tokenUsage} />
      <ConversationTimeline
        composer={composer}
        smoothStreaming={store?.smoothStreaming ?? false}
        introMode={introMode}
        messages={messages}
        pendingReasoning={pendingReasoning}
        projectPath={projectPath}
        activeThreadId={activeThreadId}
        messagePagination={messagePagination}
        loadOlderThreadMessages={loadOlderThreadMessages}
        timelineContentBlocked={timelineContentBlocked}
        messageActions={messageActions}
        onTimelineScroll={updateTimelineStickiness}
        onScrollToBottom={scrollTimelineToBottomSmooth}
        onScrollIfSticky={scrollTimelineToBottomIfSticky}
        timelineRef={timelineRef}
      />
      {!introMode ? composer : null}
    </section>
  );
}

function ContextUsageBanner({ activeThreadId, store, tokenUsage }) {
  if (!activeThreadId || !tokenUsage || tokenUsage.usedPercent < CONTEXT_USAGE_FORK_THRESHOLD) return null;
  const canFork = Boolean(store?.hasActiveThreadActions?.());
  return (
    <output className="context-usage-banner" data-testid="context-usage-banner">
      <span>上下文使用率 {Math.round(tokenUsage.usedPercent)}%</span>
      <button
        type="button"
        disabled={!canFork}
        onClick={() => {
          if (canFork) runUIAction(() => store.openForkDraft?.({ origin: 'context-usage' }));
        }}
      >
        新建继承会话
      </button>
    </output>
  );
}

function ConversationComposer({
  floating,
  draft,
  setDraft,
  sendMessage,
  attachments,
  selectFiles,
  sending,
  store,
  modelThreadId,
  showProviderToggle,
  composer,
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
      sending={sending}
      store={store}
      modelThreadId={modelThreadId}
      showProviderToggle={showProviderToggle}
      composer={composer}
      canUseProjectActions={canUseProjectActions}
    />
  );
}

function ConversationTimeline({
  composer,
  introMode,
  messages,
  pendingReasoning,
  projectPath,
  activeThreadId,
  messagePagination,
  loadOlderThreadMessages,
  timelineContentBlocked,
  messageActions,
  onTimelineScroll,
  onScrollToBottom,
  onScrollIfSticky,
  timelineRef,
  smoothStreaming,
}) {
  const {
    hiddenOlderCount,
    revealOlder,
    visibleMessages,
  } = useTimelineMaterialization({ activeThreadId, introMode, messages, timelineContentBlocked });
  const olderPageRequestingThreadRef = useRef('');
  const [olderPageRequestingThreadId, setOlderPageRequestingThreadId] = useState('');
  const olderPageRequesting = olderPageRequestingThreadId === activeThreadId;
  const bottomRef = useRef(null);
  const userScrolledRef = useRef(false);
  const olderPageLoading = Boolean(messagePagination?.loading || olderPageRequesting);
  const hasBackendOlderPage = Boolean(activeThreadId && messagePagination?.hasMore && typeof loadOlderThreadMessages === 'function');
  const canLoadBackendOlderPage = hasBackendOlderPage && !olderPageLoading;
  const requestBackendOlderPage = useCallback(() => {
    if (!activeThreadId || !messagePagination?.hasMore || messagePagination?.loading || typeof loadOlderThreadMessages !== 'function') return;
    if (olderPageRequestingThreadRef.current === activeThreadId) return;
    olderPageRequestingThreadRef.current = activeThreadId;
    setOlderPageRequestingThreadId(activeThreadId);
    void (async () => {
      try {
        await loadOlderThreadMessages(activeThreadId);
      }
      finally {
        if (olderPageRequestingThreadRef.current === activeThreadId) olderPageRequestingThreadRef.current = '';
        setOlderPageRequestingThreadId((current) => (current === activeThreadId ? '' : current));
      }
    })();
  }, [activeThreadId, loadOlderThreadMessages, messagePagination?.hasMore, messagePagination?.loading]);
  const requestOlderMessages = useCallback(() => {
    if (timelineContentBlocked) return;
    if (hiddenOlderCount > 0) {
      revealOlder();
      return;
    }
    if (canLoadBackendOlderPage) {
      const container = timelineRef.current;
      const beforeHeight = container ? container.scrollHeight : 0;
      requestBackendOlderPage();
      if (container && beforeHeight) {
        requestAnimationFrame(() => {
          container.scrollTop += container.scrollHeight - beforeHeight;
        });
      }
    }
  }, [canLoadBackendOlderPage, hiddenOlderCount, requestBackendOlderPage, revealOlder, timelineContentBlocked, timelineRef]);
  const handleScroll = useCallback((event) => {
    const el = event.currentTarget;
    onTimelineScroll?.(el);
    userScrolledRef.current = !isTimelineNearBottom(el);
    if (timelineContentBlocked) return;
    if (hiddenOlderCount <= 0 && !hasBackendOlderPage) return;
    if (el.scrollTop <= TIMELINE_SCROLL_LOAD_THRESHOLD) requestOlderMessages();
  }, [hasBackendOlderPage, hiddenOlderCount, onTimelineScroll, requestOlderMessages, timelineContentBlocked]);
  const visibleLen = visibleMessages.length;
  const pendingLen = pendingReasoning ? 1 : 0;
  const bottomAutoScrollTarget = pendingReasoning || visibleMessages[visibleMessages.length - 1] || null;
  const bottomAutoScrollEligible = shouldAutoScrollForTimelineMessage(bottomAutoScrollTarget);
  useEffect(() => {
    if (!bottomAutoScrollEligible) return;
    if (!userScrolledRef.current) {
      onScrollToBottom?.();
    }
  }, [bottomAutoScrollEligible, visibleLen, pendingLen, onScrollToBottom]);
  useEffect(() => {
    userScrolledRef.current = false;
  }, [activeThreadId]);

  const timelineMessages = [...visibleMessages];
  if (pendingReasoning) {
    timelineMessages.push(pendingReasoning);
  }

  return (
    <div className="timeline-shell">
      <div key={activeThreadId || 'intro'} className="timeline" data-testid="chat-timeline" ref={timelineRef} onScroll={handleScroll}>
        {introMode ? <IntroChatStage composer={composer} projectPath={projectPath} /> : null}
        {!introMode && !timelineContentBlocked && (hiddenOlderCount > 0 || hasBackendOlderPage) ? (
          <TimelineOlderMessagesMarker hiddenCount={hiddenOlderCount} loading={olderPageLoading} onReveal={requestOlderMessages} />
        ) : null}
        {!introMode && !timelineContentBlocked ? timelineMessages.map((message) => {
          const key = message.callId ? `tool-${message.callId}` : message.id;
          return (
            <TimelineMessage key={key} message={message} actions={messageActions} activeThreadId={activeThreadId} smoothStreaming={smoothStreaming} onScrollIfSticky={onScrollIfSticky} />
          );
        }) : null}
        {!introMode && timelineContentBlocked ? <TimelineLoadingPlaceholder /> : null}
        <div ref={bottomRef} style={{ height: 0 }} aria-hidden="true" />
      </div>
      {activeThreadId && !introMode && !timelineContentBlocked ? (
        <button
          type="button"
          className="chat-scroll-bottom-btn"
          title="滚动到底部"
          aria-label="滚动到底部"
          onClick={onScrollToBottom}
        >
          <ChevronDown size={15} aria-hidden="true" />
        </button>
      ) : null}
    </div>
  );
}

function TimelineOlderMessagesMarker({ hiddenCount, loading, onReveal }) {
  const label = hiddenCount > 0
    ? `显示更早的消息（${hiddenCount} 条）`
    : (loading ? '正在加载更早的消息' : '加载更早的消息');
  return (
    <div className="timeline-placeholder" data-testid="timeline-older-marker">
      <button type="button" className="ghost" disabled={hiddenCount <= 0 && loading} aria-busy={loading ? 'true' : 'false'} onClick={onReveal}>
        {label}
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

// resolveAttachmentImageSrc 解析附件图片 URL，处理四种格式：
// 1. data:image/... — 粘贴截图（imagePreviewSource 因 IMAGE_PATH_RE 不匹配此格式而返回空）
// 2. /clipboard/xxx.png — 历史 clipboard 图片的 Wails HTTP 路由，直接可用
// 3. http(s):// — 外部 URL
// 4. 本地文件路径 — 走 imagePreviewSource（file://, 生成图片路由等）
function resolveAttachmentImageSrc(att) {
  const preview = textValue(att.previewUrl || att.url);
  if (preview) {
    if (
      /^data:image\//i.test(preview) ||
      /^https?:\/\//i.test(preview) ||
      preview.startsWith('/clipboard/')
    ) {
      return preview;
    }
    const resolved = imagePreviewSource(preview);
    if (resolved) return resolved;
  }
  const path = textValue(att.path);
  return path ? imagePreviewSource(path) : '';
}

function UserMessageAttachments({ attachments }) {
  if (!Array.isArray(attachments) || attachments.length === 0) return null;
  const images = [];
  const files = [];
  for (const att of attachments) {
    if (!att) continue;
    const kind = textValue(att.kind).toLowerCase();
    if (kind === 'image') {
      const src = resolveAttachmentImageSrc(att);
      if (src) {
        images.push({ ...att, _resolvedSrc: src });
      } else {
        files.push(att);
      }
    } else {
      files.push(att);
    }
  }
  if (images.length === 0 && files.length === 0) return null;
  return (
    <div className="user-message-attachments">
      {images.length > 0 ? (
        <div className="user-attachment-gallery">
          {images.map((att, idx) => (
            <MarkdownImagePreview
              key={att.path || att.previewUrl || idx}
              src={att._resolvedSrc}
              label={att.name || basenameFromPath(att.path || att.previewUrl || '') || '图片附件'}
            />
          ))}
        </div>
      ) : null}
      {files.length > 0 ? (
        <div className="user-attachment-file-list">
          {files.map((att, idx) => (
            <span key={att.path || att.name || idx} className="user-attachment-file-pill">
              <File size={12} />
              <span>{att.name || basenameFromPath(att.path || '') || att.path}</span>
            </span>
          ))}
        </div>
      ) : null}
    </div>
  );
}

const TimelineMessage = React.memo(function TimelineMessage({ message, actions, activeThreadId, smoothStreaming, onScrollIfSticky }) {
  const streamKey = `${activeThreadId || ''}:${message.id || ''}`;
  const streamingAssistant = message.role === 'assistant' && message.done === false;
  const displayText = useSmoothStreamingText(message.text, {
    enabled: streamingAssistant && smoothStreaming,
    streamKey,
  });

  React.useLayoutEffect(() => {
    if (streamingAssistant && onScrollIfSticky) {
      onScrollIfSticky(false);
    }
  }, [displayText, streamingAssistant, smoothStreaming, onScrollIfSticky]);
  if (isApprovalMessage(message)) return <ApprovalTimelineMessage message={message} actions={actions} />;
  if (isReasoningMessage(message)) return <ReasoningTrace message={message} active={message.done === false} />;
  
  const isUser = message.role === 'user';
  return (
    <article className={`message ${message.role} no-avatar`}>
      <div className="bubble">
        {isUser ? (
          <header>
            <time>{formatTime(message.time)}</time>
          </header>
        ) : null}
        {isUser ? <UserMessageAttachments attachments={message.attachments} /> : null}
        <MessageContent text={displayText} actions={actions} />
        {!isUser && message.role === 'assistant' ? (
          <div className="assistant-footer">
            <time>{formatTime(message.time)}</time>
            <AssistantMessageActions text={message.text} />
          </div>
        ) : null}
      </div>
    </article>
  );
});

function ApprovalTimelineMessage({ message, actions }) {
  const [busy, setBusy] = useState(false);
  const [resolved, setResolved] = useState(false);
  const requestId = approvalRequestId(message);
  const terminal = isApprovalTerminal(message);
  const disabled = requestId <= 0 || busy || resolved || terminal || typeof actions?.onApproval !== 'function';
  const title = (message.title || message.command || '审批请求').toString().trim();
  const hint = approvalHintText({ requestId, busy, resolved, terminal });

  const submitApproval = async (approved) => {
    if (disabled) return;
    setBusy(true);
    try {
      const ok = await actions.onApproval(message, approved);
      if (ok) setResolved(true);
    }
    catch (error) {
      actions.onError?.('approval.failed', error.message || String(error));
    }
    finally {
      setBusy(false);
    }
  };

  return (
    <article className="message assistant approval-message no-avatar" data-testid={`approval-request-${requestId || 'invalid'}`}>
      <div className="bubble approval-card">
        <header>
          <span>{title}</span>
          <time>{formatTime(message.time)}</time>
        </header>
        <MessageContent text={message.text || message.command || '审批请求'} actions={actions} />
        <div className="approval-footer">
          <span className="approval-hint">{hint}</span>
          <div className="approval-actions">
            <button
              type="button"
              className="approval-action approval-action--approve"
              aria-label={`同意审批 ${requestId}`}
              disabled={disabled}
              onClick={() => submitApproval(true)}
            >
              <CheckCircle2 size={14} />
              <span>同意</span>
            </button>
            <button
              type="button"
              className="approval-action approval-action--reject"
              aria-label={`拒绝审批 ${requestId}`}
              disabled={disabled}
              onClick={() => submitApproval(false)}
            >
              <X size={14} />
              <span>拒绝</span>
            </button>
          </div>
        </div>
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
    let frameId = null;
    const onResize = () => {
      if (frameId) return;
      frameId = window.requestAnimationFrame(() => {
        frameId = null;
        const nextHeight = currentViewportHeight();
        setViewportHeight(nextHeight);
        setActivityPanelHeight((height) => clampActivityPanelHeight(height, nextHeight));
      });
    };
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      if (frameId) window.cancelAnimationFrame(frameId);
    };
  }, []);
  const beginActivityPanelResize = (event, inputType = 'pointer') => {
    event.preventDefault();
    if (inputType === 'pointer') {
      event.currentTarget?.setPointerCapture?.(event.pointerId);
    }
    const startY = event.clientY;
    const startHeight = activityPanelHeight;
    const moveEventName = inputType === 'mouse' ? 'mousemove' : 'pointermove';
    const stopEventName = inputType === 'mouse' ? 'mouseup' : 'pointerup';
    const panelEl = (event.currentTarget || event.target)?.closest?.('.runtime-panel') || document.querySelector('.runtime-panel');
    let latestHeight = startHeight;
    const move = (moveEvent) => {
      const nextHeight = clampActivityPanelHeight(startHeight + (startY - moveEvent.clientY), viewportHeight);
      latestHeight = nextHeight;
      if (panelEl) {
        panelEl.style.setProperty('--activity-panel-height', `${nextHeight}px`);
      }
    };
    const stop = () => {
      window.removeEventListener(moveEventName, move);
      window.removeEventListener(stopEventName, stop);
      if (inputType === 'pointer') {
        window.removeEventListener('pointercancel', stop);
      }
      setActivityPanelHeight(latestHeight);
    };
    window.addEventListener(moveEventName, move);
    window.addEventListener(stopEventName, stop);
    if (inputType === 'pointer') {
      window.addEventListener('pointercancel', stop);
    }
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

function RuntimePanel({ diffText, tokenUsage, activityStats, warnings, runtimeResults, projectPath, projects }) {
  const [collapsedDiffFiles, setCollapsedDiffFiles] = useState(() => new Set());
  const [diffActionNotice, setDiffActionNotice] = useState('');
  const [codePreview, setCodePreview] = useState(emptyCodePreviewState);
  const [pathChoice, setPathChoice] = useState(emptyPathChoiceState);
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
  const locateDiffFile = async (file) => {
    setDiffActionNotice(`正在定位 ${file.filename}`);
    try {
      const result = await locateCodeFile(runtimeCodeScopePayload(file.filename, projectPath, projects));
      const options = normalizeCodeLocateOptions(result);
      const count = options.length;
      if (count > 1) {
        setPathChoice({ open: true, file, options, truncated: Boolean(result?.truncated) });
      }
      setDiffActionNotice(`定位到 ${count} 个路径`);
    } catch (error) {
      setDiffActionNotice(codeActionError(error, '定位失败'));
    }
  };
  const openCodePreviewForPath = async (filePath, fallbackRelative = '') => {
    const displayPath = (fallbackRelative || filePath || '').toString();
    setCodePreview({
      ...emptyCodePreviewState(),
      open: true,
      loading: true,
      filePath,
      relative: displayPath,
    });
    try {
      const result = await openCodeFile(runtimeCodeScopePayload(filePath, projectPath, projects));
      setCodePreview(codePreviewStateFromOpenResult(result, filePath, displayPath));
    } catch (error) {
      setCodePreview((current) => ({
        ...current,
        loading: false,
        error: codeActionError(error, '打开失败'),
      }));
    }
  };
  const openDiffFile = async (file) => {
    await openCodePreviewForPath(file.filename, file.filename);
  };
  const openChosenPath = async (filePath) => {
    const fallback = pathChoice.file?.filename || filePath;
    setPathChoice(emptyPathChoiceState());
    await openCodePreviewForPath(filePath, fallback);
  };
  const savePreviewChanges = async () => {
    if (!codePreview.filePath || codePreview.saving) return;
    setCodePreview((current) => ({ ...current, saving: true, error: '', status: '' }));
    try {
      const result = await saveCodeFile({
        ...runtimeCodeScopePayload(codePreview.filePath, projectPath, projects),
        content: codePreview.draft,
      });
      const relative = codeOpenDisplayPath(result, codePreview.relative || codePreview.filePath);
      setCodePreview((current) => ({
        ...current,
        saving: false,
        filePath: (result?.filePath || current.filePath).toString(),
        relative,
        content: current.draft,
        editing: current.previewKind === 'markdown' ? false : current.editing,
        totalLines: Number.isFinite(Number(result?.totalLines)) ? Math.floor(Number(result.totalLines)) : countCodePreviewLines(current.draft),
        status: `已保存 ${relative}`,
      }));
    } catch (error) {
      setCodePreview((current) => ({
        ...current,
        saving: false,
        error: codeActionError(error, '保存失败'),
      }));
    }
  };
  return (
    <aside
      className="runtime-panel"
      data-testid="runtime-panel"
      style={runtimePanelHeightVars(runtimeLayout.activityPanelHeight, runtimeLayout.viewportHeight)}
    >
      <RuntimeToolbar diffSummary={diffSummary} />
      <RuntimeDiffView
        diffText={diffText}
        diffSummary={diffSummary}
        collapsedFiles={collapsedDiffFiles}
        actionNotice={diffActionNotice}
        onLocateFile={locateDiffFile}
        onOpenFile={openDiffFile}
        parseLineEntries={parseUnifiedDiffLineEntries}
        onToggleFile={toggleDiffFile}
      />
      <RuntimeActivityPanel
        activityStats={activityStats}
        tokenUsage={tokenUsage}
        warnings={warnings}
        runtimeResults={runtimeResults}
        activityPanelMax={runtimeLayout.activityPanelMax}
        activityPanelHeight={runtimeLayout.activityPanelHeight}
        activityPanelMinHeight={ACTIVITY_PANEL_MIN_HEIGHT}
        formatTime={formatTime}
        onResizeKeyDown={runtimeLayout.handleActivityPanelResizeKeyDown}
        onResizeStart={runtimeLayout.beginActivityPanelResize}
      />
      {codePreview.open ? (
        <CodePreviewDialog
          preview={codePreview}
          onBeginEdit={() => setCodePreview((current) => ({ ...current, editing: true, error: '', status: '' }))}
          onCancelEdit={() => setCodePreview((current) => ({ ...current, editing: false, draft: current.content, error: '', status: '' }))}
          onChangeDraft={(draft) => setCodePreview((current) => ({ ...current, draft, error: '' }))}
          onClose={() => setCodePreview(emptyCodePreviewState())}
          onDirtyClose={() => setCodePreview((current) => ({ ...current, error: '请先保存或放弃预览更改' }))}
          onSave={savePreviewChanges}
        />
      ) : null}
      {pathChoice.open ? (
        <PathChoiceDialog
          choice={pathChoice}
          onClose={() => setPathChoice(emptyPathChoiceState())}
          onSelect={(filePath) => { void openChosenPath(filePath); }}
        />
      ) : null}
    </aside>
  );
}

function PathChoiceDialog({ choice, onClose, onSelect }) {
  const options = Array.isArray(choice?.options) ? choice.options : [];
  return (
    <FocusTrapDialog ariaLabel="选择文件路径" className="modal-box path-choice-modal" onClose={onClose}>
      <header>
        <div>
          <h2>选择文件路径</h2>
          <p>{choice?.file?.filename || '请选择要打开的文件'}</p>
        </div>
        <button type="button" aria-label="关闭路径选择" title="关闭路径选择" onClick={onClose}>
          <X size={15} aria-hidden="true" />
        </button>
      </header>
      <div className="path-choice-options">
        {options.length > 0 ? options.map((path) => (
          <button className="path-choice-option" key={path} type="button" onClick={() => onSelect(path)}>
            {path}
          </button>
        )) : <p>没有可选路径</p>}
      </div>
      {choice?.truncated ? <p className="path-choice-truncated">结果已截断，仅显示部分结果</p> : null}
      <footer>
        <button type="button" onClick={onClose}>取消</button>
      </footer>
    </FocusTrapDialog>
  );
}

function CodePreviewDialog({ preview, onBeginEdit, onCancelEdit, onChangeDraft, onClose, onDirtyClose, onSave }) {
  const dirty = preview.draft !== preview.content;
  const canEdit = Boolean(preview.editable) && !preview.image && !preview.loading;
  const requestClose = () => {
    if (dirty && !preview.loading && !preview.saving) {
      onDirtyClose();
      return;
    }
    onClose();
  };
  return (
    <FocusTrapDialog ariaLabel="文件预览" className="modal-box code-preview-modal" initialFocusSelector={preview.editing && !preview.image ? 'textarea' : ''} onClose={requestClose}>
      <header>
        <div>
          <h2>文件预览</h2>
          <p className="code-preview-path">{preview.relative || preview.filePath}</p>
          {codePreviewMeta(preview) ? <p className="code-preview-meta">{codePreviewMeta(preview)}</p> : null}
        </div>
        <button type="button" aria-label="关闭文件预览" title="关闭文件预览" onClick={requestClose}>
          <X size={15} aria-hidden="true" />
        </button>
      </header>
      {preview.loading ? (
        <div className="code-preview-loading">正在打开文件</div>
      ) : preview.image ? (
        <figure className="code-preview-image">
          <img src={preview.imageSrc || preview.imageFullSrc} alt={preview.relative || preview.filePath || '图片预览'} />
        </figure>
      ) : preview.editing ? (
        <>
          <textarea
            aria-label="文件预览内容"
            className="code-preview-editor"
            spellCheck="false"
            value={preview.draft}
            onChange={(event) => onChangeDraft(event.target.value)}
          />
          {preview.previewKind === 'markdown' ? <p className="code-preview-hint">保存后会回到 Markdown 预览。</p> : null}
        </>
      ) : preview.previewKind === 'markdown' ? (
        <div className="code-preview-markdown message-markdown">
          <MarkdownBlocks lines={normalizeMessageText(preview.content).split('\n')} />
        </div>
      ) : (
        <pre className="code-preview-text">{preview.content}</pre>
      )}
      {preview.error ? <p className="code-preview-error" role="alert">{preview.error}</p> : null}
      {preview.status ? <output className="code-preview-status">{preview.status}</output> : null}
      <footer>
        <button type="button" onClick={requestClose}>关闭</button>
        {canEdit && preview.previewKind === 'markdown' && !preview.editing ? (
          <button type="button" onClick={onBeginEdit}>编辑预览</button>
        ) : null}
        {canEdit && preview.editing && preview.previewKind === 'markdown' ? (
          <button type="button" disabled={preview.saving} onClick={onCancelEdit}>放弃更改</button>
        ) : null}
        {canEdit && preview.editing ? (
          <button type="button" disabled={preview.loading || preview.saving || !dirty} onClick={onSave}>
            {preview.saving ? '保存中' : '保存预览更改'}
          </button>
        ) : null}
      </footer>
    </FocusTrapDialog>
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

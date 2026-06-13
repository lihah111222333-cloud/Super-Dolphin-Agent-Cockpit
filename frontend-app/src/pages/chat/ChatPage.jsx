import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Brain, CheckCircle2, ChevronDown, CircleStop, Copy, File, Filter, GitBranch, List, MoreHorizontal, PanelRight, PanelTopOpen, RefreshCw, Sparkles, Terminal, Wrench, X } from 'lucide-react';
import { textValue } from '../shared/pageShared.js';
import {
  codeOpenDisplayPath,
  codePreviewStateFromOpenResult,
  countCodePreviewLines,
  emptyCodePreviewState,
} from './adapters/codePreviewAdapter.js';
import {
  codeActionError,
  emptyPathChoiceState,
  fileRefPosition,
  normalizeCodeLocateOptions,
  runtimeCodeScopePayload,
} from './adapters/runtimeCodeAdapter.js';
import {
  activeThreadForStore,
  activeThreadIdentifiers,
  displayThreadName,
  normalizedThreadIdentity,
  threadScopedBooleanValue,
  threadScopedMapValue,
  workStatusForThread,
} from './adapters/threadStateAdapter.js';
import { ComposerDock } from './components/ComposerDock.jsx';
import { composerAttachmentKey } from './components/composerAttachmentKey.js';
import { CodePreviewDialog } from './components/CodePreviewDialog.jsx';
import { PathChoiceDialog } from './components/PathChoiceDialog.jsx';
import { RuntimePanel } from './components/RuntimePanel.jsx';
import { ThreadRail } from './components/ThreadRail.jsx';
import { ProjectSelector } from './components/ProjectSelector.jsx';
import { runUIAction } from './components/chatUiActions.js';
import {
  canUseProjectActionsForStore,
  runtimeProjectPath,
} from './components/projectSelectorModel.js';
import { useTimelineMaterialization } from './hooks/useTimelineMaterialization.js';
import { onFilesDropped, copyTextToClipboard, locateCodeFile, openCodeFile, saveCodeFile } from './services/chatCodeService.js';

const CONVERSATION_DROP_TARGET_ID = 'conversation-drop-zone';
const runtimeCodeActions = Object.freeze({ locateCodeFile, openCodeFile, saveCodeFile });
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
          renderMarkdownPreview={renderCodePreviewMarkdown}
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

function shouldIgnoreGlobalEscape(target) {
  const element = target instanceof Element ? target : null;
  if (!element) return false;
  const tagName = element.tagName.toLowerCase();
  if (['input', 'textarea', 'select', 'option'].includes(tagName)) return true;
  if (element.isContentEditable) return true;
  return Boolean(element.closest('dialog, [role="dialog"], [role="menu"], [role="listbox"], [data-escape-scope="local"]'));
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
        ? `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightWidth}px`
        : 'minmax(0, 1fr)';
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

function beginRightPanelDrag({ event, layoutRef, maxWidth, setOpen, store, width }) {
  event.preventDefault();
  event.currentTarget?.setPointerCapture?.(event.pointerId);
  const drag = rightPanelDragState({ event, layoutRef, maxWidth, setOpen, store, width });
  window.addEventListener('pointermove', drag.move);
  window.addEventListener('pointerup', drag.finish);
  window.addEventListener('pointercancel', drag.finish);
  window.addEventListener('blur', drag.finish);
}

function rightPanelDragState({ event, layoutRef, maxWidth, setOpen, store, width }) {
  const startX = event.clientX;
  const startWidth = width;
  const layoutColumnsForWidth = (nextWidth) => `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${nextWidth}px`;
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

function chatHeaderFeedbackForStore(store) {
  const bootstrapFailureMessage = store?.bootstrapStatus === 'failed' && textValue(store?.error)
    ? `连接后端失败：${textValue(store?.error)}`
    : '';
  if (store?.actionNotice?.message) return store.actionNotice;
  return bootstrapFailureMessage ? { message: bootstrapFailureMessage, tone: 'error' } : null;
}

function ChatPage({ store, projectPath, rightPanelOpen = false, setRightPanelOpen = () => {} }) {
  const activeThreadId = store.activeThreadId;
  const modelThreadId = composerConfigThreadId(store, activeThreadId);
  const threadData = useChatThreadData(store, activeThreadId);
  const introMode = !activeThreadId && !threadData.timelineBlocked && threadData.messages.length === 0;
  const headerFeedback = chatHeaderFeedbackForStore(store);
  const showHeader = !introMode || headerFeedback?.tone === 'error';
  const showIntroFeedback = introMode && !showHeader && Boolean(headerFeedback?.message);
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
    ? `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightPanelWidth}px`
    : 'minmax(0, 1fr)';

  return (
    <section className={`chat-page${introMode ? ' chat-page--intro' : ''}`} data-testid="chat-page">
      {showHeader ? (
        <ChatPageHeader store={store} projectPath={projectPath} rightPanelOpen={rightPanelOpen} setRightPanelOpen={setRightPanelOpen} />
      ) : null}
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
          codeFileActions={runtimeCodeActions}
          handleKeyDown={handleRuntimeResizeKeyDown}
          maxWidth={runtimeMaxWidth}
          open={rightPanelOpen}
          projectPath={runtimeProject}
          projects={store.projects}
          threadData={threadData}
          width={rightPanelWidth}
        />
      </div>
      {showIntroFeedback ? (
        <output className="sr-only" data-testid="chat-action-feedback">
          {headerFeedback.message}
        </output>
      ) : null}
      {codePreview.dialogs}
    </section>
  );
}

function ChatPageHeader({ store, projectPath, rightPanelOpen, setRightPanelOpen }) {
  const [actionsOpen, setActionsOpen] = useState(false);
  const actionsButtonRef = useRef(null);
  const actionsMenuRef = useRef(null);
  const canUseThreadActions = Boolean(store?.hasActiveThreadActions?.());
  const canInterruptThread = Boolean(store?.hasInterruptibleThreadAction?.());
  const feedback = chatHeaderFeedbackForStore(store);
  const activeThread = activeThreadForStore(store);
  const title = store?.activeThreadId && activeThread ? displayThreadName(activeThread) : '聊天页面';
  useEffect(() => {
    if (!actionsOpen) return undefined;
    const closeOnPointerDown = (event) => {
      if (actionsMenuRef.current?.contains(event.target)) return;
      if (actionsButtonRef.current?.contains(event.target)) return;
      setActionsOpen(false);
    };
    const closeOnEscape = (event) => {
      if (event.key !== 'Escape') return;
      setActionsOpen(false);
      actionsButtonRef.current?.focus?.();
    };
    window.addEventListener('pointerdown', closeOnPointerDown);
    window.addEventListener('keydown', closeOnEscape);
    return () => {
      window.removeEventListener('pointerdown', closeOnPointerDown);
      window.removeEventListener('keydown', closeOnEscape);
    };
  }, [actionsOpen]);
  const runMenuAction = useCallback((action, { close = true } = {}) => {
    if (close) setActionsOpen(false);
    runUIAction(action);
  }, []);
  return (
    <header className="chat-page-header">
      <div className="chat-page-title">
        <h1>{title}</h1>
        <button
          ref={actionsButtonRef}
          type="button"
          className={`chat-more-button ${actionsOpen ? 'active' : ''}`}
          aria-label="聊天操作"
          title="聊天操作"
          aria-haspopup="menu"
          aria-expanded={actionsOpen}
          onClick={() => setActionsOpen((current) => !current)}
        >
          <MoreHorizontal size={24} aria-hidden="true" />
        </button>
      </div>
      {actionsOpen ? (
        <ChatActionsMenu
          canInterruptThread={canInterruptThread}
          canUseThreadActions={canUseThreadActions}
          menuRef={actionsMenuRef}
          projectPath={projectPath}
          rightPanelOpen={rightPanelOpen}
          runMenuAction={runMenuAction}
          setRightPanelOpen={setRightPanelOpen}
          store={store}
        />
      ) : null}
      <div className="chat-header-tools" aria-label="聊天视图工具">
        <button type="button" className="chat-header-tool" aria-label="筛选消息" title="筛选消息" disabled>
          <Filter size={22} aria-hidden="true" />
        </button>
        <button type="button" className="chat-header-tool" aria-label="消息列表" title="消息列表" disabled>
          <List size={22} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="chat-header-tool"
          aria-label="布局视图"
          title={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
          aria-pressed={rightPanelOpen}
          onClick={() => setRightPanelOpen?.((prev) => !prev)}
        >
          <PanelRight size={22} aria-hidden="true" />
        </button>
      </div>
      <button
        type="button"
        className="chat-sidepanel-shortcut"
        aria-label={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        title={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        aria-pressed={rightPanelOpen}
        onClick={() => setRightPanelOpen?.((prev) => !prev)}
      />
      <div className="chat-legacy-actions" aria-label="聊天操作">
        <button
          type="button"
          className="icon-btn"
          aria-label="新窗口（独立进程）"
          title="新窗口（独立进程）"
          onClick={() => runUIAction(() => store.openNewWindow?.())}
        >
          <PanelTopOpen size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canUseThreadActions ? '复制当前线程' : '复制当前线程（不可用）'}
          title={canUseThreadActions ? '复制当前线程' : '请先选择会话'}
          disabled={!canUseThreadActions}
          onClick={() => runUIAction(() => store.copyActiveThreadInfo?.())}
        >
          <Copy size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canInterruptThread ? '停止' : '停止（不可用）'}
          title={canInterruptThread ? '中断当前执行' : '无运行中任务'}
          disabled={!canInterruptThread}
          onClick={() => runUIAction(() => store.interruptActiveThread?.())}
        >
          <CircleStop size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canUseThreadActions ? '强制完成' : '强制完成（不可用）'}
          title={canUseThreadActions ? '强制完成当前执行' : '请先选择会话'}
          disabled={!canUseThreadActions}
          onClick={() => runUIAction(() => store.forceCompleteActiveThread?.())}
        >
          <CheckCircle2 size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canUseThreadActions ? '进程恢复' : '请先选择会话'}
          title={canUseThreadActions ? '手动杀进程并恢复连接' : '请先选择会话'}
          disabled={!canUseThreadActions}
          onClick={() => runUIAction(() => store.recoverActiveThread?.())}
        >
          <RefreshCw size={14} />
        </button>
      </div>
      {feedback?.message ? (
        <output className={`action-feedback ${feedback.tone || 'info'}`} data-testid="chat-action-feedback">
          {feedback.message}
        </output>
      ) : null}
    </header>
  );
}

function ChatActionsMenu({
  canInterruptThread,
  canUseThreadActions,
  menuRef,
  projectPath,
  rightPanelOpen,
  runMenuAction,
  setRightPanelOpen,
  store,
}) {
  const toggleRuntimePanel = () => setRightPanelOpen?.((prev) => !prev);
  return (
    <div ref={menuRef} className="chat-actions-menu" data-testid="chat-actions-menu" role="menu" aria-label="聊天操作">
      {store?.activeThreadId ? (
        <div className="chat-actions-project">
          <ProjectSelector store={store} projectPath={projectPath} />
        </div>
      ) : null}
      <ChatActionMenuButton
        icon={PanelTopOpen}
        label="新窗口（独立进程）"
        onClick={() => runMenuAction(() => store.openNewWindow?.())}
      />
      <ChatActionMenuButton
        icon={Copy}
        label={canUseThreadActions ? '复制当前线程' : '复制当前线程（不可用）'}
        disabled={!canUseThreadActions}
        onClick={() => runMenuAction(() => store.copyActiveThreadInfo?.())}
      />
      <ChatActionMenuButton
        icon={GitBranch}
        label={canUseThreadActions ? '继承当前对话' : '继承当前对话（不可用）'}
        disabled={!canUseThreadActions}
        onClick={() => runMenuAction(() => store.openForkDraft?.())}
      />
      <ChatActionMenuButton
        icon={CircleStop}
        label={canInterruptThread ? '停止' : '停止（不可用）'}
        disabled={!canInterruptThread}
        onClick={() => runMenuAction(() => store.interruptActiveThread?.())}
      />
      <ChatActionMenuButton
        icon={CheckCircle2}
        label={canUseThreadActions ? '强制完成' : '强制完成（不可用）'}
        disabled={!canUseThreadActions}
        onClick={() => runMenuAction(() => store.forceCompleteActiveThread?.())}
      />
      <ChatActionMenuButton
        icon={RefreshCw}
        label={canUseThreadActions ? '进程恢复' : '请先选择会话'}
        disabled={!canUseThreadActions}
        onClick={() => runMenuAction(() => store.recoverActiveThread?.())}
      />
      <ChatActionMenuButton
        icon={PanelTopOpen}
        label={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        onClick={() => runMenuAction(toggleRuntimePanel)}
      />
    </div>
  );
}

function ChatActionMenuButton({ disabled = false, icon: Icon, label, onClick }) {
  return (
    <button type="button" className="chat-action-menu-item" disabled={disabled} onClick={onClick}>
      <Icon size={16} aria-hidden="true" />
      <span>{label}</span>
    </button>
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

function renderCodePreviewMarkdown(content) {
  return <MarkdownBlocks lines={normalizeMessageText(content).split('\n')} />;
}

function RuntimePanelSlot({ beginResize, codeFileActions, handleKeyDown, maxWidth, open, projectPath, projects, threadData, width }) {
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
        codeFileActions={codeFileActions}
        formatTime={formatTime}
        renderMarkdownPreview={renderCodePreviewMarkdown}
      />
    </>
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
  projectPath,
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
      projectPath={projectPath}
      modelThreadId={modelThreadId}
      showProviderToggle={showProviderToggle}
      showProjectSelector={false}
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

function IntroChatStage({ composer, projectPath: _projectPath }) {
  return (
    <div className="intro-chat-stage">
      <div className="empty-chat">
        <h2>我们应该在 Super-Dolphin 中构建什么？</h2>
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

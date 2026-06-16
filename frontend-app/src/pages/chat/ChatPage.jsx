import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ChevronDown } from 'lucide-react';
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
  workStatusForThread,
} from './adapters/threadStateAdapter.js';
import { ComposerDock } from './components/ComposerDock.jsx';
import { CodePreviewDialog } from './components/CodePreviewDialog.jsx';
import { ChatPageHeader } from './components/ChatPageHeader.jsx';
import { CodePreviewMarkdown } from './components/MarkdownMessage.jsx';
import { chatHeaderFeedbackForStore } from './components/chatHeaderModel.js';
import { isApprovalMessage } from './components/chatApprovalModel.js';
import { isReasoningMessage, syntheticReasoningMessage } from './components/chatReasoningModel.js';
import { PathChoiceDialog } from './components/PathChoiceDialog.jsx';
import { RuntimePanelSlot } from './components/RuntimePanelSlot.jsx';
import { TimelineLoadingPlaceholder, TimelineMessage } from './components/TimelineMessage.jsx';
import { ThreadRail } from './components/ThreadRail.jsx';
import { runUIAction } from './components/chatUiActions.js';
import {
  canUseProjectActionsForStore,
  runtimeProjectPath,
} from './components/projectSelectorModel.js';
import { CONVERSATION_DROP_TARGET_ID, useComposerInteractions } from './hooks/useComposerInteractions.js';
import {
  RIGHT_PANEL_CLOSE_THRESHOLD,
  SPLITTER_WIDTH,
  THREAD_RAIL_MIN_WIDTH,
  useRuntimeSidePanelLayout,
  useThreadRailLayout,
  useViewportWidth,
} from './hooks/useChatWorkbenchLayout.js';
import { useChatThreadData } from './hooks/useChatThreadData.js';
import {
  TIMELINE_SCROLL_LOAD_THRESHOLD,
  isTimelineNearBottom,
  requestTimelineBottomScroll,
  scrollTimelineElementToBottom,
} from './hooks/timelineScroll.js';
import { useTimelineMaterialization } from './hooks/useTimelineMaterialization.js';
import { locateCodeFile, openCodeFile, openPath, saveCodeFile } from './services/chatCodeService.js';
import './ChatTimeline.css';
import './ChatMessages.css';
import './ChatReasoning.css';
import './ChatPage.css';

const runtimeCodeActions = Object.freeze({ locateCodeFile, openCodeFile, saveCodeFile });

const CONTEXT_USAGE_FORK_THRESHOLD = 90;

function renderCodePreviewMarkdown(content) {
  return <CodePreviewMarkdown content={content} />;
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

  const openLocalPath = useCallback(async (payload = {}) => {
    const filePath = (payload.path || payload.filePath || '').toString().trim();
    if (!filePath) return;
    const position = fileRefPosition(payload);
    try {
      await openPath(runtimeCodeScopePayload(filePath, projectPath, projects, position));
    } catch (error) {
      setCodePreview({
        ...emptyCodePreviewState(),
        open: true,
        loading: false,
        filePath,
        relative: filePath,
        error: codeActionError(error, '鎵撳紑澶辫触'),
      });
    }
  }, [projectPath, projects]);

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

  return { dialogs, openFileRef, openLocalPath };
}

function shouldIgnoreGlobalEscape(target) {
  const element = target instanceof Element ? target : null;
  if (!element) return false;
  const tagName = element.tagName.toLowerCase();
  if (['input', 'textarea', 'select', 'option'].includes(tagName)) return true;
  if (element.isContentEditable) return true;
  return Boolean(element.closest('dialog, [role="dialog"], [role="menu"], [role="listbox"], [data-escape-scope="local"]'));
}

function timelineItemTextValue(item = {}) {
  return (item.text || item.content || item.message || item.output || item.result || item.error || '').toString().trim();
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
    onOpenPath: codePreview.openLocalPath,
    onCitation: (payload) => handleTimelineCitationAction(payload, { store, openFileRef: codePreview.openFileRef }),
    onApproval: (message, approved) => store.respondApproval?.(message, approved),
  }), [codePreview.openFileRef, codePreview.openLocalPath, store]);
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
          closeThreshold={RIGHT_PANEL_CLOSE_THRESHOLD}
          formatTime={formatTime}
          handleKeyDown={handleRuntimeResizeKeyDown}
          maxWidth={runtimeMaxWidth}
          open={rightPanelOpen}
          projectPath={runtimeProject}
          projects={store.projects}
          renderMarkdownPreview={renderCodePreviewMarkdown}
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
  /*
   * Timeline 只负责显示当前窗口和触发“加载更早”。
   * 更早的后端消息先写入 store，再从 messages 回到这里。
   */
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
            <TimelineMessage key={key} message={message} actions={messageActions} activeThreadId={activeThreadId} smoothStreaming={smoothStreaming} onScrollIfSticky={onScrollIfSticky} formatTime={formatTime} />
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

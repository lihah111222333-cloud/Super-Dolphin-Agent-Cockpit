import { useEffect, useMemo, useRef, useState } from 'react';
import { Bot, Code2, FileText, Sailboat, Sparkles } from 'lucide-react';
import {
  activeThreadForStore,
} from './adapters/threadStateAdapter.js';
import { ChatPageHeader } from './components/ChatPageHeader.jsx';
import { ChatActionFeedback } from './components/ChatActionFeedback.js';
import { CodePreviewMarkdown } from './markdown/MarkdownMessage.jsx';
import { chatHeaderFeedbackForStore } from './model/chatHeaderModel.js';
import { RuntimePanelSlot } from './runtime/RuntimePanelSlot.jsx';
import { ThreadRail } from './thread/ThreadRail.jsx';
import { Conversation } from './thread/Conversation.jsx';
import { firstText, firstTrimmedText, timeLabelFromTimestamp, trimmedText } from './markdown/markdownMessageModel.js';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { useShellLayoutStore } from '../../app/shell/model/useShellLayoutStore.js';
import { runUIAction } from './model/chatUiActions.js';
import {
  canUseProjectActionsForStore,
  runtimeProjectPath,
} from './model/projectSelectorModel.js';
import {
  RIGHT_PANEL_CLOSE_THRESHOLD,
  SPLITTER_WIDTH,
  THREAD_RAIL_MIN_WIDTH,
  useRuntimeSidePanelLayout,
  useThreadRailLayout,
  useViewportWidth,
} from './hooks/useChatWorkbenchLayout.js';
import { useChatThreadData } from './hooks/useChatThreadData.js';
import { useCodePreviewController } from './hooks/useCodePreviewController.jsx';
import { locateCodeFile, openCodeFile, saveCodeFile } from './services/chatCodeService.js';
import './ChatTimeline.css';
import './ChatMessages.css';
import './ChatReasoning.css';
import './ChatPage.css';

const runtimeCodeActions = Object.freeze({ locateCodeFile, openCodeFile, saveCodeFile });

function selectRightPanelWidth(state) {
  return state.rightPanelWidth;
}

function selectSetRightPanelWidth(state) {
  return state.setRightPanelWidth;
}

const INTRO_SUGGESTION_DEFINITIONS = Object.freeze([
  { key: 'summarizeDocument', icon: FileText },
  { key: 'codeReview', icon: Code2 },
  { key: 'creativeBrainstorm', icon: Sparkles },
]);

function renderIntroTitle(title) {
  const marker = '燧元';
  const markerIndex = title.indexOf(marker);
  if (markerIndex < 0) return title;

  return (
    <>
      {title.slice(0, markerIndex)}
      <em>{marker}</em>
      {title.slice(markerIndex + marker.length)}
    </>
  );
}

function ChatIntroSpotlight({ copy, onSuggestion }) {
  return (
    <div className="chat-intro-spotlight" aria-labelledby="chat-intro-title" data-testid="chat-intro-spotlight">
      <div className="chat-intro-spotlight__inner">
        <div className="chat-intro-logo-tile" aria-hidden="true">
          <Sailboat className="chat-intro-logo-light" data-testid="chat-intro-light-logo" size={28} strokeWidth={1.8} />
          <Bot className="chat-intro-logo-dark" data-testid="chat-intro-dark-logo" size={28} strokeWidth={1.8} />
        </div>
        <h2 id="chat-intro-title" className="chat-intro-title" aria-label={copy.introTitle}>
          <span aria-hidden="true">{renderIntroTitle(copy.introTitle)}</span>
          <span className="sr-only">{copy.introTitle}</span>
        </h2>
        <p className="chat-intro-subtitle">{copy.introSubtitle}</p>
        <div className="chat-intro-suggestions" data-testid="chat-intro-suggestions">
          {INTRO_SUGGESTION_DEFINITIONS.map(({ key, icon: Icon }) => {
            const suggestion = copy.introSuggestions[key];
            return (
              <button key={key} type="button" className="chat-intro-card" onClick={() => onSuggestion(suggestion.prompt)}>
                <span className="chat-intro-card__icon" aria-hidden="true">
                  <Icon size={17} strokeWidth={1.9} />
                </span>
                <span className="chat-intro-card__title">{suggestion.title}</span>
                <span className="chat-intro-card__description">{suggestion.description}</span>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function renderCodePreviewMarkdown(content) {
  return <CodePreviewMarkdown content={content} />;
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
  const kind = trimmedText(payload.kind);
  const raw = trimmedText(payload.raw);
  if (kind === 'task') return firstTrimmedText(payload.prompt, payload.title, raw);
  if (kind === 'automation-update') {
    const title = trimmedText(payload.title);
    const prompt = trimmedText(payload.prompt);
    const message = firstTrimmedText(payload.message, raw);
    if (title && prompt) return `Automation update (${title}):\n${prompt}`;
    if (prompt) return `Automation update:\n${prompt}`;
    if (message) return `Automation update:\n${message}`;
    return title ? `Automation update (${title})` : '';
  }
  if (kind === 'code-comment') {
    const title = trimmedText(payload.title);
    const message = firstTrimmedText(payload.message, raw);
    const path = trimmedText(payload.path);
    const header = title || path ? `Code comment${path ? ` (${path})` : ''}${title ? `: ${title}` : ''}` : 'Code comment';
    return message ? `${header}\n${message}` : (header === 'Code comment' ? '' : header);
  }
  return '';
}

function appendComposerCitation(store, payload) {
  const nextText = composerTextFromCitation(payload);
  if (!nextText || typeof store?.setDraft !== 'function') return false;
  const current = trimmedText(store.draft);
  store.setDraft(current ? `${current}\n\n${nextText}` : nextText);
  return true;
}

function handleTimelineCitationAction(payload, { store, openFileRef }) {
  const kind = trimmedText(payload?.kind);
  if (!kind) return;
  if (kind === 'conversation') {
    const nextThreadId = trimmedText(payload?.conversationId);
    if (nextThreadId) store?.selectThread?.(nextThreadId);
    return;
  }
  if (kind === 'skill') {
    const path = trimmedText(payload?.path);
    if (path) void openFileRef({ path, line: 1, column: 0, raw: firstText(payload.raw, path) });
    return;
  }
  if (kind === 'image') {
    const path = trimmedText(payload?.path);
    if (path) void openFileRef({ path, line: 1, column: 0, raw: firstText(payload.raw, path) });
    return;
  }
  if (kind === 'code-comment') {
    appendComposerCitation(store, payload);
    const path = trimmedText(payload?.path);
    const line = Number(payload.lineStart);
    if (path) void openFileRef({ path, line: Number.isFinite(line) && line > 0 ? line : 1, column: 0, raw: firstText(payload.raw, path) });
    return;
  }
  appendComposerCitation(store, payload);
}

function useActiveChatThreadSync(store, activeThreadId) {
  const timelineReady = Boolean(activeThreadId && store.threadTimelineReadyByThread?.[activeThreadId]);
  const loading = Boolean(activeThreadId && store.threadStateLoadingByThread?.[activeThreadId]);
  useEffect(() => {
    if (!activeThreadId || timelineReady || loading) return;
    runUIAction('thread.sync', () => store.syncThreadState?.(activeThreadId, {
      includeArchived: true,
      includeDiff: true,
      preserveActiveThreadId: true,
    }));
  }, [activeThreadId, loading, store, timelineReady]);
}

function ChatPage(props) {
  const { copy = APP_COPY.zh.chat, shellLayoutStore, store, projectPath, rightPanelOpen = false, setRightPanelOpen = () => {} } = props;
  const activeThreadId = store.activeThreadId;
  const modelThreadId = composerConfigThreadId(store, activeThreadId);
  const threadData = useChatThreadData(store, activeThreadId);
  const introMode = !activeThreadId && !threadData.timelineBlocked && threadData.messages.length === 0;
  const headerFeedback = chatHeaderFeedbackForStore(store);
  const showHeader = !introMode || headerFeedback?.bootstrapRecovery === true;
  const canUseProjectActions = canUseProjectActionsForStore(store);
  const runtimeProject = runtimeProjectPath(store.activeProject, projectPath);
  const codePreview = useCodePreviewController({ projectPath: runtimeProject, projects: store.projects });
  const [approvalNotice, setApprovalNotice] = useState('');
  const messageActions = useMemo(() => ({
    onFileRef: codePreview.openFileRef,
    onOpenPath: codePreview.openLocalPath,
    onCitation: (payload) => handleTimelineCitationAction(payload, { store, openFileRef: codePreview.openFileRef }),
    onApproval: (message, approved) => store.respondApproval?.(message, approved),
    // 审批失败时由 ChatApprovalMessage 调用，通知 UI 显示错误
    onError: (_event, detail) => { setApprovalNotice(detail || copy.approvalFailed); },
  }), [codePreview.openFileRef, codePreview.openLocalPath, copy.approvalFailed, store]);
  const shellRightPanelWidth = useShellLayoutStore(shellLayoutStore, selectRightPanelWidth);
  const setShellRightPanelWidth = useShellLayoutStore(shellLayoutStore, selectSetRightPanelWidth);
  const viewportWidth = useViewportWidth();
  const chatLayoutRef = useRef(null);
  const rail = useThreadRailLayout({
    viewportWidth,
    rightPanelOpen,
    rightPanelWidth: shellRightPanelWidth,
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
    rightPanelWidth: shellRightPanelWidth,
    setRightPanelWidth: setShellRightPanelWidth,
    store,
    viewportWidth,
    open: rightPanelOpen,
    setOpen: setRightPanelOpen,
    layoutRef: chatLayoutRef,
  });
  useActiveChatThreadSync(store, activeThreadId);
  const layoutColumns = rightPanelOpen
    ? `minmax(0, 1fr) ${SPLITTER_WIDTH}px ${rightPanelWidth}px`
    : 'minmax(0, 1fr)';
  const conversationCopy = useMemo(() => (introMode ? { ...copy, introTitle: '' } : copy), [copy, introMode]);
  const prefillIntroSuggestion = (prompt) => {
    store.setDraft(prompt);
  };

  return (
    <section className={`chat-page${introMode ? ' chat-page--intro' : ''}`} data-testid="chat-page">
      {showHeader ? (
        <ChatPageHeader copy={copy} store={store} projectPath={projectPath} rightPanelOpen={rightPanelOpen} setRightPanelOpen={setRightPanelOpen} />
      ) : null}
      <ChatActionFeedback copy={copy} feedback={headerFeedback} />
      {approvalNotice ? (
        <output className="approval-action-feedback" role="alert" data-testid="approval-action-feedback">
          {approvalNotice}
        </output>
      ) : null}
      <div ref={chatLayoutRef} className="chat-layout" data-testid="chat-layout" style={{ gridTemplateColumns: layoutColumns }}>
        {introMode ? <ChatIntroSpotlight copy={copy} onSuggestion={prefillIntroSuggestion} /> : null}
        <ThreadRail copy={copy} store={store} />
        <ThreadRailResizer copy={copy} rail={rail} />
        <Conversation
          copy={conversationCopy}
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
      {codePreview.dialogs}
    </section>
  );
}

function ThreadRailResizer({ copy = APP_COPY.zh.chat, rail }) {
  return (
    <button
      type="button"
      className="splitter splitter--left"
      role="separator"
      aria-label={copy.resizeRail}
      aria-orientation="vertical"
      aria-valuemin={THREAD_RAIL_MIN_WIDTH}
      aria-valuemax={rail.maxWidth}
      aria-valuenow={rail.width}
      title={copy.resizeRail}
      data-testid="thread-rail-resizer"
      onKeyDown={rail.handleKeyDown}
      onPointerDown={rail.beginResize}
    >
      <span className="sr-only">{copy.resizeRailStatus} {rail.width} {copy.pixels}</span>
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
        runUIAction('settings.provider.toggle', () => store.toggleProviderMode());
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

function formatTime(value) {
  if (!value) return '--:--';
  return firstText(timeLabelFromTimestamp(value), '--:--');
}

export { ChatPage };

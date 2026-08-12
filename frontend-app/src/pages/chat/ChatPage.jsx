import { useEffect, useMemo, useRef, useState } from 'react';
import { Bot, ChevronLeft, ChevronRight, Code2, FileText, Sailboat, Sparkles } from 'lucide-react';
import {
  activeThreadForStore,
} from './adapters/threadStateAdapter.js';
import { ChatPageHeader } from './components/ChatPageHeader.jsx';
import { ChatActionFeedback } from './components/ChatActionFeedback.js';
import { AgentBoardFloating } from './agentBoard/AgentBoardFloating.jsx';
import { AgentBoardPanelSlot } from './agentBoard/AgentBoardPanelSlot.jsx';
import {
  AGENT_BOARD_COMPACT_VIEWPORT_WIDTH,
  useAgentBoardController,
} from './agentBoard/useAgentBoardController.js';
import { CodePreviewMarkdown } from './markdown/MarkdownMessage.jsx';
import { chatHeaderFeedbackForStore } from './model/chatHeaderModel.js';
import { RuntimePanelSlot } from './runtime/RuntimePanelSlot.jsx';
import { ThreadRail } from './thread/ThreadRail.jsx';
import { Conversation } from './thread/Conversation.jsx';
import { firstText, firstTrimmedText, timeLabelFromTimestamp, trimmedText } from './markdown/markdownMessageModel.js';
import { currentTimestampMillis } from '../shared/pageShared.js';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { runUIAction } from './model/chatUiActions.js';
import {
  canUseProjectActionsForStore,
  runtimeProjectPath,
} from './model/projectSelectorModel.js';
import { useRuntimeDiffSync } from './hooks/useChatWorkbenchLayout.js';
import { useChatThreadData } from './hooks/useChatThreadData.js';
import { useCodePreviewController } from './hooks/useCodePreviewController.jsx';
import { locateCodeFile, openCodeFile, saveCodeFile } from './services/chatCodeService.js';
import './ChatTimeline.css';
import './ChatMessages.css';
import './ChatReasoning.css';
import './ChatPage.css';
import './agentBoard/AgentBoard.css';

const RIGHT_PANEL_EXIT_MS = 180;
const CLOSED_RIGHT_PANEL_COLUMNS = 'minmax(0, 1fr) 0px 0px';

function useRightPanelPresence(open) {
  const [mounted, setMounted] = useState(open);
  useEffect(() => {
    if (open) {
      setMounted(true);
      return undefined;
    }
    if (!mounted) return undefined;
    const timer = window.setTimeout(() => setMounted(false), RIGHT_PANEL_EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [mounted, open]);
  return mounted;
}

const runtimeCodeActions = Object.freeze({ locateCodeFile, openCodeFile, saveCodeFile });

const INTRO_SUGGESTION_DEFINITIONS = Object.freeze([
  { key: 'summarizeDocument', icon: FileText },
  { key: 'codeReview', icon: Code2 },
  { key: 'creativeBrainstorm', icon: Sparkles },
]);

function renderIntroTitle(title) {
  const marker = 'Super Dolphin Agent';
  const markerIndex = title.indexOf(marker);
  if (markerIndex < 0) return title;

  return (
    <>
      <span aria-hidden="true">
        {title.slice(0, markerIndex)}
        <em>{marker}</em>
        {title.slice(markerIndex + marker.length)}
      </span>
      <span className="sr-only">{title}</span>
    </>
  );
}

function ChatIntroSpotlight({ copy, onSuggestion }) {
  return (
    <div className="chat-intro-spotlight" aria-labelledby="chat-intro-title" data-testid="chat-intro-spotlight">
      <div className="chat-intro-spotlight__inner">
        <div className="chat-intro-welcome">
          <span className="chat-intro-logo-tile" aria-hidden="true">
            <Sailboat className="chat-intro-logo-light" data-testid="chat-intro-light-logo" size={28} strokeWidth={1.8} />
            <Bot className="chat-intro-logo-dark" data-testid="chat-intro-dark-logo" size={28} strokeWidth={1.8} />
          </span>
          <h2 id="chat-intro-title" className="chat-intro-title">
            {renderIntroTitle(copy.introTitle)}
          </h2>
          <p className="chat-intro-subtitle">{copy.introSubtitle}</p>
        </div>
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

const THREAD_SYNC_MAX_RETRIES = 3;
const THREAD_SYNC_RETRY_BASE_MS = 1000;
const THREAD_SYNC_RETRY_MAX_MS = 5000;

function threadSyncRetryDelayMs(attempt) {
  return Math.min(THREAD_SYNC_RETRY_BASE_MS * 2 ** (attempt - 1), THREAD_SYNC_RETRY_MAX_MS);
}

function threadSyncNowMillis() {
  return currentTimestampMillis('thread 同步退避');
}

// 推进一次同步尝试的重试账本：成功时清除记录并返回 null；
// 失败时累计次数并返回下次重试的等待毫秒数（达到上限返回 null）。
function advanceThreadSyncRetry(activeThreadId, synced, retryRef) {
  if (synced === true) {
    delete retryRef.current[activeThreadId];
    return null;
  }
  const previous = retryRef.current[activeThreadId] || { attempts: 0, cooldownUntil: 0 };
  const nextAttempt = previous.attempts + 1;
  const delayMs = threadSyncRetryDelayMs(nextAttempt);
  retryRef.current[activeThreadId] = { attempts: nextAttempt, cooldownUntil: threadSyncNowMillis() + delayMs };
  return nextAttempt < THREAD_SYNC_MAX_RETRIES ? delayMs : null;
}

function useActiveChatThreadSync(store, activeThreadId) {
  const timelineReady = Boolean(activeThreadId && store.threadTimelineReadyByThread?.[activeThreadId]);
  const loading = Boolean(activeThreadId && store.threadStateLoadingByThread?.[activeThreadId]);
  // 失败退避状态按线程记录；timelineReady 失败后仍为 false，若不限流 effect 会反复触发，
  // 在 session 无法恢复时形成 thread/messages 刷屏风暴（SD-BUG-0298）。
  const threadSyncRetryRef = useRef({});
  useEffect(() => {
    if (!activeThreadId || timelineReady || loading) return undefined;
    const retry = threadSyncRetryRef.current[activeThreadId];
    if (retry && retry.attempts >= THREAD_SYNC_MAX_RETRIES) return undefined;
    const waitMs = retry ? Math.max(0, retry.cooldownUntil - threadSyncNowMillis()) : 0;
    let cancelled = false;
    let retryTimer = 0;
    const attemptSync = () => {
      const result = runUIAction('thread.sync', () => store.syncThreadState?.(activeThreadId, {
        includeArchived: true,
        includeDiff: true,
        preserveActiveThreadId: true,
      }));
      if (!result || typeof result.then !== 'function') return;
      void Promise.resolve(result).then((synced) => {
        if (cancelled) return;
        const delayMs = advanceThreadSyncRetry(activeThreadId, synced, threadSyncRetryRef);
        if (delayMs !== null) retryTimer = window.setTimeout(attemptSync, delayMs);
      });
    };
    if (waitMs > 0) {
      retryTimer = window.setTimeout(attemptSync, waitMs);
    } else {
      attemptSync();
    }
    return () => {
      cancelled = true;
      if (retryTimer) window.clearTimeout(retryTimer);
    };
  }, [activeThreadId, loading, store, timelineReady]);
}

function ChatSidepanelShortcut({ onToggle, open }) {
  return (
    <div className="chat-sidepanel-shortcut-zone">
      <button
        type="button"
        className={`chat-sidepanel-shortcut${open ? ' is-open' : ''}`}
        aria-label={open ? '隐藏侧边栏' : '显示侧边栏'}
        title={open ? '隐藏侧边栏' : '显示侧边栏'}
        aria-pressed={open}
        onClick={() => onToggle((prev) => !prev)}
      >
        {open ? <ChevronRight size={16} aria-hidden="true" /> : <ChevronLeft size={16} aria-hidden="true" />}
      </button>
    </div>
  );
}

function ChatPage(props) {
  const {
    actionsHostedInTopBar = false,
    copy = APP_COPY.zh.chat,
    geometrySnapshot,
    layoutActions,
    store,
    projectPath,
    rightPanelOpen = false,
  } = props;
  const activeThreadId = store.activeThreadId;
  const modelThreadId = composerConfigThreadId(store, activeThreadId);
  const threadData = useChatThreadData(store, activeThreadId);
  const introMode = !activeThreadId && !threadData.timelineBlocked && threadData.messages.length === 0;
  const headerFeedback = chatHeaderFeedbackForStore(store);
  const showHeader = !introMode || headerFeedback?.bootstrapRecovery === true;
  const canUseProjectActions = canUseProjectActionsForStore(store);
  const runtimeProject = runtimeProjectPath(store.activeProject, projectPath);
  const codePreview = useCodePreviewController({ projectPath: runtimeProject, projects: store.projects });
  const messageActions = useMemo(() => ({
    onFileRef: codePreview.openFileRef,
    onOpenPath: codePreview.openLocalPath,
    onCitation: (payload) => handleTimelineCitationAction(payload, { store, openFileRef: codePreview.openFileRef }),
    onApproval: (message, approved) => store.respondApproval?.(message, approved),
  }), [codePreview.openFileRef, codePreview.openLocalPath, store]);
  if (!geometrySnapshot || !layoutActions) {
    throw new Error('ChatPage requires one geometry snapshot and layout actions');
  }
  useRuntimeDiffSync({ activeThreadId, open: rightPanelOpen, store });
  useActiveChatThreadSync(store, activeThreadId);
  const agentBoard = useAgentBoardController({
    store,
    rightPanelOpen,
    setRightPanelOpen: layoutActions.right.setOpen,
  });
  const rightPanelMounted = useRightPanelPresence(rightPanelOpen);
  const agentBoardCompact = geometrySnapshot.viewport.width <= AGENT_BOARD_COMPACT_VIEWPORT_WIDTH;
  const conversationCopy = useMemo(() => (introMode ? { ...copy, introTitle: '' } : copy), [copy, introMode]);
  const prefillIntroSuggestion = (prompt) => {
    store.setDraft(prompt);
  };

  return (
    <section
      className={`chat-page${introMode ? ' chat-page--intro' : ''}`}
      data-testid="chat-page"
      style={{ '--chat-right-offset': geometrySnapshot.cssVars['--composer-right-offset'] }}
    >
      {showHeader ? (
        <ChatPageHeader copy={copy} showActions={!actionsHostedInTopBar} store={store} projectPath={projectPath} />
      ) : null}
      <ChatSidepanelShortcut onToggle={layoutActions.right.setOpen} open={rightPanelOpen} />
      <ChatActionFeedback
        copy={copy}
        feedback={headerFeedback}
        onDismiss={() => store.dismissActionNotice?.(headerFeedback)}
      />
      <div
        className="chat-layout"
        data-testid="chat-layout"
        style={{
          '--composer-right-offset': geometrySnapshot.cssVars['--composer-right-offset'],
          gridTemplateColumns: rightPanelOpen
            ? geometrySnapshot.gridTemplateColumns
            : CLOSED_RIGHT_PANEL_COLUMNS,
        }}
      >
        {introMode ? <ChatIntroSpotlight copy={copy} onSuggestion={prefillIntroSuggestion} /> : null}
        <ThreadRail copy={copy} store={store} />
        <div className="chat-main-column" data-testid="chat-main-column">
          {!rightPanelOpen ? (
            <AgentBoardFloating
              collapsed={agentBoard.floatingCollapsed}
              compact={agentBoardCompact}
              onCollapsedChange={agentBoard.setFloatingCollapsed}
              viewModel={agentBoard.viewModel}
            />
          ) : null}
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
        </div>
        {rightPanelMounted && agentBoard.rightPanelView === 'agents' ? (
          <AgentBoardPanelSlot
            keepMounted={rightPanelMounted}
            panel={{
              formatTime,
              onCollapse: agentBoard.collapse,
              onSelectAgent: agentBoard.selectAgent,
              onShowRuntime: agentBoard.showRuntime,
              viewModel: agentBoard.viewModel,
            }}
            resize={{
              beginResize: layoutActions.right.begin,
              closeThreshold: geometrySnapshot.aria.rightMin,
              handleKeyDown: layoutActions.right.keyDown,
              maxWidth: geometrySnapshot.aria.rightMax,
              open: rightPanelOpen,
              width: geometrySnapshot.aria.rightNow,
            }}
          />
        ) : rightPanelMounted ? (
          <RuntimePanelSlot
            beginResize={layoutActions.right.begin}
            codeFileActions={runtimeCodeActions}
            formatTime={formatTime}
            geometrySnapshot={geometrySnapshot}
            handleKeyDown={layoutActions.right.keyDown}
            layoutActions={layoutActions}
            keepMounted={rightPanelMounted}
            onShowAgents={agentBoard.showAgents}
            open={rightPanelOpen}
            projectPath={runtimeProject}
            projects={store.projects}
            renderMarkdownPreview={renderCodePreviewMarkdown}
            threadData={threadData}
          />
        ) : null}
      </div>
      {codePreview.dialogs}
    </section>
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

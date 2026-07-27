import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { approvalRequestFromMessage, isApprovalMessage } from '../../../features/approval/model/approvalDecision.js';
import { workStatusForThread } from '../adapters/threadStateAdapter.js';
import { ComposerDock } from '../composer/ComposerDock.jsx';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { approvalIdentityKey } from '../../../shared/api/approvalRequestId.js';
import { runUIAction } from '../model/chatUiActions.js';
import { firstText, firstTrimmedText, textValue, timeLabelFromTimestamp, trimmedText } from '../markdown/markdownMessageModel.js';
import { TimelineLoadingPlaceholder, TimelineMessage } from './TimelineMessage.jsx';
import { TurnProcessGroup } from './TurnProcessGroup.jsx';
import { isReasoningMessage, syntheticReasoningMessage } from './chatReasoningModel.js';
import { materializeTurnTimelineEntries } from './chatTurnGroupingModel.js';
import { CONVERSATION_DROP_TARGET_ID, useComposerInteractions } from '../hooks/useComposerInteractions.js';
import { TIMELINE_SCROLL_LOAD_THRESHOLD } from '../hooks/timelineScroll.js';
import { useScrollIntentManager } from '../hooks/useScrollIntentManager.js';
import { useTimelineMaterialization } from '../hooks/useTimelineMaterialization.js';

const CONTEXT_USAGE_FORK_THRESHOLD = 90;

function timelineItemTextValue(item = {}) {
  return firstTrimmedText(item.text, item.content, item.message, item.output, item.result, item.error);
}

function hasAssistantReplyAfterLastUser(messages = []) {
  let lastUserIndex = -1;
  for (let index = 0; index < messages.length; index += 1) {
    if (trimmedText(messages[index]?.role).toLowerCase() === 'user') {
      lastUserIndex = index;
    }
  }
  return messages.some((message, index) => (
    index > lastUserIndex &&
    trimmedText(message?.role).toLowerCase() === 'assistant' &&
    !isReasoningMessage(message) &&
    Boolean(trimmedText(message?.text))
  ));
}

function preserveScrollAfterOlderPage(container, beforeHeight) {
  if (!container || !beforeHeight) return;
  requestAnimationFrame(() => {
    container.scrollTop += container.scrollHeight - beforeHeight;
  });
}

function hasReasoningMessageAfterLastUser(messages = []) {
  let lastUserIndex = -1;
  for (let index = 0; index < messages.length; index += 1) {
    if (trimmedText(messages[index]?.role).toLowerCase() === 'user') {
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
    textValue(message.id),
    firstText(message.role, message.kind),
    textValue(message.status),
    done,
    timelineItemTextValue(message),
  ].map((value) => value.toString()).join('\u0001');
}

function shouldAutoScrollForTimelineMessage(message) {
  if (!message) return false;
  const role = trimmedText(message.role).toLowerCase();
  return role === 'assistant' || isReasoningMessage(message) || isApprovalMessage(message);
}

function timelineAutoScrollKey({ activeThreadId, introMode, messages, pendingReasoning, timelineContentBlocked }) {
  if (introMode || timelineContentBlocked) return '';
  const lastMessage = messages[messages.length - 1] ?? null;
  if (!shouldAutoScrollForTimelineMessage(lastMessage) && !shouldAutoScrollForTimelineMessage(pendingReasoning)) return '';
  return [
    textValue(activeThreadId),
    shouldAutoScrollForTimelineMessage(lastMessage) ? timelineMessageAutoScrollKey(lastMessage) : '',
    timelineMessageAutoScrollKey(pendingReasoning),
  ].join('\u0002');
}

function conversationIsBusy({ activeThread, activeThreadId, sending, statusEntry, timelineContentBlocked }) {
  return workStatusForThread({
    sending,
    loading: timelineContentBlocked,
    activeThreadId,
    activeThread,
    statusEntry,
  }).busy;
}

function approvalSnapshotFromMessages(messages = []) {
  const knownIdentityKeys = new Set();
  let pendingRequest = null;
  for (const message of messages) {
    if (!isApprovalMessage(message)) continue;
    const request = approvalRequestFromMessage(message);
    if (!request.displayOnly) knownIdentityKeys.add(approvalIdentityKey(request));
    if (request.status === 'pending') pendingRequest = request;
  }
  return { knownIdentityKeys, pendingRequest };
}

function hasNewApprovalIdentity(previousKeys, currentKeys) {
  for (const identityKey of currentKeys) {
    if (!previousKeys.has(identityKey)) return true;
  }
  return false;
}

function useApprovalComposerFocus({ activeThreadId, composerInputRef, snapshot }) {
  const previousPendingRef = useRef(null);
  useLayoutEffect(() => {
    const previous = previousPendingRef.current;
    const currentPending = snapshot.pendingRequest;
    const currentIdentityKey = currentPending ? approvalIdentityKey(currentPending) : '';
    const node = composerInputRef.current;
    if (
      previous &&
      !currentPending &&
      previous.threadId === activeThreadId &&
      node &&
      previous.node === node &&
      !hasNewApprovalIdentity(previous.knownIdentityKeys, snapshot.knownIdentityKeys)
    ) {
      composerInputRef.current.focus();
    }
    if (!currentPending) {
      previousPendingRef.current = null;
      return;
    }
    if (
      !previous ||
      previous.threadId !== activeThreadId ||
      previous.identityKey !== currentIdentityKey
    ) {
      previousPendingRef.current = {
        threadId: activeThreadId,
        identityKey: currentIdentityKey,
        node,
        knownIdentityKeys: snapshot.knownIdentityKeys,
      };
    }
  }, [activeThreadId, composerInputRef, snapshot]);
}

function Conversation(props) {
  const {
    copy = APP_COPY.zh.chat,
    messages,
    sending,
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
  const pendingReasoningTimerRef = useRef(null);
  const composerInputRef = useRef(null);
  const [pendingReasoningHint, setPendingReasoningHint] = useState(false);
  if (activeTurn && pendingReasoningHint) {
    setPendingReasoningHint(false);
  }
  const justSent = !activeTurn && pendingReasoningHint;
  const clearPendingReasoningHint = useCallback(() => {
    if (pendingReasoningTimerRef.current !== null) {
      clearTimeout(pendingReasoningTimerRef.current);
      pendingReasoningTimerRef.current = null;
    }
    setPendingReasoningHint(false);
  }, []);
  const startPendingReasoningHint = useCallback(() => {
    if (pendingReasoningTimerRef.current !== null) {
      clearTimeout(pendingReasoningTimerRef.current);
    }
    setPendingReasoningHint(true);
    pendingReasoningTimerRef.current = setTimeout(() => {
      pendingReasoningTimerRef.current = null;
      setPendingReasoningHint(false);
    }, 5000);
  }, []);

  const isBusy = conversationIsBusy({ activeThread, activeThreadId, sending, statusEntry, timelineContentBlocked });
  const introMode = !activeThreadId && !timelineBlocked && messages.length === 0;
  const hasProcessingAfterLastUser = hasReasoningMessageAfterLastUser(messages);
  const lastUserMessage = [...messages].reverse().find((msg) => trimmedText(msg.role).toLowerCase() === 'user');
  const fallbackStartTime = lastUserMessage?.time;
  const pendingReasoning = !introMode && !timelineBlocked && !hasProcessingAfterLastUser && !hasAssistantReplyAfterLastUser(messages)
    ? syntheticReasoningMessage({ activeTurn, sending: sending || justSent, isBusy, fallbackStartTime })
    : null;
  const approvalSnapshot = approvalSnapshotFromMessages(messages);
  const approvalPending = Boolean(approvalSnapshot.pendingRequest);
  const effectiveCanUseProjectActions = canUseProjectActions && !approvalPending;
  useApprovalComposerFocus({ activeThreadId, composerInputRef, snapshot: approvalSnapshot });
  const composerController = useComposerInteractions({
    attachments,
    attachPaths,
    attachDroppedFiles,
    removeAttachment,
    projectActionBlocked: !effectiveCanUseProjectActions,
    canUseProjectActions: effectiveCanUseProjectActions,
  });
  const autoScrollKey = timelineAutoScrollKey({
    activeThreadId,
    introMode,
    messages,
    pendingReasoning,
    timelineContentBlocked,
  });
  const {
    markMessageSent, onTimelineKeyDown,
    onTimelineScroll,
    onTimelineTouchMove, onTimelineTouchStart,
    onTimelineWheel, scrollIfSticky,
    scrollToBottom,
    timelineRef,
  } = useScrollIntentManager({
    activeThreadId,
    autoScrollKey,
    timelineContentBlocked,
  });
  const sendMessageAndScrollToBottom = useCallback(() => {
    const result = sendMessage();
    startPendingReasoningHint();
    Promise.resolve(result).then((sent) => {
      if (sent === false) clearPendingReasoningHint();
    }).catch(() => clearPendingReasoningHint());
    markMessageSent();
    return result;
  }, [clearPendingReasoningHint, markMessageSent, sendMessage, startPendingReasoningHint]);
  const scrollTimelineToBottomSmooth = useCallback(() => {
    scrollToBottom(true);
  }, [scrollToBottom]);
  const composer = (
    <ConversationComposer
      {...props}
      composer={composerController}
      copy={copy}
      floating={introMode}
      inputRef={composerInputRef}
      approvalPending={approvalPending}
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
      onDrop={(event) => { void composerController.handleDrop(event); }}
    >
      <ContextUsageBanner activeThreadId={activeThreadId} store={store} tokenUsage={tokenUsage} />
      <ConversationTimeline
        copy={copy}
        activeCurrentTurn={isBusy || sending || justSent}
        composer={composer}
        smoothStreaming={store?.smoothStreaming ?? false}
        introMode={introMode}
        messages={messages}
        pendingReasoning={pendingReasoning}
        activeThreadId={activeThreadId}
        messagePagination={messagePagination}
        loadOlderThreadMessages={loadOlderThreadMessages}
        timelineContentBlocked={timelineContentBlocked}
        messageActions={messageActions}
        onTimelineKeyDown={onTimelineKeyDown}
        onTimelineScroll={onTimelineScroll}
        onTimelineTouchMove={onTimelineTouchMove}
        onTimelineTouchStart={onTimelineTouchStart}
        onTimelineWheel={onTimelineWheel}
        onScrollToBottom={scrollTimelineToBottomSmooth}
        onScrollIfSticky={scrollIfSticky}
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
          if (canFork) runUIAction('thread.fork.open', () => store.openForkDraft?.({ origin: 'context-usage' }));
        }}
      >
        新建继承会话
      </button>
    </output>
  );
}

function ConversationComposer({
  copy = APP_COPY.zh.chat,
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
  inputRef,
  approvalPending,
}) {
  return (
    <ComposerDock
      floating={floating}
      copy={copy}
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
      showProjectSelector
      composer={composer}
      canUseProjectActions={canUseProjectActions}
      inputRef={inputRef}
      approvalPending={approvalPending}
    />
  );
}

function ConversationTimelineEntry({
  activeThreadId,
  copy,
  entry,
  formatTime: formatTimelineTime,
  messageActions,
  onScrollIfSticky,
  smoothStreaming,
}) {
  if (entry.type === 'process') {
    return (
      <TurnProcessGroup
        active={entry.active}
        messages={entry.messages}
        actions={messageActions}
        activeThreadId={activeThreadId}
        copy={copy}
        smoothStreaming={smoothStreaming}
        onScrollIfSticky={onScrollIfSticky}
        formatTime={formatTimelineTime}
      />
    );
  }
  return (
    <TimelineMessage
      message={entry.message}
      actions={messageActions}
      activeThreadId={activeThreadId}
      copy={copy}
      smoothStreaming={smoothStreaming}
      onScrollIfSticky={onScrollIfSticky}
      formatTime={formatTimelineTime}
    />
  );
}

function ConversationTimeline({
  activeCurrentTurn,
  copy = APP_COPY.zh.chat,
  composer,
  introMode,
  messages,
  pendingReasoning,
  activeThreadId,
  messagePagination,
  loadOlderThreadMessages,
  timelineContentBlocked,
  messageActions,
  onTimelineKeyDown,
  onTimelineScroll,
  onTimelineTouchMove,
  onTimelineTouchStart,
  onTimelineWheel,
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
      preserveScrollAfterOlderPage(container, beforeHeight);
    }
  }, [canLoadBackendOlderPage, hiddenOlderCount, requestBackendOlderPage, revealOlder, timelineContentBlocked, timelineRef]);
  const handleScroll = useCallback((event) => {
    const el = event.currentTarget;
    onTimelineScroll?.(event);
    if (timelineContentBlocked) return;
    if (hiddenOlderCount <= 0 && !hasBackendOlderPage) return;
    if (el.scrollTop <= TIMELINE_SCROLL_LOAD_THRESHOLD) requestOlderMessages();
  }, [hasBackendOlderPage, hiddenOlderCount, onTimelineScroll, requestOlderMessages, timelineContentBlocked]);
  const timelineMessages = [...visibleMessages];
  if (pendingReasoning) {
    timelineMessages.push(pendingReasoning);
  }
  const timelineEntries = materializeTurnTimelineEntries(timelineMessages, { activeCurrentTurn });

  return (
    <div className="timeline-shell">
      <div
        key={activeThreadId || 'intro'}
        className="timeline"
        data-testid="chat-timeline"
        ref={timelineRef}
        role="region"
        aria-label={copy.timelineLabel}
        tabIndex={0}
        onKeyDown={onTimelineKeyDown}
        onScroll={handleScroll}
        onTouchMove={onTimelineTouchMove}
        onTouchStart={onTimelineTouchStart}
        onWheel={onTimelineWheel}
      >
        {introMode ? <IntroChatStage copy={copy} composer={composer} /> : null}
        {!introMode && !timelineContentBlocked && (hiddenOlderCount > 0 || hasBackendOlderPage) ? (
          <TimelineOlderMessagesMarker copy={copy} hiddenCount={hiddenOlderCount} loading={olderPageLoading} onReveal={requestOlderMessages} />
        ) : null}
        {!introMode && !timelineContentBlocked ? timelineEntries.map((entry) => (
          <ConversationTimelineEntry
            key={entry.key}
            entry={entry}
            activeThreadId={activeThreadId}
            copy={copy}
            messageActions={messageActions}
            smoothStreaming={smoothStreaming}
            onScrollIfSticky={onScrollIfSticky}
            formatTime={formatTime}
          />
        )) : null}
        {!introMode && timelineContentBlocked ? <TimelineLoadingPlaceholder /> : null}
        <div style={{ height: 0 }} aria-hidden="true" />
      </div>
      {activeThreadId && !introMode && !timelineContentBlocked ? (
        <button
          type="button"
          className="chat-scroll-bottom-btn"
          title={copy.scrollBottom}
          aria-label={copy.scrollBottom}
          onClick={onScrollToBottom}
        >
          <ChevronDown size={15} aria-hidden="true" />
        </button>
      ) : null}
    </div>
  );
}

function TimelineOlderMessagesMarker({ copy = APP_COPY.zh.chat, hiddenCount, loading, onReveal }) {
  const label = hiddenCount > 0
    ? `${copy.showOlder}（${hiddenCount} 条）`
    : (loading ? copy.loadingOlder : copy.loadOlder);
  return (
    <div className="timeline-placeholder" data-testid="timeline-older-marker">
      <button type="button" className="ghost" disabled={hiddenCount <= 0 && loading} aria-busy={loading ? 'true' : 'false'} onClick={onReveal}>
        {label}
      </button>
    </div>
  );
}

function IntroChatStage({ copy = APP_COPY.zh.chat, composer }) {
  return (
    <div className="intro-chat-stage">
      <div className="empty-chat">
        <h2>{copy.introTitle}</h2>
      </div>
      {composer}
    </div>
  );
}

function formatTime(value) {
  if (!value) return '--:--';
  return firstText(timeLabelFromTimestamp(value), '--:--');
}

export { Conversation, ConversationTimeline };

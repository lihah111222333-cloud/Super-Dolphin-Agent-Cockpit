import { useCallback, useEffect, useRef, useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { workStatusForThread } from '../adapters/threadStateAdapter.js';
import { ComposerDock } from '../composer/ComposerDock.jsx';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { runUIAction } from '../model/chatUiActions.js';
import { firstText, firstTrimmedText, textValue, timeLabelFromTimestamp, trimmedText } from '../markdown/markdownMessageModel.js';
import { TimelineLoadingPlaceholder, TimelineMessage } from './TimelineMessage.jsx';
import { TurnProcessGroup } from './TurnProcessGroup.jsx';
import { isApprovalMessage } from './chatApprovalModel.js';
import { isReasoningMessage, syntheticReasoningMessage } from './chatReasoningModel.js';
import { materializeTurnTimelineEntries } from './chatTurnGroupingModel.js';
import { CONVERSATION_DROP_TARGET_ID, useComposerInteractions } from '../hooks/useComposerInteractions.js';
import { TIMELINE_SCROLL_LOAD_THRESHOLD, isTimelineNearBottom, requestTimelineBottomScroll, scrollTimelineElementToBottom } from '../hooks/timelineScroll.js';
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

function useConversationScrollController({
  activeThreadId,
  autoScrollKey,
  clearPendingReasoningHint,
  sendMessage,
  startPendingReasoningHint,
  timelineContentBlocked,
}) {
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
    startPendingReasoningHint();
    Promise.resolve(result).then((sent) => {
      if (sent === false) clearPendingReasoningHint();
    }).catch(() => clearPendingReasoningHint());
    shouldStickToBottomRef.current = true;
    requestTimelineBottomScroll(scrollTimelineToBottomSmooth);
    return result;
  }, [clearPendingReasoningHint, scrollTimelineToBottomSmooth, sendMessage, startPendingReasoningHint]);

  useEffect(() => {
    timelineContentBlockedRef.current = timelineContentBlocked;
  }, [timelineContentBlocked]);
  useEffect(() => {
    shouldStickToBottomRef.current = true;
    lastTimelineAutoScrollKeyRef.current = '';
    isInitialThreadRenderRef.current = true;
    const el = timelineRef.current;
    if (el) el.scrollTop = 0;
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
    observer.observe(el, { childList: true, subtree: true, characterData: true });
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

  return { sendMessageAndScrollToBottom, scrollTimelineToBottomIfSticky, scrollTimelineToBottomSmooth, timelineRef, updateTimelineStickiness };
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
  const lastUserMessage = [...messages].reverse().find((msg) => trimmedText(msg.role).toLowerCase() === 'user');
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
  const autoScrollKey = timelineAutoScrollKey({
    activeThreadId,
    introMode,
    messages,
    pendingReasoning,
    timelineContentBlocked,
  });
  const {
    sendMessageAndScrollToBottom,
    scrollTimelineToBottomIfSticky,
    scrollTimelineToBottomSmooth,
    timelineRef,
    updateTimelineStickiness,
  } = useConversationScrollController({
    activeThreadId,
    autoScrollKey,
    clearPendingReasoningHint,
    sendMessage,
    startPendingReasoningHint,
    timelineContentBlocked,
  });
  const composer = (
    <ConversationComposer
      {...props}
      composer={composerController}
      copy={copy}
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
      preserveScrollAfterOlderPage(container, beforeHeight);
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
  const timelineEntries = materializeTurnTimelineEntries(timelineMessages, { activeCurrentTurn });

  return (
    <div className="timeline-shell">
      <div key={activeThreadId || 'intro'} className="timeline" data-testid="chat-timeline" ref={timelineRef} onScroll={handleScroll}>
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
        <div ref={bottomRef} style={{ height: 0 }} aria-hidden="true" />
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

export { Conversation };

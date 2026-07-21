import { useCallback, useRef, useState } from 'react';
import { ChevronDown } from 'lucide-react';
import { APP_COPY } from '../../../../shared/i18n/appI18n.js';
import { TIMELINE_SCROLL_LOAD_THRESHOLD } from '../../hooks/timelineScroll.js';
import { useTimelineMaterialization } from '../../hooks/useTimelineMaterialization.js';
import { materializeTurnTimelineEntries } from '../chatTurnGroupingModel.js';
import { TimelineLoadingPlaceholder, TimelineMessage } from '../TimelineMessage.jsx';
import { TurnProcessGroup } from '../TurnProcessGroup.jsx';
import { firstText, timeLabelFromTimestamp } from '../../markdown/markdownMessageModel.js';

function preserveScrollAfterOlderPage(container, beforeHeight) {
  if (!container || !beforeHeight) return;
  requestAnimationFrame(() => {
    container.scrollTop += container.scrollHeight - beforeHeight;
  });
}

async function loadOlderThreadPage({
  activeThreadId,
  loadOlderThreadMessages,
  olderPageRequestingThreadRef,
  setOlderPageRequestingThreadId,
}) {
  try {
    await loadOlderThreadMessages(activeThreadId);
  } finally {
    if (olderPageRequestingThreadRef.current === activeThreadId) {
      olderPageRequestingThreadRef.current = '';
    }
    setOlderPageRequestingThreadId((current) => (current === activeThreadId ? '' : current));
  }
}

function ConversationTimeline({ timeline }) {
  const {
    activeCurrentTurn,
    activeThreadId,
    composer,
    copy = APP_COPY.zh.chat,
    introMode,
    loadOlderThreadMessages,
    messageActions,
    messagePagination,
    messages,
    onScrollToBottom,
    pendingReasoning,
    scroll,
    smoothStreaming,
    timelineContentBlocked,
  } = timeline;
  const {
    onTimelineKeyDown,
    onTimelineTouchMove,
    onTimelineTouchStart,
    onTimelineWheel,
    scrollIfSticky,
    timelineRef,
  } = scroll;
  const { hiddenOlderCount, revealOlder, visibleMessages } = useTimelineMaterialization({
    activeThreadId,
    introMode,
    messages,
    timelineContentBlocked,
  });
  const olderPageRequestingThreadRef = useRef('');
  const [olderPageRequestingThreadId, setOlderPageRequestingThreadId] = useState('');
  const olderPageRequesting = olderPageRequestingThreadId === activeThreadId;
  const olderPageLoading = Boolean(messagePagination?.loading || olderPageRequesting);
  const hasBackendOlderPage = Boolean(activeThreadId && messagePagination?.hasMore && typeof loadOlderThreadMessages === 'function');
  const canLoadBackendOlderPage = hasBackendOlderPage && !olderPageLoading;
  const requestBackendOlderPage = useCallback(() => {
    if (
      !activeThreadId ||
      !messagePagination?.hasMore ||
      messagePagination?.loading ||
      typeof loadOlderThreadMessages !== 'function'
    ) return;
    if (olderPageRequestingThreadRef.current === activeThreadId) return;
    olderPageRequestingThreadRef.current = activeThreadId;
    setOlderPageRequestingThreadId(activeThreadId);
    void loadOlderThreadPage({
      activeThreadId,
      loadOlderThreadMessages,
      olderPageRequestingThreadRef,
      setOlderPageRequestingThreadId,
    });
  }, [
    activeThreadId,
    loadOlderThreadMessages,
    messagePagination?.hasMore,
    messagePagination?.loading,
  ]);
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
  }, [
    canLoadBackendOlderPage,
    hiddenOlderCount,
    requestBackendOlderPage,
    revealOlder,
    timelineRef,
    timelineContentBlocked,
  ]);
  const handleScroll = useCallback((event) => {
    const element = event.currentTarget;
    scroll.onTimelineScroll(event);
    if (timelineContentBlocked || (hiddenOlderCount <= 0 && !hasBackendOlderPage)) {
      return;
    }
    if (element.scrollTop <= TIMELINE_SCROLL_LOAD_THRESHOLD) requestOlderMessages();
  }, [
    hasBackendOlderPage,
    hiddenOlderCount,
    requestOlderMessages,
    scroll,
    timelineContentBlocked,
  ]);
  const timelineMessages = pendingReasoning ? [...visibleMessages, pendingReasoning] : visibleMessages;
  const timelineEntries = materializeTurnTimelineEntries(timelineMessages, { activeCurrentTurn });
  return (
    <div className="timeline-shell">
      <div
        key={activeThreadId || 'intro'}
        className="timeline"
        data-testid="chat-timeline"
        ref={timelineRef}
        tabIndex={0}
        onKeyDown={onTimelineKeyDown}
        onScroll={handleScroll}
        onTouchMove={onTimelineTouchMove}
        onTouchStart={onTimelineTouchStart}
        onWheel={onTimelineWheel}
      >
        {introMode ? <IntroChatStage copy={copy} composer={composer} /> : null}
        {!introMode && !timelineContentBlocked && (hiddenOlderCount > 0 || hasBackendOlderPage) ? (
          <TimelineOlderMessagesMarker
            copy={copy}
            hiddenCount={hiddenOlderCount}
            loading={olderPageLoading}
            onReveal={requestOlderMessages}
          />
        ) : null}
        {!introMode && !timelineContentBlocked ? timelineEntries.map((entry) => (
          <ConversationTimelineEntry
            key={entry.key}
            entry={entry}
            activeThreadId={activeThreadId}
            copy={copy}
            messageActions={messageActions}
            smoothStreaming={smoothStreaming}
            onScrollIfSticky={scrollIfSticky}
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

function ConversationTimelineEntry(props) {
  const {
    activeThreadId,
    copy,
    entry,
    formatTime: formatTimelineTime,
    messageActions,
    onScrollIfSticky,
    smoothStreaming,
  } = props;
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

function TimelineOlderMessagesMarker({ copy = APP_COPY.zh.chat, hiddenCount, loading, onReveal }) {
  const label = hiddenCount > 0
    ? `${copy.showOlder}（${hiddenCount} 条）`
    : (loading ? copy.loadingOlder : copy.loadOlder);
  return (
    <div className="timeline-placeholder" data-testid="timeline-older-marker">
      <button
        type="button"
        className="ghost"
        disabled={hiddenCount <= 0 && loading}
        aria-busy={loading ? 'true' : 'false'}
        onClick={onReveal}
      >
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

export { ConversationTimeline };

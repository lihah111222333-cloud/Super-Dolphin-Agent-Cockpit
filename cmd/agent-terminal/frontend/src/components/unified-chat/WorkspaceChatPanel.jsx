import React from 'react';
import { ChatTimeline } from '../ChatTimeline.jsx';

export function WorkspaceChatPanel({
  splitRatio = 60,
  activePinnedPlan = null,
  noActiveThread = false,
  activeTimeline = [],
  activeStatus = '',
  displayStatusText = '',
  activeStatusMeta = '',
  emptyText = '暂无消息，先发送一句话试试。',
  resolveThreadDisplayName = (value) => value,
  presenceTarget = null,
  selectedThreadId = '',
  isAtBottom = true,
  onDismissPinnedPlan,
  onFileRefClick,
  onCitationClick,
  onScrollToBottom,
  onScrollToTop,
}) {
  return (
    <div id="chat-panel" className="chat-panel-only" style={{ flex: `0 0 ${splitRatio}%` }}>
      {noActiveThread ? (
        <div className="chat-messages-vue">
          <div className="diff-empty" data-testid="chat-empty-state">
            选择或启动一个 Agent 开始对话
          </div>
        </div>
      ) : (
        <ChatTimeline
          key="timeline"
          items={activeTimeline}
          activeStatus={activeStatus}
          activeStatusText={displayStatusText}
          activeStatusMeta={activeStatusMeta}
          emptyText={emptyText}
          pinnedPlanVisible={Boolean(activePinnedPlan)}
          pinnedPlanItemId={activePinnedPlan ? activePinnedPlan.id : null}
          resolveThreadDisplayName={resolveThreadDisplayName}
          presenceTarget={presenceTarget}
          onFileRefClick={onFileRefClick}
          onCitationClick={onCitationClick}
        />
      )}
      {!noActiveThread && (
        <button
          className={`chat-scroll-toggle-btn ${isAtBottom ? 'is-at-bottom' : ''}`}
          title={isAtBottom ? '滚动到顶部' : '滚动到底部'}
          aria-label={isAtBottom ? '滚动到顶部' : '滚动到底部'}
          onClick={isAtBottom ? onScrollToTop : onScrollToBottom}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M18 15l-6-6-6 6"></path>
          </svg>
        </button>
      )}
    </div>
  );
}

WorkspaceChatPanel.components = { ChatTimeline };
WorkspaceChatPanel.emits = ['dismiss-pinned-plan', 'file-ref-click', 'citation-click', 'scroll-to-bottom', 'scroll-to-top'];
WorkspaceChatPanel.template = `
  <div id="chat-panel">
    <div data-testid="chat-empty-state"></div>
    <ChatTimeline :key="'timeline'" :empty-text="emptyText" />
  </div>
`;

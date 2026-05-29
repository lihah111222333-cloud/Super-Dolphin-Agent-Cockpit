import React, { useState } from 'react';
import { parseAgentBadge } from '../../stores/thread-view.model.js';

export function ThreadRailSidePanel({
  showArchivedThreadList = false,
  activeChatThreadCount = 0,
  archivedChatThreadCount = 0,
  visibleChatThreadCards = [],
  threadRailDragging = false,
  threadRailStyle = {},
  editingThreadId = '',
  editingAlias = '',
  renamingThreadId = '',
  setRenameInputRef = null,
  tokenLevelByThreadId = {},
  
  onOpenNewWindow,
  onToggleArchivedThreadList,
  onSelectThread,
  onToggleThreadPin,
  onToggleThreadArchive,
  onBeginInlineRename,
  onSubmitInlineRename,
  onHandleInlineRenameEnter,
  onCancelInlineRename,
  onHandleInlineRenameBlur,
  onUpdateEditingAlias,
  onDeleteStaleThreads,
}) {
  const [confirmCleanMode, setConfirmCleanMode] = useState(false);

  const startClean = () => setConfirmCleanMode(true);
  const cancelClean = () => setConfirmCleanMode(false);
  const doClean = (staleIds) => {
    setConfirmCleanMode(false);
    if (typeof onDeleteStaleThreads === 'function') {
      onDeleteStaleThreads(staleIds);
    }
  };

  const hasStaleCards = visibleChatThreadCards.some(c => c.isStale);

  return (
    <aside
      className={`thread-rail ${threadRailDragging ? 'dragging' : ''}`}
      style={threadRailStyle}
      data-testid="thread-rail"
      aria-label={showArchivedThreadList ? '归档会话列表' : '会话列表'}
    >
      <header className="thread-rail-header">
        <div className="thread-rail-header-main">
          <span
            className="thread-rail-kind-icon"
            role="img"
            aria-label={showArchivedThreadList ? '归档列表' : '会话列表'}
            title={showArchivedThreadList ? '归档列表' : '会话列表'}
          >
            <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M10 3V5"></path>
              <path d="M6.2 5H13.8C15 5 16 6 16 7.2V12.8C16 14 15 15 13.8 15H6.2C5 15 4 14 4 12.8V7.2C4 6 5 5 6.2 5Z"></path>
              <path d="M2.8 8V12"></path>
              <path d="M17.2 8V12"></path>
              <circle cx="8" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
              <circle cx="12" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
            </svg>
          </span>
          <span
            className="thread-rail-count-chip"
            role="img"
            aria-label={showArchivedThreadList ? `${archivedChatThreadCount} 个 Agent` : `${activeChatThreadCount} 个 Agent`}
            title={showArchivedThreadList ? `${archivedChatThreadCount} 个 Agent` : `${activeChatThreadCount} 个 Agent`}
          >
            <svg className="thread-rail-count-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M10 3V5"></path>
              <path d="M6.2 5H13.8C15 5 16 6 16 7.2V12.8C16 14 15 15 13.8 15H6.2C5 15 4 14 4 12.8V7.2C4 6 5 5 6.2 5Z"></path>
              <path d="M2.8 8V12"></path>
              <path d="M17.2 8V12"></path>
              <circle cx="8" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
              <circle cx="12" cy="10" r="0.9" fill="currentColor" stroke="none"></circle>
            </svg>
            <strong>{showArchivedThreadList ? archivedChatThreadCount : activeChatThreadCount}</strong>
          </span>
        </div>
        <button
          type="button"
          className="btn btn-ghost btn-xs thread-rail-new-window-btn"
          data-testid="new-window-btn"
          aria-label="新窗口 (独立进程)"
          title="新窗口 (独立进程)"
          onClick={onOpenNewWindow}
        >
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <line x1="8" y1="3" x2="8" y2="13"></line>
            <line x1="3" y1="8" x2="13" y2="8"></line>
          </svg>
        </button>
        {showArchivedThreadList && !confirmCleanMode && hasStaleCards && (
          <button
            type="button"
            className="btn btn-ghost btn-xs thread-rail-clean-btn"
            data-testid="thread-clean-stale-btn"
            aria-label="清理无用对话"
            title="清理无用对话"
            onClick={startClean}
          >
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M3 4h10"></path>
              <path d="M5 4V3a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v1"></path>
              <path d="M4 4l1 9a1 1 0 0 0 1 1h4a1 1 0 0 0 1-1l1-9"></path>
            </svg>
          </button>
        )}
        {showArchivedThreadList && confirmCleanMode && (
          <button
            type="button"
            className="btn btn-ghost btn-xs thread-rail-confirm-btn"
            data-testid="thread-clean-confirm-btn"
            onClick={() => doClean(visibleChatThreadCards.filter(c => c.isStale).map(c => c.id))}
          >
            确认
          </button>
        )}
        {showArchivedThreadList && confirmCleanMode && (
          <button
            type="button"
            className="btn btn-ghost btn-xs thread-rail-cancel-btn"
            data-testid="thread-clean-cancel-btn"
            onClick={cancelClean}
          >
            取消
          </button>
        )}
        <button
          type="button"
          className={`btn btn-ghost btn-xs thread-rail-switch-btn ${showArchivedThreadList ? 'active' : ''}`}
          data-testid="thread-archive-toggle"
          aria-label={showArchivedThreadList ? '返回会话列表' : '打开归档列表'}
          title={showArchivedThreadList ? '返回会话列表' : '打开归档列表'}
          onClick={onToggleArchivedThreadList}
        >
          {showArchivedThreadList ? (
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path d="M6 4L10 8L6 12" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" transform="rotate(180 8 8)"></path>
            </svg>
          ) : (
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
              <path d="M2.2 3.3h11.6a.9.9 0 0 1 .9.9v1.7a.9.9 0 0 1-.9.9H2.2a.9.9 0 0 1-.9-.9V4.2a.9.9 0 0 1 .9-.9Z"></path>
              <path d="M3.4 6.8h9.2V12a1 1 0 0 1-1 1h-7.2a1 1 0 0 1-1-1V6.8Z"></path>
              <path d="M6.1 9.3h3.8" strokeLinecap="round"></path>
            </svg>
          )}
        </button>
      </header>
      {visibleChatThreadCards.length === 0 ? (
        <div className="thread-rail-empty" data-testid="thread-empty-state">
          {showArchivedThreadList ? '暂无归档会话' : '暂无会话，点击顶部「新对话」开始草稿'}
        </div>
      ) : (
        <div className="thread-rail-list hide-scrollbar" data-testid="thread-list">
          {visibleChatThreadCards.map((thread) => {
            const isEditing = editingThreadId === thread.id;
            const agentInfo = parseAgentBadge(thread.name);
            const tokenLevel = tokenLevelByThreadId[thread.id];

            return (
              <div
                key={thread.id}
                className={`thread-rail-item ${thread.selected ? 'active' : ''} ${thread.isArchived ? 'archived' : ''}`}
                data-thread-id={thread.id}
                role="button"
                tabIndex={0}
                onClick={() => onSelectThread && onSelectThread(thread.id)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    if (onSelectThread) onSelectThread(thread.id);
                  }
                }}
                title={thread.name}
              >
                <div className={`thread-rail-item-head ${isEditing ? 'editing' : ''}`}>
                  {!isEditing && (
                    <button
                      type="button"
                      className={`thread-rail-pin-btn ${thread.isPinned ? 'active' : ''}`}
                      aria-label={thread.isPinned ? '取消置顶会话' : '置顶会话'}
                      title={thread.isPinned ? '取消置顶' : '置顶'}
                      onClick={(e) => {
                        e.stopPropagation();
                        if (onToggleThreadPin) onToggleThreadPin(thread.id);
                      }}
                    >
                      <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
                        <path d="M9.5 2.5L13.5 6.5L10 10L8 14L2 8L6 6L9.5 2.5Z" strokeLinejoin="round"></path>
                        <path d="M6 10L2.5 13.5" strokeLinecap="round"></path>
                      </svg>
                    </button>
                  )}

                  {isEditing ? (
                    <>
                      <input
                        ref={(el) => setRenameInputRef && setRenameInputRef(thread.id, el)}
                        defaultValue={editingAlias}
                        className="thread-rail-alias-input"
                        type="text"
                        maxLength={64}
                        aria-label="会话别名"
                        placeholder="输入别名"
                        disabled={renamingThreadId === thread.id}
                        onChange={(e) => onUpdateEditingAlias && onUpdateEditingAlias(e.target.value)}
                        onClick={(e) => e.stopPropagation()}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') {
                            e.stopPropagation();
                            if (onHandleInlineRenameEnter) onHandleInlineRenameEnter(e, thread.id);
                          } else if (e.key === 'Escape') {
                            e.preventDefault();
                            if (onCancelInlineRename) onCancelInlineRename(thread.id);
                          }
                        }}
                        onBlur={(e) => onHandleInlineRenameBlur && onHandleInlineRenameBlur(e, thread.id)}
                        autoFocus
                      />

                      <button
                        type="button"
                        className="thread-rail-save-btn"
                        data-rename-save-button-for={thread.id}
                        disabled={renamingThreadId === thread.id}
                        title="保存别名"
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={(e) => {
                          e.stopPropagation();
                          if (onSubmitInlineRename) onSubmitInlineRename(thread.id);
                        }}
                      >
                        保存
                      </button>
                    </>
                  ) : (
                    <>
                      <strong
                        className="thread-rail-name"
                        onClick={(e) => {
                          e.stopPropagation();
                          if (onBeginInlineRename) onBeginInlineRename(thread.id);
                        }}
                      >
                        {agentInfo.label && (
                          <span
                            className="thread-agent-pill"
                            title={`智能路由：${agentInfo.label}`}
                          >
                            {agentInfo.label}
                          </span>
                        )}
                        {agentInfo.name}
                      </strong>
                      <button
                        type="button"
                        className={`thread-rail-archive-btn ${thread.isArchived ? 'active' : ''}`}
                        aria-label={thread.isArchived ? '恢复会话' : '归档会话'}
                        title={thread.isArchived ? '恢复' : '归档'}
                        onClick={(e) => {
                          e.stopPropagation();
                          if (onToggleThreadArchive) onToggleThreadArchive(thread.id);
                        }}
                      >
                        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden="true">
                          <path d="M2.2 3.3h11.6a.9.9 0 0 1 .9.9v1.7a.9.9 0 0 1-.9.9H2.2a.9.9 0 0 1-.9-.9V4.2a.9.9 0 0 1 .9-.9Z"></path>
                          <path d="M3.4 6.8h9.2V12a1 1 0 0 1-1 1h-7.2a1 1 0 0 1-1-1V6.8Z"></path>
                          <path d="M6.1 9.3h3.8" strokeLinecap="round"></path>
                        </svg>
                      </button>

                      {thread.provider ? (
                        <span className={`thread-cli-badge cli-${thread.provider}`}>
                          {thread.provider === 'claude' ? 'Claude' : 'Codex'}
                        </span>
                      ) : (thread.agentTitle || thread.agentKey || thread.promptKey) ? (
                        <span 
                          className="thread-agent-badge" 
                          title={`路由 agent：${thread.agentKey || '-'}${thread.promptKey ? ` / prompt：${thread.promptKey}` : ''}`}
                        >
                          {thread.agentTitle || thread.agentKey || thread.promptKey}
                        </span>
                      ) : null}

                      {thread.cwdMismatch && (
                        <span className="thread-cwd-mismatch-badge" title={thread.cwdMismatchReason || 'CWD 不匹配'}>
                          ⚠ CWD
                        </span>
                      )}
                      
                      {tokenLevel && tokenLevel !== 'normal' && (
                        <span
                          className={`thread-context-usage-badge is-token-${tokenLevel}`}
                          title={`上下文使用率已达 ${tokenLevel} 阈值`}
                          data-testid="thread-rail-token-badge"
                        >
                          ⚠
                        </span>
                      )}
                    </>
                  )}
                </div>
                {thread.showId && <div className="thread-rail-item-id">{thread.id}</div>}
                <div className="thread-rail-item-meta">
                  <span className={`status-dot ${thread.status}`}></span>
                  <span>{thread.statusHeader}</span>
                  {thread.isStale && (
                    <span className="thread-stale-badge" data-stale-reason={thread.staleReason}>
                      {thread.staleReason === 'expired' ? '超7天' : '空对话'}
                    </span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </aside>
  );
}

ThreadRailSidePanel.emits = [
  'open-new-window',
  'toggle-archived-thread-list',
  'select-thread',
  'toggle-thread-pin',
  'toggle-thread-archive',
  'begin-inline-rename',
  'submit-inline-rename',
  'handle-inline-rename-enter',
  'cancel-inline-rename',
  'handle-inline-rename-blur',
  'update-editing-alias',
  'delete-stale-threads',
];
ThreadRailSidePanel.template = `
  <aside data-testid="thread-rail">
    <button data-testid="new-window-btn" @click="$emit('open-new-window')"></button>
    <button data-testid="thread-archive-toggle" @click="$emit('toggle-archived-thread-list')"></button>
    <div data-testid="thread-empty-state"></div>
    <div data-testid="thread-list">
      <input class="thread-rail-alias-input" />
      <button data-rename-save-button-for="123"></button>
    </div>
  </aside>
`;

export default ThreadRailSidePanel;

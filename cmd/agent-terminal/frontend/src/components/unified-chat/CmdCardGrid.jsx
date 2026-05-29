import React from 'react';

export function CmdCardGrid({
  cmdCards = [],
  layoutMode = '',
  cmdCardCols = 3,
  onSelectThread,
  onLoadCardHistory,
  onRenameCard,
  onStopCard,
}) {
  return (
    <div className={`cmd-card-grid cols-${cmdCardCols}`}>
      {cmdCards.map((card) => {
        const isSelected = card.selected;
        return (
          <article
            key={card.id}
            className={`cmd-thread-card view-${layoutMode} ${isSelected ? 'active' : ''}`}
            onClick={() => onSelectThread?.(card.id)}
            style={{ cursor: 'pointer' }}
          >
            <header className="cmd-thread-card-head">
              <div>
                <strong>{card.name}</strong>
                <small>{card.id}</small>
              </div>
              <span className={`badge badge-${card.status}`}>
                {card.statusHeader}
              </span>
              {card.provider ? (
                <span className={`thread-cli-badge cli-${card.provider}`}>
                  {card.provider === 'claude' ? 'Claude' : 'Codex'}
                </span>
              ) : (card.agentTitle || card.agentKey || card.promptKey) ? (
                <span
                  className="thread-agent-badge"
                  title={`路由 agent：${card.agentKey || '-'} ${card.promptKey ? (` / prompt：` + card.promptKey) : ''}`}
                >
                  {card.agentTitle || card.agentKey || card.promptKey}
                </span>
              ) : null}

              {card.cwdMismatch ? (
                <span className="thread-cwd-mismatch-badge" title={card.cwdMismatchReason || 'CWD 不匹配'}>
                  ⚠ CWD
                </span>
              ) : null}
            </header>

            {layoutMode !== 'overview' && (
              <div className="cmd-thread-preview">
                {!isSelected ? (
                  <p className="muted">点击卡片查看预览</p>
                ) : (
                  <>
                    {card.preview?.map((line) => (
                      <p key={line.key}>{line.text}</p>
                    ))}
                    {(!card.preview || card.preview.length === 0) && (
                      <p className="muted">暂无消息</p>
                    )}
                  </>
                )}
              </div>
            )}

            {layoutMode === 'mix' && isSelected && card.diff && (
              <pre className="cmd-thread-diff">{card.diff}</pre>
            )}

            <div className="cmd-thread-actions">
              <button
                className="btn btn-ghost btn-xs"
                onClick={(e) => {
                  e.stopPropagation();
                  onSelectThread?.(card.id);
                }}
              >
                打开
              </button>
              <button
                className="btn btn-ghost btn-xs"
                onClick={(e) => {
                  e.stopPropagation();
                  onLoadCardHistory?.(card.id);
                }}
              >
                历史
              </button>
              <button
                className="btn btn-ghost btn-xs"
                onClick={(e) => {
                  e.stopPropagation();
                  onRenameCard?.(card.id);
                }}
              >
                改名
              </button>
              <button
                className="btn btn-ghost btn-xs"
                disabled={!card.interruptible}
                title={card.interruptible ? '中断该 Agent 当前执行' : '当前没有可中断任务'}
                onClick={(e) => {
                  e.stopPropagation();
                  onStopCard?.(card.id);
                }}
              >
                停止
              </button>
            </div>
          </article>
        );
      })}
    </div>
  );
}

CmdCardGrid.emits = ['select-thread', 'load-card-history', 'rename-card', 'stop-card'];
CmdCardGrid.template = `
  <div class="cmd-card-grid">
    <div class="cmd-thread-card">
      <pre class="cmd-thread-diff"></pre>
      <div class="cmd-thread-actions"></div>
    </div>
  </div>
`;

import React from 'react';

export function CmdOverviewPanel({
  stats = { total: 0, running: 0, thinking: 0, editing: 0, error: 0 },
  recentThreads = [],
  selectedThreadId = '',
  getDisplayName = (thread) => thread?.name || '',
  onSelectThread,
}) {
  return (
    <section className="agent-overview-panel">
      <div className="overview-metrics">
        <div className="metric">
          <strong>{stats.total}</strong>
          <span>子Agent</span>
        </div>
        <div className="metric">
          <strong>{stats.running}</strong>
          <span>执行中</span>
        </div>
        <div className="metric">
          <strong>{stats.thinking}</strong>
          <span>思考/回复</span>
        </div>
        <div className="metric">
          <strong>{stats.editing}</strong>
          <span>改文件</span>
        </div>
        <div className="metric">
          <strong>{stats.error}</strong>
          <span>异常</span>
        </div>
      </div>
      {recentThreads.length > 0 && (
        <div className="overview-recent">
          <span className="recent-label">最近活跃:</span>
          {recentThreads.map((thread) => (
            <button
              key={thread.id}
              className={`recent-chip ${thread.id === selectedThreadId ? 'active' : ''}`}
              onClick={() => onSelectThread?.(thread.id)}
            >
              {getDisplayName(thread)}
            </button>
          ))}
        </div>
      )}
    </section>
  );
}

CmdOverviewPanel.emits = ['select-thread'];
CmdOverviewPanel.template = `
  <div class="agent-overview-panel">
    <div class="overview-metrics"></div>
    <div class="overview-recent">
      <button class="recent-chip"></button>
    </div>
  </div>
`;

import React from 'react';
import { Maximize2 } from 'lucide-react';
import { FLOATING_KEY_AGENT_LIMIT, hasOnlyRootAgent, keyAgentsForFloating } from './agentBoardModel.js';
import { AgentCountsSummary } from './AgentCountsSummary.jsx';
import { AgentStatusBadge } from './AgentStatusBadge.jsx';

function FloatingEntry({ agent, formatTime, onExpand }) {
  return (
    <li className="agent-board-floating__entry">
      <button
        type="button"
        className="agent-entry"
        aria-label={`在右侧栏查看 Agent ${agent.name}`}
        data-testid={`agent-entry-${agent.id}`}
        onClick={() => onExpand(agent.id)}
      >
        <span className="agent-entry__head">
          <span className="agent-entry__name">{agent.name}</span>
          <AgentStatusBadge agent={agent} />
        </span>
        <span className="agent-entry__title">{agent.assignment.title}</span>
        <span className="agent-entry__time">{formatTime(agent.progress.updatedAt)}</span>
      </button>
    </li>
  );
}

function FloatingBody({ viewModel, compact, formatTime, onExpand }) {
  const { agents } = viewModel;
  if (viewModel.loading) {
    return <p className="agent-board-floating__notice">正在加载 Agent 状态…</p>;
  }
  if (viewModel.error) {
    return <p className="agent-board-floating__error" role="alert">{viewModel.error}</p>;
  }
  if (agents.length === 0) {
    return <p className="agent-board-floating__notice">暂无 Agent</p>;
  }
  if (compact) {
    return <AgentCountsSummary counts={viewModel.counts} />;
  }
  return (
    <>
      <AgentCountsSummary counts={viewModel.counts} />
      {hasOnlyRootAgent(viewModel) ? (
        <p className="agent-board-floating__notice">暂无子 Agent，等待委派</p>
      ) : (
        <ul className="agent-board-floating__entries">
          {keyAgentsForFloating(agents).map((agent) => (
            <FloatingEntry key={agent.id} agent={agent} formatTime={formatTime} onExpand={onExpand} />
          ))}
        </ul>
      )}
      {agents.length > FLOATING_KEY_AGENT_LIMIT ? (
        <button
          type="button"
          className="agent-board-floating__show-all"
          aria-label="查看全部 Agent"
          data-testid="agent-board-show-all"
          onClick={() => onExpand()}
        >
          查看全部（{agents.length}）
        </button>
      ) : null}
    </>
  );
}

/*
 * 常态悬浮 Agent 看板：聊天页面始终可见的紧凑卡片。
 * 数据完全来自 selector view model；点击条目或展开按钮进入 docked 右侧栏。
 */
function AgentBoardFloating({ viewModel, compact = false, formatTime, onExpand }) {
  return (
    <section
      className={`agent-board-floating${compact ? ' agent-board-floating--compact' : ''}`}
      role="region"
      aria-label="Agent 看板"
      data-testid="agent-board-floating"
    >
      <header className="agent-board-floating__header">
        <h2 className="agent-board-floating__title">Agents</h2>
        <span className="agent-board-floating__total" aria-label={`Agent 总数 ${viewModel.agents.length}`}>
          {viewModel.agents.length}
        </span>
        <button
          type="button"
          className="agent-board-floating__expand"
          aria-label="展开 Agent 看板"
          title="展开 Agent 看板"
          data-testid="agent-board-expand"
          onClick={() => onExpand()}
        >
          <Maximize2 size={14} aria-hidden="true" />
        </button>
      </header>
      <FloatingBody viewModel={viewModel} compact={compact} formatTime={formatTime} onExpand={onExpand} />
    </section>
  );
}

export { AgentBoardFloating };

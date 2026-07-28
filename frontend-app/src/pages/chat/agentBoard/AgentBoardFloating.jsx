import React from 'react';
import { Maximize2 } from 'lucide-react';
import { hasOnlyRootAgent } from './agentBoardModel.js';
import { AgentCountsSummary } from './AgentCountsSummary.jsx';

function FloatingBody({ viewModel }) {
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
  return (
    <>
      <AgentCountsSummary counts={viewModel.counts} />
      {hasOnlyRootAgent(viewModel) ? <p className="agent-board-floating__notice">暂无子 Agent，等待委派</p> : null}
    </>
  );
}

/*
 * 常态悬浮 Agent 看板：聊天页面始终可见的紧凑卡片。
 * 数据完全来自 selector view model；点击展开按钮进入 docked 右侧栏。
 */
function AgentBoardFloating({ viewModel, compact = false, onExpand }) {
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
      <FloatingBody viewModel={viewModel} />
    </section>
  );
}

export { AgentBoardFloating };

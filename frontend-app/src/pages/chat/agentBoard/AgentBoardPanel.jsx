import React from 'react';
import { hasOnlyRootAgent } from './agentBoardModel.js';
import { AgentCountsSummary } from './AgentCountsSummary.jsx';
import { AgentDetail } from './AgentDetail.jsx';
import { AgentHierarchyList } from './AgentHierarchyList.jsx';
import { RightPanelHeader } from '../components/RightPanelHeader.jsx';

function PanelBody({ viewModel, formatTime, onSelectAgent }) {
  if (viewModel.loading) {
    return <p className="agent-board-panel__notice" data-testid="agent-board-loading">正在加载 Agent 状态…</p>;
  }
  if (viewModel.error) {
    return <p className="agent-board-panel__error" role="alert" data-testid="agent-board-error">{viewModel.error}</p>;
  }
  if (viewModel.agents.length === 0) {
    return <p className="agent-board-panel__notice" data-testid="agent-board-empty">暂无 Agent</p>;
  }
  const selected = viewModel.agents.find((agent) => agent.id === viewModel.selectedAgentId);
  return (
    <>
      <AgentCountsSummary counts={viewModel.counts} />
      <AgentHierarchyList
        agents={viewModel.agents}
        rootAgentId={viewModel.rootAgentId}
        selectedAgentId={viewModel.selectedAgentId}
        onSelect={onSelectAgent}
      />
      {hasOnlyRootAgent(viewModel) ? <p className="agent-board-panel__notice">暂无子 Agent，等待委派</p> : null}
      <AgentDetail agent={selected} formatTime={formatTime} />
    </>
  );
}

/*
 * docked 模式的 Agent 看板右侧栏。
 * 与 RuntimePanel 共享右侧栏布局：头部提供「运行时」切换入口和收起按钮。
 */
function AgentBoardPanel({ viewModel, formatTime, onSelectAgent, onCollapse, onShowRuntime }) {
  return (
    <aside className="agent-board-panel" aria-label="Agent 看板详情栏" data-testid="agent-board-panel">
      <RightPanelHeader activeView="agents" onCollapse={onCollapse} onShowRuntime={onShowRuntime} />
      <div className="agent-board-panel__body">
        <PanelBody viewModel={viewModel} formatTime={formatTime} onSelectAgent={onSelectAgent} />
      </div>
    </aside>
  );
}

export { AgentBoardPanel };

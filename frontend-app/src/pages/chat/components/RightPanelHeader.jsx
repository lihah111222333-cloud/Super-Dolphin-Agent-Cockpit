import React from 'react';
import { X } from 'lucide-react';

function RightPanelViewTabs({ activeView, onShowAgents, onShowRuntime }) {
  const agentsActive = activeView === 'agents';
  return (
    <div
      className={`right-panel-view-tabs${agentsActive ? ' is-agents' : ' is-runtime'}`}
      role="group"
      aria-label="右侧栏视图切换"
    >
      <button
        type="button"
        className={`right-panel-view-tab${agentsActive ? ' is-active' : ''}`}
        aria-pressed={agentsActive}
        aria-label={agentsActive ? 'Agent 看板视图' : '切换到 Agent 看板'}
        data-testid={agentsActive ? 'agent-board-tab-agents' : 'runtime-show-agents'}
        onClick={agentsActive ? undefined : onShowAgents}
      >
        Agents
      </button>
      <button
        type="button"
        className={`right-panel-view-tab${agentsActive ? '' : ' is-active'}`}
        aria-pressed={!agentsActive}
        aria-label={agentsActive ? '切换到运行时视图' : '运行时视图'}
        data-testid={agentsActive ? 'agent-board-show-runtime' : 'runtime-tab-runtime'}
        onClick={agentsActive ? onShowRuntime : undefined}
      >
        运行时
      </button>
    </div>
  );
}

/*
 * Agent 与 Runtime 共用同一右栏头部；secondary 只占第二行，
 * 不改变切换器与收起按钮在第一行的位置和行为。
 */
function RightPanelHeader({ activeView, children, onCollapse, onShowAgents, onShowRuntime }) {
  const collapseLabel = activeView === 'agents' ? '收起 Agent 看板' : '收起运行时面板';
  return (
    <header className={`right-panel-header${children ? ' has-secondary' : ''}`}>
      <div className="right-panel-header__primary">
        <span className="right-panel-header__balance" aria-hidden="true" />
        <RightPanelViewTabs
          activeView={activeView}
          onShowAgents={onShowAgents}
          onShowRuntime={onShowRuntime}
        />
        <button
          type="button"
          className="right-panel-header__collapse"
          aria-label={collapseLabel}
          title={collapseLabel}
          data-testid={activeView === 'agents' ? 'agent-board-collapse' : 'runtime-panel-collapse'}
          onClick={onCollapse}
        >
          <X size={14} aria-hidden="true" />
        </button>
      </div>
      {children ? <div className="right-panel-header__secondary">{children}</div> : null}
    </header>
  );
}

export { RightPanelHeader, RightPanelViewTabs };

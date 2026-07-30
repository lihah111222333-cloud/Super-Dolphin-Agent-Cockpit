import React from 'react';
import { agentDepth } from './agentBoardModel.js';
import { AgentStatusBadge } from './AgentStatusBadge.jsx';

function AgentNode({ agent, depth, isRoot, selected, onSelect }) {
  return (
    <li className="agent-hierarchy__item">
      <button
        type="button"
        className={`agent-node${selected ? ' agent-node--selected' : ''}`}
        style={{ '--agent-depth': depth }}
        aria-pressed={selected}
        aria-label={`选择 Agent ${agent.name}`}
        data-testid={`agent-node-${agent.id}`}
        onClick={() => onSelect(agent.id)}
      >
        <AgentStatusBadge agent={agent} />
        <span className="agent-node__body">
          <span className="agent-node__name">
            {agent.name}
            {isRoot ? <span className="agent-node__root-badge">根</span> : null}
          </span>
          <span className="agent-node__title">{agent.assignment.title}</span>
        </span>
      </button>
    </li>
  );
}

/*
 * Agent 层级列表：按 selector 输出的稳定顺序渲染，
 * 仅根据 parentAgentId 计算缩进深度，不改变排序。
 */
function AgentHierarchyList({ agents, rootAgentId, selectedAgentId, onSelect }) {
  const agentsById = new Map(agents.map((agent) => [agent.id, agent]));
  return (
    <ul className="agent-hierarchy" aria-label="Agent 层级列表" data-testid="agent-hierarchy">
      {agents.map((agent) => (
        <AgentNode
          key={agent.id}
          agent={agent}
          depth={agentDepth(agent, agentsById)}
          isRoot={agent.id === rootAgentId}
          selected={agent.id === selectedAgentId}
          onSelect={onSelect}
        />
      ))}
    </ul>
  );
}

export { AgentHierarchyList };

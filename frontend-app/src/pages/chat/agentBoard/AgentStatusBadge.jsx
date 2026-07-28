import React from 'react';
import { agentStatusLabel } from './agentBoardModel.js';

/*
 * 状态徽标：彩色状态点 + 中文状态文案。
 * 状态完全来自 agent.progress.status 与 agent.outcome 结构化字段。
 */
function AgentStatusBadge({ agent }) {
  const status = agentStatusLabel(agent);
  return (
    <span className={`agent-status agent-status--${status.category}`} data-testid={`agent-status-${status.category}`}>
      <span className="agent-status__dot" aria-hidden="true" />
      <span className="agent-status__text">{status.text}</span>
    </span>
  );
}

export { AgentStatusBadge };

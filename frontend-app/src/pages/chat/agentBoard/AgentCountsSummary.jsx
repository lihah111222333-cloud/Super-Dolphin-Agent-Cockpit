import React from 'react';

const COUNT_ITEMS = Object.freeze([
  { key: 'running', label: '运行中' },
  { key: 'waiting', label: '等待' },
  { key: 'completed', label: '完成' },
  { key: 'failed', label: '失败' },
]);

/*
 * 状态汇总：running / waiting / completed / failed 四段计数。
 * 计数直接来自 selector 的 counts，不做任何重新计算。
 */
function AgentCountsSummary({ counts }) {
  return (
    <ul className="agent-counts" aria-label="Agent 状态汇总" data-testid="agent-counts">
      {COUNT_ITEMS.map(({ key, label }) => (
        <li
          key={key}
          className={`agent-counts__item agent-counts__item--${key}${key === 'failed' && counts.failed > 0 ? ' agent-counts__item--alert' : ''}`}
          data-testid={`agent-count-${key}`}
        >
          {label} {counts[key]}
        </li>
      ))}
    </ul>
  );
}

export { AgentCountsSummary };

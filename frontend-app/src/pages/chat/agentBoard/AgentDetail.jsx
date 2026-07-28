import React from 'react';
import { agentStatusLabel } from './agentBoardModel.js';
import { AgentStatusBadge } from './AgentStatusBadge.jsx';

function hasValue(value) {
  return value !== null && value !== undefined;
}

function AgentProgressSection({ progress }) {
  const hasStep = hasValue(progress.currentStep);
  const hasCounts = Number.isFinite(progress.completedSteps) && Number.isFinite(progress.totalSteps);
  return (
    <section className="agent-detail__section" aria-label="进度详情" data-testid="agent-progress">
      <h4 className="agent-detail__heading">进度</h4>
      {hasStep ? <p className="agent-detail__line">当前步骤：{progress.currentStep}</p> : null}
      {hasCounts ? (
        <p className="agent-detail__line" data-testid="agent-progress-steps">
          已完成 {progress.completedSteps} / {progress.totalSteps} 步
        </p>
      ) : null}
      {!hasStep && !hasCounts ? <p className="agent-detail__line agent-detail__muted">暂无结构化步骤</p> : null}
    </section>
  );
}

function AgentOutcomeSection({ outcome, formatTime }) {
  if (!outcome) {
    return (
      <section className="agent-detail__section" aria-label="最终结果" data-testid="agent-outcome">
        <h4 className="agent-detail__heading">最终结果</h4>
        <p className="agent-detail__line agent-detail__muted">尚无最终结果</p>
      </section>
    );
  }
  return (
    <section className={`agent-detail__section agent-outcome agent-outcome--${outcome.kind}`} aria-label="最终结果" data-testid="agent-outcome">
      <h4 className="agent-detail__heading">最终结果</h4>
      {outcome.kind === 'success' ? <p className="agent-detail__line" data-testid="agent-outcome-summary">{outcome.summary}</p> : null}
      {outcome.kind === 'failure' || outcome.kind === 'stopped' ? (
        <p className="agent-detail__line" data-testid="agent-outcome-reason">{outcome.reason}</p>
      ) : null}
      {outcome.kind === 'failure' && hasValue(outcome.code) ? <p className="agent-detail__line">错误码：{outcome.code}</p> : null}
      {outcome.kind === 'failure' && typeof outcome.recoverable === 'boolean' ? (
        <p className="agent-detail__line">可恢复：{outcome.recoverable ? '是' : '否'}</p>
      ) : null}
      {hasValue(outcome.completedAt) ? <p className="agent-detail__line agent-detail__muted">完成于 {formatTime(outcome.completedAt)}</p> : null}
    </section>
  );
}

/*
 * 选中 Agent 详情：只渲染 assignment / progress / outcome 结构化字段，
 * 字段缺失时展示明确的占位文案，不做任何推断。
 */
function AgentDetail({ agent, formatTime }) {
  if (!agent) {
    return (
      <div className="agent-detail agent-detail--empty" data-testid="agent-detail-empty">
        请选择一个 Agent 查看详情
      </div>
    );
  }
  const status = agentStatusLabel(agent);
  return (
    <section className="agent-detail" aria-label={`Agent ${agent.name} 详情`} data-testid="agent-detail">
      <header className="agent-detail__header">
        <h3 className="agent-detail__name">{agent.name}</h3>
        <AgentStatusBadge agent={agent} />
      </header>
      <dl className="agent-detail__facts">
        <div className="agent-detail__fact">
          <dt>任务标题</dt>
          <dd>{agent.assignment.title}</dd>
        </div>
        <div className="agent-detail__fact">
          <dt>任务描述</dt>
          <dd>{agent.assignment.description}</dd>
        </div>
        <div className="agent-detail__fact">
          <dt>委派时间</dt>
          <dd>{formatTime(agent.assignment.assignedAt)}</dd>
        </div>
        <div className="agent-detail__fact">
          <dt>当前状态</dt>
          <dd>{status.text}（{agent.progress.status}）</dd>
        </div>
        <div className="agent-detail__fact">
          <dt>最近更新</dt>
          <dd>{formatTime(agent.progress.updatedAt)}</dd>
        </div>
      </dl>
      <AgentProgressSection progress={agent.progress} />
      <AgentOutcomeSection outcome={agent.outcome} formatTime={formatTime} />
    </section>
  );
}

export { AgentDetail };

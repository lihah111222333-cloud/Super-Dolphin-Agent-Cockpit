import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { CronJobDetail as VueComp, runStatusColor } from './CronJobDetail.js';

function CronJobSummary({ job }) {
  return (
    <section className="cron-detail-summary">
      <h3 data-testid="cron-detail-name">{job.name || '(未命名)'}</h3>
      <div className="data-row-vue"><strong>ID</strong><span>{job.id}</span></div>
      <div className="data-row-vue">
        <strong>Schedule</strong>
        <span>{job.schedule_expr || '-'}{job.timezone && ` (${job.timezone})`}</span>
      </div>
      <div className="data-row-vue"><strong>启用</strong><span>{job.enabled ? '是' : '否'}</span></div>
      <div className="data-row-vue"><strong>下一次</strong><span>{job.next_run_at || '-'}</span></div>
      <div className="data-row-vue"><strong>上次运行</strong><span>{job.last_run_at || '从未运行'}</span></div>
      <div className="data-row-vue"><strong>上次状态</strong><span>{job.last_status || '-'}</span></div>
      <div className="data-row-vue"><strong>Provider</strong><span>{job.provider || '-'}</span></div>
      <div className="data-row-vue"><strong>Model</strong><span>{job.model || '-'}</span></div>
      <div className="data-row-vue"><strong>CWD</strong><span title={job.cwd}>{job.cwd || '-'}</span></div>
      <div className="data-row-vue"><strong>Active Turn</strong><span>{job.active_turn_id || '-'}</span></div>
      <div className="data-row-vue"><strong>失败 / 预算</strong><span>{job.failure_count || 0} / {job.max_attempts || 0}</span></div>
      {job.last_error && (
        <div className="data-row-vue cron-detail-error">
          <strong>上次错误</strong><span>{job.last_error}</span>
        </div>
      )}
    </section>
  );
}

function CronJobRuns({ runs, runsError, loadingRuns }) {
  return (
    <section className="cron-detail-runs">
      <h4>历史 runs</h4>
      {runsError && (
        <div className="alert alert-error" data-testid="cron-detail-runs-error">
          {runsError}
        </div>
      )}
      {!loadingRuns && runs.length === 0 ? (
        <div className="empty-state" data-testid="cron-detail-runs-empty">
          <p>还没有历史 run</p>
        </div>
      ) : (
        <ul className="cron-runs-list" data-testid="cron-detail-runs-list">
          {runs.map((run, idx) => {
            const statusInfo = runStatusColor(run.status);
            return (
              <li key={run.id || idx} className="cron-run-row" data-testid={`cron-run-${idx}`}>
                <span className={`cron-status-pill tone-${statusInfo.tone}`}>
                  {statusInfo.label}
                </span>
                <span className="cron-run-time">{run.scheduled_at || '-'}</span>
                {run.turn_id && (
                  <span className="cron-run-turn" title={run.turn_id}>
                    turn={run.turn_id.slice(0, 8)}
                  </span>
                )}
                {run.error && (
                  <span className="cron-run-error" title={run.error}>
                    {run.error}
                  </span>
                )}
                {run.status === 'observe_lost' && (
                  <span className="cron-run-warn" data-testid="cron-run-observe-lost-hint">
                    观察链丢失，需人工核对 turn 真实结局
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

export function CronJobDetail(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const job = val(vm.job);
  const runs = val(vm.runs) || [];
  const loadingRuns = val(vm.loadingRuns);
  const runsError = val(vm.runsError);

  return (
    <div className="cron-job-detail" data-testid="cron-job-detail">
      <div className="cron-detail-toolbar">
        <button className="btn btn-ghost btn-xs" data-testid="cron-detail-back" onClick={vm.onBack}>← 返回</button>
        <button className="btn btn-ghost btn-xs" data-testid="cron-detail-edit" disabled={!job} onClick={vm.onEdit}>编辑</button>
        <button
          className="btn btn-ghost btn-xs"
          data-testid="cron-detail-refresh-runs"
          disabled={loadingRuns}
          onClick={vm.refreshRuns}
        >
          {loadingRuns ? '加载中…' : '刷新历史'}
        </button>
      </div>

      {!job ? (
        <div className="empty-state" data-testid="cron-detail-not-found">
          <h3>找不到任务</h3>
          <p>该任务可能已被删除，请返回列表。</p>
        </div>
      ) : (
        <>
          <CronJobSummary job={job} />
          <CronJobRuns runs={runs} runsError={runsError} loadingRuns={loadingRuns} />
        </>
      )}
    </div>
  );
}

import React from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { CronPanel as VueComp } from './CronPanel.js';
import { CronJobForm } from '../components/cron/CronJobForm.jsx';
import { CronJobDetail } from '../components/cron/CronJobDetail.jsx';

export function CronPanel(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const jobs = val(vm.jobs) || [];
  const loading = val(vm.loading);
  const errorMessage = val(vm.errorMessage);
  const view = val(vm.view);
  const editingJob = val(vm.editingJob);
  const viewingJobId = val(vm.viewingJobId);
  const confirmDeleteJob = val(vm.confirmDeleteJob);
  const deletingJobId = val(vm.deletingJobId);
  const deleteError = val(vm.deleteError);

  return (
    <div className="cron-panel" data-testid="cron-panel">
      {view === 'form' ? (
        <CronJobForm
          editingJob={editingJob}
          onCancel={vm.closeForm}
          onSaved={vm.onSaved}
        />
      ) : view === 'detail' ? (
        <CronJobDetail
          jobId={viewingJobId}
          onBack={vm.backToList}
          onEdit={vm.editFromDetail}
        />
      ) : (
        <>
          <div className="cron-panel-toolbar">
            <button
              className="btn btn-primary btn-xs"
              data-testid="cron-new-button"
              onClick={vm.openCreate}
            >
              新建定时任务
            </button>
            <button
              className="btn btn-ghost btn-xs"
              data-testid="cron-refresh-button"
              disabled={loading}
              onClick={vm.refresh}
            >
              {loading ? '加载中…' : '刷新'}
            </button>
          </div>

          {errorMessage && (
            <div className="alert alert-error" data-testid="cron-error">
              {errorMessage}
            </div>
          )}

          {!loading && jobs.length === 0 ? (
            <div className="empty-state" data-testid="cron-empty-state">
              <div className="es-icon">⏱</div>
              <h3>暂无定时任务</h3>
              <p>使用后端 cronjob/create 创建任务后会显示在此处。</p>
            </div>
          ) : jobs.length > 0 ? (
            <div className="data-list-vue" data-testid="cron-list">
              {jobs.map((job, idx) => (
                <article
                  key={job.id || `cron-${idx}`}
                  className="data-card-vue"
                  data-testid={`cron-card-${idx}`}
                >
                  <div className="data-row-vue">
                    <strong>启用</strong>
                    <span>
                      <input
                        type="checkbox"
                        checked={!!job.enabled}
                        data-testid={`cron-toggle-${idx}`}
                        onChange={(e) => vm.onToggleEnabled(job, e.target.checked)}
                      />
                    </span>
                  </div>
                  <div className="data-row-vue">
                    <strong>名称</strong>
                    <span data-testid={`cron-name-${idx}`}>{job.name || '(未命名)'}</span>
                  </div>
                  <div className="data-row-vue">
                    <strong>Schedule</strong>
                    <span>{vm.formatSchedule(job)}</span>
                  </div>
                  <div className="data-row-vue">
                    <strong>Provider</strong>
                    <span>{job.provider || '-'}</span>
                  </div>
                  <div className="data-row-vue">
                    <strong>CWD</strong>
                    <span title={job.cwd}>{job.cwd || '-'}</span>
                  </div>
                  <div className="data-row-vue">
                    <strong>上次状态</strong>
                    <span>{vm.formatLastRun(job)}</span>
                  </div>
                  <div className="data-row-vue">
                    <strong>失败 / 预算</strong>
                    <span>{vm.formatRetryBudget(job)}</span>
                  </div>
                  <div className="data-actions-vue">
                    <button
                      className="btn btn-ghost btn-xs"
                      data-testid={`cron-view-${idx}`}
                      onClick={() => vm.openDetail(job)}
                    >
                      查看
                    </button>
                    <button
                      className="btn btn-ghost btn-xs"
                      data-testid={`cron-runonce-${idx}`}
                      onClick={() => vm.onRunOnce(job)}
                    >
                      立即触发
                    </button>
                    <button
                      className="btn btn-ghost btn-xs"
                      data-testid={`cron-edit-${idx}`}
                      onClick={() => vm.openEdit(job)}
                    >
                      编辑
                    </button>
                    <button
                      className="btn btn-ghost btn-xs"
                      data-testid={`cron-delete-${idx}`}
                      onClick={() => vm.onDelete(job)}
                    >
                      删除
                    </button>
                  </div>
                </article>
              ))}
            </div>
          ) : null}
        </>
      )}

      {confirmDeleteJob && (
        <div className="modal-overlay" data-testid="cron-delete-overlay" onClick={(e) => { if (e.target === e.currentTarget) vm.cancelDeleteJob(); }}>
          <div className="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="cron-delete-modal">
            <div className="memory-modal-head">
              <div>
                <div className="modal-title">删除定时任务</div>
                <div className="memory-modal-tip">{confirmDeleteJob.name || confirmDeleteJob.id}</div>
              </div>
              <button className="btn btn-ghost" data-testid="cron-delete-close" disabled={Boolean(deletingJobId)} onClick={vm.cancelDeleteJob}>关闭</button>
            </div>
            <div className="memory-form-helper">确认删除定时任务 “{confirmDeleteJob.name || confirmDeleteJob.id}”？该操作不可撤销。</div>
            {deleteError && <div className="memory-form-helper" style={{ color: 'var(--color-danger,#f87171)' }}>{deleteError}</div>}
            <div className="memory-editor-actions">
              <button className="btn btn-ghost" data-testid="cron-delete-cancel" disabled={Boolean(deletingJobId)} onClick={vm.cancelDeleteJob}>取消</button>
              <button className="btn btn-danger" data-testid="cron-delete-confirm" disabled={Boolean(deletingJobId)} onClick={vm.confirmDelete}>{deletingJobId === confirmDeleteJob.id ? '删除中...' : '确认删除'}</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

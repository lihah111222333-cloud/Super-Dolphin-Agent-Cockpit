import React, { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import cronParser from 'cron-parser';
import { Clock, ClipboardList, History, Pencil, Play, Plus, RefreshCw, Save, Trash2, X } from 'lucide-react';
import { createCronJob, deleteCronJob, getDashboardPage, listCronJobRuns, listCronJobs, runCronJobOnce, setCronJobEnabled, updateCronJob } from '../../shared/api/backendApi.js';
import { dashboardQueryErrorState, dashboardQueryKey, errorMessage, firstText, objectValue, optionalSettingsCwd, queryHasSnapshot, sharedFileTimestamp, SKILLS_REQUEST_TIMEOUT_MS, textValue, useDashboardFocusInvalidation, withTimeout, wordListFromText } from '../shared/pageShared.js';
import { PageHeader, RetryableSyncError } from '../shared/pageComponents.jsx';

const TASK_TABS = Object.freeze([
  { key: 'acks', label: '任务工单' },
  { key: 'traces', label: '执行追踪' },
  { key: 'cron', label: '定时任务' },
]);

const EMPTY_CRON_FORM = Object.freeze({
  id: '',
  name: '',
  prompt: '',
  schedule_expr: '',
  timezone: '',
  provider: 'codex',
  model: '',
  cwd: '',
  codexHome: '',
  codexInstanceKey: '',
  codexModelProvider: '',
  skills: '',
  notify_channel: '',
  enabled: true,
  max_attempts: '0',
});

async function fetchTasksDashboard(cwd) {
  const response = await withTimeout(
    getDashboardPage({ cwd, page: 'tasks' }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '任务加载超时，请检查任务追踪服务或后端状态。',
  );
  return normalizeTasksResponse(response);
}

async function fetchCronJobs() {
  const response = await withTimeout(
    listCronJobs(),
    SKILLS_REQUEST_TIMEOUT_MS,
    '定时任务加载超时，请检查 cronjob 后端状态。',
  );
  return normalizeCronJobsResponse(response);
}

async function fetchCronRuns(jobId) {
  const response = await withTimeout(
    listCronJobRuns({ jobId, limit: 50 }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '定时任务历史加载超时，请检查 cronjob 后端状态。',
  );
  return normalizeCronRunsResponse(response);
}

function normalizeTasksResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('tasks dashboard response must be an object');
  }
  if (!Array.isArray(response.taskAcks)) throw new Error('tasks dashboard response taskAcks must be an array');
  if (!Array.isArray(response.taskTraces)) throw new Error('tasks dashboard response taskTraces must be an array');
  return {
    taskAcks: response.taskAcks,
    taskTraces: response.taskTraces,
  };
}

function normalizeCronJobsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('cronjob/list response must be an object');
  }
  if (!Array.isArray(response.jobs)) throw new Error('cronjob/list response jobs must be an array');
  return response.jobs;
}

function normalizeCronRunsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('cronjob/listRuns response must be an object');
  }
  if (!Array.isArray(response.runs)) throw new Error('cronjob/listRuns response runs must be an array');
  return response.runs;
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function browserTimezone() {
  return textValue(Intl.DateTimeFormat().resolvedOptions().timeZone);
}

function emptyCronForm(projectCwd) {
  return {
    ...EMPTY_CRON_FORM,
    timezone: browserTimezone(),
    cwd: projectCwd || '',
  };
}

function cronConfigValue(job, key) {
  return textValue(objectValue(job?.config)[key]);
}

function cronJobToForm(job, projectCwd) {
  return {
    id: textValue(job?.id),
    name: textValue(job?.name),
    prompt: textValue(job?.prompt),
    schedule_expr: textValue(job?.schedule_expr || job?.scheduleExpr),
    timezone: textValue(job?.timezone) || browserTimezone(),
    provider: textValue(job?.provider) || 'codex',
    model: textValue(job?.model),
    cwd: textValue(job?.cwd) || projectCwd || '',
    codexHome: cronConfigValue(job, 'codexHome'),
    codexInstanceKey: cronConfigValue(job, 'codexInstanceKey'),
    codexModelProvider: cronConfigValue(job, 'codexModelProvider'),
    skills: Array.isArray(job?.skills) ? job.skills.join(', ') : '',
    notify_channel: textValue(job?.notify_channel || job?.notifyChannel),
    enabled: typeof job?.enabled === 'boolean' ? job.enabled : true,
    max_attempts: String(Number(job?.max_attempts ?? job?.maxAttempts ?? 0) || 0),
  };
}

function requireFormText(form, key, label) {
  const value = textValue(form[key]);
  if (!value) throw new Error(`${label}不能为空`);
  return value;
}

function nextRunAtFromSchedule(scheduleExpr, timezone) {
  try {
    return cronParser.parseExpression(scheduleExpr, { tz: timezone }).next().toDate().toISOString();
  } catch (err) {
    throw new Error(`Cron 表达式无效：${errorMessage(err)}`, { cause: err });
  }
}

function cronPayloadFromForm(form) {
  const maxAttempts = Number(form.max_attempts);
  if (!Number.isInteger(maxAttempts) || maxAttempts < 0) throw new Error('最大重试次数必须是非负整数');
  const provider = requireFormText(form, 'provider', 'Provider');
  if (provider !== 'codex') throw new Error('当前 cron job 仅支持 codex provider');
  const config = {
    codexHome: requireFormText(form, 'codexHome', 'Codex Home'),
    codexInstanceKey: requireFormText(form, 'codexInstanceKey', 'Instance Key'),
    codexModelProvider: requireFormText(form, 'codexModelProvider', 'Model Provider'),
  };
  const scheduleExpr = requireFormText(form, 'schedule_expr', 'Cron 表达式');
  const timezone = requireFormText(form, 'timezone', '时区');
  return cleanObject({
    name: requireFormText(form, 'name', '名称'),
    prompt: requireFormText(form, 'prompt', '提示词'),
    schedule_type: 'cron',
    schedule_expr: scheduleExpr,
    timezone,
    provider,
    model: textValue(form.model),
    cwd: requireFormText(form, 'cwd', '工作目录'),
    config,
    skills: wordListFromText(form.skills),
    notify_channel: textValue(form.notify_channel),
    enabled: Boolean(form.enabled),
    max_attempts: maxAttempts,
    next_run_at: nextRunAtFromSchedule(scheduleExpr, timezone),
  });
}

function taskAckTitle(item, index) {
  return firstText(item.title, item.ack_key, `任务工单 ${index + 1}`);
}

function taskTraceTitle(item, index) {
  return firstText(item.span_name, item.trace_id, `执行追踪 ${index + 1}`);
}

function cronJobTitle(job, index) {
  return firstText(job.name, job.id, `定时任务 ${index + 1}`);
}

function cronStatus(job) {
  return firstText(job.last_status, job.status, job.enabled ? 'enabled' : 'disabled');
}

function cronRunStatus(run) {
  return firstText(run.status, run.state, '-');
}

function TasksPage({ projectPath, store, refreshKey = 0 }) {
  const projectCwd = optionalSettingsCwd(projectPath);
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState('acks');
  const tasksKey = useMemo(() => dashboardQueryKey(projectCwd, 'tasks'), [projectCwd]);
  const cronKey = useMemo(() => dashboardQueryKey(projectCwd || 'global', 'cronjobs'), [projectCwd]);
  useDashboardFocusInvalidation(projectCwd, 'tasks');

  const tasksQuery = useQuery({
    queryKey: tasksKey,
    queryFn: () => fetchTasksDashboard(projectCwd),
    enabled: Boolean(projectCwd),
  });
  const cronQuery = useQuery({
    queryKey: cronKey,
    queryFn: fetchCronJobs,
    enabled: Boolean(projectCwd),
  });

  useEffect(() => {
    if (!projectCwd || Number(refreshKey || 0) <= 0) return;
    void queryClient.invalidateQueries({ queryKey: tasksKey });
    void queryClient.invalidateQueries({ queryKey: cronKey });
  }, [cronKey, projectCwd, queryClient, refreshKey, tasksKey]);

  const tasksError = dashboardQueryErrorState(tasksQuery, queryHasSnapshot(tasksQuery));
  const cronError = dashboardQueryErrorState(cronQuery, queryHasSnapshot(cronQuery));
  const data = tasksQuery.data || { taskAcks: [], taskTraces: [] };

  return (
    <section className="tasks-page" data-testid="tasks-page">
      <PageHeader icon={ClipboardList} title="任务" subtitle={projectCwd ? '当前项目：' + projectCwd : '正在连接本地项目...'} />
      <div className="tabs" role="tablist" aria-label="任务分类">
        {TASK_TABS.map((tab) => (
          <button key={tab.key} type="button" role="tab" aria-selected={activeTab === tab.key ? 'true' : 'false'} className={activeTab === tab.key ? 'active' : ''} onClick={() => setActiveTab(tab.key)}>
            {tab.label}
          </button>
        ))}
      </div>
      {tasksError.cachedSyncError ? <RetryableSyncError className="danger-text workflow-sync-alert" message={tasksError.cachedSyncError} onRetry={() => queryClient.invalidateQueries({ queryKey: tasksKey })} /> : null}
      {activeTab === 'acks' ? <TaskAckList items={data.taskAcks} loading={tasksQuery.isLoading} error={tasksError.blockingError} /> : null}
      {activeTab === 'traces' ? <TaskTraceList items={data.taskTraces} loading={tasksQuery.isLoading} error={tasksError.blockingError} /> : null}
      {activeTab === 'cron' ? (
        <CronPanel
          cronKey={cronKey}
          error={cronError}
          jobs={cronQuery.data || []}
          loading={cronQuery.isLoading}
          projectCwd={projectCwd}
          store={store}
        />
      ) : null}
    </section>
  );
}

function TaskAckList({ error, items, loading }) {
  if (loading) return <p className="console-message">正在加载任务工单...</p>;
  if (error) return <p className="danger-text" role="alert">{error}</p>;
  if (items.length === 0) return <EmptyTasksState title="暂无任务工单" text="后端没有返回 task ack 记录。" />;
  return (
    <div className="task-card-list" data-testid="task-acks-list">
      {items.map((item, index) => (
        <article className="task-card" key={firstText(item.ack_key, item.id, index)}>
          <div>
            <strong>{taskAckTitle(item, index)}</strong>
            <span>{firstText(item.ack_key, '-')}</span>
          </div>
          <dl>
            <dt>状态</dt><dd>{firstText(item.status, '-')}</dd>
            <dt>负责人</dt><dd>{firstText(item.assigned_to, item.assignedTo, '-')}</dd>
            <dt>进度</dt><dd>{firstText(item.progress, '-')}</dd>
          </dl>
        </article>
      ))}
    </div>
  );
}

function TaskTraceList({ error, items, loading }) {
  if (loading) return <p className="console-message">正在加载执行追踪...</p>;
  if (error) return <p className="danger-text" role="alert">{error}</p>;
  if (items.length === 0) return <EmptyTasksState title="暂无执行追踪" text="后端没有返回 task trace 记录。" />;
  return (
    <div className="task-card-list" data-testid="task-traces-list">
      {items.map((item, index) => (
        <article className="task-card" key={firstText(item.trace_id, item.id, index)}>
          <div>
            <strong>{taskTraceTitle(item, index)}</strong>
            <span>{firstText(item.trace_id, '-')}</span>
          </div>
          <dl>
            <dt>状态</dt><dd>{firstText(item.status, '-')}</dd>
            <dt>开始</dt><dd>{sharedFileTimestamp(item.started_at || item.startedAt)}</dd>
            <dt>耗时</dt><dd>{firstText(item.duration_ms, item.durationMs, '-')}</dd>
          </dl>
        </article>
      ))}
    </div>
  );
}

function EmptyTasksState({ title, text }) {
  return (
    <div className="empty-state">
      <Clock size={34} aria-hidden="true" />
      <h3>{title}</h3>
      <p>{text}</p>
    </div>
  );
}

function CronPanel({ cronKey, error, jobs, loading, projectCwd, store }) {
  const queryClient = useQueryClient();
  const [form, setForm] = useState(() => emptyCronForm(projectCwd));
  const [formOpen, setFormOpen] = useState(false);
  const [actioning, setActioning] = useState('');
  const [notice, setNotice] = useState('');
  const [actionError, setActionError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [runsJob, setRunsJob] = useState(null);

  useEffect(() => {
    if (!projectCwd) return;
    setForm((current) => (current.id || current.cwd ? current : { ...current, cwd: projectCwd }));
  }, [projectCwd]);

  const runsQuery = useQuery({
    queryKey: dashboardQueryKey(projectCwd || 'global', 'cronjob-runs', runsJob?.id || ''),
    queryFn: () => fetchCronRuns(runsJob.id),
    enabled: Boolean(runsJob?.id),
  });

  const invalidateCron = () => queryClient.invalidateQueries({ queryKey: cronKey });
  const runAction = async (key, action, successText) => {
    setActioning(key);
    setActionError('');
    setNotice('');
    try {
      await action();
      setNotice(successText);
      await invalidateCron();
      if (runsJob?.id) await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(projectCwd || 'global', 'cronjob-runs', runsJob.id) });
      return true;
    } catch (err) {
      setActionError(errorMessage(err));
      store?.addWarning?.('error', 'cron.ui.action.failed', { action: key, error: errorMessage(err) });
      return false;
    } finally {
      setActioning('');
    }
  };

  const openCreateForm = () => {
    setForm(emptyCronForm(projectCwd));
    setFormOpen(true);
    setActionError('');
  };
  const editJob = (job) => {
    setForm(cronJobToForm(job, projectCwd));
    setFormOpen(true);
    setActionError('');
  };
  const submitForm = async (event) => {
    event.preventDefault();
    const ok = await runAction(
      form.id ? 'save:' + form.id : 'create',
      () => {
        const payload = cronPayloadFromForm(form);
        return form.id ? updateCronJob({ id: form.id, ...payload }) : createCronJob(payload);
      },
      form.id ? '定时任务已保存' : '定时任务已创建',
    );
    if (ok) {
      setFormOpen(false);
      setForm(emptyCronForm(projectCwd));
    }
  };

  return (
    <div className="cron-panel" data-testid="cron-panel">
      {error.cachedSyncError ? <RetryableSyncError className="danger-text workflow-sync-alert" message={error.cachedSyncError} onRetry={invalidateCron} /> : null}
      {error.blockingError ? <p className="danger-text" role="alert">{error.blockingError}</p> : null}
      {actionError ? <p className="danger-text" role="alert">{actionError}</p> : null}
      {notice ? <output className="settings-status">{notice}</output> : null}
      <div className="task-toolbar">
        <button type="button" onClick={openCreateForm}><Plus size={15} /> 新建定时任务</button>
        <button type="button" className="ghost" onClick={() => { void invalidateCron(); }}><RefreshCw size={15} /> 刷新</button>
      </div>
      {formOpen ? <CronJobForm actioning={actioning} form={form} onCancel={() => setFormOpen(false)} onChange={setForm} onSubmit={submitForm} /> : null}
      {loading ? <p className="console-message">正在加载定时任务...</p> : null}
      {!loading && jobs.length === 0 && !error.blockingError ? <EmptyTasksState title="暂无定时任务" text="创建 cron job 后会显示在这里。" /> : null}
      <div className="cron-job-list" data-testid="cron-jobs-list">
        {jobs.map((job, index) => (
          <CronJobCard
            actioning={actioning}
            index={index}
            job={job}
            key={firstText(job.id, index)}
            onDelete={setDeleteTarget}
            onEdit={editJob}
            onRuns={setRunsJob}
            onRun={(item) => runAction('run:' + item.id, () => runCronJobOnce({ id: item.id }), '已触发立即运行')}
            onToggle={(item) => runAction('toggle:' + item.id, () => setCronJobEnabled({ id: item.id, enabled: !item.enabled }), item.enabled ? '已停用定时任务' : '已启用定时任务')}
          />
        ))}
      </div>
      {runsJob ? <CronRunsPanel job={runsJob} query={runsQuery} onClose={() => setRunsJob(null)} /> : null}
      {deleteTarget ? (
        <CronDeletePanel
          actioning={actioning}
          job={deleteTarget}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={async () => {
            const ok = await runAction('delete:' + deleteTarget.id, () => deleteCronJob({ id: deleteTarget.id }), '定时任务已删除');
            if (ok) setDeleteTarget(null);
          }}
        />
      ) : null}
    </div>
  );
}

function CronJobForm({ actioning, form, onCancel, onChange, onSubmit }) {
  const update = (key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    onChange((current) => ({ ...current, [key]: value }));
  };
  return (
    <form className="cron-form" data-testid="cron-job-form" onSubmit={onSubmit}>
      <div className="cron-form-grid">
        <label>名称<input aria-label="定时任务名称" value={form.name} onChange={update('name')} /></label>
        <label>Cron 表达式<input aria-label="Cron 表达式" value={form.schedule_expr} onChange={update('schedule_expr')} placeholder="*/15 * * * *" /></label>
        <label>工作目录<input aria-label="定时任务工作目录" value={form.cwd} onChange={update('cwd')} /></label>
        <label>时区<input aria-label="定时任务时区" value={form.timezone} onChange={update('timezone')} /></label>
        <label>Provider<select aria-label="定时任务 Provider" value={form.provider} onChange={update('provider')}><option value="codex">codex</option></select></label>
        <label>模型<input aria-label="定时任务模型" value={form.model} onChange={update('model')} /></label>
        <label>Codex Home<input aria-label="Codex Home" value={form.codexHome} onChange={update('codexHome')} placeholder="~/.codex" /></label>
        <label>Instance Key<input aria-label="Instance Key" value={form.codexInstanceKey} onChange={update('codexInstanceKey')} /></label>
        <label>Model Provider<input aria-label="Model Provider" value={form.codexModelProvider} onChange={update('codexModelProvider')} /></label>
        <label>Skills<input aria-label="定时任务 Skills" value={form.skills} onChange={update('skills')} /></label>
        <label>通知渠道<input aria-label="通知渠道" value={form.notify_channel} onChange={update('notify_channel')} /></label>
        <label>最大重试次数<input aria-label="最大重试次数" type="number" min="0" value={form.max_attempts} onChange={update('max_attempts')} /></label>
      </div>
      <label className="task-toggle"><input type="checkbox" checked={form.enabled} onChange={update('enabled')} /> 启用</label>
      <label>提示词<textarea aria-label="定时任务提示词" value={form.prompt} onChange={update('prompt')} rows={5} /></label>
      <div className="task-actions">
        <button type="button" className="ghost" onClick={onCancel}><X size={15} /> 取消</button>
        <button type="submit" disabled={Boolean(actioning)}><Save size={15} /> {actioning ? '保存中...' : '保存'}</button>
      </div>
    </form>
  );
}

function CronJobCard({ actioning, index, job, onDelete, onEdit, onRuns, onRun, onToggle }) {
  const busy = actioning.endsWith(':' + job.id);
  return (
    <article className="cron-card" data-testid={`cron-job-${index}`}>
      <div className="cron-card-head">
        <div>
          <strong>{cronJobTitle(job, index)}</strong>
          <span>{firstText(job.schedule_expr, job.scheduleExpr, '-')}</span>
        </div>
        <em className={job.enabled ? 'enabled' : 'disabled'}>{job.enabled ? '启用' : '停用'}</em>
      </div>
      <dl>
        <dt>状态</dt><dd>{cronStatus(job)}</dd>
        <dt>工作目录</dt><dd>{firstText(job.cwd, '-')}</dd>
        <dt>下次运行</dt><dd>{sharedFileTimestamp(job.next_run_at || job.nextRunAt)}</dd>
        <dt>最近运行</dt><dd>{sharedFileTimestamp(job.last_run_at || job.lastRunAt)}</dd>
        <dt>失败次数</dt><dd>{firstText(job.failure_count, job.failureCount, 0)} / {firstText(job.max_attempts, job.maxAttempts, 0)}</dd>
      </dl>
      {job.last_error ? <p className="danger-text">{job.last_error}</p> : null}
      <div className="task-actions">
        <button type="button" className="ghost" disabled={busy} onClick={() => onRuns(job)}><History size={15} /> 历史</button>
        <button type="button" className="ghost" disabled={busy} onClick={() => onEdit(job)}><Pencil size={15} /> 编辑</button>
        <button type="button" className="ghost" disabled={busy} onClick={() => onToggle(job)}>{job.enabled ? '停用' : '启用'}</button>
        <button type="button" disabled={busy || !job.enabled} onClick={() => onRun(job)}><Play size={15} /> 立即运行</button>
        <button type="button" className="danger" disabled={busy} onClick={() => onDelete(job)}><Trash2 size={15} /> 删除</button>
      </div>
    </article>
  );
}

function CronRunsPanel({ job, onClose, query }) {
  const runs = query.data || [];
  return (
    <section className="cron-runs-panel" data-testid="cron-runs-panel">
      <div className="detail-top">
        <h2>{firstText(job.name, job.id)} 运行历史</h2>
        <button type="button" className="ghost" onClick={onClose}><X size={15} /> 关闭</button>
      </div>
      {query.isLoading ? <p className="console-message">正在加载运行历史...</p> : null}
      {query.error ? <p className="danger-text" role="alert">{errorMessage(query.error)}</p> : null}
      {!query.isLoading && !query.error && runs.length === 0 ? <p className="console-message">暂无运行记录</p> : null}
      <div className="cron-run-list">
        {runs.map((run, index) => (
          <article className="cron-run-row" key={firstText(run.id, index)}>
            <strong>{cronRunStatus(run)}</strong>
            <span>{sharedFileTimestamp(run.submitted_at || run.submittedAt || run.created_at || run.createdAt)}</span>
            <em>{firstText(run.thread_id, run.threadId, run.turn_id, run.turnId, '-')}</em>
            {run.error ? <p className="danger-text">{run.error}</p> : null}
          </article>
        ))}
      </div>
    </section>
  );
}

function CronDeletePanel({ actioning, job, onCancel, onConfirm }) {
  const busy = actioning === 'delete:' + job.id;
  return (
    <dialog className="cron-delete-panel" open aria-labelledby="cron-delete-panel-title" data-testid="cron-delete-panel">
      <div className="detail-top">
        <h2 id="cron-delete-panel-title">删除定时任务</h2>
        <button type="button" className="ghost" disabled={busy} onClick={onCancel}><X size={15} /> 关闭</button>
      </div>
      <p>确认删除 “{firstText(job.name, job.id)}”？该操作不可撤销。</p>
      <div className="task-actions">
        <button type="button" className="ghost" disabled={busy} onClick={onCancel}>取消</button>
        <button type="button" className="danger" disabled={busy} onClick={() => { void onConfirm(); }}>{busy ? '删除中...' : '确认删除'}</button>
      </div>
    </dialog>
  );
}

export { TasksPage };

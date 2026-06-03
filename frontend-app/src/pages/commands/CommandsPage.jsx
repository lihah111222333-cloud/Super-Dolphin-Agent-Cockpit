import React, { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { RefreshCw, Send, Terminal } from 'lucide-react';
import { getDashboardPage } from '../../shared/api/backendApi.js';
import { dashboardQueryErrorState, dashboardQueryKey, errorMessage, firstText, optionalSettingsCwd, queryHasSnapshot, sharedFileTimestamp, SKILLS_REQUEST_TIMEOUT_MS, textValue, useDashboardFocusInvalidation, withTimeout } from '../shared/pageShared.js';
import { PageHeader, RetryableSyncError } from '../shared/pageComponents.jsx';

async function fetchCommandsDashboard(cwd) {
  const response = await withTimeout(
    getDashboardPage({ cwd, page: 'commands' }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '命令卡加载超时，请检查 dashboard 后端状态。',
  );
  return normalizeCommandsResponse(response);
}

function normalizeCommandsResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('commands dashboard response must be an object');
  }
  if (!Array.isArray(response.commandCards)) {
    throw new Error('commands dashboard response commandCards must be an array');
  }
  return response.commandCards;
}

function commandKey(card, index) {
  return firstText(card.card_key, card.cardKey, card.id, `command:${index}`);
}

function commandTitle(card, index) {
  return firstText(card.title, card.card_key, card.cardKey, `命令卡 ${index + 1}`);
}

function commandTemplate(card) {
  return textValue(card.command_template || card.commandTemplate);
}

function riskLabel(card) {
  return firstText(card.risk_level, card.riskLevel, '-');
}

function CommandsPage({ projectPath, store, refreshKey = 0 }) {
  const projectCwd = optionalSettingsCwd(projectPath);
  const queryClient = useQueryClient();
  const queryKey = useMemo(() => dashboardQueryKey(projectCwd, 'commands'), [projectCwd]);
  const [actioning, setActioning] = useState('');
  const [actionError, setActionError] = useState('');
  const [notice, setNotice] = useState('');
  useDashboardFocusInvalidation(projectCwd, 'commands');

  const query = useQuery({
    queryKey,
    queryFn: () => fetchCommandsDashboard(projectCwd),
    enabled: Boolean(projectCwd),
  });

  useEffect(() => {
    if (!projectCwd || Number(refreshKey || 0) <= 0) return;
    void queryClient.invalidateQueries({ queryKey });
  }, [projectCwd, queryClient, queryKey, refreshKey]);

  const syncError = dashboardQueryErrorState(query, queryHasSnapshot(query));
  const cards = useMemo(() => query.data || [], [query.data]);
  const refresh = () => queryClient.invalidateQueries({ queryKey });

  const runCommand = async (card, index) => {
    const key = commandKey(card, index);
    setActioning(key);
    setActionError('');
    setNotice('');
    try {
      if (typeof store?.runDashboardCommand !== 'function') throw new Error('命令卡发送链路不可用');
      await store.runDashboardCommand(card);
      setNotice('命令卡已发送到会话');
    } catch (err) {
      const message = errorMessage(err);
      setActionError(message);
      store?.addWarning?.('error', 'dashboard.command.run.failed', { card: key, error: message });
    } finally {
      setActioning('');
    }
  };

  return (
    <section className="commands-page" data-testid="commands-page">
      <PageHeader
        icon={Terminal}
        title="命令卡"
        subtitle={projectCwd ? '当前项目：' + projectCwd : '正在连接本地项目...'}
        actions={<button type="button" className="ghost" onClick={() => { void refresh(); }}><RefreshCw size={15} /> 刷新</button>}
      />
      {syncError.cachedSyncError ? <RetryableSyncError className="danger-text workflow-sync-alert" message={syncError.cachedSyncError} onRetry={refresh} /> : null}
      {syncError.blockingError ? <p className="danger-text" role="alert">{syncError.blockingError}</p> : null}
      {actionError ? <p className="danger-text" role="alert">{actionError}</p> : null}
      {notice ? <output className="settings-status">{notice}</output> : null}
      {query.isLoading ? <p className="console-message">正在加载命令卡...</p> : null}
      {!query.isLoading && !syncError.blockingError && cards.length === 0 ? <CommandsEmptyState /> : null}
      <div className="command-card-list" data-testid="commands-list">
        {cards.map((card, index) => (
          <CommandCard
            actioning={actioning}
            card={card}
            index={index}
            key={commandKey(card, index)}
            onRun={runCommand}
          />
        ))}
      </div>
    </section>
  );
}

function CommandsEmptyState() {
  return (
    <div className="empty-state" data-testid="commands-empty-state">
      <Terminal size={34} aria-hidden="true" />
      <h3>暂无命令卡</h3>
      <p>后端没有返回可展示的 commandCards。</p>
    </div>
  );
}

function CommandCard({ actioning, card, index, onRun }) {
  const key = commandKey(card, index);
  const template = commandTemplate(card);
  const disabled = card.enabled === false || !template;
  const busy = actioning === key;
  return (
    <article className="command-card" data-testid={`command-card-${index}`}>
      <div className="command-card-head">
        <div>
          <strong>{commandTitle(card, index)}</strong>
          <span>{firstText(card.card_key, card.cardKey, '-')}</span>
        </div>
        <em>{riskLabel(card)}</em>
      </div>
      {card.description ? <p>{card.description}</p> : null}
      <dl>
        <dt>状态</dt><dd>{card.enabled === false ? '停用' : '启用'}</dd>
        <dt>运行次数</dt><dd>{firstText(card.run_count, card.runCount, 0)}</dd>
        <dt>最近运行</dt><dd>{sharedFileTimestamp(card.last_run_at || card.lastRunAt)}</dd>
      </dl>
      <pre className="command-template"><code>{template || 'command_template 为空'}</code></pre>
      <div className="task-actions">
        <button type="button" disabled={disabled || busy} title={disabled ? '命令卡未启用或没有 command_template' : ''} onClick={() => { void onRun(card, index); }}>
          <Send size={15} /> {busy ? '发送中...' : '发送到当前会话'}
        </button>
      </div>
    </article>
  );
}

export { CommandsPage };

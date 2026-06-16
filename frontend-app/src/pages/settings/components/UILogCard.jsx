import React, { useCallback, useState } from 'react';
import { textValue } from '../../shared/pageShared.js';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './UILogCard.css';

function UILogCard({ loadLogs, store }) {
  const [remoteLogs, setRemoteLogs] = useState([]);
  const [logError, setLogError] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const refreshLogs = useCallback(async () => {
    setRefreshing(true);
    setLogError('');
    try {
      setRemoteLogs(normalizeDashboardLogs(await loadLogs()));
    } catch (error) {
      setLogError('刷新日志失败：' + (error?.message || error));
    } finally {
      setRefreshing(false);
    }
  }, [loadLogs]);
  const localLogs = store.logEntries ? store.logEntries.slice(0, 14) : [];
  const logList = remoteLogs.length > 0 ? remoteLogs : localLogs;
  return (
    <>
      <div className="section-header">UI LOG</div>
      <div className="data-card-vue settings-log-card" data-testid="settings-log-card">
        <div className="data-row-vue"><strong>日志级别</strong><span>{store.logLevel}</span></div>
        <UILogLevelRow store={store} />
        <div className="settings-action-row settings-log-action-row"><button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-log-refresh-button" onClick={() => { void refreshLogs(); }} disabled={refreshing}>{refreshing ? '刷新中...' : '刷新日志'}</button></div>
        {logError ? <SettingsPromptNotice notice={{ level: 'error', message: logError }} testId="settings-log-notice" /> : null}
        <UILogList logList={logList} />
      </div>
    </>
  );
}

function normalizeDashboardLogs(payload) {
  const list = Array.isArray(payload?.logs) ? payload.logs : [];
  return list.map(normalizeDashboardLogEntry);
}

function normalizeDashboardLogEntry(entry, index) {
  const scope = textValue(entry.component || entry.logger || entry.source || 'dashboard') || 'dashboard';
  const event = textValue(entry.event_type || entry.eventType || entry.message || entry.raw || `log.${entry.id || index}`);
  return {
    id: entry.id || `${scope}-${index}`,
    ts: entry.timestamp || entry.ts || entry.createdAt || entry.created_at,
    level: textValue(entry.level || 'info').toLowerCase() || 'info',
    scope,
    event,
    fields: entry,
  };
}

function UILogLevelRow({ store }) {
  return (
    <div className="settings-stall-row settings-log-control-row">
      <label className="settings-stall-label" htmlFor="settings-log-level-select">日志级别</label>
      <select id="settings-log-level-select" className="settings-stall-input settings-log-level-select" data-testid="settings-log-level-select" value={store.logLevel} onChange={(event) => store.setLogLevel(event.target.value)}>
        <option value="debug">debug（最详细）</option><option value="info">info（默认）</option><option value="warn">warn</option><option value="error">error（仅错误）</option>
      </select>
      <span className="settings-stall-unit">立即生效（跨 tab 同步）</span>
    </div>
  );
}

function UILogList({ logList }) {
  if (logList.length === 0) return <div className="settings-log-empty" data-testid="settings-log-empty">暂无日志</div>;
  return (
    <div className="settings-log-list" data-testid="settings-log-list">
      {logList.map((entry) => <UILogItem entry={entry} key={entry.seq || entry.id} />)}
    </div>
  );
}

function UILogItem({ entry }) {
  return (
    <div className="settings-log-item">
      <span className="settings-log-time">{formatLogTime(entry.ts)}</span>
      <span className={'settings-log-level is-' + entry.level}>{entry.level}</span>
      <span className="settings-log-event">{entry.scope}.{entry.event}</span>
    </div>
  );
}

function formatLogTime(value) {
  if (!value) return '--:--:--';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '--:--:--';
  return date.toLocaleTimeString('zh-CN', { hour12: false });
}

export { UILogCard };

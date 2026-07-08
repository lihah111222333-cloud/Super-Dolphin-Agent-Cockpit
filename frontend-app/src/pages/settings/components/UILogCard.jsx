import React, { useCallback, useState } from 'react';
import { optionalDateFromValue, firstPresentRawText, firstPresentText } from '../../shared/pageShared.js';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './UILogCard.css';

function UILogCard({ copy, loadLogs, store }) {
  const logsCopy = copy.logs;
  const [remoteLogs, setRemoteLogs] = useState([]);
  const [logError, setLogError] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const refreshLogs = useCallback(async () => {
    setRefreshing(true);
    setLogError('');
    try {
      setRemoteLogs(normalizeDashboardLogs(await loadLogs()));
    } catch (error) {
      setLogError(logsCopy.refreshFailed + firstPresentRawText(error?.message, error));
    } finally {
      setRefreshing(false);
    }
  }, [loadLogs, logsCopy]);
  const localLogs = store.logEntries ? store.logEntries.slice(0, 14) : [];
  const logList = remoteLogs.length > 0 ? remoteLogs : localLogs;
  return (
    <>
      <div className="section-header">{logsCopy.title}</div>
      <div className="data-card-vue settings-log-card" data-testid="settings-log-card">
        <div className="data-row-vue"><strong>{logsCopy.level}</strong><span>{store.logLevel}</span></div>
        <UILogLevelRow logsCopy={logsCopy} store={store} />
        <div className="settings-action-row settings-log-action-row"><button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-log-refresh-button" onClick={() => { void refreshLogs(); }} disabled={refreshing}>{refreshing ? logsCopy.refreshing : logsCopy.refresh}</button></div>
        {logError ? <SettingsPromptNotice notice={{ level: 'error', message: logError }} testId="settings-log-notice" /> : null}
        <UILogList logList={logList} logsCopy={logsCopy} />
      </div>
    </>
  );
}

function normalizeDashboardLogs(payload) {
  const list = Array.isArray(payload?.logs) ? payload.logs : [];
  return list.map(normalizeDashboardLogEntry);
}

function normalizeDashboardLogEntry(entry, index) {
  const scope = firstPresentText(entry.component, entry.logger, entry.source, 'dashboard');
  const event = firstPresentText(entry.event_type, entry.eventType, entry.message, entry.raw, `log.${firstPresentText(entry.id, index)}`);
  return {
    id: firstPresentText(entry.id, `${scope}-${index}`),
    ts: firstPresentRawText(entry.timestamp, entry.ts, entry.createdAt, entry.created_at),
    level: firstPresentText(entry.level, 'info').toLowerCase(),
    scope,
    event,
    fields: entry,
  };
}

function UILogLevelRow({ logsCopy, store }) {
  return (
    <div className="settings-stall-row settings-log-control-row">
      <label className="settings-stall-label" htmlFor="settings-log-level-select">{logsCopy.level}</label>
      <select id="settings-log-level-select" className="settings-stall-input settings-log-level-select" data-testid="settings-log-level-select" value={store.logLevel} onChange={(event) => store.setLogLevel(event.target.value)}>
        <option value="debug">{logsCopy.debug}</option><option value="info">{logsCopy.info}</option><option value="warn">{logsCopy.warn}</option><option value="error">{logsCopy.error}</option>
      </select>
      <span className="settings-stall-unit">{logsCopy.live}</span>
    </div>
  );
}

function UILogList({ logList, logsCopy }) {
  if (logList.length === 0) return <div className="settings-log-empty" data-testid="settings-log-empty">{logsCopy.empty}</div>;
  return (
    <div className="settings-log-list" data-testid="settings-log-list">
      {logList.map((entry) => <UILogItem entry={entry} key={entry.seq || entry.id} locale={logsCopy.timeLocale} />)}
    </div>
  );
}

function UILogItem({ entry, locale }) {
  return (
    <div className="settings-log-item">
      <span className="settings-log-time">{formatLogTime(entry.ts, locale)}</span>
      <span className={'settings-log-level is-' + entry.level}>{entry.level}</span>
      <span className="settings-log-event">{entry.scope}.{entry.event}</span>
    </div>
  );
}

function formatLogTime(value, locale) {
  if (!value) return '--:--:--';
  const date = optionalDateFromValue(value, 'settings log timestamp');
  if (!date) return '--:--:--';
  return date.toLocaleTimeString(locale, { hour12: false });
}

export { UILogCard };

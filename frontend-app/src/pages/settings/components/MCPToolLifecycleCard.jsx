import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { listMCPServers, listMCPToolLifecycleStates, upsertMCPToolLifecycleState } from '../services/settingsPageService.js';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './MCPToolLifecycleCard.css';

const LIFECYCLE_STATES = Object.freeze(['active', 'suspended', 'removed']);

// useMCPToolLifecycleSettings 只管理当前 settings 卡片的加载/保存状态，后端仍是 lifecycle 事实来源。
function useMCPToolLifecycleSettings(cwd, copy) {
  const lifecycleCopy = copy.mcpLifecycle;
  const currentCwd = textValue(cwd);
  const loadRequestRef = useRef(0);
  const [records, setRecords] = useState([]);
  const [loading, setLoading] = useState(false);
  const [savingIds, setSavingIds] = useState({});
  const [notice, setNotice] = useState({ level: 'info', message: '' });

  const load = useCallback(async () => {
    const requestCwd = currentCwd;
    const requestId = loadRequestRef.current + 1;
    loadRequestRef.current = requestId;
    const isCurrent = () => loadRequestRef.current === requestId;

    if (!requestCwd) {
      setRecords([]);
      setLoading(false);
      return;
    }

    setLoading(true);
    try {
      const serverNames = normalizeMCPServerNames(await listMCPServers());
      const payloads = await Promise.all(serverNames.map((serverName) => (
        listMCPToolLifecycleStates({ workspaceRoot: requestCwd, serverName })
      )));
      if (isCurrent()) {
        setRecords(normalizeLifecyclePayloads(payloads));
        setNotice({ level: 'info', message: '' });
      }
    } catch (error) {
      if (isCurrent()) setNotice({ level: 'error', message: lifecycleCopy.loadFailed + (error?.message || error) });
    } finally {
      if (isCurrent()) setLoading(false);
    }
  }, [currentCwd, lifecycleCopy]);

  const upsert = useCallback(async (record, state, reason) => {
    if (!currentCwd || savingIds[record.id]) return;
    const previousRecords = records;
    const nextState = normalizeLifecycleState(state);
    const nextReason = textValue(reason);
    setSavingIds((current) => ({ ...current, [record.id]: true }));
    setRecords((current) => current.map((item) => (
      item.id === record.id ? { ...item, reason: nextReason, state: nextState } : item
    )));
    try {
      const saved = normalizeLifecycleRecord(await upsertMCPToolLifecycleState({
        workspaceRoot: currentCwd,
        serverName: record.serverName,
        toolName: record.toolName,
        state: nextState,
        reason: nextReason,
      }));
      setRecords((current) => current.map((item) => (item.id === record.id ? saved : item)));
      setNotice({ level: 'info', message: savedLifecycleMessage(lifecycleCopy, saved) });
    } catch (error) {
      setRecords(previousRecords);
      setNotice({ level: 'error', message: lifecycleCopy.saveFailed + (error?.message || error) });
    } finally {
      setSavingIds((current) => ({ ...current, [record.id]: false }));
    }
  }, [currentCwd, lifecycleCopy, records, savingIds]);

  const updateReason = useCallback((record, reason) => {
    setRecords((current) => current.map((item) => (
      item.id === record.id ? { ...item, reason: textValue(reason) } : item
    )));
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return { lifecycleCopy, load, loading, notice, records, savingIds, updateReason, upsert };
}

function MCPToolLifecycleCard({ copy, cwd }) {
  const lifecycle = useMCPToolLifecycleSettings(cwd, copy);
  const summary = useMemo(() => lifecycleSummary(lifecycle.records, lifecycle.lifecycleCopy), [lifecycle.lifecycleCopy, lifecycle.records]);
  return (
    <>
      <div className="section-header">{lifecycle.lifecycleCopy.title}</div>
      <div className="data-card-vue settings-mcp-lifecycle-card" data-testid="settings-mcp-lifecycle-card">
        <div className="data-row-vue">
          <strong>{lifecycle.lifecycleCopy.switchTitle}</strong>
          <span data-testid="settings-mcp-lifecycle-summary">{lifecycle.loading ? lifecycle.lifecycleCopy.loading : summary}</span>
        </div>
        <div className="settings-prompt-desc">{lifecycle.lifecycleCopy.description}</div>
        {lifecycle.notice.message ? <SettingsPromptNotice notice={lifecycle.notice} testId="settings-mcp-lifecycle-notice" /> : null}
        <MCPToolLifecycleContent lifecycle={lifecycle} />
        <div className="settings-action-row">
          <button type="button" className="btn btn-secondary btn-toolbar-sm" onClick={() => void lifecycle.load()} disabled={lifecycle.loading}>
            {lifecycle.loading ? lifecycle.lifecycleCopy.loading : lifecycle.lifecycleCopy.refresh}
          </button>
        </div>
      </div>
    </>
  );
}

function MCPToolLifecycleContent({ lifecycle }) {
  const { lifecycleCopy, loading, records } = lifecycle;
  if (records.length === 0 && !loading) {
    return <div className="settings-log-empty" data-testid="settings-mcp-lifecycle-empty">{lifecycleCopy.empty}</div>;
  }
  return (
    <div className="settings-mcp-lifecycle-list" aria-label={lifecycleCopy.controls} data-testid="settings-mcp-lifecycle-list">
      <div className="settings-mcp-lifecycle-row settings-mcp-lifecycle-head">
        <span>{lifecycleCopy.server}</span>
        <span>{lifecycleCopy.tool}</span>
        <span>{lifecycleCopy.state}</span>
        <span>{lifecycleCopy.reason}</span>
        <span aria-hidden="true" />
      </div>
      {records.map((record) => (
        <MCPToolLifecycleRow lifecycle={lifecycle} record={record} key={record.id} />
      ))}
    </div>
  );
}

function MCPToolLifecycleRow({ lifecycle, record }) {
  const { lifecycleCopy, savingIds, updateReason, upsert } = lifecycle;
  const saving = Boolean(savingIds[record.id]);
  const labelPrefix = record.serverName + '/' + record.toolName;
  return (
    <div className="settings-mcp-lifecycle-row" data-testid={'settings-mcp-lifecycle-row-' + record.id}>
      <span className="settings-mcp-lifecycle-server">{record.serverName}</span>
      <span className="settings-mcp-lifecycle-tool">{record.toolName}</span>
      <select
        aria-label={labelPrefix + ' ' + lifecycleCopy.state}
        className={'settings-mcp-lifecycle-state is-' + record.state}
        data-testid={'settings-mcp-lifecycle-state-' + record.id}
        disabled={saving}
        value={record.state}
        onChange={(event) => void upsert(record, event.target.value, record.reason)}
      >
        {LIFECYCLE_STATES.map((state) => <option key={state} value={state}>{lifecycleCopy[state]}</option>)}
      </select>
      <input
        aria-label={labelPrefix + ' ' + lifecycleCopy.reason}
        className="settings-mcp-lifecycle-reason"
        data-testid={'settings-mcp-lifecycle-reason-' + record.id}
        disabled={saving}
        placeholder={lifecycleCopy.reasonPlaceholder}
        value={record.reason}
        onChange={(event) => updateReason(record, event.target.value)}
      />
      <button
        type="button"
        className="btn btn-secondary btn-toolbar-sm"
        data-testid={'settings-mcp-lifecycle-save-' + record.id}
        disabled={saving}
        onClick={() => void upsert(record, record.state, record.reason)}
      >
        {saving ? lifecycleCopy.saving : lifecycleCopy.saveReason}
      </button>
    </div>
  );
}

function normalizeLifecyclePayload(payload) {
  const list = Array.isArray(payload) ? payload : payload?.records;
  if (!Array.isArray(list)) throw new Error('mcp tool lifecycle response must be an array or { records }');
  return list.map(normalizeLifecycleRecord);
}

function normalizeLifecyclePayloads(payloads) {
  return payloads.flatMap(normalizeLifecyclePayload).sort(compareLifecycleRecords);
}

function normalizeMCPServerNames(payload) {
  const servers = payload?.mcpServers || payload?.mcp_servers;
  if (!servers || typeof servers !== 'object' || Array.isArray(servers)) {
    throw new Error('mcp server list response must include mcpServers');
  }
  return Object.keys(servers).map(textValue).filter(Boolean).sort();
}

function normalizeLifecycleRecord(payload) {
  const record = payload?.record && typeof payload.record === 'object' ? payload.record : payload;
  if (!record || typeof record !== 'object' || Array.isArray(record)) {
    throw new Error('mcp tool lifecycle record must be an object');
  }
  const serverName = textValue(record.serverName || record.server_name);
  const toolName = textValue(record.toolName || record.tool_name);
  const state = normalizeLifecycleState(record.state || record.lifecycleState || record.lifecycle_state);
  if (!serverName) throw new Error('mcp tool lifecycle record serverName is required');
  if (!toolName) throw new Error('mcp tool lifecycle record toolName is required');
  return {
    id: lifecycleRecordId(serverName, toolName),
    reason: textValue(record.reason),
    serverName,
    state,
    toolName,
  };
}

function normalizeLifecycleState(value) {
  const state = textValue(value);
  if (!LIFECYCLE_STATES.includes(state)) throw new Error('mcp tool lifecycle state must be active, suspended, or removed');
  return state;
}

function lifecycleRecordId(serverName, toolName) {
  return serverName + '-' + toolName;
}

function lifecycleSummary(records, copy) {
  const counts = Object.fromEntries(LIFECYCLE_STATES.map((state) => [state, 0]));
  records.forEach((record) => { counts[record.state] += 1; });
  return copy.summary
    .replace('{total}', String(records.length))
    .replace('{active}', String(counts.active))
    .replace('{suspended}', String(counts.suspended))
    .replace('{removed}', String(counts.removed));
}

function savedLifecycleMessage(copy, record) {
  return copy.saved
    .replace('{server}', record.serverName)
    .replace('{tool}', record.toolName)
    .replace('{state}', record.state);
}

function compareLifecycleRecords(left, right) {
  return (left.serverName + '\0' + left.toolName).localeCompare(right.serverName + '\0' + right.toolName);
}

function textValue(value) {
  return (value || '').toString().trim();
}

export { MCPToolLifecycleCard };

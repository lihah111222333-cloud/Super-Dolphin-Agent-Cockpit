import React, { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { Copy } from 'lucide-react';
import { getObservabilityTrace, listObservabilityRecent as getObservabilityRecent, copyTextToClipboard } from '../../shared/api/backendApi.js';
import { errorMessage, textValue } from '../shared/pageShared.js';

const recentLogRefreshIntervalMS = 2000;

const OBSERVABILITY_PAGE_INITIAL_STATE = Object.freeze({
  copiedTraceId: '',
  loading: false,
  notice: '',
  recentResult: null,
});

function observabilityPageReducer(state, action) {
  switch (action.type) {
    case 'copy/success':
      return { ...state, copiedTraceId: action.traceId };
    case 'notice/set':
      return { ...state, notice: action.message };
    case 'query/finish':
      return { ...state, loading: false };
    case 'query/start':
      return { ...state, copiedTraceId: '', loading: true, notice: '' };
    case 'recent/set':
      return { ...state, notice: action.clearNotice ? '' : state.notice, recentResult: action.result };
    default:
      return state;
  }
}

function stableBackendLogValue(value, seen = new WeakSet()) {
  if (!value || typeof value !== 'object') return value;
  if (seen.has(value)) return '[Circular]';
  seen.add(value);
  if (Array.isArray(value)) {
    const items = value.map((item) => stableBackendLogValue(item, seen));
    seen.delete(value);
    return items;
  }
  const record = Object.fromEntries(
    Object.keys(value)
      .sort()
      .map((key) => [key, stableBackendLogValue(value[key], seen)]),
  );
  seen.delete(value);
  return record;
}

function formatObservabilityTimestamp(value) {
  const text = textValue(value);
  if (!text) return '-';
  const matched = text.match(/^(\d{4})-(\d{2})-(\d{2})[T\s](\d{2}):(\d{2}):(\d{2})/);
  if (matched) {
    const [, year, month, day, hour, minute, second] = matched;
    return `${year}-${month}-${day} ${hour}:${minute}:${second}`;
  }
  const parsed = new Date(text);
  if (Number.isNaN(parsed.getTime())) return text;
  const year = String(parsed.getFullYear()).padStart(4, '0');
  const month = String(parsed.getMonth() + 1).padStart(2, '0');
  const day = String(parsed.getDate()).padStart(2, '0');
  const hour = String(parsed.getHours()).padStart(2, '0');
  const minute = String(parsed.getMinutes()).padStart(2, '0');
  const second = String(parsed.getSeconds()).padStart(2, '0');
  return `${year}-${month}-${day} ${hour}:${minute}:${second}`;
}

function formatObservabilityDuration(value) {
  if (value === null || value === undefined || value === '') return '耗时未记录';
  const duration = Number(value);
  if (!Number.isFinite(duration) || duration <= 0) return '耗时未记录';
  return `${duration}ms`;
}

function useObservabilityFilters() {
  const [filters, setFilters] = useState({
    agentId: '',
    component: '',
    keyword: '',
    limit: '50',
    method: '',
    status: '',
    threadId: '',
    traceId: '',
  });
  const setFilter = useCallback((key, value) => {
    setFilters((current) => ({ ...current, [key]: value }));
  }, []);
  const queryLimit = useMemo(() => {
    const value = Number(filters.limit);
    return Number.isInteger(value) && value > 0 ? value : 50;
  }, [filters.limit]);
  const buildRecentParams = useCallback((overrides = {}) => ({
    limit: queryLimit,
    status: overrides.status ?? filters.status.trim(),
    component: overrides.component ?? filters.component.trim(),
    method: overrides.method ?? filters.method.trim(),
    traceId: overrides.traceId ?? filters.traceId.trim(),
    threadId: overrides.threadId ?? filters.threadId.trim(),
    agentId: overrides.agentId ?? filters.agentId.trim(),
    keyword: overrides.keyword ?? filters.keyword.trim(),
  }), [filters, queryLimit]);
  return { buildRecentParams, filters, queryLimit, setFilter };
}

function useObservabilityTraceExpansion({ queryLimit, setFilter, setNotice }) {
  const [expandedTraces, setExpandedTraces] = useState({});
  const toggleTraceExpansion = useCallback(async (value) => {
    const nextTraceId = textValue(value);
    if (!nextTraceId) return;
    const currentEntry = expandedTraces[nextTraceId];
    if (currentEntry?.expanded) {
      setExpandedTraces((current) => ({ ...current, [nextTraceId]: { ...current[nextTraceId], expanded: false } }));
      return;
    }
    setNotice('');
    setFilter('traceId', nextTraceId);
    if (currentEntry?.result && currentEntry.limit === queryLimit) {
      setExpandedTraces((current) => ({
        ...current,
        [nextTraceId]: { ...current[nextTraceId], expanded: true, loading: false, error: '' },
      }));
      return;
    }
    setExpandedTraces((current) => ({
      ...current,
      [nextTraceId]: { ...current[nextTraceId], expanded: true, loading: true, error: '', limit: queryLimit },
    }));
    try {
      const trace = await getObservabilityTrace({ traceId: nextTraceId, limit: queryLimit });
      setExpandedTraces((current) => expandedTraceLoadedState(current, nextTraceId, trace, queryLimit));
    }
    catch (error) {
      const message = errorMessage(error);
      setNotice(message);
      setExpandedTraces((current) => expandedTraceErrorState(current, nextTraceId, message, queryLimit));
    }
  }, [expandedTraces, queryLimit, setExpandedTraces, setFilter, setNotice]);
  return { expandedTraces, setExpandedTraces, toggleTraceExpansion };
}

function expandedTraceLoadedState(current, traceId, trace, queryLimit) {
  const latestEntry = current[traceId] || {};
  return {
    ...current,
    [traceId]: {
      ...latestEntry,
      expanded: latestEntry.expanded !== false,
      loading: false,
      error: '',
      result: trace,
      limit: queryLimit,
    },
  };
}

function expandedTraceErrorState(current, traceId, message, queryLimit) {
  return {
    ...current,
    [traceId]: {
      ...current[traceId],
      expanded: true,
      loading: false,
      error: message,
      limit: queryLimit,
    },
  };
}

function ObservabilityPage() {
  const { buildRecentParams, filters, queryLimit, setFilter } = useObservabilityFilters();
  const [pageState, dispatchPageState] = useReducer(observabilityPageReducer, OBSERVABILITY_PAGE_INITIAL_STATE);
  const activeRecentParamsRef = useRef(null);
  const { copiedTraceId, loading, notice, recentResult } = pageState;
  const setNotice = useCallback((message) => {
    dispatchPageState({ type: 'notice/set', message });
  }, []);
  const { expandedTraces, setExpandedTraces, toggleTraceExpansion } = useObservabilityTraceExpansion({ queryLimit, setFilter, setNotice });

  const refreshRecent = useCallback(async (params) => {
    dispatchPageState({ type: 'recent/set', result: await getObservabilityRecent(params), clearNotice: false });
  }, []);

  useEffect(() => {
    let disposed = false;
    let refreshInFlight = false;
    const refresh = async () => {
      const activeRecentParams = activeRecentParamsRef.current;
      if (!activeRecentParams) return;
      if (refreshInFlight) return;
      refreshInFlight = true;
      try {
        const result = await getObservabilityRecent(activeRecentParams);
        if (!disposed) {
          dispatchPageState({ type: 'recent/set', result, clearNotice: true });
        }
      }
      catch (error) {
        if (!disposed) dispatchPageState({ type: 'notice/set', message: errorMessage(error) });
      }
      finally {
        refreshInFlight = false;
      }
    };
    const intervalID = window.setInterval(refresh, recentLogRefreshIntervalMS);
    return () => {
      disposed = true;
      window.clearInterval(intervalID);
    };
  }, []);

  const copyTraceId = useCallback(async (value) => {
    const nextTraceId = textValue(value);
    if (!nextTraceId) return;
    setNotice('');
    try {
      await copyTextToClipboard(nextTraceId);
      dispatchPageState({ type: 'copy/success', traceId: nextTraceId });
    }
    catch (error) {
      setNotice(`复制 Trace ID 失败：${errorMessage(error)}`);
    }
  }, [setNotice]);

  const runQuery = useCallback(async () => {
    dispatchPageState({ type: 'query/start' });
    activeRecentParamsRef.current = null;
    setExpandedTraces({});
    const params = buildRecentParams();
    try {
      await refreshRecent(params);
      activeRecentParamsRef.current = params;
    }
    catch (error) {
      setNotice(errorMessage(error));
    }
    finally {
      dispatchPageState({ type: 'query/finish' });
    }
  }, [buildRecentParams, refreshRecent, setExpandedTraces, setNotice]);

  return (
    <section className="settings-page observability-page" data-testid="observability-page">
      <ObservabilityHeader />
      {notice ? <div className="settings-alert error" role="alert">{notice}</div> : null}
      <ObservabilitySearchForm filters={filters} loading={loading} onFilter={setFilter} onSubmit={runQuery} />
      <ObservabilityRecentLogs
        result={recentResult}
        onOpenTrace={toggleTraceExpansion}
        onCopyTrace={copyTraceId}
        copiedTraceId={copiedTraceId}
        expandedTraces={expandedTraces}
      />
    </section>
  );
}

function ObservabilityHeader() {
  return (
    <div className="settings-header">
      <div>
        <h1>链路追踪</h1>
      </div>
    </div>
  );
}

function ObservabilitySearchForm({ filters, loading, onFilter, onSubmit }) {
  const submit = (event) => {
    event.preventDefault();
    void onSubmit();
  };
  return (
    <form className="observability-search" onSubmit={submit} aria-busy={loading}>
      <div className="observability-filter-grid">
        <ObservabilityTextFilter label="Trace ID" value={filters.traceId} placeholder="00-... 或 trace_id" onChange={(value) => onFilter('traceId', value)} />
        <ObservabilityTextFilter label="Thread ID" value={filters.threadId} placeholder="thread_..." onChange={(value) => onFilter('threadId', value)} />
        <ObservabilityTextFilter label="Agent ID" value={filters.agentId} placeholder="agent_..." onChange={(value) => onFilter('agentId', value)} />
        <ObservabilityTextFilter label="组件" value={filters.component} placeholder="rpc / tool / wails" onChange={(value) => onFilter('component', value)} />
        <ObservabilityStatusFilter value={filters.status} onChange={(value) => onFilter('status', value)} />
        <ObservabilityTextFilter label="Method" value={filters.method} placeholder="thread/start" onChange={(value) => onFilter('method', value)} />
        <ObservabilityTextFilter label="关键词" value={filters.keyword} placeholder="消息 / request id / method" onChange={(value) => onFilter('keyword', value)} />
        <ObservabilityTextFilter label="Limit" value={filters.limit} inputMode="numeric" onChange={(value) => onFilter('limit', value)} />
      </div>
      <div className="settings-actions">
        <button type="submit" className="btn primary" disabled={loading}>{loading ? '查询中...' : '查询最新日志'}</button>
      </div>
    </form>
  );
}

function ObservabilityTextFilter({ inputMode, label, placeholder = '', value, onChange }) {
  return (
    <label>
      {label}
      <input value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} inputMode={inputMode} />
    </label>
  );
}

function ObservabilityStatusFilter({ value, onChange }) {
  return (
    <label>
      状态
      <select value={value} onChange={(event) => onChange(event.target.value)}>
        <option value="">全部</option>
        <option value="ok">ok</option>
        <option value="slow">slow</option>
        <option value="error">error</option>
        <option value="panic">panic</option>
        <option value="sampled">sampled</option>
        <option value="dropped_summary">dropped_summary</option>
      </select>
    </label>
  );
}

function ObservabilityRecentLogs({ result, onOpenTrace, onCopyTrace, copiedTraceId, expandedTraces }) {
  if (!result) return null;
  const traceRows = groupObservabilityTraceRows(result.events);
  const eventCount = traceRows.reduce((total, row) => total + row.events.length, 0);
  return (
    <div className="settings-card observability-result observability-system-log" data-testid="observability-recent-logs">
      <div className="observability-result-header">
        <div>
          <h2>最新日志</h2>
          <p>{traceRows.length} 条 trace · {eventCount} 个匹配 event · source={result.source || 'memory'} · truncated={String(Boolean(result.truncated))}</p>
        </div>
      </div>
      {traceRows.length === 0 ? (
        <div className="empty-state">没有匹配的最近请求</div>
      ) : (
        <table className="observability-log-table">
          <thead className="observability-log-table-head">
            <tr>
              <th scope="col">时间</th>
              <th scope="col">状态</th>
              <th scope="col">请求摘要</th>
              <th scope="col">操作</th>
            </tr>
          </thead>
          <tbody>
            {traceRows.map((row) => (
              <ObservabilityLogTableRow
                row={row}
                onOpenTrace={onOpenTrace}
                onCopyTrace={onCopyTrace}
                copied={Boolean(row.traceID) && row.traceID === copiedTraceId}
                traceState={row.traceID ? expandedTraces[row.traceID] : undefined}
                key={row.key}
              />
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function groupObservabilityTraceRows(events) {
  const rowsByTrace = new Map();
  const source = Array.isArray(events) ? events : [];
  for (let index = 0; index < source.length; index += 1) {
    const event = source[index];
    const traceID = textValue(event.trace_id);
    const rowKey = traceID || observabilityEventRowKey(event, index);
    const existing = rowsByTrace.get(rowKey);
    if (!existing) {
      rowsByTrace.set(rowKey, { key: rowKey, traceID, events: [event], representative: event, firstIndex: index });
      continue;
    }
    existing.events.push(event);
    existing.representative = preferredObservabilityRepresentative(existing.representative, event);
  }
  return Array.from(rowsByTrace.values()).map((row) => {
    const status = worstObservabilityStatus(row.events);
    const durationMS = row.events.reduce((total, event) => total + (Number(event.duration_ms) || 0), 0);
    return {
      ...row,
      status,
      durationMS,
      timestamp: latestObservabilityTimestamp(row.events),
      eventCount: row.events.length,
      error: row.events.find((event) => textValue(event.error))?.error || '',
    };
  }).sort(compareObservabilityTraceRows);
}

function compareObservabilityTraceRows(left, right) {
  const leftTime = observabilityTimestampMillis(left.timestamp);
  const rightTime = observabilityTimestampMillis(right.timestamp);
  if (leftTime !== rightTime) return rightTime - leftTime;
  return (left.firstIndex || 0) - (right.firstIndex || 0);
}

function observabilityTimestampMillis(value) {
  const parsed = Date.parse(textValue(value));
  return Number.isFinite(parsed) ? parsed : 0;
}

function observabilityEventRowKey(event, index) {
  const parts = [event.ts, event.span_id, event.method, event.phase, event.kind]
    .map(textValue)
    .filter(Boolean);
  return `event-${parts.join(':') || 'unknown'}-${index}`;
}

function preferredObservabilityRepresentative(current, next) {
  if (!current) return next;
  if (observabilityEventPriority(next) > observabilityEventPriority(current)) return next;
  return current;
}

function observabilityEventPriority(event) {
  const kind = textValue(event.kind).toLowerCase();
  const phase = textValue(event.phase).toLowerCase();
  const method = textValue(event.method);
  if (kind === 'frontend' || phase.startsWith('frontend.')) return 4;
  if (method.startsWith('thread/') || method.startsWith('ui/') || method.startsWith('api/')) return 3;
  if (textValue(event.error)) return 2;
  return 1;
}

function worstObservabilityStatus(events) {
  const statuses = new Set((events || []).map((event) => textValue(event.status).toLowerCase()));
  if (statuses.has('panic')) return 'panic';
  if (statuses.has('error')) return 'error';
  if (statuses.has('slow')) return 'slow';
  if (statuses.has('sampled')) return 'sampled';
  if (statuses.has('dropped_summary')) return 'dropped_summary';
  return 'ok';
}

function latestObservabilityTimestamp(events) {
  let latestText = '';
  let latestValue = 0;
  for (const event of events || []) {
    const text = textValue(event.ts);
    const parsed = Date.parse(text);
    const value = Number.isFinite(parsed) ? parsed : 0;
    if (!latestText || value >= latestValue) {
      latestText = text;
      latestValue = value;
    }
  }
  return latestText;
}

function ObservabilityLogTableRow({ row, onOpenTrace, onCopyTrace, copied, traceState }) {
  const event = row.representative || {};
  const traceID = row.traceID;
  const summary = observabilityTraceSummary(row);
  const expanded = Boolean(traceState?.expanded);
  const detailId = observabilityTraceDetailId(traceID);
  const actionLabel = expanded ? '收起 Trace' : '打开 Trace';
  return (
    <>
      <tr className="observability-log-table-entry observability-log-table-row">
        <td>
          <time dateTime={row.timestamp}>{formatObservabilityTimestamp(row.timestamp)}</time>
        </td>
        <td>
          <span className={`observability-status-pill is-${observabilityStatusClass(row.status)}`}>{row.status || 'ok'}</span>
        </td>
        <td>
          <div className="observability-log-summary">
            <strong>{event.method || event.phase || event.kind || 'event'}</strong>
            <p>{summary}</p>
            <p className="observability-log-identifiers">
              trace={traceID || '-'} · thread={event.thread_id || '-'} · {row.eventCount} 个匹配 event
            </p>
            {row.error ? <p className="observability-event-error">{row.error}</p> : null}
          </div>
        </td>
        <td>
          <div className="observability-log-row-actions">
            <button
              type="button"
              className={`btn primary observability-copy-trace${copied ? ' is-copied' : ''}`}
              onClick={() => onCopyTrace(traceID)}
              disabled={!traceID}
              aria-label={`复制 Trace ID ${traceID || '-'}`}
            >
              <Copy size={14} />
              <span>{copied ? '已复制' : '复制 Trace ID'}</span>
            </button>
            <button
              type="button"
              className="btn secondary observability-open-trace"
              onClick={() => onOpenTrace(traceID)}
              disabled={!traceID}
              aria-controls={detailId}
              aria-expanded={expanded}
              aria-label={`${actionLabel} ${traceID || '-'}`}
            >
              {actionLabel}
            </button>
          </div>
        </td>
      </tr>
      {expanded ? (
        <tr className="observability-log-table-detail-row">
          <td colSpan={4}>
            <ObservabilityInlineTraceResult traceID={traceID} detailId={detailId} state={traceState} />
          </td>
        </tr>
      ) : null}
    </>
  );
}

function observabilityTraceDetailId(traceID) {
  const safeTraceID = textValue(traceID).replace(/[^a-zA-Z0-9_-]+/g, '-');
  return `observability-trace-detail-${safeTraceID || 'unknown'}`;
}

function observabilityTraceSummary(row) {
  const event = row.representative || {};
  const durationText = formatObservabilityDuration(row.durationMS);
  const parts = [
    event.kind,
    event.phase,
    event.client_route,
    event.agent_id ? `agent=${event.agent_id}` : '',
    event.call_id ? `call=${event.call_id}` : '',
    event.tool_name ? `tool=${event.tool_name}` : '',
    durationText,
  ].map(textValue).filter(Boolean);
  return parts.join(' · ');
}

function ObservabilityInlineTraceResult({ traceID, detailId, state }) {
  if (!state?.expanded) return null;
  const result = state.result;
  return (
    <section
      className="observability-log-trace-detail"
      data-testid={`observability-inline-trace-${traceID}`}
      id={detailId}
      aria-label={`Trace ${traceID || '-'} 结果`}
      aria-busy={state.loading}
    >
      <div className="observability-inline-trace-header">
        <div>
          <h3>Trace 结果</h3>
          {result ? (
            <p>source={result.source || 'memory'} total_duration_ms={result.total_duration_ms || 0} truncated={String(Boolean(result.truncated))}</p>
          ) : null}
        </div>
      </div>
      {state.loading ? <output className="empty-state">Trace 加载中...</output> : null}
      {state.error ? <div className="settings-alert error" role="alert">Trace 加载失败：{state.error}</div> : null}
      {!state.loading && !state.error && result ? <TraceEventTable events={result.events || []} /> : null}
      {!state.loading && !state.error && !result ? <div className="empty-state">没有匹配的 trace events</div> : null}
    </section>
  );
}

function TraceEventTable({ events }) {
  const sourceEvents = useMemo(() => (Array.isArray(events) ? events : []), [events]);
  const eventSignature = useMemo(() => sourceEvents
    .map((event, index) => `${textValue(event.trace_id)}:${textValue(event.span_id) || index}:${textValue(event.ts)}:${textValue(event.method)}`)
    .join('|'), [sourceEvents]);
  const [displayState, setDisplayState] = useState({ eventSignature: '', showAll: false });
  const showAll = displayState.eventSignature === eventSignature ? displayState.showAll : false;
  const keyEvents = useMemo(() => selectKeyTraceEvents(sourceEvents), [sourceEvents]);
  const visibleEvents = showAll ? sourceEvents : keyEvents;
  const hiddenCount = Math.max(sourceEvents.length - keyEvents.length, 0);

  if (!sourceEvents.length) return <div className="empty-state">没有匹配的 trace events</div>;

  return (
    <>
      {hiddenCount > 0 ? (
        <div className="observability-trace-filter">
          <p>
            默认显示关键事件 {visibleEvents.length}/{sourceEvents.length} · 已折叠 {hiddenCount} 条成功过程事件
          </p>
          <button
            type="button"
            className="btn secondary"
            onClick={() => setDisplayState({ eventSignature, showAll: !showAll })}
          >
            {showAll ? '只看关键事件' : '显示全部事件'}
          </button>
        </div>
      ) : null}
      <ol className="observability-table" aria-label="Trace events">
        {visibleEvents.map((event, index) => (
          <TraceEventRow event={event} index={index} key={`${event.trace_id || 'trace'}-${event.span_id || index}`} />
        ))}
      </ol>
    </>
  );
}

function selectKeyTraceEvents(events) {
  const source = Array.isArray(events) ? events : [];
  if (source.length <= 2) return source;
  const selectedIndexes = new Set();
  source.forEach((event, index) => {
    if (isKeyTraceEvent(event)) selectedIndexes.add(index);
  });
  const contextIndex = lastMeaningfulTraceEventIndex(source);
  if (contextIndex >= 0) selectedIndexes.add(contextIndex);
  return (
    Array.from(selectedIndexes)
    .sort((left, right) => left - right)
    .map((index) => source[index])
  );
}

function isKeyTraceEvent(event) {
  const status = textValue(event.status).toLowerCase();
  if (status === 'error' || status === 'panic' || status === 'slow') return true;
  if (textValue(event.error)) return true;
  const method = textValue(event.method).toLowerCase();
  const phase = textValue(event.phase).toLowerCase();
  if (method.includes('failed') || method.includes('error') || method.includes('panic')) return true;
  if (phase.includes('failed') || phase.includes('error') || phase.includes('panic')) return true;
  return false;
}

function lastMeaningfulTraceEventIndex(events) {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    if (!isNoisyTraceEvent(events[index])) return index;
  }
  return events.length - 1;
}

function isNoisyTraceEvent(event) {
  const status = textValue(event.status).toLowerCase();
  const kind = textValue(event.kind).toLowerCase();
  const method = textValue(event.method).toLowerCase();
  if (status === 'sampled' || status === 'dropped_summary') return true;
  if (kind === 'bus_event' || kind === 'ui_state') return true;
  return method.startsWith('bus.event.') || method.startsWith('uistate.');
}

function TraceEventRow({ event, index }) {
  const title = event.method || event.phase || event.kind || 'event';
  const formattedTimestamp = formatObservabilityTimestamp(event.ts);
  const timestampText = formattedTimestamp === '-' ? '' : formattedTimestamp;
  const durationText = formatObservabilityDuration(event.duration_ms);
  const codeText = formatCodeAnchor(event.code);
  const context = [
    event.kind,
    event.phase,
    event.client_kind,
    event.client_route,
  ].map(textValue).filter(Boolean);
  const requestContext = [
    ['组件', event.kind],
    ['阶段', event.phase],
    ['客户端', event.client_kind],
    ['页面', event.client_route],
    ['方法', event.method],
  ].map(([label, value]) => [label, textValue(value)]).filter(([, value]) => value);
  const traceContext = [
    ['trace', event.trace_id],
    ['span', event.span_id],
    ['parent', event.parent_span_id],
    ['thread', event.thread_id],
    ['turn', event.turn_id],
    ['agent', event.agent_id],
    ['call', event.call_id],
    ['tool', event.tool_name],
  ].map(([label, value]) => [label, textValue(value)]).filter(([, value]) => value);
  const metadataText = stableTraceEventMetadata(event.metadata);
  const stackText = Array.isArray(event.stack) && event.stack.length
    ? event.stack.map(formatCodeAnchor).join('\n')
    : '';
  return (
    <li className={`observability-event-row is-${observabilityStatusClass(event.status)}`}>
      <div className="observability-event-head">
        <div className="observability-event-title">
          <strong>{title}</strong>
          {context.length ? <p>{context.join(' · ')}</p> : null}
        </div>
        <div className="observability-event-metrics" aria-label={`trace event ${index + 1} status`}>
          <span className={`observability-status-pill is-${observabilityStatusClass(event.status)}`}>{event.status || 'ok'}</span>
          {timestampText ? <time dateTime={textValue(event.ts)}>{timestampText}</time> : null}
          <span>{durationText}</span>
        </div>
      </div>
      {requestContext.length ? (
        <TraceEventFieldGroup label="请求上下文" fields={requestContext} />
      ) : null}
      {traceContext.length ? (
        <TraceEventFieldGroup label="链路标识" fields={traceContext} />
      ) : null}
      {codeText !== '-' ? <p className="observability-event-code"><span>代码位置</span><code>{codeText}</code></p> : null}
      {event.error ? (
        <div className="observability-event-failure">
          <div className="observability-detail-label">失败原因</div>
          <p>{event.error}</p>
        </div>
      ) : null}
      {stackText ? (
        <div className="observability-event-detail">
          <div className="observability-detail-label">调用栈</div>
          <pre>{stackText}</pre>
        </div>
      ) : null}
      {metadataText ? (
        <div className="observability-event-detail">
          <div className="observability-detail-label">附加信息</div>
          <pre>{metadataText}</pre>
        </div>
      ) : null}
    </li>
  );
}

function TraceEventFieldGroup({ label, fields }) {
  return (
    <div className="observability-event-section">
      <div className="observability-detail-label">{label}</div>
      <div className="observability-event-meta">
        {fields.map(([fieldLabel, value]) => (
          <span key={`${fieldLabel}-${value}`}>
            <em>{fieldLabel}</em>
            <code>{value}</code>
          </span>
        ))}
      </div>
    </div>
  );
}

function stableTraceEventMetadata(metadata) {
  if (!metadata || typeof metadata !== 'object' || Array.isArray(metadata)) return '';
  return JSON.stringify(stableBackendLogValue(metadata), null, 2);
}

function observabilityStatusClass(status) {
  return (textValue(status) || 'ok').toLowerCase().replace(/[^a-z0-9-]+/g, '-');
}

function formatCodeAnchor(anchor) {
  if (!anchor || typeof anchor !== 'object') return '-';
  const file = anchor.file || '-';
  const fn = anchor.function || '-';
  const line = anchor.line || 0;
  return `${file}:${line} ${fn}`;
}

export { ObservabilityPage };

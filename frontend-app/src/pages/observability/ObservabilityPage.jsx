import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy } from 'lucide-react';
import { FrontendHealthPanel } from '../../features/health/FrontendHealthPanel.jsx';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { runBackgroundAction, runUIAction } from '../../shared/ui/runUIAction.js';
import { optionalArrayValue, optionalDateFromValue, errorMessage, firstPresentRawText, textValue, optionalTimestampMillis } from '../shared/pageShared.js';
import { copyTextToClipboard, getObservabilityTrace, listObservabilityRecent as getObservabilityRecent } from './services/observabilityPageService.js'; import './ObservabilityPage.css';
function observabilityRecentQueryKey(cwd, params) { return ['observability', 'recent', textValue(cwd), Number(params?.limit) || 0, params || null]; }
function observabilityTraceQueryKey(cwd, traceId, limit) { return ['observability', 'trace', textValue(cwd), textValue(traceId), Number(limit) || 0]; }
function validatedObservabilityResult(value, label) { if (!value || typeof value !== 'object' || Array.isArray(value) || !Array.isArray(value.events)) { throw new TypeError(`${label} response must contain an events array`); } return value; }
function stableBackendLogValue(value, seen = new WeakSet()) { if (!value || typeof value !== 'object') return value; if (seen.has(value)) return '[Circular]'; seen.add(value); if (Array.isArray(value)) {
const items = value.map((item) => stableBackendLogValue(item, seen)); seen.delete(value); return items; } const record = Object.fromEntries( Object.keys(value) .sort((left, right) => left.localeCompare(right)) .map((key) => [key, stableBackendLogValue(value[key], seen)]),
); seen.delete(value); return record; }
function formatObservabilityTimestamp(value) { const text = textValue(value); if (!text) return '-'; const parsed = optionalDateFromValue(text, 'observability timestamp'); if (!parsed) return text;
const year = String(parsed.getFullYear()).padStart(4, '0'); const month = String(parsed.getMonth() + 1).padStart(2, '0'); const day = String(parsed.getDate()).padStart(2, '0'); const hour = String(parsed.getHours()).padStart(2, '0');
const minute = String(parsed.getMinutes()).padStart(2, '0'); const second = String(parsed.getSeconds()).padStart(2, '0'); return `${year}-${month}-${day} ${hour}:${minute}:${second}`; }
function formatObservabilityDuration(value) { if (value === null || value === undefined || value === '') return '耗时未记录'; const duration = Number(value); if (!Number.isFinite(duration) || duration <= 0) return '耗时未记录'; return `${duration}ms`; }
function formatMatchedEventDuration(value) { const durationText = formatObservabilityDuration(value); if (durationText === '耗时未记录') return '匹配 event 耗时未记录'; return `匹配 event 耗时合计 ${durationText}`; }
function traceEventValue(event, camelKey, snakeKey) { if (!event || typeof event !== 'object') return ''; const value = event[camelKey] ?? event[snakeKey]; return value === undefined || value === null ? '' : value; }
function traceEventTraceId(event) { return textValue(traceEventValue(event, 'traceId', 'trace_id')); }
function traceEventSpanId(event) { return textValue(traceEventValue(event, 'spanId', 'span_id')); }
function traceEventParentSpanId(event) { return textValue(traceEventValue(event, 'parentSpanId', 'parent_span_id')); }
function traceEventThreadId(event) { return textValue(traceEventValue(event, 'threadId', 'thread_id')); }
function traceEventTurnId(event) { return textValue(traceEventValue(event, 'turnId', 'turn_id')); }
function traceEventAgentId(event) { return textValue(traceEventValue(event, 'agentId', 'agent_id')); }
function traceEventCallId(event) { return textValue(traceEventValue(event, 'callId', 'call_id')); }
function traceEventToolName(event) { return textValue(traceEventValue(event, 'toolName', 'tool_name')); }
function traceEventClientKind(event) { return textValue(traceEventValue(event, 'clientKind', 'client_kind')); }
function traceEventClientRoute(event) { return textValue(traceEventValue(event, 'clientRoute', 'client_route')); }
function traceEventDurationMs(event) { return traceEventValue(event, 'durationMs', 'duration_ms'); }
function traceResultTotalDurationMs(result) { return traceEventValue(result, 'totalDurationMs', 'total_duration_ms') || 0; }
function observabilityTailDiagnosticText(result) { if (!result || typeof result !== 'object') return '';
const parts = []; if (result.degraded) parts.push('degraded=true'); const parseError = textValue(result.parseError || result.parse_error); if (parseError) parts.push(`parse_error=${parseError}`); const tailError = textValue(result.tailError || result.tail_error);
if (tailError) parts.push(`tail_error=${tailError}`); if (result.tailTimedOut || result.tail_timed_out) parts.push('tail_timed_out=true'); const filesScanned = Number(result.tailFilesScanned ?? result.tail_files_scanned);
if (Number.isFinite(filesScanned) && filesScanned > 0) parts.push(`tail_files_scanned=${filesScanned}`); return parts.join(' · '); }
function useObservabilityFilters() { const [filters, setFilters] = useState({ agentId: '', component: '', keyword: '', limit: '50', method: '', status: '', threadId: '', traceId: '', }); const setFilter = useCallback((key, value) => {
setFilters((current) => ({ ...current, [key]: value })); }, []); const queryLimit = filters.limit; const buildRecentParams = useCallback((overrides = {}) => ({ limit: overrides.limit ?? filters.limit, status: overrides.status ?? filters.status.trim(),
component: overrides.component ?? filters.component.trim(), method: overrides.method ?? filters.method.trim(), traceId: overrides.traceId ?? filters.traceId.trim(), threadId: overrides.threadId ?? filters.threadId.trim(), agentId: overrides.agentId ?? filters.agentId.trim(),
keyword: overrides.keyword ?? filters.keyword.trim(), }), [filters]); return { buildRecentParams, filters, queryLimit, setFilter }; }
function useObservabilityTraceExpansion({ setFilter, setNotice }) { const [expandedTraces, setExpandedTraces] = useState({}); const toggleTraceExpansion = useCallback((value) => { const nextTraceId = textValue(value); if (!nextTraceId) return;
const currentEntry = expandedTraces[nextTraceId]; if (currentEntry?.expanded) { setExpandedTraces((current) => collapsedTraceState(current, nextTraceId)); return; } setNotice(''); setFilter('traceId', nextTraceId); setExpandedTraces((current) => ({ ...current,
[nextTraceId]: { ...current[nextTraceId], expanded: true }, })); }, [expandedTraces, setFilter, setNotice]); return { expandedTraces, setExpandedTraces, toggleTraceExpansion }; }
function collapsedTraceState(current, traceId) { const entry = { ...current[traceId], expanded: false }; return { ...current, [traceId]: entry }; }
function ObservabilityPage({ copy = APP_COPY.zh.observability, cwd = '' }) {
const queryClient = useQueryClient(); const queryCwd = textValue(cwd); const { buildRecentParams, filters, queryLimit, setFilter } = useObservabilityFilters(); const [submittedRecentParams, setSubmittedRecentParams] = useState(null);
const [copiedTraceId, setCopiedTraceId] = useState(''); const [notice, setNotice] = useState(''); const { expandedTraces, setExpandedTraces, toggleTraceExpansion } = useObservabilityTraceExpansion({ setFilter, setNotice }); const recentQueryKey = useMemo(
() => observabilityRecentQueryKey(queryCwd, submittedRecentParams), [queryCwd, submittedRecentParams], ); const recentQuery = useQuery({ queryKey: recentQueryKey,
queryFn: () => runBackgroundAction('observability.recent.load', async () => validatedObservabilityResult(
await getObservabilityRecent(submittedRecentParams),
'observability recent',
)), enabled: Boolean(submittedRecentParams),
refetchOnWindowFocus: false, retry: false, }); const loading = recentQuery.isFetching; const recentResult = recentQuery.data || null;
useEffect(() => { if (recentQuery.error) setNotice('查询最近日志失败，请重试。'); }, [recentQuery.error]);
const copyTraceId = useCallback((value) => { const nextTraceId = textValue(value); if (!nextTraceId) return undefined; return runUIAction('observability.trace-id.copy', async () => {
setNotice('');
try { await copyTextToClipboard(nextTraceId); setCopiedTraceId(nextTraceId); } catch (error) { setNotice('复制 Trace ID 失败，请重试。'); throw error; }
}, { retryable: true });
}, [setNotice]);
const runQuery = useCallback(() => {
return runUIAction('observability.query', () => { setCopiedTraceId(''); setNotice(''); setExpandedTraces({}); const params = { ...buildRecentParams(), includeTail: true };
runBackgroundAction('observability.recent.invalidate', async () => queryClient.invalidateQueries({
queryKey: observabilityRecentQueryKey(queryCwd, params), exact: true,
}));
setSubmittedRecentParams(params); });
}, [buildRecentParams, queryClient, queryCwd, setExpandedTraces]);
return ( <section className="settings-page observability-page" data-testid="observability-page"> <ObservabilityHeader copy={copy} /> <FrontendHealthPanel /> {notice ? <div className="settings-alert error" role="alert">{notice}</div> : null}
<ObservabilitySearchForm copy={copy} filters={filters} loading={loading} onFilter={setFilter} onSubmit={runQuery} /> <ObservabilityRecentLogs result={recentResult} onOpenTrace={toggleTraceExpansion} onCopyTrace={copyTraceId} copiedTraceId={copiedTraceId}
expandedTraces={expandedTraces} queryCwd={queryCwd} queryLimit={queryLimit} onTraceError={setNotice} /> </section> ); }
function ObservabilityHeader({ copy }) { return ( <div className="settings-header"> <div> <h1>{copy.title}</h1> </div> </div> ); }
function ObservabilitySearchForm({ copy, filters, loading, onFilter, onSubmit }) { const submit = (event) => { event.preventDefault(); void onSubmit(); }; return (
<form className="observability-search fusion-surface" onSubmit={submit} aria-busy={loading}> <div className="observability-filter-grid"> <ObservabilityTextFilter label="Trace ID" value={filters.traceId} placeholder="00-... 或 trace_id" onChange={(value) => onFilter('traceId', value)} />
<ObservabilityTextFilter label="Thread ID" value={filters.threadId} placeholder="thread_..." onChange={(value) => onFilter('threadId', value)} />
<ObservabilityTextFilter label="Agent ID" value={filters.agentId} placeholder="agent_..." onChange={(value) => onFilter('agentId', value)} />
<ObservabilityTextFilter label={copy.component} value={filters.component} placeholder="rpc / tool / wails" onChange={(value) => onFilter('component', value)} />
<ObservabilityStatusFilter copy={copy} value={filters.status} onChange={(value) => onFilter('status', value)} /> <ObservabilityTextFilter label="Method" value={filters.method} placeholder="thread/start" onChange={(value) => onFilter('method', value)} />
<ObservabilityTextFilter label={copy.keyword} value={filters.keyword} placeholder={copy.statusPlaceholder} onChange={(value) => onFilter('keyword', value)} />
<ObservabilityTextFilter label="Limit" value={filters.limit} inputMode="numeric" onChange={(value) => onFilter('limit', value)} /> </div> <div className="settings-actions">
<button type="submit" className="suiyuan-btn-fusion" disabled={loading}>{loading ? copy.querying : copy.queryLatest}</button> </div> </form> ); }
function ObservabilityTextFilter({ inputMode, label, placeholder = '', value, onChange }) { return ( <label> {label} <input value={value} onChange={(event) => onChange(event.target.value)} placeholder={placeholder} inputMode={inputMode} /> </label> ); }
function ObservabilityStatusFilter({ copy, value, onChange }) { return (
<label> {copy.status} <select value={value} onChange={(event) => onChange(event.target.value)}> <option value="">{copy.all}</option> <option value="ok">ok</option> <option value="slow">slow</option> <option value="error">error</option> <option value="panic">panic</option>
<option value="sampled">sampled</option> <option value="dropped_summary">dropped_summary</option> </select> </label> ); }
function ObservabilityRecentLogs(props) { const { result, onOpenTrace, onCopyTrace, copiedTraceId, expandedTraces, queryCwd, queryLimit, onTraceError } = props; if (!result) return null; let traceRows; try { traceRows = groupObservabilityTraceRows(result.events);
} catch (error) { return ( <div className="settings-alert error" data-testid="observability-recent-logs-error" role="alert"> 最近日志数据无效：{errorMessage(error)} </div> );
} const eventCount = traceRows.reduce((total, row) => total + row.events.length, 0); const tailDiagnostics = observabilityTailDiagnosticText(result); return (
<div className="settings-card observability-result observability-system-log fusion-surface" data-testid="observability-recent-logs"> <div className="observability-result-header"> <div> <h2>最新匹配 event 分组</h2>
<p>{traceRows.length} 条匹配 event 分组 · {eventCount} 个匹配 event · source={result.source || 'memory'} · truncated={String(Boolean(result.truncated))}{tailDiagnostics ? ` · ${tailDiagnostics}` : ''}</p> </div> </div> {traceRows.length === 0 ? (
<div className="empty-state">没有匹配的最近请求</div> ) : (
<div className="observability-log-table" role="table" aria-label="最新匹配 event 分组"> <div className="observability-log-table-head" role="rowgroup"> <div className="observability-log-table-head-row" role="row"> <div role="columnheader">时间</div>
<div role="columnheader">匹配 event 状态</div> <div role="columnheader">匹配 event 摘要</div> <div role="columnheader">操作</div> </div> </div> <div className="observability-log-table-body" role="rowgroup"> {traceRows.map((row) => (
<ObservabilityLogTableRow row={row} onOpenTrace={onOpenTrace} onCopyTrace={onCopyTrace} copied={Boolean(row.traceID) && row.traceID === copiedTraceId} traceState={row.traceID ? expandedTraces[row.traceID] : undefined} queryCwd={queryCwd} queryLimit={queryLimit}
onTraceError={onTraceError} key={row.key} /> ))} </div> </div> )} </div> ); }
function groupObservabilityTraceRows(events) { const rowsByTrace = new Map(); const source = Array.isArray(events) ? events : []; for (let index = 0; index < source.length; index += 1) {
const event = source[index]; const traceID = traceEventTraceId(event); const rowKey = traceID || observabilityEventRowKey(event, index); const existing = rowsByTrace.get(rowKey); if (!existing) {
rowsByTrace.set(rowKey, { key: rowKey, traceID, events: [event], representative: event, firstIndex: index }); continue; } existing.events.push(event); existing.representative = preferredObservabilityRepresentative(existing.representative, event);
} return Array.from(rowsByTrace.values()).map((row) => { const status = worstObservabilityStatus(row.events); const durationMS = row.events.reduce((total, event) => total + (Number(traceEventDurationMs(event)) || 0), 0); return { ...row, status, durationMS,
timestamp: latestObservabilityTimestamp(row.events), eventCount: row.events.length, error: firstPresentRawText(row.events.find((event) => textValue(event.error))?.error), }; }).sort(compareObservabilityTraceRows); }
function compareObservabilityTraceRows(left, right) { const leftTime = observabilityTimestampMillis(left.timestamp); const rightTime = observabilityTimestampMillis(right.timestamp); if (leftTime !== rightTime) return rightTime - leftTime;
return (left.firstIndex || 0) - (right.firstIndex || 0); }
function observabilityTimestampMillis(value) { return optionalTimestampMillis(value, 'observability row timestamp'); }
function observabilityEventRowKey(event, index) { const parts = [event.ts, traceEventSpanId(event), event.method, event.phase, event.kind] .map(textValue) .filter(Boolean); return `event-${parts.join(':') || 'unknown'}-${index}`; }
function preferredObservabilityRepresentative(current, next) { if (!current) return next; if (observabilityEventPriority(next) > observabilityEventPriority(current)) return next; return current; }
function observabilityEventPriority(event) { const statusPriority = observabilityStatusPriority(event); if (statusPriority > 0) return statusPriority;
const kind = textValue(event.kind).toLowerCase(); const phase = textValue(event.phase).toLowerCase(); const method = textValue(event.method); if (kind === 'frontend' || phase.startsWith('frontend.')) return 4;
if (method.startsWith('thread/') || method.startsWith('ui/') || method.startsWith('api/')) return 3; if (textValue(event.error)) return 2; return 1; }
function observabilityStatusPriority(event) { const status = textValue(event.status).toLowerCase(); if (status === 'panic') return 7; if (status === 'error') return 6; if (status === 'slow') return 5; return 0; }
function worstObservabilityStatus(events) { const sourceEvents = optionalArrayValue(events, 'observability status events'); const statuses = new Set(sourceEvents.map((event) => textValue(event.status).toLowerCase())); if (statuses.has('panic')) return 'panic';
if (statuses.has('error')) return 'error'; if (statuses.has('slow')) return 'slow'; if (statuses.has('sampled')) return 'sampled'; if (statuses.has('dropped_summary')) return 'dropped_summary'; if (statuses.has('unknown') || statuses.has('')) return 'unknown'; return 'ok'; }
function latestObservabilityTimestamp(events) { let latestText = ''; let latestValue = 0; const sourceEvents = optionalArrayValue(events, 'observability timestamp events'); for (const event of sourceEvents) {
const text = textValue(event.ts); const value = optionalTimestampMillis(text, 'observability event timestamp'); if (!latestText || value >= latestValue) { latestText = text; latestValue = value; } } return latestText; }
function ObservabilityLogTableRow(props) { const { row, onOpenTrace, onCopyTrace, copied, traceState, queryCwd, queryLimit, onTraceError } = props; const event = row.representative && typeof row.representative === 'object' ? row.representative : {}; const traceID = row.traceID;
const summary = observabilityTraceSummary(row); const expanded = Boolean(traceState?.expanded); const detailId = observabilityTraceDetailId(traceID); const actionLabel = expanded ? '收起 Trace' : '打开 Trace'; return (
<> <div className="observability-log-table-entry observability-log-table-row" role="row"> <div role="cell"> <time dateTime={row.timestamp}>{formatObservabilityTimestamp(row.timestamp)}</time> </div> <div role="cell">
<span className={`observability-status-pill is-${observabilityStatusClass(row.status)}`}>{observabilityStatusText(row.status)}</span> </div> <div role="cell"> <div className="observability-log-summary">
<strong>{event.method || event.phase || event.kind || 'event'}</strong> <p>{summary}</p> <p className="observability-log-identifiers"> trace={traceID || '-'} · thread={traceEventThreadId(event) || '-'} · {row.eventCount} 个匹配 event </p>
{row.error ? <p className="observability-event-error">{row.error}</p> : null} </div> </div> <div role="cell"> <div className="observability-log-row-actions"> <button type="button" className={`btn primary observability-copy-trace${copied ? ' is-copied' : ''}`}
onClick={() => onCopyTrace(traceID)} disabled={!traceID} aria-label={`复制 Trace ID ${traceID || '-'}`} > <Copy size={14} /> <span>{copied ? '已复制' : '复制 Trace ID'}</span> </button> <button type="button" className="btn secondary observability-open-trace"
onClick={() => onOpenTrace(traceID)} disabled={!traceID} aria-controls={detailId} aria-expanded={expanded} aria-label={`${actionLabel} ${traceID || '-'}`} > {actionLabel} </button> </div> </div> </div> {expanded ? (
<div className="observability-log-table-detail-row" role="row"> <div className="observability-log-table-detail-cell" role="cell"> <ObservabilityInlineTraceResult traceID={traceID} detailId={detailId} state={traceState} queryCwd={queryCwd} queryLimit={queryLimit}
onTraceError={onTraceError} /> </div> </div> ) : null} </> ); }
function observabilityTraceDetailId(traceID) { const safeTraceID = textValue(traceID).replace(/[^a-zA-Z0-9_-]+/g, '-'); return `observability-trace-detail-${safeTraceID || 'unknown'}`; }
function observabilityTraceSummary(row) { const event = row.representative && typeof row.representative === 'object' ? row.representative : {}; const durationText = formatMatchedEventDuration(row.durationMS); const parts = [ event.kind, event.phase, traceEventClientRoute(event),
traceEventAgentId(event) ? `agent=${traceEventAgentId(event)}` : '', traceEventCallId(event) ? `call=${traceEventCallId(event)}` : '', traceEventToolName(event) ? `tool=${traceEventToolName(event)}` : '', durationText, ].map(textValue).filter(Boolean); return parts.join(' · '); }
function ObservabilityInlineTraceResult(props) { const { traceID, detailId, state, queryCwd, queryLimit, onTraceError } = props; const expanded = Boolean(state?.expanded); const traceQuery = useQuery({ queryKey: observabilityTraceQueryKey(queryCwd, traceID, queryLimit),
queryFn: () => runBackgroundAction('observability.trace.load', async () => validatedObservabilityResult(
await getObservabilityTrace({ traceId: traceID, limit: queryLimit }),
'observability trace',
)), enabled: expanded && Boolean(traceID), refetchOnWindowFocus: false, retry: false, staleTime: Infinity,
}); const result = traceQuery.data; const traceError = traceQuery.error ? 'Trace 加载失败，请重试。' : ''; const tailDiagnostics = observabilityTailDiagnosticText(result); let traceEvents = []; let traceShapeError = ''; if (result) { try {
traceEvents = optionalArrayValue(result.events, 'observability trace events'); } catch { traceShapeError = 'Trace 数据无效，请查看 Health。'; } } useEffect(() => { if (traceError) onTraceError(traceError); }, [onTraceError, traceError]); if (!expanded) return null; return (
<section className="observability-log-trace-detail" data-testid={`observability-inline-trace-${traceID}`} id={detailId} aria-label={`Trace ${traceID || '-'} 结果`} aria-busy={traceQuery.isFetching} > <div className="observability-inline-trace-header"> <div> <h3>Trace 结果</h3>
{result ? ( <p>source={result.source || 'memory'} total_duration_ms={traceResultTotalDurationMs(result)} truncated={String(Boolean(result.truncated))}{tailDiagnostics ? ` ${tailDiagnostics}` : ''}</p>
) : null} </div> </div> {traceQuery.isFetching && !result ? <output className="empty-state">Trace 加载中...</output> : null} {traceError ? <div className="settings-alert error" role="alert">Trace 加载失败：{traceError}</div> : null}
{traceShapeError ? <div className="settings-alert error" role="alert">Trace 数据无效：{traceShapeError}</div> : null} {!traceQuery.isFetching && !traceError && result && !traceShapeError ? <TraceEventTable events={traceEvents} /> : null}
{!traceQuery.isFetching && !traceError && !result ? <div className="empty-state">没有匹配的 trace events</div> : null} </section> ); }
function TraceEventTable({ events }) { const sourceEvents = useMemo(() => (Array.isArray(events) ? events : []), [events]); const eventSignature = useMemo(() => sourceEvents
.map((event, index) => `${traceEventTraceId(event)}:${traceEventSpanId(event) || index}:${textValue(event.phase)}:${textValue(event.ts)}:${textValue(event.method)}`) .join('|'), [sourceEvents]);
const [displayState, setDisplayState] = useState({ eventSignature: '', showAll: false }); const showAll = displayState.eventSignature === eventSignature ? displayState.showAll : false; const allEventIndexes = useMemo(() => sourceEvents.map((_, index) => index), [sourceEvents]);
const keyEventIndexes = useMemo(() => selectKeyTraceEventIndexes(sourceEvents), [sourceEvents]); const visibleEventIndexes = showAll ? allEventIndexes : keyEventIndexes; const hiddenCount = Math.max(sourceEvents.length - keyEventIndexes.length, 0);
if (!sourceEvents.length) return <div className="empty-state">没有匹配的 trace events</div>;
return ( <> {hiddenCount > 0 ? (
<div className="observability-trace-filter"> <p> 默认显示关键事件 {visibleEventIndexes.length}/{sourceEvents.length} · 已折叠 {hiddenCount} 条成功过程事件 </p> <button type="button" className="btn secondary" onClick={() => setDisplayState({ eventSignature, showAll: !showAll })} >
{showAll ? '只看关键事件' : '显示全部事件'} </button> </div> ) : null} <ol className="observability-table" aria-label="Trace events"> {visibleEventIndexes.map((sourceIndex, index) => { const event = sourceEvents[sourceIndex]; return (
<TraceEventRow event={event} index={index} key={traceEventRenderKey(event, sourceIndex)} /> ); })} </ol> </> ); }
function traceEventRenderKey(event, sourceIndex) { const parts = [traceEventTraceId(event), traceEventSpanId(event), event.phase, event.ts, event.method, String(sourceIndex)] .map(textValue) .filter(Boolean); return `trace-event-${parts.join(':')}`; }
function selectKeyTraceEventIndexes(events) { const source = Array.isArray(events) ? events : []; if (source.length <= 2) return source.map((_, index) => index); const selectedIndexes = new Set(); source.forEach((event, index) => {
if (isKeyTraceEvent(event)) selectedIndexes.add(index); }); const contextIndex = lastMeaningfulTraceEventIndex(source); if (contextIndex >= 0) selectedIndexes.add(contextIndex); return ( Array.from(selectedIndexes) .sort((left, right) => left - right) ); }
function isKeyTraceEvent(event) { const status = textValue(event.status).toLowerCase(); if (status === 'error' || status === 'panic' || status === 'slow') return true; if (textValue(event.error)) return true;
const method = textValue(event.method).toLowerCase(); const phase = textValue(event.phase).toLowerCase(); if (method.includes('failed') || method.includes('error') || method.includes('panic')) return true;
if (phase.includes('failed') || phase.includes('error') || phase.includes('panic')) return true; return false; }
function lastMeaningfulTraceEventIndex(events) { for (let index = events.length - 1; index >= 0; index -= 1) { if (!isNoisyTraceEvent(events[index])) return index; } return events.length - 1; }
function isNoisyTraceEvent(event) { const status = textValue(event.status).toLowerCase(); const kind = textValue(event.kind).toLowerCase(); const method = textValue(event.method).toLowerCase(); if (status === 'sampled' || status === 'dropped_summary') return true;
if (kind === 'bus_event' || kind === 'ui_state') return true; return method.startsWith('bus.event.') || method.startsWith('uistate.'); }
function TraceEventRow({ event, index }) { const title = event.method || event.phase || event.kind || 'event'; const formattedTimestamp = formatObservabilityTimestamp(event.ts); const timestampText = formattedTimestamp === '-' ? '' : formattedTimestamp;
const durationText = formatObservabilityDuration(traceEventDurationMs(event)); const codeText = formatCodeAnchor(event.code); const context = [ event.kind, event.phase, traceEventClientKind(event), traceEventClientRoute(event),
].map(textValue).filter(Boolean); const requestContext = [ ['组件', event.kind], ['阶段', event.phase], ['客户端', traceEventClientKind(event)], ['页面', traceEventClientRoute(event)], ['方法', event.method],
].map(([label, value]) => [label, textValue(value)]).filter(([, value]) => value); const traceContext = [ ['trace', traceEventTraceId(event)], ['span', traceEventSpanId(event)], ['parent', traceEventParentSpanId(event)], ['thread', traceEventThreadId(event)],
['turn', traceEventTurnId(event)], ['agent', traceEventAgentId(event)], ['call', traceEventCallId(event)], ['tool', traceEventToolName(event)],
].map(([label, value]) => [label, textValue(value)]).filter(([, value]) => value); const metadataText = stableTraceEventMetadata(event.metadata); const stackText = Array.isArray(event.stack) && event.stack.length ? event.stack.map(formatCodeAnchor).join('\n') : ''; return (
<li className={`observability-event-row is-${observabilityStatusClass(event.status)}`}> <div className="observability-event-head"> <div className="observability-event-title"> <strong>{title}</strong> {context.length ? <p>{context.join(' · ')}</p> : null} </div>
<div className="observability-event-metrics" aria-label={`trace event ${index + 1} status`}> <span className={`observability-status-pill is-${observabilityStatusClass(event.status)}`}>{observabilityStatusText(event.status)}</span>
{timestampText ? <time dateTime={textValue(event.ts)}>{timestampText}</time> : null} <span>{durationText}</span> </div> </div> {requestContext.length ? ( <TraceEventFieldGroup label="请求上下文" fields={requestContext} /> ) : null} {traceContext.length ? (
<TraceEventFieldGroup label="链路标识" fields={traceContext} /> ) : null} {codeText !== '-' ? <p className="observability-event-code"><span>代码位置</span><code>{codeText}</code></p> : null} {event.error ? (
<div className="observability-event-failure"> <div className="observability-detail-label">失败原因</div> <p>{event.error}</p> </div> ) : null} {stackText ? (
<div className="observability-event-detail"> <div className="observability-detail-label">调用栈</div> <pre>{stackText}</pre> </div> ) : null} {metadataText ? (
<div className="observability-event-detail"> <div className="observability-detail-label">附加信息</div> <pre>{metadataText}</pre> </div> ) : null} </li> ); }
function TraceEventFieldGroup({ label, fields }) { return ( <div className="observability-event-section"> <div className="observability-detail-label">{label}</div> <div className="observability-event-meta"> {fields.map(([fieldLabel, value]) => (
<span key={`${fieldLabel}-${value}`}> <em>{fieldLabel}</em> <code>{value}</code> </span> ))} </div> </div> ); }
function stableTraceEventMetadata(metadata) { if (!metadata || typeof metadata !== 'object' || Array.isArray(metadata)) return ''; return JSON.stringify(stableBackendLogValue(metadata), null, 2); }
function observabilityStatusClass(status) { return observabilityStatusText(status).toLowerCase().replace(/[^a-z0-9-]+/g, '-'); }
function observabilityStatusText(status) { return textValue(status) || 'unknown'; }
function formatCodeAnchor(anchor) { if (!anchor || typeof anchor !== 'object') return '-'; const file = anchor.file || '-'; const fn = anchor.function || '-'; const line = anchor.line || 0; return `${file}:${line} ${fn}`; }
export { ObservabilityPage };

import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'; import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, ChevronDown, MemoryStick, Plus, Search } from 'lucide-react'; import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx'; import { APP_BRAND_NAME, APP_COPY } from '../../shared/i18n/appI18n.js';
import { deleteMemoryEntry, fetchMemoryDashboard, getMemoryConsolidationStatus, getMemoryEntry, ignoreMemorySimilarity, mergeMemoryEntries, setMemoryAutoDreamIntent, startConsolidateMemorySimilarities, upsertMemoryEntry } from './services/memoryPageService.js';
import { dashboardQueryErrorState, dashboardQueryKey, errorMessage, firstPresentText, firstText, memoryHealth, memoryNoticeText, optionalSettingsCwd, queryHasSnapshot, sharedFileTimestamp, textValue, optionalTimestampMillis } from '../shared/pageShared.js';
import { runBackgroundAction, runUIAction } from '../../shared/ui/runUIAction.js';
import { PageHeader, Panel } from '../shared/pageComponents.jsx'; import './MemoryPage.css';
const MEMORY_CONSOLIDATION_POLL_MS = 2000;
const MEMORY_CONSOLIDATION_MAX_POLLS = 180;
const MEMORY_CATEGORY_KEYS = Object.freeze(['preference', 'project', 'all']);
const MEMORY_EDITOR_TYPES = Object.freeze([ { key: 'feedback', label: '偏好' }, { key: 'project', label: '项目' }, ]);
function memoryPollingAbortError() { const error = new Error('智能整合轮询已取消'); error.name = 'AbortError'; return error; }
function throwIfMemoryPollingAborted(signal) { if (signal?.aborted) throw memoryPollingAbortError(); }
function memoryTemplateForType(type) { switch (textValue(type)) { case 'feedback': return '规则\n原因：\n如何应用：'; case 'project': return '事实\n原因：\n如何应用：'; case 'reference': return '指向：\n为什么重要：'; default: return '用户偏好：'; } }
function memoryTargetForType(type) { return textValue(type) === 'project' ? 'team' : 'private'; }
function memorySlugText(value) { return ( textValue(value) .toLowerCase() .replace(/[^a-z0-9]+/g, '-') .replace(/^-+|-+$/g, '') .slice(0, 48) ); }
function memoryNameHash(value) { let hash = 5381; const text = textValue(value); for (let index = 0; index < text.length; index += 1) { hash = ((hash << 5) + hash + text.charCodeAt(index)) >>> 0; } return hash.toString(36); }
function memoryAutoName(form) { const existing = textValue(form?.name); if (existing) return existing;
const type = textValue(form?.type) || 'project'; const base = firstText(form?.title, form?.description, form?.content, type); const slug = memorySlugText(base); if (slug) return slug; return `${type}-${memoryNameHash(base)}`; }
function defaultMemoryForm(type = 'project', target = memoryTargetForType(type)) { return { target, existingPath: '', name: '', description: '', title: '', type, content: memoryTemplateForType(type), }; }
function normalizeAutoDreamIntent(value) { if (value === true) return true; if (value === false) return false; return null; }
function memoryHealthPercent(count, max) { const safeMax = Number(max) || 1; const safeCount = Number(count) || 0; return Math.min(100, Math.max(0, Math.round((safeCount / safeMax) * 100))); }
function memoryHealthClass(percent) { if (percent >= 100) return 'danger'; if (percent >= 80) return 'warning'; return ''; }
function memoryMatches(entry, query) { const needle = textValue(query).toLowerCase(); if (!needle) return true; return ( [entry.title, entry.name, entry.description, entry.path, entry.type, entry.preview] .some((value) => textValue(value).toLowerCase().includes(needle)) ); }
function sortMemoryEntries(entries) { return [...entries].sort((left, right) => {
const leftTime = optionalTimestampMillis(left.updatedAt, 'memory entry updatedAt'); const rightTime = optionalTimestampMillis(right.updatedAt, 'memory entry updatedAt'); return rightTime - leftTime || left.title.localeCompare(right.title); }); }
function memoryPairKey(group) { return `${group.targetA}:${group.pathA}|${group.targetB}:${group.pathB}`; }
function formatMemoryScore(score) { return `${Math.round((Number(score) || 0) * 100)}%`; }
function memoryEntryTitle(entry) { return firstText(entry.title, entry.description, entry.name, entry.path); }
function memoryConsolidationStats(result) { return { merged: Number(result?.merged) || 0, ignored: Number(result?.ignored) || 0, failed: Number(result?.failed) || 0, skipped: Number(result?.skipped) || 0, }; }
function memoryConsolidationParts(stats) { return [ `已整合 ${stats.merged} 组`, stats.ignored ? `${stats.ignored} 组判定不应合` : '', stats.failed ? `${stats.failed} 组失败` : '', stats.skipped ? `${stats.skipped} 组跳过` : '', ].filter(Boolean); }
function memoryConsolidationResultMessage(result) { const stats = memoryConsolidationStats(result); return { level: stats.failed || stats.skipped ? 'warning' : 'info',
message: `${memoryConsolidationParts(stats).join('，')}${stats.failed || stats.skipped ? '，详细原因请查看 Health 诊断 ID' : ''}`, }; }
function memoryConsolidationJobFailed(status) { const message = textValue(status?.error) || '智能整合暂时失败，请稍后重试'; return new Error(message); }
function plainMemoryObject(value) { return value && typeof value === 'object' && !Array.isArray(value) ? value : null; }
function memoryHealthHasSimilarGroups(health) { return Array.isArray(health?.similarGroups) && health.similarGroups.length > 0; }
function memoryScanDiagnostic(snapshot) { const overview = plainMemoryObject(snapshot?.overview); if (!overview) return null; const scan = plainMemoryObject(overview.scan); switch (textValue(scan?.reason)) { case 'memory_scan_truncated':
return { level: 'warning', message: '记忆扫描已达到安全上限，列表仅显示部分条目。' }; case 'memory_scan_canceled': return { level: 'info', message: '记忆扫描已取消。' }; default: return null; } }
function memorySnapshotWithClearedSimilarGroups(snapshot, overview, health) { return { ...snapshot, overview: { ...overview, health: { ...health, similarGroups: [], }, }, }; }
function clearMemorySimilarGroups(snapshot) { const validSnapshot = plainMemoryObject(snapshot); const overview = plainMemoryObject(validSnapshot?.overview); if (!overview) return snapshot;
const health = plainMemoryObject(overview.health); if (!validSnapshot || !memoryHealthHasSimilarGroups(health)) return snapshot; return memorySnapshotWithClearedSimilarGroups(validSnapshot, overview, health); }
async function fetchMemoryConsolidationPoll(cwd, jobID, { signal } = {}) {
throwIfMemoryPollingAborted(signal); const status = await getMemoryConsolidationStatus({ cwd, jobId: jobID }); throwIfMemoryPollingAborted(signal); if (status?.status === 'failed') throw memoryConsolidationJobFailed(status);
if (status?.status !== 'running' && status?.status !== 'succeeded') { throw new Error('智能整合状态异常，请稍后重试'); } return status; }
function memoryConsolidationSucceededResult(status) { if (!plainMemoryObject(status?.result)) { throw new Error('智能整合完成但没有返回结果'); } return status.result; }
function useMemoryDashboard(projectPath) { const memoryCwd = optionalSettingsCwd(projectPath); const queryClient = useQueryClient(); const { data: memoryData, error: memoryError, isPending: memoryPending } = useQuery({ queryKey: dashboardQueryKey(memoryCwd, 'memory'),
queryFn: ({ signal }) => runBackgroundAction('memory.dashboard.load', () => fetchMemoryDashboardWithSignal(memoryCwd, signal)), enabled: Boolean(memoryCwd),
}); const memoryQuery = { data: memoryData, error: memoryError, isPending: memoryPending }; const hasSnapshot = queryHasSnapshot(memoryQuery); const snapshot = memoryQuery.data || { overview: {}, entries: [] };
const loading = Boolean(memoryCwd) && memoryQuery.isPending && !hasSnapshot;
const queryErrorState = dashboardQueryErrorState(memoryQuery, hasSnapshot);
const syncError = queryErrorState.cachedSyncError ? '同步记忆失败，当前显示上次成功数据。' : '';
const error = queryErrorState.blockingError ? '读取记忆失败，请重试。' : '';
const refreshMemory = useCallback(async () => { if (!memoryCwd) return;
await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(memoryCwd, 'memory') }); }, [memoryCwd, queryClient]); return { error, isProjectPending: !memoryCwd, loading, memoryCwd, queryClient, refreshMemory, snapshot, syncError }; }
async function fetchMemoryDashboardWithSignal(memoryCwd, signal) {
const snapshot = typeof fetchMemoryDashboard.withSignal === 'function' ? await fetchMemoryDashboard.withSignal(memoryCwd, signal) : await fetchMemoryDashboard(memoryCwd); memorySimilarityDegraded(snapshot?.overview); return snapshot;
}
function memorySimilarityDegraded(overview) {
const health = plainMemoryObject(overview?.health); if (!health || !Object.prototype.hasOwnProperty.call(health, 'similarityDegraded')) return false;
if (typeof health.similarityDegraded !== 'boolean') throw new Error('memory health similarityDegraded must be a boolean'); return health.similarityDegraded;
}
function useMemoryNotice(memoryCwd) { const [noticeState, setNoticeState] = useState({ memoryCwd, notice: { level: 'info', message: '' } }); if (noticeState.memoryCwd !== memoryCwd) { setNoticeState({ memoryCwd, notice: { level: 'info', message: '' } });
} const notice = noticeState.memoryCwd === memoryCwd ? noticeState.notice : { level: 'info', message: '' }; const showNotice = useCallback((level, message) => { setNoticeState({ memoryCwd, notice: { level: level || 'info', message: memoryNoticeText(message) } });
}, [memoryCwd]); return { notice, showNotice }; }
function useMemoryDerivedState(snapshot, activeCategory, searchText, onSimilarCountChange) {
const preferenceEntries = useMemo(() => snapshot.entries.filter((entry) => entry.category === 'preference'), [snapshot.entries]); const projectEntries = useMemo(() => snapshot.entries.filter((entry) => entry.category === 'project'), [snapshot.entries]);
const entriesByCategory = useMemo(() => ({ all: snapshot.entries, preference: preferenceEntries, project: projectEntries, }), [preferenceEntries, projectEntries, snapshot.entries]); const categoryCounts = useMemo(() => ({ all: snapshot.entries.length,
preference: preferenceEntries.length, project: projectEntries.length, }), [preferenceEntries.length, projectEntries.length, snapshot.entries.length]); const visibleEntries = useMemo(() => (
sortMemoryEntries((Array.isArray(entriesByCategory[activeCategory]) ? entriesByCategory[activeCategory] : []).filter((entry) => memoryMatches(entry, searchText)))
), [activeCategory, entriesByCategory, searchText]); const health = useMemo(() => memoryHealth(snapshot.overview, categoryCounts), [snapshot.overview, categoryCounts]);
const similarGroups = Array.isArray(health?.similarGroups) ? health.similarGroups : []; const similarityDegraded = memorySimilarityDegraded(snapshot.overview); useEffect(() => {
if (typeof onSimilarCountChange === 'function') onSimilarCountChange(similarGroups.length); }, [onSimilarCountChange, similarGroups.length]); return { categoryCounts, health, similarGroups, similarityDegraded, visibleEntries }; }
function useMemoryAutoDream({ dashboard, showNotice }) {
const [autoToggling, setAutoToggling] = useState(false); const runtime = dashboard.snapshot.overview?.autoDreamEnabled === true; const intent = normalizeAutoDreamIntent(dashboard.snapshot.overview?.autoDreamIntent); const enabled = intent === null ? runtime : intent;
 const pendingRestart = intent !== null && intent !== runtime; const toggleAutoDream = useCallback(async () => { if (autoToggling) return;
 if (dashboard.isProjectPending || !dashboard.memoryCwd) { showNotice('info', '请先在聊天页选择项目，再切换自动沉淀。'); return; }
const next = !enabled; setAutoToggling(true); try { await setMemoryAutoDreamIntent({ cwd: dashboard.memoryCwd, enabled: next }); await dashboard.refreshMemory();
// 切换后回读快照校验是否生效：当前后端 intent 写入项目记忆根、snapshot 读全局根（读写路径不一致），
// 状态会静默回退；读不到预期状态时给出明确的未生效反馈，而不是假装切换成功。
const refreshed = dashboard.queryClient.getQueryData(dashboardQueryKey(dashboard.memoryCwd, 'memory')); const runtimeAfter = refreshed?.overview?.autoDreamEnabled === true;
const intentAfter = normalizeAutoDreamIntent(refreshed?.overview?.autoDreamIntent); const effectiveAfter = intentAfter === null ? runtimeAfter : intentAfter;
if (effectiveAfter !== next) { showNotice('error', '自动沉淀切换未生效：记忆服务的自动梦境配置存在读写路径不一致（后端已知问题），请检查记忆目录配置或联系管理员。'); return; }
 showNotice('warning', `自动沉淀已切换为${next ? '开启' : '关闭'}，重启 ${APP_BRAND_NAME} 后生效`); } catch (err) {
 showNotice('error', `切换自动沉淀失败：${errorMessage(err)}`); throw err; } finally { setAutoToggling(false); } }, [autoToggling, dashboard, enabled, showNotice]); return { enabled, pendingRestart, toggleAutoDream, toggling: autoToggling }; }
function useLatestValue(value) { const valueRef = useRef(value); useLayoutEffect(() => { valueRef.current = value; }, [value]); return valueRef; }
function useMemoryEditor({ dashboard, showNotice }) {
const [createMenuOpen, setCreateMenuOpen] = useState(false); const [editor, setEditor] = useState({ open: false, mode: 'create', scopeCwd: '', form: defaultMemoryForm('project') }); const [busyKey, setBusyKey] = useState(''); const [saving, setSaving] = useState(false);
const memoryCwdRef = useLatestValue(dashboard.memoryCwd); const editRequestRef = useRef(0); useEffect(() => { editRequestRef.current += 1; setCreateMenuOpen(false); setBusyKey(''); setEditor((current) => {
if (!current.scopeCwd || current.scopeCwd === dashboard.memoryCwd) return current; return { open: false, mode: 'create', scopeCwd: '', form: defaultMemoryForm('project') }; });
}, [dashboard.memoryCwd]); const updateEditorForm = useCallback((patch) => setEditor((current) => ({ ...current, form: { ...current.form, ...patch } })), []); const openCreate = useCallback((type) => {
if (dashboard.isProjectPending || !dashboard.memoryCwd) { showNotice('info', '请先在聊天页选择项目，再创建记忆。'); return; }
editRequestRef.current += 1; setEditor({ open: true, mode: 'create', scopeCwd: dashboard.memoryCwd, form: defaultMemoryForm(type, memoryTargetForType(type)) }); setCreateMenuOpen(false);
}, [dashboard.isProjectPending, dashboard.memoryCwd, showNotice]); const openEdit = useCallback(async (entry) => { const requestCwd = dashboard.memoryCwd; if (!requestCwd) return;
const requestID = editRequestRef.current + 1; editRequestRef.current = requestID; const key = `${entry.target}:${entry.path}`; setBusyKey(key); try {
const detail = await getMemoryEntry({ cwd: requestCwd, target: entry.target, path: entry.path }); if (editRequestRef.current !== requestID || memoryCwdRef.current !== requestCwd) return; if (!memoryDetailHasContent(detail)) { showNotice('error', '加载失败：记忆详情缺少内容，已阻断编辑保存'); return;
} setEditor({ open: true, mode: 'edit', scopeCwd: requestCwd, form: memoryEditorFormFromDetail(detail, entry) }); } catch (err) { if (editRequestRef.current !== requestID || memoryCwdRef.current !== requestCwd) return; showNotice('error', '加载记忆失败，请重试。'); throw err; } finally {
if (editRequestRef.current === requestID) setBusyKey(''); } }, [dashboard.memoryCwd, memoryCwdRef, showNotice]); const closeEditor = useCallback(() => { if (!saving) setEditor((current) => ({ ...current, open: false }));
}, [saving]); const saveEditor = useMemoryEditorSave({ dashboard, editor, saving, setEditor, setSaving, showNotice }); return { busyKey, closeEditor, createMenuOpen, editor, openCreate, openEdit, saveEditor, saving, setCreateMenuOpen, setEditor, updateEditorForm }; }
function memoryDetailHasContent(detail) { return Boolean(detail && typeof detail === 'object' && Object.prototype.hasOwnProperty.call(detail, 'content')); }
function memoryEditorFormFromDetail(detail, entry) { return { target: firstText(detail?.target, entry.target), existingPath: firstText(detail?.path, entry.path), name: firstText(detail?.name, entry.name), description: firstText(detail?.description, entry.description),
title: firstText(detail?.title, entry.title), type: firstText(detail?.type, entry.type), content: textValue(detail.content), }; }
function useMemoryEditorSave(options) { const { dashboard, editor, saving, setEditor, setSaving, showNotice } = options; return useCallback(async () => { if (saving) return; if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
if (!editor.scopeCwd || editor.scopeCwd !== dashboard.memoryCwd) { setEditor((current) => ({ ...current, open: false })); showNotice('error', '记忆编辑所属项目已变化，请重新打开后再保存'); return;
} const form = editor.form; const description = textValue(form.description); const content = textValue(form.content); if (!description) { showNotice('error', '请先填写描述'); return; } if (!content) { showNotice('error', '内容不能为空'); return; } setSaving(true); try {
const type = textValue(form.type) || 'project'; await upsertMemoryEntry(memoryUpsertPayload(editor.scopeCwd, form, type, description, content)); setEditor((current) => ({ ...current, open: false })); showNotice('info', '已保存'); await dashboard.refreshMemory(); } catch (err) {
showNotice('error', '保存记忆失败，请重试。'); throw err; } finally { setSaving(false); } }, [dashboard, editor.form, editor.scopeCwd, saving, setEditor, setSaving, showNotice]); }
function memoryUpsertPayload(cwd, form, type, description, content) { return { cwd, target: form.existingPath ? form.target : memoryTargetForType(type), existingPath: form.existingPath, name: memoryAutoName({ ...form, type }), description, title: textValue(form.title), type,
content, }; }
function useMemoryDelete({ dashboard, showNotice }) { const [deleteTarget, setDeleteTarget] = useState(null); const [deletingKey, setDeletingKey] = useState(''); useEffect(() => { setDeletingKey(''); setDeleteTarget((current) => {
if (!current?.scopeCwd || current.scopeCwd === dashboard.memoryCwd) return current; return null; }); }, [dashboard.memoryCwd]); const requestDelete = useCallback((entry) => { if (!entry) { setDeleteTarget(null); return;
} if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; } const scopeCwd = textValue(entry.scopeCwd) || dashboard.memoryCwd; if (scopeCwd !== dashboard.memoryCwd) { showNotice('error', '记忆删除所属项目已变化，请重新打开后再删除'); return; } setDeleteTarget({ ...entry, scopeCwd });
}, [dashboard.memoryCwd, showNotice]); const confirmDelete = useCallback(async () => { if (!deleteTarget || deletingKey) return; if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
if (!deleteTarget.scopeCwd || deleteTarget.scopeCwd !== dashboard.memoryCwd) { setDeleteTarget(null); showNotice('error', '记忆删除所属项目已变化，请重新打开后再删除'); return; } const key = `${deleteTarget.target}:${deleteTarget.path}`; setDeletingKey(key); try {
await deleteMemoryEntry({ cwd: deleteTarget.scopeCwd, target: deleteTarget.target, path: deleteTarget.path }); showNotice('info', `已删除：${memoryEntryTitle(deleteTarget)}`); setDeleteTarget(null); await dashboard.refreshMemory(); } catch (err) {
showNotice('error', '删除记忆失败，请重试。'); throw err; } finally { setDeletingKey(''); } }, [dashboard, deleteTarget, deletingKey, showNotice]); return { confirmDelete, deletingKey, deleteTarget, setDeleteTarget: requestDelete }; }
function useMemorySimilarityActions(options) {
const { applyConsolidationResult, dashboard, resolveLaunchPreferences, showNotice, similarGroups, similarityDegraded } = options;
const [mergeTarget, setMergeTarget] = useState(null); const [mergingAll, setMergingAll] = useState(false); const [ignoringKey, setIgnoringKey] = useState(''); const [mergingKey, setMergingKey] = useState(''); const [consolidationJob, setConsolidationJob] = useState(null);
useEffect(() => { setMergingKey(''); setMergeTarget((current) => { if (similarityDegraded || (!current?.scopeCwd || current.scopeCwd !== dashboard.memoryCwd)) return null; return current; }); }, [dashboard.memoryCwd, similarityDegraded]); const requestMerge = useCallback((group) => { if (!group) {
setMergeTarget(null); return; } if (similarityDegraded) { showNotice('warning', '相似记忆状态暂不可用，已暂停整合与忽略操作'); return; } if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; } setMergeTarget({ ...group, scopeCwd: dashboard.memoryCwd });
}, [dashboard.memoryCwd, showNotice, similarityDegraded]); const confirmMerge = useMemoryConfirmMerge({
dashboard, mergeTarget, mergingKey, setMergeTarget, setMergingKey, showNotice, similarityDegraded,
}); const ignoreGroup = useMemoryIgnoreGroup({ dashboard, ignoringKey, setIgnoringKey, showNotice, similarityDegraded });
const mergeAllGroups = useMemoryMergeAllGroups({ applyConsolidationResult, consolidationJob, dashboard, mergingAll, resolveLaunchPreferences, setConsolidationJob, setMergingAll, showNotice, similarGroups, similarityDegraded });
return { confirmMerge, consolidationJob, ignoreGroup, ignoringKey, mergeAllGroups, mergeTarget, mergingAll, mergingKey, setConsolidationJob, setMergeTarget: requestMerge }; }
function useMemoryConfirmMerge(options) { const { dashboard, mergeTarget, mergingKey, setMergeTarget, setMergingKey, showNotice, similarityDegraded } = options; return useCallback(async () => { if (!mergeTarget || mergingKey || similarityDegraded) return;
if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; } if (!mergeTarget.scopeCwd || mergeTarget.scopeCwd !== dashboard.memoryCwd) { setMergeTarget(null); showNotice('error', '记忆整合所属项目已变化，请重新打开后再整合'); return;
} const key = memoryPairKey(mergeTarget); setMergingKey(key); try { await mergeMemoryEntries({ cwd: mergeTarget.scopeCwd, targetA: mergeTarget.targetA, pathA: mergeTarget.pathA, targetB: mergeTarget.targetB, pathB: mergeTarget.pathB });
showNotice('info', `已整合「${mergeTarget.nameA || mergeTarget.pathA}」与「${mergeTarget.nameB || mergeTarget.pathB}」`); setMergeTarget(null); await dashboard.refreshMemory(); } catch (err) { showNotice('error', '整合记忆失败，请重试。'); throw err; } finally { setMergingKey(''); }
}, [dashboard, mergeTarget, mergingKey, setMergeTarget, setMergingKey, showNotice, similarityDegraded]); }
function useMemoryIgnoreGroup({ dashboard, ignoringKey, setIgnoringKey, showNotice, similarityDegraded }) { return useCallback(async (group) => { const key = memoryPairKey(group); if (ignoringKey || similarityDegraded) return; if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
setIgnoringKey(key); try {
await ignoreMemorySimilarity({ cwd: dashboard.memoryCwd, targetA: group.targetA, pathA: group.pathA, targetB: group.targetB, pathB: group.pathB }); showNotice('info', `已忽略「${group.nameA || group.pathA}」与「${group.nameB || group.pathB}」`); await dashboard.refreshMemory();
} catch (err) { showNotice('error', '忽略相似记忆失败，请重试。'); throw err; } finally { setIgnoringKey(''); } }, [dashboard, ignoringKey, setIgnoringKey, showNotice, similarityDegraded]); }
function shouldSkipMemoryMergeAll({ consolidationJob, mergingAll, similarGroups, similarityDegraded }) { return similarityDegraded || !similarGroups.length || mergingAll || consolidationJob; }
async function memoryLaunchPreferences(resolveLaunchPreferences, cwd) { return typeof resolveLaunchPreferences === 'function' ? resolveLaunchPreferences(cwd) : null; }
function assertMemoryConsolidationStarted(started, jobID) { if (started?.status === 'failed') throw memoryConsolidationJobFailed(started); if (started?.status !== 'succeeded' && !jobID) throw new Error('智能整合未能启动，请稍后重试'); }
async function applyStartedMemoryConsolidation(options) { const { applyConsolidationResult, cwd, jobID, setConsolidationJob, showNotice, started } = options; if (started?.status === 'succeeded') {
await applyConsolidationResult(cwd, memoryConsolidationSucceededResult(started)); return; } setConsolidationJob({ cwd, jobId: jobID }); showNotice('info', '智能整合已在后台进行，完成后会自动更新'); }
function useMemoryMergeAllGroups(options) { const { applyConsolidationResult, consolidationJob, dashboard, mergingAll, resolveLaunchPreferences, setConsolidationJob, setMergingAll, showNotice, similarGroups, similarityDegraded } = options; return useCallback(async () => {
if (shouldSkipMemoryMergeAll({ consolidationJob, mergingAll, similarGroups, similarityDegraded })) return; if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; } setMergingAll(true); try {
const launchPreferences = await memoryLaunchPreferences(resolveLaunchPreferences, dashboard.memoryCwd); const started = await startConsolidateMemorySimilarities(memoryConsolidationStartPayload(dashboard.memoryCwd, launchPreferences));
const jobID = textValue(started?.jobId); assertMemoryConsolidationStarted(started, jobID); await applyStartedMemoryConsolidation({ applyConsolidationResult, cwd: dashboard.memoryCwd, jobID, setConsolidationJob, showNotice, started }); } catch (err) {
showNotice('error', '智能整合失败，请重试。'); throw err; } finally { setMergingAll(false); } }, [applyConsolidationResult, consolidationJob, dashboard, mergingAll, resolveLaunchPreferences, setConsolidationJob, setMergingAll, showNotice, similarGroups, similarityDegraded]); }
function memoryConsolidationStartPayload(cwd, launchPreferences) { return { cwd, provider: firstPresentText(launchPreferences?.modelProvider, launchPreferences?.provider), model: textValue(launchPreferences?.model),
codexModelProvider: firstPresentText(launchPreferences?.codexModelProvider, launchPreferences?.config?.codexModelProvider), }; }
function useMemoryConsolidationPolling({ applyConsolidationResult, setConsolidationJob, showNotice, similarity }) {
const job = similarity.consolidationJob; const jobKey = job?.cwd && job?.jobId ? `${job.cwd}\u0000${job.jobId}` : ''; const pollCountRef = useRef(0); const completedJobRef = useRef(''); useEffect(() => { pollCountRef.current = 0; completedJobRef.current = ''; }, [jobKey]);
const pollQuery = useQuery({ queryKey: ['memory', 'consolidation-job', textValue(job?.cwd), textValue(job?.jobId)], enabled: Boolean(jobKey), retry: false, refetchInterval: (query) => { if (!jobKey || query.state.error) return false;
return query.state.data?.status === 'succeeded' ? false : MEMORY_CONSOLIDATION_POLL_MS; }, queryFn: ({ signal }) => { if (!job?.cwd || !job?.jobId) throw new Error('智能整合任务缺少必要信息'); pollCountRef.current += 1; if (pollCountRef.current > MEMORY_CONSOLIDATION_MAX_POLLS) {
throw new Error('智能整合仍在进行，请稍后查看结果'); } return runBackgroundAction('memory.consolidation.poll', () => fetchMemoryConsolidationPoll(job.cwd, job.jobId, { signal })); }, });
useEffect(() => { if (!jobKey || !pollQuery.error) return; showMemoryConsolidationError(pollQuery.error, showNotice); setConsolidationJob(null); }, [jobKey, pollQuery.error, setConsolidationJob, showNotice]);
useEffect(() => { if (!jobKey || pollQuery.data?.status !== 'succeeded' || completedJobRef.current === jobKey) return undefined; completedJobRef.current = jobKey; let cancelled = false; (async () => { try {
const result = memoryConsolidationSucceededResult(pollQuery.data);
await runBackgroundAction('memory.consolidation.apply', () => applyConsolidationResult(job.cwd, result));
} catch (err) { if (!cancelled) showMemoryConsolidationError(err, showNotice); } finally { if (!cancelled) setConsolidationJob(null); } })(); return () => {
cancelled = true; }; }, [applyConsolidationResult, job?.cwd, jobKey, pollQuery.data, setConsolidationJob, showNotice]); }
function showMemoryConsolidationError(err, showNotice) { const message = errorMessage(err); const level = message.includes('仍在进行') ? 'warning' : 'error'; showNotice(level, level === 'warning' ? '智能整合仍在进行，请稍后查看结果。' : '智能整合失败，请查看 Health 诊断 ID。'); }
function useMemoryApplyConsolidationResult(queryClient, showNotice) { return useCallback(async (cwd, result) => { const summary = memoryConsolidationResultMessage(result); if (!Number(result?.failed) && !Number(result?.skipped)) {
queryClient.setQueryData(dashboardQueryKey(cwd, 'memory'), clearMemorySimilarGroups); } showNotice(summary.level, summary.message); await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(cwd, 'memory') }); }, [queryClient, showNotice]); }
function useMemoryPageModel({ projectPath, onSimilarCountChange, resolveLaunchPreferences }) {
const dashboard = useMemoryDashboard(projectPath); const { notice, showNotice } = useMemoryNotice(dashboard.memoryCwd); const [searchText, setSearchText] = useState(''); const [activeCategory, setActiveCategory] = useState('preference');
const [similarExpanded, setSimilarExpanded] = useState(false); const derived = useMemoryDerivedState(dashboard.snapshot, activeCategory, searchText, onSimilarCountChange); const autoDream = useMemoryAutoDream({ dashboard, showNotice });
const editor = useMemoryEditor({ dashboard, showNotice }); const deletion = useMemoryDelete({ dashboard, showNotice }); const { queryClient } = dashboard; const applyConsolidationResult = useMemoryApplyConsolidationResult(queryClient, showNotice);
const similarity = useMemorySimilarityActions({
applyConsolidationResult, dashboard, resolveLaunchPreferences, showNotice, similarGroups: derived.similarGroups, similarityDegraded: derived.similarityDegraded,
}); useMemoryConsolidationPolling({ applyConsolidationResult, setConsolidationJob: similarity.setConsolidationJob,
showNotice, similarity, }); return {
activeCategory,
autoDream: { ...autoDream, toggleAutoDream: () => runUIAction('memory.auto-dream.toggle', autoDream.toggleAutoDream) },
dashboard,
deletion: { ...deletion, confirmDelete: () => runUIAction('memory.entry.delete', deletion.confirmDelete) },
derived,
editor: { ...editor, openEdit: (entry) => runUIAction('memory.entry.open', () => editor.openEdit(entry)), saveEditor: () => runUIAction('memory.entry.save', editor.saveEditor) },
notice,
searchText,
setActiveCategory,
setSearchText,
setSimilarExpanded,
similarExpanded,
similarity: {
...similarity,
confirmMerge: () => runUIAction('memory.similarity.merge', similarity.confirmMerge),
ignoreGroup: (group) => runUIAction('memory.similarity.ignore', () => similarity.ignoreGroup(group)),
mergeAllGroups: () => runUIAction('memory.similarity.consolidate', similarity.mergeAllGroups),
},
showNotice,
}; }
function MemoryPage({ copy = APP_COPY.zh.memory, projectPath, onSimilarCountChange, resolveLaunchPreferences }) { const model = useMemoryPageModel({ projectPath, onSimilarCountChange, resolveLaunchPreferences }); return <MemoryPageView copy={copy} model={model} />; }
function MemoryPageView({ copy, model }) { return ( <section className="memory-page"> <MemoryPageHeader copy={copy} />
<MemoryStats autoDream={model.autoDream} categoryCounts={model.derived.categoryCounts} copy={copy} disabled={model.dashboard.isProjectPending} health={model.derived.health} />
<MemoryToolbar copy={copy} disabled={model.dashboard.isProjectPending} editor={model.editor} searchText={model.searchText} setSearchText={model.setSearchText} showNotice={model.showNotice} />
<MemorySimilaritySection copy={copy} degraded={model.derived.similarityDegraded} expanded={model.similarExpanded}
groups={model.derived.similarGroups} setExpanded={model.setSimilarExpanded} similarity={model.similarity} /> <MemoryStatusMessages copy={copy} dashboard={model.dashboard} notice={model.notice} />
<MemoryTabs activeCategory={model.activeCategory} categoryCounts={model.derived.categoryCounts} copy={copy} setActiveCategory={model.setActiveCategory} /> <MemoryCardsSection copy={copy} dashboard={model.dashboard} deletion={model.deletion} editor={model.editor}
searchText={model.searchText} visibleEntries={model.derived.visibleEntries} /> <MemoryModals deletion={model.deletion} editor={model.editor} similarity={model.similarity} /> </section> ); }
function MemoryPageHeader({ copy }) {
  return (
    <PageHeader icon={MemoryStick} title={copy.title} />
  );
}
function MemoryToolbar(props) {
  const { copy, disabled, editor, searchText, setSearchText, showNotice } = props;
  // 未选择项目时不使用原生 disabled（点击零反馈），改为 aria-disabled + 点击提示引导。
  const handleCreateClick = () => {
    if (disabled) {
      showNotice?.('info', '请先在聊天页选择项目，再创建记忆。');
      return;
    }
    editor.setCreateMenuOpen((open) => !open);
  };
  return (
    <div className="memory-toolbar fusion-toolbar" data-testid="memory-toolbar">
        <label className="memory-search fusion-toolbar-input">
          <Search size={17} />
          <input aria-label={copy.search} placeholder={copy.searchPlaceholder} value={searchText} onChange={(event) => setSearchText(event.target.value)} />
        </label>
        <div className="memory-create">
          <button type="button" className="suiyuan-btn-fusion-ghost memory-create-button" aria-label={`+ ${copy.new} ▾`} aria-haspopup="menu" aria-expanded={editor.createMenuOpen} aria-disabled={disabled || undefined} title={disabled ? '请先选择项目' : undefined} onClick={handleCreateClick}>
            <Plus size={15} aria-hidden="true" />
            <span>{copy.new}</span>
            <ChevronDown size={14} aria-hidden="true" className="memory-create-chevron" />
          </button>
          {editor.createMenuOpen ? <MemoryCreateMenu copy={copy} onCreate={editor.openCreate} /> : null}
        </div>
    </div>
  );
}
function MemoryCreateMenu({ copy, onCreate }) { return ( <div role="menu" aria-label="新建记忆" className="memory-create-menu memory-create-menu-list">
<button type="button" role="menuitem" onClick={() => onCreate('feedback')}>{copy.newPreference}</button>
<button type="button" role="menuitem" onClick={() => onCreate('project')}>{copy.newProject}</button> </div> ); }
function MemoryStats({ autoDream, categoryCounts, copy, disabled, health }) {
  return (
    <div className="memory-stats">
      <Panel className="memory-overview-panel" title={copy.overview}>
        <div className="memory-overview-content">
          <strong className="big memory-overview-total">{categoryCounts.all}</strong>
          <div className="memory-overview-breakdown" aria-label={copy.overview}>
            <span><span className="orange-dot" />{categoryCounts.preference} {copy.preference}</span>
            <span><span className="green-dot" />{categoryCounts.project} {copy.project}</span>
          </div>
        </div>
      </Panel>
      {health ? <MemoryHealthPanel copy={copy} health={health} /> : null}
      <MemoryAutoDreamPanel autoDream={autoDream} copy={copy} disabled={disabled} />
    </div>
  );
}
function MemoryHealthPanel({ copy, health }) { const prefPercent = memoryHealthPercent(health.preferenceCount, health.maxPerCategory); const projPercent = memoryHealthPercent(health.projectCount, health.maxPerCategory); return (
<Panel className="memory-health-panel" title={copy.health}> <p>{copy.preference} <meter value={health.preferenceCount} max={health.maxPerCategory} /> {health.preferenceCount} / {health.maxPerCategory}</p>
<div className={'memory-health-track ' + memoryHealthClass(prefPercent)}><span style={{ width: String(prefPercent) + '%' }} /></div> <p>{copy.project} <meter value={health.projectCount} max={health.maxPerCategory} /> {health.projectCount} / {health.maxPerCategory}</p>
<div className={'memory-health-track ' + memoryHealthClass(projPercent)}><span style={{ width: String(projPercent) + '%' }} /></div> <p><span className="green-dot" /> {copy.healthy}</p> </Panel> ); }
function MemoryAutoDreamPanel({ autoDream, copy, disabled }) {
  return (
    <Panel className="memory-auto-dream-panel" title={copy.autoDream}>
      <div className="memory-auto-dream-content">
        <p className="memory-auto-dream-status">
          <span className={autoDream.enabled ? 'green-dot' : 'orange-dot'} /> {autoDream.enabled ? copy.autoDreamOn : copy.autoDreamOff}
        </p>
        <small className="memory-auto-dream-description">{copy.autoDreamDescription}</small>
        <button type="button" className="memory-auto-dream-toggle" aria-disabled={disabled || undefined} title={disabled ? '请先选择项目' : undefined} onClick={() => { void autoDream.toggleAutoDream(); }} disabled={autoDream.toggling}>
          {autoDream.enabled ? copy.disable : copy.enable}
        </button>
        {autoDream.pendingRestart ? <small className="memory-pending">{copy.pendingRestart}</small> : null}
      </div>
    </Panel>
  );
}
function MemorySimilaritySection(options) {
const { copy, degraded, expanded, groups, setExpanded, similarity } = options; if (!groups.length && !degraded) return null; const busy = degraded || memorySimilarityBusy(similarity); return (
<> <div className={'similar-alert' + (degraded ? ' is-degraded' : '')} role={degraded ? 'status' : undefined}> <AlertTriangle size={20} />
<span>{degraded ? '相似记忆状态暂不可用，已暂停整合与忽略操作' : `${groups.length} ${copy.similarGroupsSuffix}`}</span>
{groups.length ? <button type="button" onClick={() => { void similarity.mergeAllGroups(); }} disabled={busy}>{memoryMergeAllLabel(similarity, copy)}</button> : null}
{groups.length ? <button type="button" onClick={() => setExpanded((current) => !current)}>{expanded ? copy.collapse : copy.expand}</button> : null} </div>
{expanded && groups.length ? <MemorySimilarityList busy={busy} copy={copy} groups={groups} similarity={similarity} /> : null} </> ); }
function MemorySimilarityList({ busy, copy, groups, similarity }) { return ( <div className="memory-similar-list"> {groups.map((group) => ( <MemorySimilarItem busy={busy} copy={copy} group={group} similarity={similarity} key={memoryPairKey(group)} /> ))} </div> ); }
function MemorySimilarItem({ busy, copy, group, similarity }) { const key = memoryPairKey(group); return (
<div className="memory-similar-item"> <span>「{group.nameA || group.pathA}」与「{group.nameB || group.pathB}」</span> <strong>{formatMemoryScore(group.score)}</strong> <button type="button" onClick={() => similarity.setMergeTarget(group)} disabled={busy}>{copy.merge}</button>
<button type="button" className="ghost" onClick={() => { void similarity.ignoreGroup(group); }} disabled={busy}> {similarity.ignoringKey === key ? '...' : copy.ignore} </button> </div> ); }
function memorySimilarityBusy(similarity) { return similarity.mergingAll || Boolean(similarity.consolidationJob) || Boolean(similarity.mergingKey) || Boolean(similarity.ignoringKey); }
function memoryMergeAllLabel(similarity, copy) { if (similarity.mergingAll) return copy.mergeStarting; if (similarity.consolidationJob) return copy.mergeRunning; return copy.mergeAll; }
function MemoryStatusMessages({ copy, dashboard, notice }) { const scanNotice = memoryScanDiagnostic(dashboard.snapshot); return (
<> {notice.message ? <div className={'memory-notice is-' + notice.level}>{notice.message}</div> : null} {scanNotice ? <div className={'memory-notice is-' + scanNotice.level}>{scanNotice.message}</div> : null}
{dashboard.isProjectPending ? <div className="memory-notice is-info">{copy.connecting}</div> : null} {!dashboard.isProjectPending && dashboard.loading ? <div className="memory-notice is-info">{copy.loading}</div> : null}
{dashboard.syncError ? <MemorySyncError copy={copy} message={dashboard.syncError} onRefresh={dashboard.refreshMemory} /> : null} {dashboard.error ? <MemorySyncError copy={copy} message={dashboard.error} onRefresh={dashboard.refreshMemory} /> : null} </> ); }
function MemorySyncError({ copy, message, onRefresh }) { return ( <div className="memory-notice is-error" role="alert"> <span>{message}</span> <button type="button" onClick={() => { void onRefresh(); }}>{copy.retrySync}</button> </div> ); }
function MemoryTabs({ activeCategory, categoryCounts, copy, setActiveCategory }) { return ( <div className="memory-tabs" role="tablist" aria-label={copy.tabsAria}> {MEMORY_CATEGORY_KEYS.map((key) => (
<button key={key} type="button" role="tab" aria-selected={activeCategory === key} className={activeCategory === key ? 'active' : ''} onClick={() => setActiveCategory(key)} > {copy[key]} {categoryCounts[key] || 0} </button> ))} </div> ); }
function MemoryCardsSection(props) { const { copy, dashboard, deletion, editor, searchText, visibleEntries } = props; if (!dashboard.error && !dashboard.isProjectPending && !dashboard.loading && visibleEntries.length === 0) {
return <MemoryEmptyState copy={copy} searchText={searchText} />; } if (!dashboard.error && !dashboard.isProjectPending && visibleEntries.length > 0) { return <MemoryCardsList copy={copy} deletion={deletion} editor={editor} visibleEntries={visibleEntries} />; } return null; }
function MemoryEmptyState({ copy, searchText }) { return ( <div className="empty-state memory-empty"> <span><MemoryStick size={24} /></span> <h2>{searchText ? copy.emptySearchTitle : copy.emptyTitle}</h2> <p>{searchText ? copy.emptySearchText : copy.emptyText}</p> </div> ); }
function memoryEntryKey(entry) { return [entry.target, entry.path].join(':'); }
function MemoryCardsList({ copy, deletion, editor, visibleEntries }) { return ( <div className="memory-cards"> {visibleEntries.map((entry) => (
<MemoryCard key={entry.id} entry={entry} busy={editor.busyKey === memoryEntryKey(entry)} copy={copy} deleting={deletion.deletingKey === memoryEntryKey(entry)} onEdit={editor.openEdit} onDelete={deletion.setDeleteTarget} /> ))} </div> ); }
function MemoryModals({ deletion, editor, similarity }) { const editorState = editor.editor; return ( <> {editorState.open ? (
<MemoryEditorModal editor={editorState} saving={editor.saving} onClose={editor.closeEditor} onChange={editor.updateEditorForm} onSave={editor.saveEditor} onDelete={() => requestMemoryEditorDelete(editor, deletion)} />
) : null} {deletion.deleteTarget ? <MemoryDeleteDialog deletion={deletion} /> : null} {similarity.mergeTarget ? <MemoryMergeDialog similarity={similarity} /> : null} </> ); }
function requestMemoryEditorDelete(editor, deletion) { const form = editor.editor.form; deletion.setDeleteTarget({ target: form.target, path: form.existingPath, name: form.name, title: form.title, }); editor.setEditor((current) => ({ ...current, open: false })); }
function MemoryDeleteDialog({ deletion }) { const entry = deletion.deleteTarget; return ( <MemoryDeleteModal entry={entry} deleting={deletion.deletingKey === memoryEntryKey(entry)} onClose={() => deletion.setDeleteTarget(null)} onConfirm={deletion.confirmDelete} /> ); }
function MemoryMergeDialog({ similarity }) { const group = similarity.mergeTarget; return ( <MemoryMergeModal group={group} merging={similarity.mergingKey === memoryPairKey(group)} onClose={() => similarity.setMergeTarget(null)} onConfirm={similarity.confirmMerge} /> ); }
function MemoryCard(props) { const { copy, entry, busy, deleting, onEdit, onDelete } = props; return (
<article className={`memory-card ${entry.category === 'project' ? 'type-project' : 'type-preference'}`}> <header> <h3>{memoryEntryTitle(entry)}</h3> <span>{entry.tag}</span> {entry.source === 'dream' ? <em>{copy.dream}</em> : null} </header>
{entry.description ? <p>{entry.description}</p> : null} <code>{entry.preview || copy.noPreview}</code> <footer> <time>{sharedFileTimestamp(entry.updatedAt)}</time>
<button type="button" onClick={() => { void onEdit(entry); }} disabled={busy}>{busy ? copy.loadingAction : copy.edit}</button> <button type="button" className="danger" onClick={() => onDelete(entry)} disabled={deleting}>{deleting ? copy.deleting : copy.delete}</button>
</footer> </article> ); }
function memoryEditorTypeChangePatch(type) { return { type, target: memoryTargetForType(type), content: memoryTemplateForType(type), }; }
function MemoryEditorHeader({ editor, form }) { return ( <header> <div> <h2>{editor.mode === 'edit' ? '编辑记忆' : '新建记忆'}</h2> <p>{form.type === 'project' ? '项目记忆' : '偏好记忆'}</p> </div> </header> ); }
function MemoryEditorFields({ form, identityLocked, onChange }) { return ( <div className="memory-form-grid"> <label>分类 <select value={form.type} onChange={(event) => onChange(memoryEditorTypeChangePatch(event.target.value))} disabled={identityLocked} >
{MEMORY_EDITOR_TYPES.map((type) => <option key={type.key} value={type.key}>{type.label}</option>)} </select> </label> <label>描述 <input value={form.description} onChange={(event) => onChange({ description: event.target.value })} placeholder="一句话描述为什么值得长期保留" /> </label>
<label>卡片标题 <input value={form.title} onChange={(event) => onChange({ title: event.target.value })} placeholder="卡片上显示的短标题" /> </label> </div> ); }
function MemoryEditorContentField({ form, onChange }) { return ( <label className="memory-content-label">内容 <textarea rows={12} value={form.content} onChange={(event) => onChange({ content: event.target.value })} /> </label> ); }
function MemoryEditorActions(props) { const { form, saving, onClose, onChange, onDelete, onSave } = props; return (
<div className="memory-editor-actions"> <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button> {form.existingPath ? <button type="button" className="danger" onClick={onDelete} disabled={saving}>删除</button> : null}
<button type="button" onClick={() => onChange({ content: memoryTemplateForType(form.type) })} disabled={saving}>套用当前类型模板</button>
<button type="button" className="light" onClick={() => { void onSave(); }} disabled={saving || !textValue(form.description) || !textValue(form.content)}> {saving ? '保存中...' : '保存'} </button> </div> ); }
function MemoryEditorModal(props) { const { editor, saving, onClose, onChange, onSave, onDelete } = props; const form = editor.form; const identityLocked = editor.mode === 'edit' && Boolean(form.existingPath); return (
<FocusTrapDialog ariaLabel={editor.mode === 'edit' ? '编辑记忆' : '新建记忆'} className="modal-box memory-editor-modal" closeDisabled={saving} onClose={onClose} > <MemoryEditorHeader editor={editor} form={form} />
<MemoryEditorFields form={form} identityLocked={identityLocked} onChange={onChange} /> <MemoryEditorContentField form={form} onChange={onChange} />
<MemoryEditorActions form={form} saving={saving} onClose={onClose} onChange={onChange} onDelete={onDelete} onSave={onSave} /> </FocusTrapDialog> ); }
function MemoryDeleteModal({ entry, deleting, onClose, onConfirm }) { return (
<FocusTrapDialog ariaLabel="删除记忆" closeDisabled={deleting} onClose={onClose}> <header> <h2>删除记忆</h2> <button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button> </header> <p>删除后无法恢复。如果后续可能重用，建议先编辑备份内容。</p>
<p className="path">{memoryEntryTitle(entry)}</p> <footer> <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
<button type="button" className="danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button> </footer> </FocusTrapDialog> ); }
function MemoryMergeModal({ group, merging, onClose, onConfirm }) { return (
<FocusTrapDialog ariaLabel="整合相似记忆" closeDisabled={merging} onClose={onClose}> <header> <div> <h2>整合相似记忆</h2> <p>相似度 {formatMemoryScore(group.score)}</p> </div> <button type="button" className="ghost" onClick={onClose} disabled={merging}>关闭</button> </header>
<p>合并到：{group.nameA || '保留项'}</p> <p>移除：{group.nameB || '重复项'}</p> <footer> <button type="button" className="ghost" onClick={onClose} disabled={merging}>取消</button>
<button type="button" className="light" onClick={() => { void onConfirm(); }} disabled={merging}>{merging ? '整合中...' : '确认整合'}</button> </footer> </FocusTrapDialog> ); }
export { MemoryPage };

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, MemoryStick, Plus, Search } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { APP_BRAND_NAME, APP_COPY } from '../../shared/i18n/appI18n.js';
import { deleteMemoryEntry, getMemoryConsolidationStatus, getMemoryEntry, ignoreMemorySimilarity, mergeMemoryEntries, setMemoryAutoDreamIntent, startConsolidateMemorySimilarities, upsertMemoryEntry } from '../../services/modules/memoryService.js';
import { dashboardQueryErrorState, dashboardQueryKey, errorMessage, fetchMemoryDashboard, firstText, memoryHealth, memoryNoticeText, optionalSettingsCwd, queryHasSnapshot, sharedFileTimestamp, textValue } from '../shared/pageShared.js';
import { PageHeader, Panel } from '../shared/pageComponents.jsx';
import './MemoryPage.css';

const MEMORY_CONSOLIDATION_POLL_MS = 2000;

const MEMORY_CONSOLIDATION_MAX_POLLS = 180;

const MEMORY_CATEGORY_KEYS = Object.freeze(['preference', 'project', 'all']);

const MEMORY_EDITOR_TYPES = Object.freeze([
  { key: 'feedback', label: '偏好' },
  { key: 'project', label: '项目' },
]);

function delay(ms) {
  return new Promise((resolve) => {
    globalThis.setTimeout(resolve, ms);
  });
}

function memoryTemplateForType(type) {
  switch (textValue(type)) {
    case 'feedback':
      return '规则\n原因：\n如何应用：';
    case 'project':
      return '事实\n原因：\n如何应用：';
    case 'reference':
      return '指向：\n为什么重要：';
    default:
      return '用户偏好：';
  }
}

function memoryTargetForType(type) {
  return textValue(type) === 'project' ? 'team' : 'private';
}

function memorySlugText(value) {
  return (
    textValue(value)
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48)
  );
}

function memoryNameHash(value) {
  let hash = 5381;
  const text = textValue(value);
  for (let index = 0; index < text.length; index += 1) {
    hash = ((hash << 5) + hash + text.charCodeAt(index)) >>> 0;
  }
  return hash.toString(36);
}

function memoryAutoName(form) {
  const existing = textValue(form?.name);
  if (existing) return existing;
  const type = textValue(form?.type) || 'project';
  const base = firstText(form?.title, form?.description, form?.content, type);
  const slug = memorySlugText(base);
  if (slug) return slug;
  return `${type}-${memoryNameHash(base)}`;
}

function defaultMemoryForm(type = 'project', target = memoryTargetForType(type)) {
  return {
    target,
    existingPath: '',
    name: '',
    description: '',
    title: '',
    type,
    content: memoryTemplateForType(type),
  };
}

function normalizeAutoDreamIntent(value) {
  if (value === true) return true;
  if (value === false) return false;
  return null;
}

function memoryHealthPercent(count, max) {
  const safeMax = Number(max) || 1;
  const safeCount = Number(count) || 0;
  return Math.min(100, Math.max(0, Math.round((safeCount / safeMax) * 100)));
}

function memoryHealthClass(percent) {
  if (percent >= 100) return 'danger';
  if (percent >= 80) return 'warning';
  return '';
}

function memoryMatches(entry, query) {
  const needle = textValue(query).toLowerCase();
  if (!needle) return true;
  return (
    [entry.title, entry.name, entry.description, entry.path, entry.type, entry.preview]
    .some((value) => textValue(value).toLowerCase().includes(needle))
  );
}

function sortMemoryEntries(entries) {
  return [...entries].sort((left, right) => {
    const leftTime = new Date(left.updatedAt || 0).getTime();
    const rightTime = new Date(right.updatedAt || 0).getTime();
    const safeLeft = Number.isFinite(leftTime) ? leftTime : 0;
    const safeRight = Number.isFinite(rightTime) ? rightTime : 0;
    return safeRight - safeLeft || left.title.localeCompare(right.title);
  });
}

function memoryPairKey(group) {
  return `${group.targetA}:${group.pathA}|${group.targetB}:${group.pathB}`;
}

function formatMemoryScore(score) {
  return `${Math.round((Number(score) || 0) * 100)}%`;
}

function memoryEntryTitle(entry) {
  return firstText(entry.title, entry.description, entry.name, entry.path);
}

function memoryConsolidationStats(result) {
  return {
    merged: Number(result?.merged) || 0,
    ignored: Number(result?.ignored) || 0,
    failed: Number(result?.failed) || 0,
    skipped: Number(result?.skipped) || 0,
  };
}

function memoryConsolidationParts(stats) {
  return [
    `已整合 ${stats.merged} 组`,
    stats.ignored ? `${stats.ignored} 组判定不应合` : '',
    stats.failed ? `${stats.failed} 组失败` : '',
    stats.skipped ? `${stats.skipped} 组跳过` : '',
  ].filter(Boolean);
}

function firstMemoryConsolidationError(result) {
  return Array.isArray(result?.errors) ? textValue(result.errors[0]) : '';
}

function memoryConsolidationResultMessage(result) {
  const stats = memoryConsolidationStats(result);
  const firstError = firstMemoryConsolidationError(result);
  return {
    level: stats.failed || stats.skipped ? 'warning' : 'info',
    message: `${memoryConsolidationParts(stats).join('，')}${firstError ? `，原因：${firstError}` : ''}`,
  };
}

function memoryConsolidationJobFailed(status) {
  const message = textValue(status?.error) || '智能整合暂时失败，请稍后重试';
  return new Error(message);
}

function plainMemoryObject(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : null;
}

function memoryHealthHasSimilarGroups(health) {
  return Array.isArray(health?.similarGroups) && health.similarGroups.length > 0;
}

function memorySnapshotWithClearedSimilarGroups(snapshot, overview, health) {
  return {
    ...snapshot,
    overview: {
      ...overview,
      health: {
        ...health,
        similarGroups: [],
      },
    },
  };
}

function clearMemorySimilarGroups(snapshot) {
  const validSnapshot = plainMemoryObject(snapshot);
  const overview = plainMemoryObject(validSnapshot?.overview) || {};
  const health = plainMemoryObject(overview.health);
  if (!validSnapshot || !memoryHealthHasSimilarGroups(health)) return snapshot;
  return memorySnapshotWithClearedSimilarGroups(validSnapshot, overview, health);
}

async function waitForMemoryConsolidationJob(cwd, jobID) {
  for (let attempt = 0; attempt < MEMORY_CONSOLIDATION_MAX_POLLS; attempt += 1) {
    const status = await getMemoryConsolidationStatus({ cwd, jobId: jobID });
    if (status?.status === 'succeeded') return status.result || {};
    if (status?.status === 'failed') throw memoryConsolidationJobFailed(status);
    if (status?.status !== 'running') {
      throw new Error('智能整合状态异常，请稍后重试');
    }
    await delay(MEMORY_CONSOLIDATION_POLL_MS);
  }
  throw new Error('智能整合仍在进行，请稍后查看结果');
}

function useMemoryDashboard(projectPath) {
  const memoryCwd = optionalSettingsCwd(projectPath);
  const queryClient = useQueryClient();
  const memoryQuery = useQuery({
    queryKey: dashboardQueryKey(memoryCwd, 'memory'),
    queryFn: () => fetchMemoryDashboard(memoryCwd),
    enabled: Boolean(memoryCwd),
  });
  const hasSnapshot = queryHasSnapshot(memoryQuery);
  const snapshot = memoryQuery.data || { overview: {}, entries: [] };
  const loading = Boolean(memoryCwd) && memoryQuery.isPending && !hasSnapshot;
  const { cachedSyncError: syncError, blockingError: error } = dashboardQueryErrorState(memoryQuery, hasSnapshot);
  const refreshMemory = useCallback(async () => {
    if (!memoryCwd) return;
    await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(memoryCwd, 'memory') });
  }, [memoryCwd, queryClient]);
  return { error, isProjectPending: !memoryCwd, loading, memoryCwd, queryClient, refreshMemory, snapshot, syncError };
}

function useMemoryNotice(memoryCwd) {
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  useEffect(() => { setNotice({ level: 'info', message: '' }); }, [memoryCwd]);
  const showNotice = useCallback((level, message) => {
    setNotice({ level: level || 'info', message: memoryNoticeText(message) });
  }, []);
  return { notice, showNotice };
}

function useMemoryDerivedState(snapshot, activeCategory, searchText, onSimilarCountChange) {
  const preferenceEntries = useMemo(() => snapshot.entries.filter((entry) => entry.category === 'preference'), [snapshot.entries]);
  const projectEntries = useMemo(() => snapshot.entries.filter((entry) => entry.category === 'project'), [snapshot.entries]);
  const entriesByCategory = useMemo(() => ({
    all: snapshot.entries,
    preference: preferenceEntries,
    project: projectEntries,
  }), [preferenceEntries, projectEntries, snapshot.entries]);
  const categoryCounts = useMemo(() => ({
    all: snapshot.entries.length,
    preference: preferenceEntries.length,
    project: projectEntries.length,
  }), [preferenceEntries.length, projectEntries.length, snapshot.entries.length]);
  const visibleEntries = useMemo(() => (
    sortMemoryEntries((entriesByCategory[activeCategory] || []).filter((entry) => memoryMatches(entry, searchText)))
  ), [activeCategory, entriesByCategory, searchText]);
  const health = useMemo(() => memoryHealth(snapshot.overview, categoryCounts), [snapshot.overview, categoryCounts]);
  const similarGroups = health?.similarGroups || [];
  useEffect(() => {
    if (typeof onSimilarCountChange === 'function') onSimilarCountChange(similarGroups.length);
  }, [onSimilarCountChange, similarGroups.length]);
  return { categoryCounts, health, similarGroups, visibleEntries };
}

function useMemoryAutoDream({ dashboard, showNotice }) {
  const [autoToggling, setAutoToggling] = useState(false);
  const runtime = dashboard.snapshot.overview?.autoDreamEnabled === true;
  const intent = normalizeAutoDreamIntent(dashboard.snapshot.overview?.autoDreamIntent);
  const enabled = intent === null ? runtime : intent;
  const pendingRestart = intent !== null && intent !== runtime;
  const toggleAutoDream = useCallback(async () => {
    if (autoToggling || dashboard.isProjectPending) return;
    const next = !enabled;
    setAutoToggling(true);
    try {
      await setMemoryAutoDreamIntent({ enabled: next });
      showNotice('warning', `自动沉淀已切换为${next ? '开启' : '关闭'}，重启 ${APP_BRAND_NAME} 后生效`);
      await dashboard.refreshMemory();
    } catch (err) {
      showNotice('error', `切换自动沉淀失败：${errorMessage(err)}`);
    } finally {
      setAutoToggling(false);
    }
  }, [autoToggling, dashboard, enabled, showNotice]);
  return { enabled, pendingRestart, toggleAutoDream, toggling: autoToggling };
}

function useMemoryEditor({ dashboard, showNotice }) {
  const [createMenuOpen, setCreateMenuOpen] = useState(false);
  const [editor, setEditor] = useState({ open: false, mode: 'create', form: defaultMemoryForm('project') });
  const [busyKey, setBusyKey] = useState('');
  const [saving, setSaving] = useState(false);
  const updateEditorForm = useCallback((patch) => setEditor((current) => ({ ...current, form: { ...current.form, ...patch } })), []);
  const openCreate = useCallback((type) => {
    if (dashboard.isProjectPending) return;
    setEditor({ open: true, mode: 'create', form: defaultMemoryForm(type, memoryTargetForType(type)) });
    setCreateMenuOpen(false);
  }, [dashboard.isProjectPending]);
  const openEdit = useCallback(async (entry) => {
    if (!dashboard.memoryCwd) return;
    const key = `${entry.target}:${entry.path}`;
    setBusyKey(key);
    try {
      const detail = await getMemoryEntry({ cwd: dashboard.memoryCwd, target: entry.target, path: entry.path });
      setEditor({ open: true, mode: 'edit', form: memoryEditorFormFromDetail(detail, entry) });
    } catch (err) {
      showNotice('error', `加载失败：${errorMessage(err)}`);
    } finally {
      setBusyKey('');
    }
  }, [dashboard.memoryCwd, showNotice]);
  const closeEditor = useCallback(() => {
    if (!saving) setEditor((current) => ({ ...current, open: false }));
  }, [saving]);
  const saveEditor = useMemoryEditorSave({ dashboard, editor, saving, setEditor, setSaving, showNotice });
  return { busyKey, closeEditor, createMenuOpen, editor, openCreate, openEdit, saveEditor, saving, setCreateMenuOpen, setEditor, updateEditorForm };
}

function memoryEditorFormFromDetail(detail, entry) {
  return {
    target: firstText(detail?.target, entry.target),
    existingPath: firstText(detail?.path, entry.path),
    name: firstText(detail?.name, entry.name),
    description: firstText(detail?.description, entry.description),
    title: firstText(detail?.title, entry.title),
    type: firstText(detail?.type, entry.type),
    content: firstText(detail?.content, entry.preview, memoryTemplateForType(entry.type)),
  };
}

function useMemoryEditorSave({ dashboard, editor, saving, setEditor, setSaving, showNotice }) {
  return useCallback(async () => {
    if (saving) return;
    if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    const form = editor.form;
    const description = textValue(form.description);
    const content = textValue(form.content);
    if (!description) { showNotice('error', '请先填写描述'); return; }
    if (!content) { showNotice('error', '内容不能为空'); return; }
    setSaving(true);
    try {
      const type = textValue(form.type) || 'project';
      await upsertMemoryEntry(memoryUpsertPayload(dashboard.memoryCwd, form, type, description, content));
      setEditor((current) => ({ ...current, open: false }));
      showNotice('info', '已保存');
      await dashboard.refreshMemory();
    } catch (err) {
      showNotice('error', `保存失败：${errorMessage(err)}`);
    } finally {
      setSaving(false);
    }
  }, [dashboard, editor.form, saving, setEditor, setSaving, showNotice]);
}

function memoryUpsertPayload(cwd, form, type, description, content) {
  return {
    cwd,
    target: form.existingPath ? form.target : memoryTargetForType(type),
    existingPath: form.existingPath,
    name: memoryAutoName({ ...form, type }),
    description,
    title: textValue(form.title),
    type,
    content,
  };
}

function useMemoryDelete({ dashboard, showNotice }) {
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deletingKey, setDeletingKey] = useState('');
  const confirmDelete = useCallback(async () => {
    if (!deleteTarget || deletingKey) return;
    if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    const key = `${deleteTarget.target}:${deleteTarget.path}`;
    setDeletingKey(key);
    try {
      await deleteMemoryEntry({ cwd: dashboard.memoryCwd, target: deleteTarget.target, path: deleteTarget.path });
      showNotice('info', `已删除：${memoryEntryTitle(deleteTarget)}`);
      setDeleteTarget(null);
      await dashboard.refreshMemory();
    } catch (err) {
      showNotice('error', `删除失败：${errorMessage(err)}`);
    } finally {
      setDeletingKey('');
    }
  }, [dashboard, deleteTarget, deletingKey, showNotice]);
  return { confirmDelete, deletingKey, deleteTarget, setDeleteTarget };
}

function useMemorySimilarityActions({ applyConsolidationResult, dashboard, resolveLaunchPreferences, showNotice, similarGroups }) {
  const [mergeTarget, setMergeTarget] = useState(null);
  const [mergingAll, setMergingAll] = useState(false);
  const [ignoringKey, setIgnoringKey] = useState('');
  const [mergingKey, setMergingKey] = useState('');
  const [consolidationJob, setConsolidationJob] = useState(null);
  const confirmMerge = useMemoryConfirmMerge({ dashboard, mergeTarget, mergingKey, setMergeTarget, setMergingKey, showNotice });
  const ignoreGroup = useMemoryIgnoreGroup({ dashboard, ignoringKey, setIgnoringKey, showNotice });
  const mergeAllGroups = useMemoryMergeAllGroups({ applyConsolidationResult, consolidationJob, dashboard, mergingAll, resolveLaunchPreferences, setConsolidationJob, setMergingAll, showNotice, similarGroups });
  return { confirmMerge, consolidationJob, ignoreGroup, ignoringKey, mergeAllGroups, mergeTarget, mergingAll, mergingKey, setConsolidationJob, setMergeTarget };
}

function useMemoryConfirmMerge({ dashboard, mergeTarget, mergingKey, setMergeTarget, setMergingKey, showNotice }) {
  return useCallback(async () => {
    if (!mergeTarget || mergingKey) return;
    if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    const key = memoryPairKey(mergeTarget);
    setMergingKey(key);
    try {
      await mergeMemoryEntries({ cwd: dashboard.memoryCwd, targetA: mergeTarget.targetA, pathA: mergeTarget.pathA, targetB: mergeTarget.targetB, pathB: mergeTarget.pathB });
      showNotice('info', `已整合「${mergeTarget.nameA || mergeTarget.pathA}」与「${mergeTarget.nameB || mergeTarget.pathB}」`);
      setMergeTarget(null);
      await dashboard.refreshMemory();
    } catch (err) {
      showNotice('error', `整合失败：${errorMessage(err)}`);
    } finally {
      setMergingKey('');
    }
  }, [dashboard, mergeTarget, mergingKey, setMergeTarget, setMergingKey, showNotice]);
}

function useMemoryIgnoreGroup({ dashboard, ignoringKey, setIgnoringKey, showNotice }) {
  return useCallback(async (group) => {
    const key = memoryPairKey(group);
    if (ignoringKey) return;
    if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    setIgnoringKey(key);
    try {
      await ignoreMemorySimilarity({ cwd: dashboard.memoryCwd, targetA: group.targetA, pathA: group.pathA, targetB: group.targetB, pathB: group.pathB });
      showNotice('info', `已忽略「${group.nameA || group.pathA}」与「${group.nameB || group.pathB}」`);
      await dashboard.refreshMemory();
    } catch (err) {
      showNotice('error', `忽略失败：${errorMessage(err)}`);
    } finally {
      setIgnoringKey('');
    }
  }, [dashboard, ignoringKey, setIgnoringKey, showNotice]);
}

function shouldSkipMemoryMergeAll({ consolidationJob, mergingAll, similarGroups }) {
  return !similarGroups.length || mergingAll || consolidationJob;
}

async function memoryLaunchPreferences(resolveLaunchPreferences, cwd) {
  return typeof resolveLaunchPreferences === 'function' ? resolveLaunchPreferences(cwd) : null;
}

function assertMemoryConsolidationStarted(started, jobID) {
  if (started?.status === 'failed') throw memoryConsolidationJobFailed(started);
  if (started?.status !== 'succeeded' && !jobID) throw new Error('智能整合未能启动，请稍后重试');
}

async function applyStartedMemoryConsolidation({ applyConsolidationResult, cwd, jobID, setConsolidationJob, showNotice, started }) {
  if (started?.status === 'succeeded') {
    await applyConsolidationResult(cwd, started.result || {});
    return;
  }
  setConsolidationJob({ cwd, jobId: jobID });
  showNotice('info', '智能整合已在后台进行，完成后会自动更新');
}

function useMemoryMergeAllGroups({ applyConsolidationResult, consolidationJob, dashboard, mergingAll, resolveLaunchPreferences, setConsolidationJob, setMergingAll, showNotice, similarGroups }) {
  return useCallback(async () => {
    if (shouldSkipMemoryMergeAll({ consolidationJob, mergingAll, similarGroups })) return;
    if (!dashboard.memoryCwd) { showNotice('error', '正在连接本地项目...'); return; }
    setMergingAll(true);
    try {
      const launchPreferences = await memoryLaunchPreferences(resolveLaunchPreferences, dashboard.memoryCwd);
      const started = await startConsolidateMemorySimilarities(memoryConsolidationStartPayload(dashboard.memoryCwd, launchPreferences));
      const jobID = textValue(started?.jobId);
      assertMemoryConsolidationStarted(started, jobID);
      await applyStartedMemoryConsolidation({ applyConsolidationResult, cwd: dashboard.memoryCwd, jobID, setConsolidationJob, showNotice, started });
    } catch (err) {
      showNotice('error', `智能整合失败：${errorMessage(err)}`);
    } finally {
      setMergingAll(false);
    }
  }, [applyConsolidationResult, consolidationJob, dashboard, mergingAll, resolveLaunchPreferences, setConsolidationJob, setMergingAll, showNotice, similarGroups]);
}

function memoryConsolidationStartPayload(cwd, launchPreferences) {
  return {
    cwd,
    provider: textValue(launchPreferences?.modelProvider || launchPreferences?.provider),
    model: textValue(launchPreferences?.model),
    codexModelProvider: textValue(launchPreferences?.codexModelProvider || launchPreferences?.config?.codexModelProvider),
  };
}

function useMemoryConsolidationPolling({ applyConsolidationResult, setConsolidationJob, showNotice, similarity }) {
  useEffect(() => {
    if (!similarity.consolidationJob?.jobId || !similarity.consolidationJob?.cwd) return undefined;
    let cancelled = false;
    (async () => {
      try {
        const result = await waitForMemoryConsolidationJob(similarity.consolidationJob.cwd, similarity.consolidationJob.jobId);
        if (!cancelled) await applyConsolidationResult(similarity.consolidationJob.cwd, result);
      } catch (err) {
        if (!cancelled) showMemoryConsolidationError(err, showNotice);
      } finally {
        if (!cancelled) setConsolidationJob(null);
      }
    })();
    return () => { cancelled = true; };
  }, [applyConsolidationResult, setConsolidationJob, showNotice, similarity.consolidationJob]);
}

function showMemoryConsolidationError(err, showNotice) {
  const message = errorMessage(err);
  const level = message.includes('仍在进行') ? 'warning' : 'error';
  showNotice(level, level === 'warning' ? message : `智能整合失败：${message}`);
}

function useMemoryApplyConsolidationResult(queryClient, showNotice) {
  return useCallback(async (cwd, result) => {
    const summary = memoryConsolidationResultMessage(result);
    if (!Number(result?.failed) && !Number(result?.skipped)) {
      queryClient.setQueryData(dashboardQueryKey(cwd, 'memory'), clearMemorySimilarGroups);
    }
    showNotice(summary.level, summary.message);
    await queryClient.invalidateQueries({ queryKey: dashboardQueryKey(cwd, 'memory') });
  }, [queryClient, showNotice]);
}

function useMemoryPageModel({ projectPath, onSimilarCountChange, resolveLaunchPreferences }) {
  const dashboard = useMemoryDashboard(projectPath);
  const { notice, showNotice } = useMemoryNotice(dashboard.memoryCwd);
  const [searchText, setSearchText] = useState('');
  const [activeCategory, setActiveCategory] = useState('preference');
  const [similarExpanded, setSimilarExpanded] = useState(false);
  const derived = useMemoryDerivedState(dashboard.snapshot, activeCategory, searchText, onSimilarCountChange);
  const autoDream = useMemoryAutoDream({ dashboard, showNotice });
  const editor = useMemoryEditor({ dashboard, showNotice });
  const deletion = useMemoryDelete({ dashboard, showNotice });
  const { queryClient } = dashboard;
  const applyConsolidationResult = useMemoryApplyConsolidationResult(queryClient, showNotice);
  const similarity = useMemorySimilarityActions({
    applyConsolidationResult,
    dashboard,
    resolveLaunchPreferences,
    showNotice,
    similarGroups: derived.similarGroups,
  });
  useMemoryConsolidationPolling({
    applyConsolidationResult,
    setConsolidationJob: similarity.setConsolidationJob,
    showNotice,
    similarity,
  });
  return { activeCategory, autoDream, dashboard, deletion, derived, editor, notice, searchText, setActiveCategory, setSearchText, setSimilarExpanded, similarExpanded, similarity };
}

function MemoryPage({ copy = APP_COPY.zh.memory, projectPath, onSimilarCountChange, resolveLaunchPreferences }) {
  const model = useMemoryPageModel({ projectPath, onSimilarCountChange, resolveLaunchPreferences });
  return <MemoryPageView copy={copy} model={model} />;
}

function MemoryPageView({ copy, model }) {
  return (
    <section className="memory-page">
      <MemoryPageHeader
        copy={copy}
        disabled={model.dashboard.isProjectPending}
        editor={model.editor}
        searchText={model.searchText}
        setSearchText={model.setSearchText}
      />
      <MemoryStats autoDream={model.autoDream} categoryCounts={model.derived.categoryCounts} copy={copy} disabled={model.dashboard.isProjectPending} health={model.derived.health} />
      <MemorySimilaritySection
        copy={copy}
        expanded={model.similarExpanded}
        groups={model.derived.similarGroups}
        setExpanded={model.setSimilarExpanded}
        similarity={model.similarity}
      />
      <MemoryStatusMessages copy={copy} dashboard={model.dashboard} notice={model.notice} />
      <MemoryTabs activeCategory={model.activeCategory} categoryCounts={model.derived.categoryCounts} copy={copy} setActiveCategory={model.setActiveCategory} />
      <MemoryCardsSection
        copy={copy}
        dashboard={model.dashboard}
        deletion={model.deletion}
        editor={model.editor}
        searchText={model.searchText}
        visibleEntries={model.derived.visibleEntries}
      />
      <MemoryModals deletion={model.deletion} editor={model.editor} similarity={model.similarity} />
    </section>
  );
}

function MemoryPageHeader({ copy, disabled, editor, searchText, setSearchText }) {
  return (
    <PageHeader
      icon={MemoryStick}
      title={copy.title}
      actions={(
        <>
          <label>
            <Search size={17} />
            <input
              aria-label={copy.search}
              placeholder={copy.searchPlaceholder}
              value={searchText}
              onChange={(event) => setSearchText(event.target.value)}
            />
          </label>
          <div className="memory-create">
            <button type="button" className="light" aria-label={`+ ${copy.new} ▾`} onClick={() => editor.setCreateMenuOpen((open) => !open)} disabled={disabled}>
              <Plus size={15} /> {copy.new} ▾
            </button>
            {editor.createMenuOpen ? <MemoryCreateMenu copy={copy} onCreate={editor.openCreate} /> : null}
          </div>
        </>
      )}
    />
  );
}

function MemoryCreateMenu({ copy, onCreate }) {
  return (
    <div className="memory-create-menu">
      <button type="button" onClick={() => onCreate('feedback')}>{copy.newPreference}</button>
      <button type="button" onClick={() => onCreate('project')}>{copy.newProject}</button>
    </div>
  );
}

function MemoryStats({ autoDream, categoryCounts, copy, disabled, health }) {
  return (
    <div className="memory-stats">
      <Panel title={copy.overview}>
        <strong className="big">{categoryCounts.all}</strong>
        <p><span className="orange-dot" />{categoryCounts.preference} {copy.preference} <span />{categoryCounts.project} {copy.project}</p>
      </Panel>
      {health ? <MemoryHealthPanel copy={copy} health={health} /> : null}
      <MemoryAutoDreamPanel autoDream={autoDream} copy={copy} disabled={disabled} />
    </div>
  );
}

function MemoryHealthPanel({ copy, health }) {
  const prefPercent = memoryHealthPercent(health.preferenceCount, health.maxPerCategory);
  const projPercent = memoryHealthPercent(health.projectCount, health.maxPerCategory);
  return (
    <Panel title={copy.health}>
      <p>{copy.preference} <meter value={health.preferenceCount} max={health.maxPerCategory} /> {health.preferenceCount} / {health.maxPerCategory}</p>
      <div className={'memory-health-track ' + memoryHealthClass(prefPercent)}><span style={{ width: String(prefPercent) + '%' }} /></div>
      <p>{copy.project} <meter value={health.projectCount} max={health.maxPerCategory} /> {health.projectCount} / {health.maxPerCategory}</p>
      <div className={'memory-health-track ' + memoryHealthClass(projPercent)}><span style={{ width: String(projPercent) + '%' }} /></div>
      <p><span className="green-dot" /> {copy.healthy}</p>
    </Panel>
  );
}

function MemoryAutoDreamPanel({ autoDream, copy, disabled }) {
  return (
    <Panel title={copy.autoDream}>
      <p><span className={autoDream.enabled ? 'green-dot' : 'orange-dot'} /> {autoDream.enabled ? copy.autoDreamOn : copy.autoDreamOff}</p>
      <small>{copy.autoDreamDescription}</small>
      <button type="button" onClick={() => { void autoDream.toggleAutoDream(); }} disabled={autoDream.toggling || disabled}>
        {autoDream.enabled ? copy.disable : copy.enable}
      </button>
      {autoDream.pendingRestart ? <small className="memory-pending">{copy.pendingRestart}</small> : null}
    </Panel>
  );
}

function MemorySimilaritySection({ copy, expanded, groups, setExpanded, similarity }) {
  if (!groups.length) return null;
  const busy = memorySimilarityBusy(similarity);
  return (
    <>
      <div className="similar-alert">
        <AlertTriangle size={20} />
        <span>{groups.length} {copy.similarGroupsSuffix}</span>
        <button type="button" onClick={() => { void similarity.mergeAllGroups(); }} disabled={busy}>{memoryMergeAllLabel(similarity, copy)}</button>
        <button type="button" onClick={() => setExpanded((current) => !current)}>{expanded ? copy.collapse : copy.expand}</button>
      </div>
      {expanded ? <MemorySimilarityList busy={busy} copy={copy} groups={groups} similarity={similarity} /> : null}
    </>
  );
}

function MemorySimilarityList({ busy, copy, groups, similarity }) {
  return (
    <div className="memory-similar-list">
      {groups.map((group) => (
        <MemorySimilarItem busy={busy} copy={copy} group={group} similarity={similarity} key={memoryPairKey(group)} />
      ))}
    </div>
  );
}

function MemorySimilarItem({ busy, copy, group, similarity }) {
  const key = memoryPairKey(group);
  return (
    <div className="memory-similar-item">
      <span>「{group.nameA || group.pathA}」与「{group.nameB || group.pathB}」</span>
      <strong>{formatMemoryScore(group.score)}</strong>
      <button type="button" onClick={() => similarity.setMergeTarget(group)} disabled={busy}>{copy.merge}</button>
      <button type="button" className="ghost" onClick={() => { void similarity.ignoreGroup(group); }} disabled={busy}>
        {similarity.ignoringKey === key ? '...' : copy.ignore}
      </button>
    </div>
  );
}

function memorySimilarityBusy(similarity) {
  return similarity.mergingAll || Boolean(similarity.consolidationJob) || Boolean(similarity.mergingKey) || Boolean(similarity.ignoringKey);
}

function memoryMergeAllLabel(similarity, copy) {
  if (similarity.mergingAll) return copy.mergeStarting;
  if (similarity.consolidationJob) return copy.mergeRunning;
  return copy.mergeAll;
}

function MemoryStatusMessages({ copy, dashboard, notice }) {
  return (
    <>
      {notice.message ? <div className={'memory-notice is-' + notice.level}>{notice.message}</div> : null}
      {dashboard.isProjectPending ? <div className="memory-notice is-info">{copy.connecting}</div> : null}
      {!dashboard.isProjectPending && dashboard.loading ? <div className="memory-notice is-info">{copy.loading}</div> : null}
      {dashboard.syncError ? <MemorySyncError copy={copy} message={dashboard.syncError} onRefresh={dashboard.refreshMemory} /> : null}
      {dashboard.error ? <MemorySyncError copy={copy} message={dashboard.error} onRefresh={dashboard.refreshMemory} /> : null}
    </>
  );
}

function MemorySyncError({ copy, message, onRefresh }) {
  return (
    <div className="memory-notice is-error" role="alert">
      <span>{message}</span>
      <button type="button" onClick={() => { void onRefresh(); }}>{copy.retrySync}</button>
    </div>
  );
}

function MemoryTabs({ activeCategory, categoryCounts, copy, setActiveCategory }) {
  return (
    <div className="memory-tabs" role="tablist" aria-label={copy.tabsAria}>
      {MEMORY_CATEGORY_KEYS.map((key) => (
        <button
          key={key}
          type="button"
          role="tab"
          aria-selected={activeCategory === key}
          className={activeCategory === key ? 'active' : ''}
          onClick={() => setActiveCategory(key)}
        >
          {copy[key]} {categoryCounts[key] || 0}
        </button>
      ))}
    </div>
  );
}

function MemoryCardsSection({ copy, dashboard, deletion, editor, searchText, visibleEntries }) {
  if (!dashboard.error && !dashboard.isProjectPending && !dashboard.loading && visibleEntries.length === 0) {
    return <MemoryEmptyState copy={copy} searchText={searchText} />;
  }
  if (!dashboard.error && !dashboard.isProjectPending && visibleEntries.length > 0) {
    return <MemoryCardsList copy={copy} deletion={deletion} editor={editor} visibleEntries={visibleEntries} />;
  }
  return null;
}

function MemoryEmptyState({ copy, searchText }) {
  return (
    <div className="empty-state memory-empty">
      <span><MemoryStick size={24} /></span>
      <h2>{searchText ? copy.emptySearchTitle : copy.emptyTitle}</h2>
      <p>{searchText ? copy.emptySearchText : copy.emptyText}</p>
    </div>
  );
}

function memoryEntryKey(entry) {
  return [entry.target, entry.path].join(':');
}

function MemoryCardsList({ copy, deletion, editor, visibleEntries }) {
  return (
    <div className="memory-cards">
      {visibleEntries.map((entry) => (
        <MemoryCard
          key={entry.id}
          entry={entry}
          busy={editor.busyKey === memoryEntryKey(entry)}
          copy={copy}
          deleting={deletion.deletingKey === memoryEntryKey(entry)}
          onEdit={editor.openEdit}
          onDelete={deletion.setDeleteTarget}
        />
      ))}
    </div>
  );
}

function MemoryModals({ deletion, editor, similarity }) {
  const editorState = editor.editor;
  return (
    <>
      {editorState.open ? (
        <MemoryEditorModal
          editor={editorState}
          saving={editor.saving}
          onClose={editor.closeEditor}
          onChange={editor.updateEditorForm}
          onSave={editor.saveEditor}
          onDelete={() => requestMemoryEditorDelete(editor, deletion)}
        />
      ) : null}
      {deletion.deleteTarget ? <MemoryDeleteDialog deletion={deletion} /> : null}
      {similarity.mergeTarget ? <MemoryMergeDialog similarity={similarity} /> : null}
    </>
  );
}

function requestMemoryEditorDelete(editor, deletion) {
  const form = editor.editor.form;
  deletion.setDeleteTarget({
    target: form.target,
    path: form.existingPath,
    name: form.name,
    title: form.title,
  });
  editor.setEditor((current) => ({ ...current, open: false }));
}

function MemoryDeleteDialog({ deletion }) {
  const entry = deletion.deleteTarget;
  return (
    <MemoryDeleteModal
      entry={entry}
      deleting={deletion.deletingKey === memoryEntryKey(entry)}
      onClose={() => deletion.setDeleteTarget(null)}
      onConfirm={deletion.confirmDelete}
    />
  );
}

function MemoryMergeDialog({ similarity }) {
  const group = similarity.mergeTarget;
  return (
    <MemoryMergeModal
      group={group}
      merging={similarity.mergingKey === memoryPairKey(group)}
      onClose={() => similarity.setMergeTarget(null)}
      onConfirm={similarity.confirmMerge}
    />
  );
}

function MemoryCard({ copy, entry, busy, deleting, onEdit, onDelete }) {
  return (
    <article className={`memory-card ${entry.category === 'project' ? 'type-project' : 'type-preference'}`}>
      <header>
        <h3>{memoryEntryTitle(entry)}</h3>
        <span>{entry.tag}</span>
        {entry.source === 'dream' ? <em>{copy.dream}</em> : null}
      </header>
      {entry.description ? <p>{entry.description}</p> : null}
      <code>{entry.preview || copy.noPreview}</code>
      <footer>
        <time>{sharedFileTimestamp(entry.updatedAt)}</time>
        <button type="button" onClick={() => { void onEdit(entry); }} disabled={busy}>{busy ? copy.loadingAction : copy.edit}</button>
        <button type="button" className="danger" onClick={() => onDelete(entry)} disabled={deleting}>{deleting ? copy.deleting : copy.delete}</button>
      </footer>
    </article>
  );
}

function memoryEditorTypeChangePatch(type) {
  return {
    type,
    target: memoryTargetForType(type),
    content: memoryTemplateForType(type),
  };
}

function MemoryEditorHeader({ editor, form }) {
  return (
    <header>
      <div>
        <h2>{editor.mode === 'edit' ? '编辑记忆' : '新建记忆'}</h2>
        <p>{form.type === 'project' ? '项目记忆' : '偏好记忆'}</p>
      </div>
    </header>
  );
}

function MemoryEditorFields({ form, identityLocked, onChange }) {
  return (
    <div className="memory-form-grid">
      <label>分类
        <select
          value={form.type}
          onChange={(event) => onChange(memoryEditorTypeChangePatch(event.target.value))}
          disabled={identityLocked}
        >
          {MEMORY_EDITOR_TYPES.map((type) => <option key={type.key} value={type.key}>{type.label}</option>)}
        </select>
      </label>
      <label>描述
        <input value={form.description} onChange={(event) => onChange({ description: event.target.value })} placeholder="一句话描述为什么值得长期保留" />
      </label>
      <label>卡片标题
        <input value={form.title} onChange={(event) => onChange({ title: event.target.value })} placeholder="卡片上显示的短标题" />
      </label>
    </div>
  );
}

function MemoryEditorContentField({ form, onChange }) {
  return (
    <label className="memory-content-label">内容
      <textarea rows={12} value={form.content} onChange={(event) => onChange({ content: event.target.value })} />
    </label>
  );
}

function MemoryEditorActions({ form, saving, onClose, onChange, onDelete, onSave }) {
  return (
    <div className="memory-editor-actions">
      <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
      {form.existingPath ? <button type="button" className="danger" onClick={onDelete} disabled={saving}>删除</button> : null}
      <button type="button" onClick={() => onChange({ content: memoryTemplateForType(form.type) })} disabled={saving}>套用当前类型模板</button>
      <button type="button" className="light" onClick={() => { void onSave(); }} disabled={saving || !textValue(form.description) || !textValue(form.content)}>
        {saving ? '保存中...' : '保存'}
      </button>
    </div>
  );
}

function MemoryEditorModal({ editor, saving, onClose, onChange, onSave, onDelete }) {
  const form = editor.form;
  const identityLocked = editor.mode === 'edit' && Boolean(form.existingPath);
  return (
    <FocusTrapDialog
      ariaLabel={editor.mode === 'edit' ? '编辑记忆' : '新建记忆'}
      className="modal-box memory-editor-modal"
      closeDisabled={saving}
      onClose={onClose}
    >
        <MemoryEditorHeader editor={editor} form={form} />
        <MemoryEditorFields form={form} identityLocked={identityLocked} onChange={onChange} />
        <MemoryEditorContentField form={form} onChange={onChange} />
        <MemoryEditorActions form={form} saving={saving} onClose={onClose} onChange={onChange} onDelete={onDelete} onSave={onSave} />
    </FocusTrapDialog>
  );
}

function MemoryDeleteModal({ entry, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除记忆" closeDisabled={deleting} onClose={onClose}>
        <header>
          <h2>删除记忆</h2>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button>
        </header>
        <p>删除后无法恢复。如果后续可能重用，建议先编辑备份内容。</p>
        <p className="path">{memoryEntryTitle(entry)}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="danger" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? '删除中...' : '确认删除'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

function MemoryMergeModal({ group, merging, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="整合相似记忆" closeDisabled={merging} onClose={onClose}>
        <header>
          <div>
            <h2>整合相似记忆</h2>
            <p>相似度 {formatMemoryScore(group.score)}</p>
          </div>
          <button type="button" className="ghost" onClick={onClose} disabled={merging}>关闭</button>
        </header>
        <p>合并到：{group.nameA || '保留项'}</p>
        <p>移除：{group.nameB || '重复项'}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={merging}>取消</button>
          <button type="button" className="light" onClick={() => { void onConfirm(); }} disabled={merging}>{merging ? '整合中...' : '确认整合'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

export { MemoryPage };

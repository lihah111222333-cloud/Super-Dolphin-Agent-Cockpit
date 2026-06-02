import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, Download, Eye, File, FolderOpen, MessageCircle, Search, Trash2, X } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { deleteSharedFile, listSharedFiles, readSharedFile, saveTextFile } from '../../shared/api/backendApi.js';
import { dashboardQueryErrorState, firstText, optionalSettingsCwd, queryHasSnapshot, sharedFileTimestamp, SKILLS_REQUEST_TIMEOUT_MS, textValue, useDashboardQueryFocusInvalidation, withTimeout } from '../shared/pageShared.js';
import { PageHeader, RetryableSyncError } from '../shared/pageComponents.jsx';

const SHARED_FILE_CATEGORIES = Object.freeze([
  { key: 'all', label: '全部' },
  { key: 'final', label: '最终产物' },
  { key: 'work', label: '工作文件' },
]);

const SHARED_FILE_SORTS = Object.freeze([
  { key: 'updated-desc', label: '最新更新' },
  { key: 'updated-asc', label: '最早更新' },
  { key: 'path-asc', label: '按文件名' },
]);

function dashboardGlobalQueryKey(page, ...parts) {
  return ['dashboard', 'global', page, ...parts.map((part) => textValue(part)).filter(Boolean)];
}

async function fetchSharedFilesDashboard() {
  const response = await withTimeout(
    listSharedFiles(),
    SKILLS_REQUEST_TIMEOUT_MS,
    '共享文件加载超时，请检查文件索引或后端状态。',
  );
  return normalizeSharedFilesResponse(response);
}

function splitSharedFilePath(path) {
  const value = textValue(path);
  if (!value) return { dir: '', base: '未命名文件' };
  const index = value.lastIndexOf('/');
  if (index < 0) return { dir: '', base: value };
  return {
    dir: value.slice(0, index + 1),
    base: value.slice(index + 1) || value,
  };
}

function sharedFileExportName(path) {
  const base = splitSharedFilePath(path).base;
  return base && base !== '未命名文件' ? base : 'shared-file.txt';
}

function formatBytes(size) {
  const value = Number(size);
  if (!Number.isFinite(value) || value <= 0) return '0 B';
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}

function sharedFileContent(file) {
  return (file?.content || '').toString();
}

function sharedFileSummary(file) {
  const text = sharedFileContent(file).trim();
  if (!text) return '点击“打开”加载全文。';
  const lines = text.split('\n').map((line) => line.trim()).filter(Boolean).slice(0, 2).join(' ');
  return lines.length > 180 ? `${lines.slice(0, 180)}...` : lines;
}

function sharedFilePreview(file) {
  const text = sharedFileContent(file).trim();
  if (!text) return '文件为空';
  const preview = text.split('\n').slice(0, 8).join('\n');
  return preview.length > 600 ? `${preview.slice(0, 600)}...` : preview;
}

function normalizeSharedFile(raw, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`shared file item ${index} must be an object`);
  }
  const path = textValue(raw.path);
  if (!path) throw new Error(`shared file item ${index} path is required`);
  return {
    id: `${path}:${index}`,
    path,
    content: (raw.content || '').toString(),
    updatedBy: firstText(raw.updated_by, raw.updatedBy),
    updatedAt: firstText(raw.updated_at, raw.updatedAt),
    createdAt: firstText(raw.created_at, raw.createdAt),
  };
}

function normalizeFinalOutputRefs(value) {
  if (value === undefined) return [];
  if (!Array.isArray(value)) throw new Error('shared files dashboard finalOutputRefs must be an array');
  return value.map((item, index) => {
    if (typeof item === 'string') {
      const path = textValue(item);
      if (!path) throw new Error(`final output ref ${index} path is required`);
      return { path, runKey: '', dagKey: '', sourceNodeKey: '' };
    }
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`final output ref ${index} must be an object`);
    }
    const path = firstText(item.path, item.sharedfile?.path, item.sharedFile?.path, item.shared_file?.path);
    if (!path) throw new Error(`final output ref ${index} path is required`);
    return {
      path,
      runKey: firstText(item.runKey, item.run_key),
      dagKey: firstText(item.dagKey, item.dag_key),
      sourceNodeKey: firstText(item.sourceNodeKey, item.source_node_key),
    };
  });
}

function normalizeSharedFileRetention(value) {
  if (value === undefined) {
    return { items: [], protectedCount: 0, cleanupCandidateCount: 0 };
  }
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('shared files dashboard sharedFileRetention must be an object');
  }
  if (!Array.isArray(value.items)) {
    throw new Error('shared files dashboard sharedFileRetention.items must be an array');
  }
  return {
    items: value.items.map((item, index) => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) {
        throw new Error(`shared file retention item ${index} must be an object`);
      }
      const path = textValue(item.path);
      if (!path) throw new Error(`shared file retention item ${index} path is required`);
      return {
        path,
        protected: Boolean(item.protected),
        cleanupCandidate: Boolean(item.cleanupCandidate),
        reason: textValue(item.reason),
        finalOutput: item.finalOutput || item.final_output || null,
      };
    }),
    protectedCount: Number(value.protectedCount) || 0,
    cleanupCandidateCount: Number(value.cleanupCandidateCount) || 0,
  };
}

function normalizeSharedFilesResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('shared files dashboard response must be an object');
  }
  const rawFiles = Array.isArray(response.files) ? response.files : response.memory;
  if (!Array.isArray(rawFiles)) {
    throw new Error('shared files dashboard response files must be an array');
  }
  const rawRefs = response.finalOutputRefs;
  const rawRetention = response.sharedFileRetention;
  return {
    files: rawFiles.map((item, index) => normalizeSharedFile(item, index)),
    finalOutputRefs: normalizeFinalOutputRefs(rawRefs),
    retention: normalizeSharedFileRetention(rawRetention),
  };
}

function sharedFileMatches(file, query) {
  const needle = textValue(query).toLowerCase();
  if (!needle) return true;
  return [
    file.path,
    file.updatedBy,
    file.content,
  ].some((value) => value.toLowerCase().includes(needle));
}

function sortSharedFiles(files, sortMode) {
  const list = [...files];
  if (sortMode === 'path-asc') {
    return list.sort((left, right) => left.path.localeCompare(right.path));
  }
  const updatedTime = (file) => {
    const parsed = new Date(file.updatedAt || 0).getTime();
    return Number.isFinite(parsed) ? parsed : 0;
  };
  return list.sort((left, right) => (
    sortMode === 'updated-asc'
      ? updatedTime(left) - updatedTime(right)
      : updatedTime(right) - updatedTime(left)
  ));
}

function sharedFileCategoryOf(file, finalOutputByPath) {
  return finalOutputByPath.has(file.path) ? 'final' : 'work';
}

function useSharedFilesDashboard(store) {
  const queryClient = useQueryClient();
  const queryKey = useMemo(() => dashboardGlobalQueryKey('shared-files'), []);
  useDashboardQueryFocusInvalidation(queryKey);
  const query = useQuery({ queryKey, queryFn: fetchSharedFilesDashboard });
  const hasSnapshot = queryHasSnapshot(query);
  const data = query.data || {
    files: [],
    finalOutputRefs: [],
    retention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
  };
  const loading = query.isPending && !hasSnapshot;
  const { cachedSyncError: syncError, blockingError } = dashboardQueryErrorState(query, hasSnapshot);
  const refreshFiles = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey });
  }, [queryClient, queryKey]);
  useEffect(() => {
    const revision = Number(store?.sharedFilesRevision || 0);
    if (revision > 0) void refreshFiles();
  }, [refreshFiles, store?.sharedFilesRevision]);
  return {
    error: blockingError ? `加载共享文件失败：${blockingError}` : '',
    files: data.files,
    finalOutputRefs: data.finalOutputRefs,
    loading,
    refreshFiles,
    retention: data.retention,
    syncError,
  };
}

function useSharedFilesFilters(files, finalOutputRefs, retention) {
  const [searchText, setSearchText] = useState('');
  const [sortMode, setSortMode] = useState('updated-desc');
  const [category, setCategory] = useState('all');
  const finalOutputByPath = useMemo(() => (
    new Map(finalOutputRefs.filter((ref) => files.some((file) => file.path === ref.path)).map((ref) => [ref.path, ref]))
  ), [files, finalOutputRefs]);
  const retentionByPath = useMemo(() => new Map(retention.items.map((item) => [item.path, item])), [retention.items]);
  const finalCount = files.filter((file) => finalOutputByPath.has(file.path)).length;
  const workCount = Math.max(0, files.length - finalCount);
  const visibleFiles = useMemo(() => {
    const filtered = sortSharedFiles(files.filter((file) => sharedFileMatches(file, searchText)), sortMode);
    if (category === 'final') return filtered.filter((file) => sharedFileCategoryOf(file, finalOutputByPath) === 'final');
    if (category === 'work') return filtered.filter((file) => sharedFileCategoryOf(file, finalOutputByPath) === 'work');
    return filtered;
  }, [category, files, finalOutputByPath, searchText, sortMode]);
  const protectionFor = useCallback((file) => {
    const retentionItem = retentionByPath.get(file.path);
    if (retentionItem?.protected) return retentionItem;
    const ref = finalOutputByPath.get(file.path);
    if (ref) return { path: file.path, protected: true, reason: 'final_output', finalOutput: ref };
    return null;
  }, [finalOutputByPath, retentionByPath]);
  return {
    activeSortLabel: SHARED_FILE_SORTS.find((item) => item.key === sortMode)?.label || '最新更新',
    category,
    categoryCounts: { all: files.length, final: finalCount, work: workCount },
    finalCount,
    finalOutputByPath,
    protectionFor,
    searchText,
    setCategory,
    setSearchText,
    setSortMode,
    sortMode,
    visibleFiles,
    workCount,
  };
}

function useSharedFileActions({ exportDefaultPath, refreshFiles, store, protectionFor }) {
  const [notice, setNotice] = useState(null);
  const [selectedFile, setSelectedFile] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [busyPath, setBusyPath] = useState('');
  const [exportingPath, setExportingPath] = useState('');
  const [deletingPath, setDeletingPath] = useState('');
  const [copied, setCopied] = useState(false);
  useEffect(() => { setNotice(null); }, []);
  const loadFileDetail = useSharedFileDetailLoader(setBusyPath);
  const openFile = useCallback(async (file) => {
    setNotice(null);
    setCopied(false);
    try {
      setSelectedFile(await loadFileDetail(file));
    } catch (err) {
      setNotice({ level: 'error', message: `读取文件失败：${err.message || String(err)}` });
    }
  }, [loadFileDetail]);
  const exportFile = useSharedFileExport({ exportDefaultPath, exportingPath, loadFileDetail, setExportingPath, setNotice });
  const askDelete = useCallback((file) => {
    if (protectionFor(file)) setNotice({ level: 'error', message: `最终产物不能直接删除：${file.path}` });
    else setDeleteTarget(file);
  }, [protectionFor]);
  const confirmDelete = useSharedFileDelete({ deleteTarget, deletingPath, refreshFiles, selectedFile, setDeleteTarget, setDeletingPath, setNotice, setSelectedFile });
  const continueWithFile = useCallback((file) => {
    if (typeof store?.continueWithSharedFile === 'function') store.continueWithSharedFile(file.path);
  }, [store]);
  const copySelectedContent = useCallback(async () => {
    const text = selectedFile?.content || '';
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
    } catch (err) {
      setNotice({ level: 'error', message: `复制失败：${err.message || String(err)}` });
    }
  }, [selectedFile]);
  return {
    askDelete,
    busyPath,
    copied,
    continueWithFile,
    confirmDelete,
    copySelectedContent,
    deletingPath,
    deleteTarget,
    exportFile,
    exportingPath,
    notice,
    openFile,
    selectedFile,
    setDeleteTarget,
    setSelectedFile,
  };
}

function useSharedFileDetailLoader(setBusyPath) {
  return useCallback(async (file) => {
    const path = textValue(file?.path);
    if (!path) throw new Error('shared file path is required');
    setBusyPath(path);
    try {
      const detail = await readSharedFile({ path });
      return normalizeSharedFile({
        path: detail?.path || path,
        content: detail?.content || file?.content || '',
        updatedBy: detail?.updatedBy || file?.updatedBy || '',
        updatedAt: detail?.updatedAt || file?.updatedAt || '',
      }, 0);
    } finally {
      setBusyPath('');
    }
  }, [setBusyPath]);
}

function useSharedFileExport({ exportDefaultPath, exportingPath, loadFileDetail, setExportingPath, setNotice }) {
  return useCallback(async (file) => {
    const path = textValue(file?.path);
    if (!path || exportingPath) return;
    setNotice(null);
    setExportingPath(path);
    try {
      const detail = await loadFileDetail(file);
      const savedPath = await saveTextFile({
        defaultPath: exportDefaultPath,
        defaultFilename: sharedFileExportName(detail.path),
        content: detail.content,
      });
      setNotice({ level: 'info', message: savedPath ? `已保存到：${savedPath}` : '已取消保存。' });
    } catch (err) {
      setNotice({ level: 'error', message: `导出失败：${err.message || String(err)}` });
    } finally {
      setExportingPath('');
    }
  }, [exportDefaultPath, exportingPath, loadFileDetail, setExportingPath, setNotice]);
}

function useSharedFileDelete({ deleteTarget, deletingPath, refreshFiles, selectedFile, setDeleteTarget, setDeletingPath, setNotice, setSelectedFile }) {
  return useCallback(async () => {
    const target = deleteTarget;
    if (!target?.path || deletingPath) return;
    setNotice(null);
    setDeletingPath(target.path);
    try {
      await deleteSharedFile({ path: target.path });
      if (selectedFile?.path === target.path) setSelectedFile(null);
      setDeleteTarget(null);
      setNotice({ level: 'info', message: `已删除文件：${target.path}` });
      await refreshFiles();
    } catch (err) {
      setNotice({ level: 'error', message: `删除失败：${err.message || String(err)}` });
    } finally {
      setDeletingPath('');
    }
  }, [deleteTarget, deletingPath, refreshFiles, selectedFile, setDeleteTarget, setDeletingPath, setNotice, setSelectedFile]);
}

function FilesPage({ projectPath, store }) {
  const exportDefaultPath = optionalSettingsCwd(projectPath);
  const dashboard = useSharedFilesDashboard(store);
  const filters = useSharedFilesFilters(dashboard.files, dashboard.finalOutputRefs, dashboard.retention);
  const actions = useSharedFileActions({
    exportDefaultPath,
    refreshFiles: dashboard.refreshFiles,
    store,
    protectionFor: filters.protectionFor,
  });
  return <SharedFilesPageView actions={actions} dashboard={dashboard} filters={filters} />;
}

function SharedFilesPageView({ actions, dashboard, filters }) {
  return (
    <section className="console-page shared-files-page">
      <SharedFilesHeader dashboard={dashboard} filters={filters} />
      <SharedFilesIntro />
      <SharedFilesTabs category={filters.category} categoryCounts={filters.categoryCounts} onCategory={filters.setCategory} />
      <SharedFilesStatus actions={actions} dashboard={dashboard} />
      <SharedFilesContent actions={actions} dashboard={dashboard} filters={filters} />
      <SharedFilesModals actions={actions} />
    </section>
  );
}

function SharedFilesHeader({ dashboard, filters }) {
  return (
    <PageHeader
      icon={FolderOpen}
      title="文件产物"
      subtitle={`${filters.activeSortLabel} · 全部${dashboard.files.length} 最终产物${filters.finalCount} 工作文件${filters.workCount}`}
      actions={(
        <>
          <label className="shared-files-search">
            <Search size={15} />
            <input aria-label="搜索共享文件" placeholder="搜索文件名 / 内容" value={filters.searchText} onChange={(event) => filters.setSearchText(event.target.value)} />
          </label>
          <select aria-label="共享文件排序" value={filters.sortMode} onChange={(event) => filters.setSortMode(event.target.value)}>
            {SHARED_FILE_SORTS.map((item) => <option key={item.key} value={item.key}>{item.label}</option>)}
          </select>
        </>
      )}
    />
  );
}

function SharedFilesIntro() {
  return (
    <div className="file-intro">
      <FolderOpen size={29} />
      <h2>共享文件 · Agent 协作中转站</h2>
      <p>Agent 在运行过程中产生的所有数据产物都保存在这里。</p>
    </div>
  );
}

function SharedFilesTabs({ category, categoryCounts, onCategory }) {
  return (
    <div className="shared-files-tabs" role="tablist" aria-label="文件产物分类">
      {SHARED_FILE_CATEGORIES.map((item) => (
        <button key={item.key} type="button" role="tab" aria-selected={category === item.key} className={category === item.key ? 'active' : ''} onClick={() => onCategory(item.key)}>
          {item.label} {categoryCounts[item.key] || 0}
        </button>
      ))}
    </div>
  );
}

function SharedFilesStatus({ actions, dashboard }) {
  return (
    <>
      {actions.notice ? <p className={actions.notice.level === 'error' ? 'danger-text' : 'settings-status'}>{actions.notice.message}</p> : null}
      {dashboard.syncError ? (
        <div className="danger-text shared-files-sync-alert" role="alert">
          <span>{dashboard.syncError}</span>
          <button type="button" className="ghost" onClick={() => { void dashboard.refreshFiles(); }}>重试同步</button>
        </div>
      ) : null}
      <RetryableSyncError className="danger-text shared-files-sync-alert" message={dashboard.error} onRetry={dashboard.refreshFiles} />
    </>
  );
}

function SharedFilesContent({ actions, dashboard, filters }) {
  if (!dashboard.error && dashboard.loading && dashboard.files.length === 0) return <p className="console-message">正在加载共享文件...</p>;
  if (!dashboard.error && !dashboard.loading && dashboard.files.length === 0) return <SharedFilesEmptyState kind="none" />;
  if (!dashboard.error && dashboard.files.length > 0 && filters.visibleFiles.length === 0) return <SharedFilesEmptyState kind="search" />;
  if (dashboard.error || filters.visibleFiles.length === 0) return null;
  return <SharedFilesList actions={actions} filters={filters} />;
}

function SharedFilesEmptyState({ kind }) {
  const search = kind === 'search';
  return (
    <div className="empty-state">
      <span>{search ? <Search size={24} /> : <File size={24} />}</span>
      <h2>{search ? '没有匹配的文件' : '还没有文件产物'}</h2>
      <p>{search ? '清空搜索或切换分类后再试。' : 'Agent 生成报告、草稿或数据文件后，会显示在这里。'}</p>
    </div>
  );
}

function SharedFilesList({ actions, filters }) {
  return (
    <div className="file-list" data-testid="shared-files-list">
      {filters.visibleFiles.map((file) => (
        <SharedFileRow
          key={file.path}
          file={file}
          finalOutputRef={filters.finalOutputByPath.get(file.path)}
          protectedFile={Boolean(filters.protectionFor(file))}
          busy={actions.busyPath === file.path}
          exporting={actions.exportingPath === file.path}
          deleting={actions.deletingPath === file.path}
          onOpen={actions.openFile}
          onExport={actions.exportFile}
          onDelete={actions.askDelete}
          onContinue={actions.continueWithFile}
        />
      ))}
    </div>
  );
}

function SharedFilesModals({ actions }) {
  return (
    <>
      {actions.selectedFile ? (
        <SharedFileViewer
          file={actions.selectedFile}
          copied={actions.copied}
          exporting={actions.exportingPath === actions.selectedFile.path}
          onClose={() => actions.setSelectedFile(null)}
          onCopy={actions.copySelectedContent}
          onExport={() => { void actions.exportFile(actions.selectedFile); }}
        />
      ) : null}
      {actions.deleteTarget ? (
        <ConfirmSharedFileDeleteModal
          file={actions.deleteTarget}
          deleting={actions.deletingPath === actions.deleteTarget.path}
          onClose={() => actions.setDeleteTarget(null)}
          onConfirm={actions.confirmDelete}
        />
      ) : null}
    </>
  );
}

function SharedFileRow({
  file,
  finalOutputRef,
  protectedFile,
  busy,
  exporting,
  deleting,
  onOpen,
  onExport,
  onDelete,
  onContinue,
}) {
  const path = splitSharedFilePath(file.path);
  const role = finalOutputRef ? '最终产物' : '工作文件';
  let deleteLabel = '删除';
  if (protectedFile) {
    deleteLabel = '不可删除';
  } else if (deleting) {
    deleteLabel = '删除中...';
  }
  return (
    <article className={`file-row${finalOutputRef ? ' is-final-output' : ''}`}>
      <header>
        <h3>{path.base}</h3>
        <span>{role}</span>
      </header>
      <p>{role} {sharedFileTimestamp(file.updatedAt)} {formatBytes(sharedFileContent(file).length)}</p>
      <code>{file.path}</code>
      {finalOutputRef ? (
        <small>Run {finalOutputRef.runKey || '-'} · DAG {finalOutputRef.dagKey || '-'} · Node {finalOutputRef.sourceNodeKey || '-'}</small>
      ) : null}
      <pre>{sharedFileSummary(file)}</pre>
      <footer>
        <button type="button" onClick={() => { void onOpen(file); }} disabled={busy}>
          <Eye size={14} /> {busy ? '加载中...' : '打开'}
        </button>
        <button type="button" onClick={() => { void onExport(file); }} disabled={busy || exporting}>
          <Download size={14} /> {exporting ? '导出中...' : '导出'}
        </button>
        <button
          type="button"
          className={protectedFile ? 'ghost' : 'text-danger'}
          onClick={() => onDelete(file)}
          disabled={protectedFile || deleting}
          title={protectedFile ? '最终产物由任务结果引用，不能直接删除。' : ''}
        >
          <Trash2 size={14} /> {deleteLabel}
        </button>
        <button type="button" className="ghost" onClick={() => onContinue(file)}>
          <MessageCircle size={14} /> 用此文件继续对话
        </button>
      </footer>
    </article>
  );
}

function SharedFileViewer({ file, copied, exporting, onClose, onCopy, onExport }) {
  return (
    <FocusTrapDialog ariaLabel="文件预览" className="modal-box shared-file-viewer-modal" onClose={onClose}>
        <header>
          <div>
            <h2>文件预览</h2>
            <p className="path">{file.path}</p>
          </div>
          <div className="modal-actions">
            <button type="button" onClick={onExport} disabled={exporting}><Download size={14} /> {exporting ? '导出中...' : '导出'}</button>
            <button type="button" onClick={() => { void onCopy(); }} disabled={!file.content}><Copy size={14} /> {copied ? '已复制' : '复制内容'}</button>
            <button type="button" className="ghost" onClick={onClose}><X size={14} /> 关闭</button>
          </div>
        </header>
        <dl className="shared-file-viewer-meta">
          <dt>来源</dt><dd>{file.updatedBy || '-'}</dd>
          <dt>更新时间</dt><dd>{sharedFileTimestamp(file.updatedAt)}</dd>
        </dl>
        <pre className="shared-file-content-preview">{sharedFilePreview(file)}</pre>
    </FocusTrapDialog>
  );
}

function ConfirmSharedFileDeleteModal({ file, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel="删除文件" closeDisabled={deleting} onClose={onClose}>
        <header>
          <h2>删除文件</h2>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>关闭</button>
        </header>
        <p>文件删除后无法恢复。删除前请确认这份内容不再需要。</p>
        <p className="path">{file.path}</p>
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={deleting}>取消</button>
          <button type="button" className="text-danger" onClick={() => { void onConfirm(); }} disabled={deleting}>
            {deleting ? '删除中...' : '确认删除'}
          </button>
        </footer>
    </FocusTrapDialog>
  );
}

export { FilesPage };

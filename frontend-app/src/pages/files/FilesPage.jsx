import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Copy, Download, Eye, File, FolderOpen, MessageCircle, Search, Trash2, X } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { deleteSharedFile, listSharedFilesDashboard, openSharedFile, readSharedFile, saveTextFile } from '../../services/modules/fileService.js';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { dashboardQueryErrorState, optionalSettingsCwd, queryHasSnapshot, sharedFileTimestamp, textValue, useDashboardQueryFocusInvalidation } from '../shared/pageShared.js';
import { PageHeader, RetryableSyncError } from '../shared/pageComponents.jsx';
import './FilesPage.css';

const SHARED_FILE_CATEGORY_KEYS = Object.freeze(['all', 'final', 'work']);
const SHARED_FILE_SORT_KEYS = Object.freeze(['updated-desc', 'updated-asc', 'path-asc']);

const SHARED_FILE_FORMAT_LABELS = Object.freeze({
  csv: 'CSV',
  css: 'CSS',
  go: 'Go',
  htm: 'HTML',
  html: 'HTML',
  java: 'Java',
  js: 'JavaScript',
  json: 'JSON',
  jsonl: 'JSONL',
  jsx: 'React JSX',
  log: '日志',
  md: 'Markdown',
  markdown: 'Markdown',
  py: 'Python',
  rs: 'Rust',
  sh: 'Shell',
  sql: 'SQL',
  toml: 'TOML',
  ts: 'TypeScript',
  tsx: 'React TSX',
  txt: '文本',
  xml: 'XML',
  yaml: 'YAML',
  yml: 'YAML',
});

function dashboardGlobalQueryKey(page, ...parts) {
  const normalizedParts = [];
  for (const part of parts) {
    const value = textValue(part);
    if (value) normalizedParts.push(value);
  }
  return ['dashboard', 'global', page, ...normalizedParts];
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

function sharedFileExtension(path) {
  const base = splitSharedFilePath(path).base.toLowerCase();
  const dot = base.lastIndexOf('.');
  if (dot <= 0 || dot === base.length - 1) return '';
  return base.slice(dot + 1);
}

function isBinaryMediaPath(path) {
  return /\.(mp4|mov|webm|ogg)$/i.test(textValue(path));
}

function sharedFileFormatLabel(file, language = '') {
  const normalizedLanguage = textValue(language).toLowerCase();
  if (normalizedLanguage) return SHARED_FILE_FORMAT_LABELS[normalizedLanguage] || normalizedLanguage.toUpperCase();
  const extension = sharedFileExtension(file?.path);
  return SHARED_FILE_FORMAT_LABELS[extension] || (extension ? extension.toUpperCase() : '文本');
}

function markdownFenceFromWholeText(text) {
  const normalized = text.replace(/^\uFEFF/, '').trim();
  if (!normalized) return null;
  const lines = normalized.split('\n');
  const opening = lines[0]?.trim().match(/^([`~]{3,})\s*([A-Za-z0-9_-]+)?\s*$/);
  if (!opening || lines.length < 2) return null;

  const marker = opening[1][0];
  const minLength = opening[1].length;
  const closing = lines[lines.length - 1]?.trim().match(/^([`~]{3,})\s*$/);
  if (!closing || closing[1][0] !== marker || closing[1].length < minLength) return null;

  return {
    body: lines.slice(1, -1).join('\n').trim(),
    language: textValue(opening[2]).toLowerCase(),
  };
}

function jsonItemLabel(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return '';
  const key = ['title', 'name', 'path', 'id', 'key'].find((candidate) => textValue(value[candidate]));
  if (!key) return '';
  const raw = textValue(value[key]);
  const text = raw.length > 56 ? `${raw.slice(0, 56)}...` : raw;
  return `首项 ${key}: ${text}`;
}

function jsonValueSummary(value) {
  if (Array.isArray(value)) {
    const itemLabel = jsonItemLabel(value[0]);
    return `JSON 数组 · ${value.length} 项${itemLabel ? ` · ${itemLabel}` : ''}`;
  }
  if (!value || typeof value !== 'object') return `JSON 值 · ${String(value)}`;

  const entries = Object.entries(value).slice(0, 4).map(([key, item]) => {
    if (Array.isArray(item)) {
      const itemLabel = jsonItemLabel(item[0]);
      return `${key}: ${item.length} 项${itemLabel ? ` (${itemLabel})` : ''}`;
    }
    if (item && typeof item === 'object') return `${key}: 对象`;
    const scalar = textValue(item);
    if (!scalar) return `${key}: ${item === null ? 'null' : String(item)}`;
    return `${key}: ${scalar.length > 44 ? `${scalar.slice(0, 44)}...` : scalar}`;
  });
  return entries.length > 0 ? `JSON 对象 · ${entries.join(' · ')}` : 'JSON 对象';
}

function jsonLikeFirstLabel(value) {
  const match = value.match(/"(title|name|path|id|key)"\s*:\s*"([^"]+)/);
  if (!match) return '';
  const text = match[2].length > 56 ? `${match[2].slice(0, 56)}...` : match[2];
  return `首项 ${match[1]}: ${text}`;
}

function jsonLikeSummary(value) {
  const arrayMatch = value.match(/"([A-Za-z0-9_-]+)"\s*:\s*\[/);
  if (arrayMatch) {
    const titleCount = (value.match(/"title"\s*:/g) || []).length;
    const objectCount = Math.max(0, (value.match(/\{\s*"[^"]+"\s*:/g) || []).length - 1);
    const count = titleCount || objectCount;
    const label = jsonLikeFirstLabel(value);
    return `类 JSON · ${arrayMatch[1]}${count ? `: ${count} 项` : ''}${label ? ` · ${label}` : ''}`;
  }
  const keys = [...value.matchAll(/"([A-Za-z0-9_-]+)"\s*:/g)].slice(0, 4).map((match) => match[1]);
  return keys.length > 0 ? `类 JSON · ${keys.join(' · ')}` : '类 JSON 文本';
}

function formatJsonLikeText(value) {
  return value
    .replace(/^\{\s*"([A-Za-z0-9_-]+)"\s*:\s*\[/, '{\n  "$1": [')
    .replace(/\[\s*\{/g, '[\n  {')
    .replace(/\},\s*\{/g, '\n  },\n  {')
    .replace(/\{\s*"([A-Za-z0-9_-]+)"\s*:/g, '{\n    "$1":')
    .replace(/",\s*"([A-Za-z0-9_-]+)"\s*:/g, '",\n    "$1":')
    .replace(/\}\s*\]\s*\}$/g, '\n  }\n  ]\n}')
    .replace(/^\{\n {4}/, '{\n  ')
    .replace(/"([A-Za-z0-9_-]+)":(?!\s)/g, '"$1": ')
    .trim();
}

function jsonDisplayFromText(text, strict) {
  const value = text.trim();
  if (!value || (!strict && !value.startsWith('{') && !value.startsWith('['))) return null;
  try {
    const parsed = JSON.parse(value);
    return {
      loose: false,
      summary: jsonValueSummary(parsed),
      text: JSON.stringify(parsed, null, 2),
    };
  } catch {
    if (!strict) return null;
    return {
      loose: true,
      summary: jsonLikeSummary(value),
      text: formatJsonLikeText(value),
    };
  }
}

function sharedFileDisplay(file) {
  const raw = sharedFileContent(file).trim();
  if (!raw) return { formatLabel: '空文件', summary: '文件为空', text: '文件为空' };

  const fence = markdownFenceFromWholeText(raw);
  const body = fence ? fence.body : raw;
  const extension = sharedFileExtension(file?.path);
  const language = fence?.language || '';
  const strictJson = language === 'json' || extension === 'json';
  const jsonDisplay = jsonDisplayFromText(body, strictJson);
  if (jsonDisplay) {
    const jsonLabel = jsonDisplay.loose ? '类 JSON' : sharedFileFormatLabel(file, language || 'json');
    const label = fence ? `${jsonLabel}（Markdown 代码块）` : jsonLabel;
    return { formatLabel: label, summary: jsonDisplay.summary, text: jsonDisplay.text };
  }

  const label = fence ? `${sharedFileFormatLabel(file, language)}（Markdown 代码块）` : sharedFileFormatLabel(file);
  return { formatLabel: label, summary: '', text: body || raw };
}

function sharedFileSummary(file) {
  const display = sharedFileDisplay(file);
  const text = (display.summary || display.text).trim();
  if (!text) return '点击“打开”加载全文。';
  const lines = [];
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    lines.push(trimmed);
    if (lines.length >= 2) break;
  }
  const summary = lines.join(' ');
  return summary.length > 180 ? `${summary.slice(0, 180)}...` : summary;
}

function sharedFilePreview(file) {
  return sharedFileDisplay(file).text;
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
  const query = useQuery({ queryKey, queryFn: listSharedFilesDashboard });
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

function useSharedFilesFilters(files, finalOutputRefs, retention, copy) {
  const [searchText, setSearchText] = useState('');
  const [sortMode, setSortMode] = useState('updated-desc');
  const [category, setCategory] = useState('all');
  const finalOutputByPath = useMemo(() => {
    const filePaths = new Set();
    for (const file of files) filePaths.add(file.path);
    const refs = new Map();
    for (const ref of finalOutputRefs) {
      if (filePaths.has(ref.path)) refs.set(ref.path, ref);
    }
    return refs;
  }, [files, finalOutputRefs]);
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
    activeSortLabel: copy.sorts[sortMode] || copy.sorts['updated-desc'],
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
  const loadFileDetail = useSharedFileDetailLoader(setBusyPath);
  const openFile = useCallback(async (file) => {
    const path = textValue(file?.path);
    const binaryMedia = isBinaryMediaPath(path);
    setNotice(null);
    setCopied(false);
    try {
      if (binaryMedia) {
        setBusyPath(path);
        try {
          await openSharedFile({ path });
        } finally {
          setBusyPath('');
        }
        setNotice({ level: 'success', message: '已打开媒体文件。' });
        return;
      }
      setSelectedFile(await loadFileDetail(file));
    } catch (err) {
      const action = binaryMedia ? '打开文件失败' : '读取文件失败';
      setNotice({ level: 'error', message: `${action}：${err.message || String(err)}` });
    }
  }, [loadFileDetail, setBusyPath]);
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
      return await readSharedFile({ path }, file);
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

function FilesPage({ copy = APP_COPY.zh.files, projectPath, store }) {
  const exportDefaultPath = optionalSettingsCwd(projectPath);
  const dashboard = useSharedFilesDashboard(store);
  const filters = useSharedFilesFilters(dashboard.files, dashboard.finalOutputRefs, dashboard.retention, copy);
  const actions = useSharedFileActions({
    exportDefaultPath,
    refreshFiles: dashboard.refreshFiles,
    store,
    protectionFor: filters.protectionFor,
  });
  return <SharedFilesPageView actions={actions} copy={copy} dashboard={dashboard} filters={filters} />;
}

function SharedFilesPageView({ actions, copy, dashboard, filters }) {
  return (
    <section className="console-page shared-files-page">
      <SharedFilesHeader copy={copy} dashboard={dashboard} filters={filters} />
      <SharedFilesOverview copy={copy} dashboard={dashboard} filters={filters} />
      <SharedFilesIntro copy={copy} />
      <SharedFilesTabs category={filters.category} categoryCounts={filters.categoryCounts} copy={copy} onCategory={filters.setCategory} />
      <SharedFilesStatus actions={actions} copy={copy} dashboard={dashboard} />
      <SharedFilesContent actions={actions} copy={copy} dashboard={dashboard} filters={filters} />
      <SharedFilesModals actions={actions} />
    </section>
  );
}

function SharedFilesOverview({ copy, dashboard, filters }) {
  const cleanupCount = Number(dashboard.retention?.cleanupCandidateCount || 0);
  return (
    <section className="shared-files-overview" aria-label={copy.overviewAria}>
      <div className="shared-files-overview-copy">
        <span>{copy.currentAssets}</span>
        <h2>{copy.overviewTitle}</h2>
      </div>
      <dl>
        <div><dt>{copy.allFiles}</dt><dd>{dashboard.files.length}</dd></div>
        <div><dt>{copy.finalOutputs}</dt><dd>{filters.finalCount}</dd></div>
        <div><dt>{copy.workFiles}</dt><dd>{filters.workCount}</dd></div>
        <div><dt>{copy.cleanupCandidates}</dt><dd>{cleanupCount}</dd></div>
      </dl>
    </section>
  );
}

function SharedFilesHeader({ copy, dashboard, filters }) {
  return (
    <PageHeader
      icon={FolderOpen}
      title={copy.title}
      subtitle={`${filters.activeSortLabel} · ${copy.allFiles} ${dashboard.files.length} · ${copy.finalOutputs} ${filters.finalCount} · ${copy.workFiles} ${filters.workCount}`}
      actions={(
        <>
          <label className="shared-files-search">
            <Search size={15} />
            <input aria-label={copy.search} placeholder={copy.searchPlaceholder} value={filters.searchText} onChange={(event) => filters.setSearchText(event.target.value)} />
          </label>
          <select aria-label={copy.sort} value={filters.sortMode} onChange={(event) => filters.setSortMode(event.target.value)}>
            {SHARED_FILE_SORT_KEYS.map((key) => <option key={key} value={key}>{copy.sorts[key]}</option>)}
          </select>
        </>
      )}
    />
  );
}

function SharedFilesIntro({ copy }) {
  return (
    <div className="file-intro">
      <FolderOpen size={29} />
      <h2>{copy.introTitle}</h2>
      <p>{copy.introText}</p>
    </div>
  );
}

function SharedFilesTabs({ category, categoryCounts, copy, onCategory }) {
  return (
    <div className="shared-files-tabs" role="tablist" aria-label={copy.categoryAria}>
      {SHARED_FILE_CATEGORY_KEYS.map((key) => (
        <button key={key} type="button" role="tab" aria-selected={category === key} className={category === key ? 'active' : ''} onClick={() => onCategory(key)}>
          {copy.categories[key]} {categoryCounts[key] || 0}
        </button>
      ))}
    </div>
  );
}

function SharedFilesStatus({ actions, copy, dashboard }) {
  return (
    <>
      {actions.notice ? <p className={actions.notice.level === 'error' ? 'danger-text' : 'settings-status'}>{actions.notice.message}</p> : null}
      {dashboard.syncError ? (
        <div className="danger-text shared-files-sync-alert" role="alert">
          <span>{dashboard.syncError}</span>
          <button type="button" className="ghost" onClick={() => { void dashboard.refreshFiles(); }}>{copy.retrySync}</button>
        </div>
      ) : null}
      <RetryableSyncError className="danger-text shared-files-sync-alert" message={dashboard.error} onRetry={dashboard.refreshFiles} />
    </>
  );
}

function SharedFilesContent({ actions, copy, dashboard, filters }) {
  if (!dashboard.error && dashboard.loading && dashboard.files.length === 0) return <p className="console-message">{copy.loading}</p>;
  if (!dashboard.error && !dashboard.loading && dashboard.files.length === 0) return <SharedFilesEmptyState copy={copy} kind="none" />;
  if (!dashboard.error && dashboard.files.length > 0 && filters.visibleFiles.length === 0) return <SharedFilesEmptyState copy={copy} kind="search" />;
  if (dashboard.error || filters.visibleFiles.length === 0) return null;
  return <SharedFilesList actions={actions} copy={copy} filters={filters} />;
}

function SharedFilesEmptyState({ copy, kind }) {
  const search = kind === 'search';
  return (
    <div className="empty-state">
      <span>{search ? <Search size={24} /> : <File size={24} />}</span>
      <h2>{search ? copy.emptySearchTitle : copy.emptyTitle}</h2>
      <p>{search ? copy.emptySearchText : copy.emptyText}</p>
    </div>
  );
}

function SharedFilesList({ actions, copy, filters }) {
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
          copy={copy}
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
  copy,
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
  const role = finalOutputRef ? copy.roleFinal : copy.roleWork;
  let deleteLabel = copy.delete;
  if (protectedFile) {
    deleteLabel = copy.protectedDelete;
  } else if (deleting) {
    deleteLabel = copy.deleting;
  }
  return (
    <article className={`file-row${finalOutputRef ? ' is-final-output' : ''}`}>
      <header>
        <h3>{path.base}</h3>
        <span>{role}</span>
      </header>
      <p>{role} {sharedFileTimestamp(file.updatedAt)} {formatBytes(sharedFileContent(file).length)}</p>
      <div className="file-path-container">
        <code>{file.path}</code>
      </div>
      {finalOutputRef ? (
        <small>Run {finalOutputRef.runKey || '-'} · DAG {finalOutputRef.dagKey || '-'} · Node {finalOutputRef.sourceNodeKey || '-'}</small>
      ) : null}
      <pre className="shared-file-summary">{sharedFileSummary(file)}</pre>
      <footer>
        <button type="button" className="ghost continue-with-file" onClick={() => onContinue(file)}>
          <MessageCircle size={14} /> {copy.continueWithFile}
        </button>
        <div className="file-row-actions">
          <button type="button" onClick={() => { void onOpen(file); }} disabled={busy}>
            <Eye size={14} /> {busy ? copy.loadingAction : copy.open}
          </button>
          <button type="button" onClick={() => { void onExport(file); }} disabled={busy || exporting}>
            <Download size={14} /> {exporting ? copy.exporting : copy.export}
          </button>
          <button
            type="button"
            className={protectedFile ? 'ghost' : 'text-danger'}
            onClick={() => onDelete(file)}
            disabled={protectedFile || deleting}
            title={protectedFile ? copy.protectedDeleteTitle : ''}
          >
            <Trash2 size={14} /> {deleteLabel}
          </button>
        </div>
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
          <dt>格式</dt><dd>{sharedFileDisplay(file).formatLabel}</dd>
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

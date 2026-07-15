import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import { Database, Eye, Pencil, Trash2, RefreshCw, Search, Upload } from 'lucide-react';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';
import { cleanScalar, errorMessage } from '../shared/pageShared.js';
import { skillsPageService } from './services/skillsPageService.js';

const {
  deleteDatasourceDocument,
  getDatasourceDocument,
  listDatasourceChunks,
  listDatasourceDocuments,
  selectDatasourceImportFile,
  updateDatasourceDocument,
} = skillsPageService;

const DATASOURCE_LIST_LIMIT = 200;
const DATASOURCE_CHUNK_PAGE_LIMIT = 50;
const DATASOURCE_IMPORT_FILTERS = [{ displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' }];

const DATASOURCE_UI = {
  actions: '操作',
  cancel: '取消',
  chunks: '分块数',
  close: '关闭',
  confirmDelete: '确认删除',
  content: '内容',
  delete: '删除',
  deletePrompt: '确定删除该数据源吗？删除后将无法恢复相关分块数据。',
  deleteSuccess: '已删除数据源。',
  deleteTitle: '删除数据源',
  detailTitle: '数据源详情',
  edit: '编辑',
  editTitle: '编辑数据源',
  empty: '暂无数据源',
  errorPrefix: '操作失败：',
  extension: '扩展名',
  fileName: '文件名',
  id: 'ID',
  import: '导入',
  importPlaceholder: '支持 PDF、TXT 和 TEXT 文件',
  importSuccess: '已导入数据源。',
  loadingMore: '继续读取分块...',
  loading: '读取中...',
  noChunks: '暂无分块。',
  path: '路径',
  refresh: '刷新',
  save: '保存',
  size: '大小',
  sourcePath: '本地文件路径',
  status: '状态',
  totalChars: '字符',
  updateSuccess: '已更新数据源。',
  view: '查看',
};

function textFromValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

function hasOwnField(raw, field) {
  return Boolean(raw && typeof raw === 'object' && Object.prototype.hasOwnProperty.call(raw, field));
}

function datasourceDocumentsQueryKey() {
  return ['datasourceV2', 'documents'];
}

function datasourceDocumentQueryKey(documentId) {
  return ['datasourceV2', 'document', documentId];
}

function normalizeDatasourceDocument(raw, index = 0) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`datasource document ${index} must be an object`);
  }
  const documentId = Number(raw.documentId ?? raw.document_id ?? raw.id);
  if (!Number.isInteger(documentId) || documentId <= 0) {
    throw new Error(`datasource document ${index} is missing documentId`);
  }
  return {
    documentId,
    sourcePath: cleanScalar(raw.sourcePath ?? raw.source_path),
    fileName: cleanScalar(raw.fileName ?? raw.file_name),
    extension: cleanScalar(raw.extension),
    sizeBytes: Number(raw.sizeBytes ?? raw.size_bytes ?? 0),
    contentHash: cleanScalar(raw.contentHash ?? raw.content_hash),
    chunkCount: Number(raw.chunkCount ?? raw.chunk_count ?? 0),
    totalChars: Number(raw.totalChars ?? raw.total_chars ?? 0),
    status: cleanScalar(raw.status),
    errorMessage: cleanScalar(raw.errorMessage ?? raw.error_message),
    createdAt: cleanScalar(raw.createdAt ?? raw.created_at),
    updatedAt: cleanScalar(raw.updatedAt ?? raw.updated_at),
  };
}

function normalizeDatasourceDocuments(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('datasourceV2/list response must be an object');
  }
  if (!Array.isArray(response.documents)) {
    throw new Error('datasourceV2/list response.documents must be an array');
  }
  return response.documents.map(normalizeDatasourceDocument);
}

function normalizeDatasourceDetail(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('datasourceV2/get response must be an object');
  }
  const document = normalizeDatasourceDocument(response.document, 0);
  return {
    document,
    ...normalizeDatasourceChunkPage(response, document, 'datasourceV2/get'),
  };
}

function normalizeDatasourceChunkPage(response, document, source) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error(`${source} response must be an object`);
  }
  if (!Array.isArray(response.chunks)) {
    throw new Error(`${source} response.chunks must be an array`);
  }
  const chunks = response.chunks.map((raw, index) => normalizeDatasourceChunk(raw, document, index));
  return {
    chunks,
    hasMore: Boolean(response.hasMore ?? response.has_more),
    nextCursor: Number(response.nextCursor ?? response.next_cursor ?? -1),
  };
}

function normalizeDatasourceChunk(raw, document, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`datasource chunk ${index} must be an object`);
  }
  return {
    id: Number(raw.id),
    documentId: Number(hasOwnField(raw, 'documentId') ? raw.documentId : document.documentId),
    chunkIndex: Number(hasOwnField(raw, 'chunkIndex') ? raw.chunkIndex : index),
    content: textFromValue(raw.content),
    charCount: Number(raw.charCount),
    byteCount: Number(raw.byteCount),
  };
}

function assertDatasourceChunkPageProgress(page, source) {
  if (page.hasMore && page.chunks.length === 0) {
    throw new Error(`${source} returned hasMore without chunks`);
  }
  return page;
}

async function fetchDatasourceDetailPage(documentId, pageParam) {
  if (pageParam === null || pageParam === undefined) {
    return assertDatasourceChunkPageProgress(
      normalizeDatasourceDetail(await getDatasourceDocument({ documentId })),
      'datasourceV2/get',
    );
  }
  return {
    document: null,
    ...assertDatasourceChunkPageProgress(
      normalizeDatasourceChunkPage(
        await listDatasourceChunks({ documentId, limit: DATASOURCE_CHUNK_PAGE_LIMIT, cursor: pageParam }),
        { documentId },
        'datasourceV2/list_chunks',
      ),
      'datasourceV2/list_chunks',
    ),
  };
}

function combineDatasourceDetailPages(pages) {
  if (!Array.isArray(pages) || pages.length === 0) return undefined;
  const first = pages[0];
  const last = pages[pages.length - 1];
  return {
    ...first,
    chunks: pages.flatMap((page) => page.chunks),
    hasMore: last.hasMore,
    nextCursor: last.nextCursor,
  };
}

function datasourceMatches(doc, search) {
  const keyword = search.trim().toLowerCase();
  if (!keyword) return true;
  return [doc.fileName, doc.sourcePath, doc.extension, doc.status]
    .some((value) => value.toLowerCase().includes(keyword));
}

function formatDatasourceBytes(value) {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return '-';
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let size = bytes / 1024;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size >= 10 ? size.toFixed(0) : size.toFixed(1)} ${units[unitIndex]}`;
}

function datasourceDetailPagesWithDocument(current, normalized) {
  if (!current || !Array.isArray(current.pages)) return current;
  return {
    ...current,
    pages: current.pages.map((page, index) => (index === 0 ? datasourceFirstDetailPage(page, normalized) : page)),
  };
}

function datasourceFirstDetailPage(page, normalized) {
  return { ...page, document: normalized };
}


function datasourceEditForm(doc) {
  return {
    sourcePath: doc.sourcePath,
    fileName: doc.fileName,
    extension: doc.extension,
    sizeBytes: String(Number.isFinite(doc.sizeBytes) ? doc.sizeBytes : 0),
  };
}

// eslint-disable-next-line react-refresh/only-export-components
export async function importDatasourceSelection(ctx) {
  await ctx.facade.importDatasourceLocalFile({ sourcePath: ctx.sourcePath, pickerToken: ctx.pickerToken });
  ctx.setSourcePath('');
  ctx.setNotice(ctx.successText);
  await ctx.invalidateDocuments();
}

function DataSourceImporterCard({ busyAction, handleImport, sourcePath }) {
  return (
    <div className="mcp-tool-card add-new-card suiyuan-upload-card fusion-surface" data-testid="datasource-import-zone" onClick={() => { if (busyAction !== 'import') void handleImport(); }}>
      <div className="mcp-tool-icon add-new" aria-hidden="true">
        <Upload size={20} />
      </div>
      <div className="mcp-tool-main">
        <div className="mcp-tool-title-line">
          <h2>导入本地数据源</h2>
        </div>
        <p className="mcp-tool-notice">{DATASOURCE_UI.importPlaceholder}</p>
        {sourcePath ? <code className="selected-path">{sourcePath}</code> : null}
      </div>
      <div className="mcp-tool-actions">
        <button
          type="button"
          data-testid="datasource-import-button"
          className="suiyuan-btn-fusion"
          disabled={busyAction === 'import'}
          onClick={(e) => { e.stopPropagation(); void handleImport(); }}
        >
          {busyAction === 'import' ? DATASOURCE_UI.loading : DATASOURCE_UI.import}
        </button>
      </div>
    </div>
  );
}

function DataSourceDocumentCard({ doc, setDetailID, setEditingDoc, setDeletingDoc }) {
  return (
    <div className="datasource-card">
      <div className="datasource-card-icon">
        <Database size={20} />
      </div>
      <div className="datasource-card-body">
        <h3 title={doc.fileName || doc.sourcePath}>
          {doc.fileName || doc.sourcePath || '未命名数据源'}
        </h3>
        <p className="datasource-card-path" title={doc.sourcePath}>
          {doc.sourcePath || '-'}
        </p>
        <div className="datasource-card-meta">
          <span>{formatDatasourceBytes(doc.sizeBytes)}</span>
          <span className="dot">•</span>
          <span>{doc.chunkCount} 个分块</span>
        </div>
      </div>
      <span className={`datasource-status mcp-tool-status is-${doc.status === 'ready' ? 'enabled' : doc.status === 'failed' ? 'error' : 'loading'}`}>
        {doc.status || '-'}
      </span>
      <div className="datasource-card-actions">
        <button
          type="button"
          data-testid={`datasource-view-${doc.documentId}`}
          title={DATASOURCE_UI.view}
          aria-label={`${DATASOURCE_UI.view} ${doc.fileName}`}
          onClick={() => setDetailID(doc.documentId)}
        >
          <Eye size={16} />
        </button>
        <button
          type="button"
          data-testid={`datasource-edit-${doc.documentId}`}
          title={DATASOURCE_UI.edit}
          aria-label={`${DATASOURCE_UI.edit} ${doc.fileName}`}
          onClick={() => setEditingDoc(doc)}
        >
          <Pencil size={16} />
        </button>
        <button
          type="button"
          data-testid={`datasource-delete-${doc.documentId}`}
          title={DATASOURCE_UI.delete}
          aria-label={`${DATASOURCE_UI.delete} ${doc.fileName}`}
          onClick={() => setDeletingDoc(doc)}
          className="danger"
        >
          <Trash2 size={16} />
        </button>
      </div>
    </div>
  );
}

function DatasourceDetailModal(props) {
  const { detail, error, isError, isFetchingNextPage, isLoading, onClose } = props;
  return (
    <FocusTrapDialog ariaLabel={DATASOURCE_UI.detailTitle} className="modal-box datasource-modal" closeDisabled={false} onClose={onClose}>
      <header>
        <h2>{DATASOURCE_UI.detailTitle}</h2>
        <button type="button" className="ghost" onClick={onClose}>{DATASOURCE_UI.close}</button>
      </header>
      {isLoading ? <p>{DATASOURCE_UI.loading}</p> : null}
      {isError ? <p className="datasource-error" role="alert">{`${DATASOURCE_UI.errorPrefix}${errorMessage(error)}`}</p> : null}
      {detail ? (
        <>
          <dl className="datasource-detail-grid">
            <div><dt>{DATASOURCE_UI.id}</dt><dd>{detail.document.documentId}</dd></div>
            <div><dt>{DATASOURCE_UI.fileName}</dt><dd>{detail.document.fileName || '-'}</dd></div>
            <div><dt>{DATASOURCE_UI.path}</dt><dd>{detail.document.sourcePath || '-'}</dd></div>
            <div><dt>{DATASOURCE_UI.size}</dt><dd>{formatDatasourceBytes(detail.document.sizeBytes)}</dd></div>
            <div><dt>{DATASOURCE_UI.totalChars}</dt><dd>{detail.document.totalChars}</dd></div>
            <div><dt>{DATASOURCE_UI.status}</dt><dd>{detail.document.status || '-'}</dd></div>
          </dl>
          <div className="datasource-chunks">
            <h3>{DATASOURCE_UI.content}</h3>
            {detail.chunks.length === 0 ? <p>{DATASOURCE_UI.noChunks}</p> : detail.chunks.map((chunk) => (
              <pre key={`${chunk.id}-${chunk.chunkIndex}`} data-testid="datasource-detail-chunk">{chunk.content}</pre>
            ))}
            {isFetchingNextPage ? <p className="datasource-chunk-loading" role="status">{DATASOURCE_UI.loadingMore}</p> : null}
          </div>
        </>
      ) : null}
    </FocusTrapDialog>
  );
}

function DatasourceEditModal({ doc, saving, onClose, onSave }) {
  const [form, setForm] = useState(() => datasourceEditForm(doc));
  const update = (key) => (event) => setForm((current) => ({ ...current, [key]: event.target.value }));
  return (
    <FocusTrapDialog ariaLabel={DATASOURCE_UI.editTitle} className="modal-box datasource-modal" closeDisabled={saving} onClose={onClose}>
      <header>
        <h2>{DATASOURCE_UI.editTitle}</h2>
        <button type="button" className="ghost" onClick={onClose} disabled={saving}>{DATASOURCE_UI.close}</button>
      </header>
      <div className="datasource-form-grid">
        <label>{DATASOURCE_UI.sourcePath}<input data-testid="datasource-edit-source-path" value={form.sourcePath} onChange={update('sourcePath')} /></label>
        <label>{DATASOURCE_UI.fileName}<input data-testid="datasource-edit-file-name" value={form.fileName} onChange={update('fileName')} /></label>
        <label>{DATASOURCE_UI.extension}<input value={form.extension} onChange={update('extension')} /></label>
        <label>{DATASOURCE_UI.size}<input type="number" min="0" value={form.sizeBytes} onChange={update('sizeBytes')} /></label>
      </div>
      <footer>
        <button type="button" className="ghost" onClick={onClose} disabled={saving}>{DATASOURCE_UI.cancel}</button>
        <button type="button" data-testid="datasource-edit-save" onClick={() => { void onSave(form); }} disabled={saving}>{saving ? DATASOURCE_UI.loading : DATASOURCE_UI.save}</button>
      </footer>
    </FocusTrapDialog>
  );
}

function DatasourceDeleteModal({ doc, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog ariaLabel={DATASOURCE_UI.deleteTitle} className="modal-box datasource-modal" closeDisabled={deleting} onClose={onClose}>
      <header>
        <h2>{DATASOURCE_UI.deleteTitle}</h2>
        <button type="button" className="ghost" onClick={onClose} disabled={deleting}>{DATASOURCE_UI.close}</button>
      </header>
      <p>{DATASOURCE_UI.deletePrompt}</p>
      <p className="datasource-delete-target">{doc.fileName || doc.sourcePath || `#${doc.documentId}`}</p>
      <footer>
        <button type="button" className="ghost" onClick={onClose} disabled={deleting}>{DATASOURCE_UI.cancel}</button>
        <button type="button" className="text-danger" data-testid="datasource-delete-confirm" onClick={() => { void onConfirm(); }} disabled={deleting}>{deleting ? DATASOURCE_UI.loading : DATASOURCE_UI.confirmDelete}</button>
      </footer>
    </FocusTrapDialog>
  );
}

function useDataSourceViewModel({
  detailID,
  setDetailID,
  editingDoc,
  setEditingDoc,
  deletingDoc,
  setDeletingDoc,
  setSourcePath,
}) {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState('');
  const [busyAction, setBusyAction] = useState('');
  const [notice, setNotice] = useState('');
  const [actionError, setActionError] = useState('');

  const {
    data: documents = [],
    error: documentsError,
    isError: documentsIsError,
    isFetching: documentsIsFetching,
    isLoading: documentsIsLoading,
    refetch: refetchDocuments,
  } = useQuery({
    queryKey: datasourceDocumentsQueryKey(),
    queryFn: async () => normalizeDatasourceDocuments(await listDatasourceDocuments({ limit: DATASOURCE_LIST_LIMIT })),
  });

  const {
    data: detailPagesData,
    error: detailError,
    fetchNextPage: fetchNextDatasourcePage,
    hasNextPage: detailHasNextPage,
    isError: detailIsError,
    isFetchingNextPage: detailIsFetchingNextPage,
    isLoading: detailIsLoading,
  } = useInfiniteQuery({
    queryKey: datasourceDocumentQueryKey(detailID),
    enabled: detailID > 0,
    initialPageParam: null,
    queryFn: async ({ pageParam }) => fetchDatasourceDetailPage(detailID, pageParam),
    getNextPageParam: (lastPage) => (lastPage.hasMore ? lastPage.nextCursor : undefined),
  });

  const detailData = useMemo(() => combineDatasourceDetailPages(detailPagesData?.pages), [detailPagesData]);
  const filtered = documents.filter((doc) => datasourceMatches(doc, search));

  useEffect(() => {
    if (detailID <= 0 || detailIsError || !detailHasNextPage || detailIsFetchingNextPage) return;
    void fetchNextDatasourcePage();
  }, [detailHasNextPage, detailID, detailIsError, detailIsFetchingNextPage, fetchNextDatasourcePage]);

  const invalidateDocuments = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: datasourceDocumentsQueryKey() });
  }, [queryClient]);

  const runAction = useCallback(async (action, successText) => {
    setNotice('');
    setActionError('');
    try {
      await action();
      setNotice(successText);
      await invalidateDocuments();
    } catch (error) {
      setActionError(`${DATASOURCE_UI.errorPrefix}${errorMessage(error)}`);
    }
  }, [invalidateDocuments]);

  const handleImport = useCallback(async () => {
    setBusyAction('import');
    setNotice('');
    setActionError('');
    try {
      const selected = await selectDatasourceImportFile({ filters: DATASOURCE_IMPORT_FILTERS });
      const selectedPath = cleanScalar(selected?.sourcePath);
      if (!selectedPath) return;
      const pickerToken = cleanScalar(selected?.pickerToken);
      if (!pickerToken) throw new Error('pickerToken is required');
      setSourcePath(selectedPath);
      await importDatasourceSelection({
        facade: skillsPageService,
        invalidateDocuments,
        pickerToken,
        setNotice,
        setSourcePath,
        sourcePath: selectedPath,
        successText: DATASOURCE_UI.importSuccess,
      });
    } catch (error) {
      setActionError(`${DATASOURCE_UI.errorPrefix}${errorMessage(error)}`);
    } finally {
      setBusyAction('');
    }
  }, [invalidateDocuments, setSourcePath]);

  const handleUpdate = useCallback(async (form) => {
    if (!editingDoc) return;
    setBusyAction('update');
    await runAction(async () => {
      const updated = await updateDatasourceDocument({
        documentId: editingDoc.documentId,
        sourcePath: form.sourcePath,
        fileName: form.fileName,
        extension: form.extension,
        sizeBytes: form.sizeBytes,
      });
      setEditingDoc(null);
      const normalized = normalizeDatasourceDocument(updated, 0);
      if (detailID === normalized.documentId) {
        queryClient.setQueryData(datasourceDocumentQueryKey(detailID), (current) => datasourceDetailPagesWithDocument(current, normalized));
      }
    }, DATASOURCE_UI.updateSuccess);
    setBusyAction('');
  }, [detailID, editingDoc, queryClient, runAction, setEditingDoc]);

  const handleDelete = useCallback(async () => {
    if (!deletingDoc) return;
    const documentID = deletingDoc.documentId;
    setBusyAction('delete');
    await runAction(async () => {
      await deleteDatasourceDocument({ documentId: documentID });
      setDeletingDoc(null);
      if (detailID === documentID) setDetailID(0);
      queryClient.removeQueries({ queryKey: datasourceDocumentQueryKey(documentID) });
    }, DATASOURCE_UI.deleteSuccess);
    setBusyAction('');
  }, [deletingDoc, detailID, queryClient, runAction, setDeletingDoc, setDetailID]);

  return {
    search,
    setSearch,
    busyAction,
    notice,
    actionError,
    documentsError,
    documentsIsError,
    documentsIsFetching,
    documentsIsLoading,
    refetchDocuments,
    detailData,
    detailError,
    detailIsError,
    detailIsFetchingNextPage,
    detailIsLoading,
    filtered,
    handleImport,
    handleUpdate,
    handleDelete,
  };
}

export function DataSourceView({ copy }) {
  const [sourcePath, setSourcePath] = useState('');
  const [detailID, setDetailID] = useState(0);
  const [editingDoc, setEditingDoc] = useState(null);
  const [deletingDoc, setDeletingDoc] = useState(null);

  const model = useDataSourceViewModel({
    detailID,
    setDetailID,
    editingDoc,
    setEditingDoc,
    deletingDoc,
    setDeletingDoc,
    setSourcePath,
  });

  return (
    <div className="datasource-container">
      <div className="plugins-square-header">
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', width: '100%' }}>
          <h1>{copy.datasourceTitle}</h1>
          <button
            type="button"
            className="ghost refresh-btn-compact"
            title={DATASOURCE_UI.refresh}
            aria-label={DATASOURCE_UI.refresh}
            disabled={model.documentsIsFetching}
            onClick={() => { void model.refetchDocuments(); }}
          >
            <RefreshCw size={16} />
          </button>
        </div>
      </div>

      <DataSourceImporterCard
        busyAction={model.busyAction}
        handleImport={model.handleImport}
        sourcePath={sourcePath}
      />

      <div className="plugins-search-bar-wrap">
        <div className="plugins-search-input-container">
          <Search className="search-icon" size={18} />
          <input
            type="text"
            placeholder={copy.datasourceSearch}
            value={model.search}
            onChange={(e) => model.setSearch(e.target.value)}
            aria-label={copy.datasourceSearch}
          />
        </div>
      </div>

      {model.notice ? <div className="status-surface-line success-status" role="status">{model.notice}</div> : null}
      {model.actionError ? <div className="status-surface-line error-status" role="alert">{model.actionError}</div> : null}
      {model.documentsIsError ? (
        <div className="status-surface-line error-status" role="alert">
          {`${DATASOURCE_UI.errorPrefix}${errorMessage(model.documentsError)}`}
        </div>
      ) : null}

      <div className="datasource-grid-wrap">
        {model.documentsIsLoading ? (
          <div className="status-surface-line info-status">{DATASOURCE_UI.loading}</div>
        ) : model.filtered.length === 0 ? (
          <div className="empty-state datasource-empty-card" data-testid="datasource-empty-state">
            <Database size={48} className="empty-icon" aria-hidden="true" />
            <h2>{DATASOURCE_UI.empty}</h2>
            <p>暂未包含相关数据源文件，请在上方导入本地文件。</p>
          </div>
        ) : (
          <div className="datasource-grid">
            {model.filtered.map((doc) => (
              <DataSourceDocumentCard
                key={doc.documentId}
                doc={doc}
                setDetailID={setDetailID}
                setEditingDoc={setEditingDoc}
                setDeletingDoc={setDeletingDoc}
              />
            ))}
          </div>
        )}
      </div>

      {detailID > 0 ? (
        <DatasourceDetailModal
          detail={model.detailData}
          error={model.detailError}
          isError={model.detailIsError}
          isFetchingNextPage={model.detailIsFetchingNextPage}
          isLoading={model.detailIsLoading}
          onClose={() => setDetailID(0)}
        />
      ) : null}
      {editingDoc ? (
        <DatasourceEditModal
          key={editingDoc.documentId}
          doc={editingDoc}
          saving={model.busyAction === 'update'}
          onClose={() => setEditingDoc(null)}
          onSave={model.handleUpdate}
        />
      ) : null}
      {deletingDoc ? (
        <DatasourceDeleteModal
          doc={deletingDoc}
          deleting={model.busyAction === 'delete'}
          onClose={() => setDeletingDoc(null)}
          onConfirm={model.handleDelete}
        />
      ) : null}
    </div>
  );
}

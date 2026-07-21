import { cleanScalar } from '../../shared/pageShared.js';
import { skillsPageService } from '../services/skillsPageService.js';

export const DATASOURCE_LIST_LIMIT = 200;
export const DATASOURCE_CHUNK_PAGE_LIMIT = 50;
export const DATASOURCE_IMPORT_FILTERS = [
  { displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' },
];

export const DATASOURCE_UI = {
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

const { getDatasourceDocument, listDatasourceChunks } = skillsPageService;

function textFromValue(value) {
  return value === null || value === undefined ? '' : value.toString();
}
function hasOwnField(raw, field) {
  return Boolean(
    raw &&
      typeof raw === 'object' &&
      Object.prototype.hasOwnProperty.call(raw, field),
  );
}

export function datasourceDocumentsQueryKey() {
  return ['datasourceV2', 'documents'];
}
export function datasourceDocumentQueryKey(documentId) {
  return ['datasourceV2', 'document', documentId];
}

export function normalizeDatasourceDocument(raw, index = 0) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw))
    throw new Error(`datasource document ${index} must be an object`);
  const documentId = Number(raw.documentId ?? raw.document_id ?? raw.id);
  if (!Number.isInteger(documentId) || documentId <= 0)
    throw new Error(`datasource document ${index} is missing documentId`);
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

export function normalizeDatasourceDocuments(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response))
    throw new Error('datasourceV2/list response must be an object');
  if (!Array.isArray(response.documents))
    throw new Error('datasourceV2/list response.documents must be an array');
  return response.documents.map(normalizeDatasourceDocument);
}

function normalizeDatasourceChunk(raw, document, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw))
    throw new Error(`datasource chunk ${index} must be an object`);
  return {
    id: Number(raw.id),
    documentId: Number(
      hasOwnField(raw, 'documentId') ? raw.documentId : document.documentId,
    ),
    chunkIndex: Number(hasOwnField(raw, 'chunkIndex') ? raw.chunkIndex : index),
    content: textFromValue(raw.content),
    charCount: Number(raw.charCount),
    byteCount: Number(raw.byteCount),
  };
}

function normalizeDatasourceChunkPage(response, document, source) {
  if (!response || typeof response !== 'object' || Array.isArray(response))
    throw new Error(`${source} response must be an object`);
  if (!Array.isArray(response.chunks))
    throw new Error(`${source} response.chunks must be an array`);
  return {
    chunks: response.chunks.map((raw, index) =>
      normalizeDatasourceChunk(raw, document, index),
    ),
    hasMore: Boolean(response.hasMore ?? response.has_more),
    nextCursor: Number(response.nextCursor ?? response.next_cursor ?? -1),
  };
}
function assertDatasourceChunkPageProgress(page, source) {
  if (page.hasMore && page.chunks.length === 0)
    throw new Error(`${source} returned hasMore without chunks`);
  return page;
}

export async function fetchDatasourceDetailPage(documentId, pageParam) {
  if (pageParam === null || pageParam === undefined)
    return assertDatasourceChunkPageProgress(
      normalizeDatasourceDetail(await getDatasourceDocument({ documentId })),
      'datasourceV2/get',
    );
  return {
    document: null,
    ...assertDatasourceChunkPageProgress(
      normalizeDatasourceChunkPage(
        await listDatasourceChunks({
          documentId,
          limit: DATASOURCE_CHUNK_PAGE_LIMIT,
          cursor: pageParam,
        }),
        { documentId },
        'datasourceV2/list_chunks',
      ),
      'datasourceV2/list_chunks',
    ),
  };
}
function normalizeDatasourceDetail(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response))
    throw new Error('datasourceV2/get response must be an object');
  const document = normalizeDatasourceDocument(response.document, 0);
  return {
    document,
    ...normalizeDatasourceChunkPage(response, document, 'datasourceV2/get'),
  };
}
export function combineDatasourceDetailPages(pages) {
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
export function datasourceMatches(doc, search) {
  const keyword = search.trim().toLowerCase();
  return (
    !keyword ||
    [doc.fileName, doc.sourcePath, doc.extension, doc.status].some((value) =>
      value.toLowerCase().includes(keyword),
    )
  );
}
export function formatDatasourceBytes(value) {
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
export function datasourceEditForm(doc) {
  return {
    sourcePath: doc.sourcePath,
    fileName: doc.fileName,
    extension: doc.extension,
    sizeBytes: String(Number.isFinite(doc.sizeBytes) ? doc.sizeBytes : 0),
  };
}
export function datasourceDetailPagesWithDocument(current, normalized) {
  if (!current || !Array.isArray(current.pages)) return current;
  return {
    ...current,
    pages: current.pages.map((page, index) =>
      index === 0 ? { ...page, document: normalized } : page,
    ),
  };
}

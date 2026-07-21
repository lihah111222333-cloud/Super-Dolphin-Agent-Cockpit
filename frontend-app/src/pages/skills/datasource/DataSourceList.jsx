import React from 'react';
import { Database, Eye, Pencil, Trash2, Upload } from 'lucide-react';
import { DATASOURCE_UI, formatDatasourceBytes } from './dataSourceModel.js';

export function DataSourceImporterCard({
  busyAction,
  handleImport,
  sourcePath,
}) {
  return (
    <div
      className="mcp-tool-card add-new-card suiyuan-upload-card fusion-surface"
      data-testid="datasource-import-zone"
      onClick={() => {
        if (busyAction !== 'import') void handleImport();
      }}
    >
      <div className="mcp-tool-icon add-new" aria-hidden="true">
        <Upload size={20} />
      </div>
      <div className="mcp-tool-main">
        <div className="mcp-tool-title-line">
          <h2>导入本地数据源</h2>
        </div>
        <p className="mcp-tool-notice">{DATASOURCE_UI.importPlaceholder}</p>
        {sourcePath ? (
          <code className="selected-path">{sourcePath}</code>
        ) : null}
      </div>
      <div className="mcp-tool-actions">
        <button
          type="button"
          data-testid="datasource-import-button"
          className="suiyuan-btn-fusion"
          disabled={busyAction === 'import'}
          onClick={(event) => {
            event.stopPropagation();
            void handleImport();
          }}
        >
          {busyAction === 'import'
            ? DATASOURCE_UI.loading
            : DATASOURCE_UI.import}
        </button>
      </div>
    </div>
  );
}
function DataSourceDocumentCard({ doc, onDelete, onEdit, onView }) {
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
      <span
        className={`datasource-status mcp-tool-status is-${doc.status === 'ready' ? 'enabled' : doc.status === 'failed' ? 'error' : 'loading'}`}
      >
        {doc.status || '-'}
      </span>
      <div className="datasource-card-actions">
        <button
          type="button"
          data-testid={`datasource-view-${doc.documentId}`}
          title={DATASOURCE_UI.view}
          aria-label={`${DATASOURCE_UI.view} ${doc.fileName}`}
          onClick={() => onView(doc.documentId)}
        >
          <Eye size={16} />
        </button>
        <button
          type="button"
          data-testid={`datasource-edit-${doc.documentId}`}
          title={DATASOURCE_UI.edit}
          aria-label={`${DATASOURCE_UI.edit} ${doc.fileName}`}
          onClick={() => onEdit(doc)}
        >
          <Pencil size={16} />
        </button>
        <button
          type="button"
          data-testid={`datasource-delete-${doc.documentId}`}
          title={DATASOURCE_UI.delete}
          aria-label={`${DATASOURCE_UI.delete} ${doc.fileName}`}
          onClick={() => onDelete(doc)}
          className="danger"
        >
          <Trash2 size={16} />
        </button>
      </div>
    </div>
  );
}
export function DataSourceList({
  documents,
  isLoading,
  onDelete,
  onEdit,
  onView,
}) {
  if (isLoading)
    return (
      <div className="status-surface-line info-status">
        {DATASOURCE_UI.loading}
      </div>
    );
  if (documents.length === 0)
    return (
      <div
        className="empty-state datasource-empty-card"
        data-testid="datasource-empty-state"
      >
        <Database size={48} className="empty-icon" aria-hidden="true" />
        <h2>{DATASOURCE_UI.empty}</h2>
        <p>暂未包含相关数据源文件，请在上方导入本地文件。</p>
      </div>
    );
  return (
    <div className="datasource-grid">
      {documents.map((doc) => (
        <DataSourceDocumentCard
          key={doc.documentId}
          doc={doc}
          onView={onView}
          onEdit={onEdit}
          onDelete={onDelete}
        />
      ))}
    </div>
  );
}

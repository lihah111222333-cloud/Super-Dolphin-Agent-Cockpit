import React, { useState } from 'react';
import { RefreshCw, Search } from 'lucide-react';
import { runUIAction } from '../../../shared/ui/runUIAction.js';
import {
  DataSourceImporterCard,
  DataSourceList,
} from './DataSourceList.jsx';
import {
  DatasourceDeleteModal,
  DatasourceDetailModal,
  DatasourceEditModal,
} from './DataSourceDialogs.jsx';
import {
  datasourceMatches,
  DATASOURCE_UI,
} from './dataSourceModel.js';
import { useDataSourceActions } from './useDataSourceActions.js';
import { useDataSourceQueries } from './useDataSourceQueries.js';

export function DataSourceView({ copy }) {
  const [sourcePath, setSourcePath] = useState('');
  const [detailID, setDetailID] = useState(0);
  const [editingDoc, setEditingDoc] = useState(null);
  const [deletingDoc, setDeletingDoc] = useState(null);
  const [search, setSearch] = useState('');
  const { documents, documentsQuery, detailData, detailQuery } =
    useDataSourceQueries(detailID);
  const actions = useDataSourceActions(detailID, editingDoc, deletingDoc);
  const filteredDocuments = documents.filter((doc) =>
    datasourceMatches(doc, search),
  );
  const refreshDocuments = () =>
    runUIAction(
      'datasource.documents.refresh',
      () => documentsQuery.refetch({ throwOnError: true }),
      { retryable: true },
    );
  return (
    <div className="plugins-square-container datasource-container">
      <div className="plugins-square-header">
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            width: '100%',
          }}
        >
          <h1>{copy.datasourceTitle}</h1>
          <button
            type="button"
            className="ghost refresh-btn-compact"
            title={DATASOURCE_UI.refresh}
            aria-label={DATASOURCE_UI.refresh}
            disabled={documentsQuery.isFetching}
            onClick={() => {
              void refreshDocuments();
            }}
          >
            <RefreshCw size={16} />
          </button>
        </div>
      </div>
      <DataSourceImporterCard
        busyAction={actions.busyAction}
        handleImport={() => actions.handleImport(setSourcePath)}
        sourcePath={sourcePath}
      />
      <div className="plugins-search-bar-wrap">
        <div className="plugins-search-input-container">
          <Search className="search-icon" size={18} />
          <input
            type="text"
            placeholder={copy.datasourceSearch}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            aria-label={copy.datasourceSearch}
          />
        </div>
      </div>
      {actions.notice ? (
        <div className="status-surface-line success-status" role="status">
          {actions.notice}
        </div>
      ) : null}
      {actions.actionError ? (
        <div className="status-surface-line error-status" role="alert">
          {actions.actionError}
        </div>
      ) : null}
      {documentsQuery.isError ? (
        <div
          className="status-surface-line error-status"
          role="alert"
        >{`${DATASOURCE_UI.errorPrefix}读取数据源失败，请重试。`}</div>
      ) : null}
      <div className="datasource-grid-wrap">
        <DataSourceList
          documents={filteredDocuments}
          isLoading={documentsQuery.isLoading}
          onView={setDetailID}
          onEdit={setEditingDoc}
          onDelete={setDeletingDoc}
        />
      </div>
      {detailID > 0 ? (
        <DatasourceDetailModal
          detail={detailData}
          isError={detailQuery.isError}
          isFetchingNextPage={detailQuery.isFetchingNextPage}
          isLoading={detailQuery.isLoading}
          onClose={() => setDetailID(0)}
        />
      ) : null}
      {editingDoc ? (
        <DatasourceEditModal
          key={editingDoc.documentId}
          doc={editingDoc}
          saving={actions.busyAction === 'update'}
          onClose={() => setEditingDoc(null)}
          onSave={(form) =>
            actions.handleUpdate(form, () => setEditingDoc(null))
          }
        />
      ) : null}
      {deletingDoc ? (
        <DatasourceDeleteModal
          doc={deletingDoc}
          deleting={actions.busyAction === 'delete'}
          onClose={() => setDeletingDoc(null)}
          onConfirm={() =>
            actions.handleDelete(
              () => setDeletingDoc(null),
              () => setDetailID(0),
            )
          }
        />
      ) : null}
    </div>
  );
}

import React, { useState } from 'react';
import { FocusTrapDialog } from '../../../shared/ui/FocusTrapDialog.jsx';
import {
  DATASOURCE_UI,
  datasourceEditForm,
  formatDatasourceBytes,
} from './dataSourceModel.js';

export function DatasourceDetailModal({
  detail,
  isError,
  isFetchingNextPage,
  isLoading,
  onClose,
}) {
  return (
    <FocusTrapDialog
      ariaLabel={DATASOURCE_UI.detailTitle}
      className="modal-box datasource-modal"
      closeDisabled={false}
      onClose={onClose}
    >
      <header>
        <h2>{DATASOURCE_UI.detailTitle}</h2>
        <button type="button" className="ghost" onClick={onClose}>
          {DATASOURCE_UI.close}
        </button>
      </header>
      {isLoading ? <p>{DATASOURCE_UI.loading}</p> : null}
      {isError ? (
        <p
          className="datasource-error"
          role="alert"
        >{`${DATASOURCE_UI.errorPrefix}读取详情失败，请重试。`}</p>
      ) : null}
      {detail ? (
        <>
          <dl className="datasource-detail-grid">
            <div>
              <dt>{DATASOURCE_UI.id}</dt>
              <dd>{detail.document.documentId}</dd>
            </div>
            <div>
              <dt>{DATASOURCE_UI.fileName}</dt>
              <dd>{detail.document.fileName || '-'}</dd>
            </div>
            <div>
              <dt>{DATASOURCE_UI.path}</dt>
              <dd>{detail.document.sourcePath || '-'}</dd>
            </div>
            <div>
              <dt>{DATASOURCE_UI.size}</dt>
              <dd>{formatDatasourceBytes(detail.document.sizeBytes)}</dd>
            </div>
            <div>
              <dt>{DATASOURCE_UI.totalChars}</dt>
              <dd>{detail.document.totalChars}</dd>
            </div>
            <div>
              <dt>{DATASOURCE_UI.status}</dt>
              <dd>{detail.document.status || '-'}</dd>
            </div>
          </dl>
          <div className="datasource-chunks">
            <h3>{DATASOURCE_UI.content}</h3>
            {detail.chunks.length === 0 ? (
              <p>{DATASOURCE_UI.noChunks}</p>
            ) : (
              detail.chunks.map((chunk) => (
                <pre
                  key={`${chunk.id}-${chunk.chunkIndex}`}
                  data-testid="datasource-detail-chunk"
                >
                  {chunk.content}
                </pre>
              ))
            )}
            {isFetchingNextPage ? (
              <p className="datasource-chunk-loading" role="status">
                {DATASOURCE_UI.loadingMore}
              </p>
            ) : null}
          </div>
        </>
      ) : null}
    </FocusTrapDialog>
  );
}
export function DatasourceEditModal({ doc, saving, onClose, onSave }) {
  const [form, setForm] = useState(() => datasourceEditForm(doc));
  const update = (key) => (event) =>
    setForm((current) => ({ ...current, [key]: event.target.value }));
  return (
    <FocusTrapDialog
      ariaLabel={DATASOURCE_UI.editTitle}
      className="modal-box datasource-modal"
      closeDisabled={saving}
      onClose={onClose}
    >
      <header>
        <h2>{DATASOURCE_UI.editTitle}</h2>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={saving}
        >
          {DATASOURCE_UI.close}
        </button>
      </header>
      <div className="datasource-form-grid">
        <label>
          {DATASOURCE_UI.sourcePath}
          <input
            data-testid="datasource-edit-source-path"
            value={form.sourcePath}
            onChange={update('sourcePath')}
          />
        </label>
        <label>
          {DATASOURCE_UI.fileName}
          <input
            data-testid="datasource-edit-file-name"
            value={form.fileName}
            onChange={update('fileName')}
          />
        </label>
        <label>
          {DATASOURCE_UI.extension}
          <input value={form.extension} onChange={update('extension')} />
        </label>
        <label>
          {DATASOURCE_UI.size}
          <input
            type="number"
            min="0"
            value={form.sizeBytes}
            onChange={update('sizeBytes')}
          />
        </label>
      </div>
      <footer>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={saving}
        >
          {DATASOURCE_UI.cancel}
        </button>
        <button
          type="button"
          data-testid="datasource-edit-save"
          onClick={() => {
            void onSave(form);
          }}
          disabled={saving}
        >
          {saving ? DATASOURCE_UI.loading : DATASOURCE_UI.save}
        </button>
      </footer>
    </FocusTrapDialog>
  );
}
export function DatasourceDeleteModal({ doc, deleting, onClose, onConfirm }) {
  return (
    <FocusTrapDialog
      ariaLabel={DATASOURCE_UI.deleteTitle}
      className="modal-box datasource-modal"
      closeDisabled={deleting}
      onClose={onClose}
    >
      <header>
        <h2>{DATASOURCE_UI.deleteTitle}</h2>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={deleting}
        >
          {DATASOURCE_UI.close}
        </button>
      </header>
      <p>{DATASOURCE_UI.deletePrompt}</p>
      <p className="datasource-delete-target">
        {doc.fileName || doc.sourcePath || `#${doc.documentId}`}
      </p>
      <footer>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={deleting}
        >
          {DATASOURCE_UI.cancel}
        </button>
        <button
          type="button"
          className="text-danger"
          data-testid="datasource-delete-confirm"
          onClick={() => {
            void onConfirm();
          }}
          disabled={deleting}
        >
          {deleting ? DATASOURCE_UI.loading : DATASOURCE_UI.confirmDelete}
        </button>
      </footer>
    </FocusTrapDialog>
  );
}

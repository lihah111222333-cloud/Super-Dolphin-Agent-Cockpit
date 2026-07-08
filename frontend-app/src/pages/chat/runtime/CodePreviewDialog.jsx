import React from 'react';
import { X } from 'lucide-react';
import { codePreviewMeta } from '../adapters/codePreviewMetaAdapter.js';
import { FocusTrapDialog } from '../../../shared/ui/FocusTrapDialog.jsx';
import './CodePreviewDialog.css';

function CodePreviewDialog({
  preview,
  renderMarkdownPreview,
  onBeginEdit,
  onCancelEdit,
  onChangeDraft,
  onClose,
  onDirtyClose,
  onSave,
}) {
  const dirty = preview.draft !== preview.content;
  const canEdit = preview.previewMode === 'full' && Boolean(preview.editable) && !preview.image && !preview.loading;
  const requestClose = () => {
    if (dirty && !preview.loading && !preview.saving) {
      onDirtyClose();
      return;
    }
    onClose();
  };
  const meta = codePreviewMeta(preview);
  const imageSrc = preview.imageSrc || preview.imageFullSrc;
  return (
    <FocusTrapDialog ariaLabel="文件预览" className="modal-box code-preview-modal" initialFocusSelector={preview.editing && !preview.image ? 'textarea' : ''} onClose={requestClose}>
      <header>
        <div>
          <h2>文件预览</h2>
          <p className="code-preview-path">{preview.relative || preview.filePath}</p>
          {meta ? <p className="code-preview-meta">{meta}</p> : null}
        </div>
        <button type="button" aria-label="关闭文件预览" title="关闭文件预览" onClick={requestClose}>
          <X size={15} aria-hidden="true" />
        </button>
      </header>
      {preview.loading ? (
        <div className="code-preview-loading">正在打开文件</div>
      ) : preview.image ? (
        <figure className="code-preview-image">
          {imageSrc ? (
            <img src={imageSrc} alt={preview.relative || preview.filePath || '图片预览'} />
          ) : (
            <figcaption role="note">{preview.error || '图片预览需要后端提供安全预览 URL'}</figcaption>
          )}
        </figure>
      ) : preview.editing ? (
        <>
          <textarea
            aria-label="文件预览内容"
            className="code-preview-editor"
            spellCheck="false"
            value={preview.draft}
            onChange={(event) => onChangeDraft(event.target.value)}
          />
          {preview.previewKind === 'markdown' ? <p className="code-preview-hint">保存后会回到 Markdown 预览。</p> : null}
        </>
      ) : preview.previewKind === 'markdown' ? (
        <div className="code-preview-markdown message-markdown">
          {renderMarkdownPreview(preview.content)}
        </div>
      ) : (
        <pre className="code-preview-text">{preview.content}</pre>
      )}
      {preview.error ? <p className="code-preview-error" role="alert">{preview.error}</p> : null}
      {preview.status ? <output className="code-preview-status">{preview.status}</output> : null}
      <footer>
        <button type="button" onClick={requestClose}>关闭</button>
        {canEdit && preview.previewKind === 'markdown' && !preview.editing ? <button type="button" onClick={onBeginEdit}>编辑预览</button> : null}
        {canEdit && preview.editing && preview.previewKind === 'markdown' ? <button type="button" disabled={preview.saving} onClick={onCancelEdit}>放弃更改</button> : null}
        {canEdit && preview.editing ? (
          <button type="button" disabled={preview.loading || preview.saving || !dirty} onClick={onSave}>
            {preview.saving ? '保存中' : '保存预览更改'}
          </button>
        ) : null}
      </footer>
    </FocusTrapDialog>
  );
}

export { CodePreviewDialog };

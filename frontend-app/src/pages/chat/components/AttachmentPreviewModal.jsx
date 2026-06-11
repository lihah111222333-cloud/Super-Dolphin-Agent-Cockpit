import React from 'react';
import { File, Trash2, X } from 'lucide-react';
import { FocusTrapDialog } from '../../../shared/ui/FocusTrapDialog.jsx';

function AttachmentPreviewModal({ attachment, onClose, onRemove }) {
  const isImage = attachment.kind === 'image' && attachment.previewUrl;
  return (
    <FocusTrapDialog ariaLabel="附件预览" className="modal-box attachment-preview-modal" onClose={onClose}>
      <header>
        <div>
          <strong>{attachment.name || attachment.path}</strong>
          <p>{attachment.path}</p>
        </div>
        <button type="button" aria-label="关闭附件预览" onClick={onClose}><X size={16} /></button>
      </header>
      {isImage ? (
        <img className="attachment-preview-image" src={attachment.previewUrl} alt={attachment.name || '附件图片预览'} />
      ) : (
        <div className="attachment-preview-file">
          <File size={28} />
          <code>{attachment.path}</code>
        </div>
      )}
      <footer>
        <button type="button" onClick={onRemove}><Trash2 size={14} /> 移除附件</button>
        <button type="button" onClick={onClose}>关闭</button>
      </footer>
    </FocusTrapDialog>
  );
}

export { AttachmentPreviewModal };

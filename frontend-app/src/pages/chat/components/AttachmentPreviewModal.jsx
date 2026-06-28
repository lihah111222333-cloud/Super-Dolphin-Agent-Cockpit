import React from 'react';
import { File, Trash2, X } from 'lucide-react';
import { attachmentDisplayName } from '../../../entities/client/model/composerAttachments.js';
import { FocusTrapDialog } from '../../../shared/ui/FocusTrapDialog.jsx';
import { resolveAttachmentImageSrc } from './timelineMessageModel.js';

const ATTACHMENT_PREVIEW_LABEL = '\u9644\u4ef6\u9884\u89c8';
const CLOSE_PREVIEW_LABEL = '\u5173\u95ed\u9644\u4ef6\u9884\u89c8';
const REMOVE_ATTACHMENT_LABEL = '\u79fb\u9664\u9644\u4ef6';
const CLOSE_LABEL = '\u5173\u95ed';
const IMAGE_PREVIEW_ALT = '\u9644\u4ef6\u56fe\u7247\u9884\u89c8';

function AttachmentPreviewModal({ attachment, onClose, onRemove }) {
  const imageSrc = resolveAttachmentImageSrc(attachment);
  const title = attachmentDisplayName(attachment);
  const detailLabel = attachment?.kind === 'image' ? '本地图片' : '本地文件';
  return (
    <FocusTrapDialog ariaLabel={ATTACHMENT_PREVIEW_LABEL} className="modal-box attachment-preview-modal" onClose={onClose}>
      <header>
        <div>
          <strong>{title}</strong>
          <p>{detailLabel}</p>
        </div>
        <button type="button" aria-label={CLOSE_PREVIEW_LABEL} onClick={onClose}><X size={16} /></button>
      </header>
      {imageSrc ? (
        <img className="attachment-preview-image" src={imageSrc} alt={title || IMAGE_PREVIEW_ALT} />
      ) : (
        <div className="attachment-preview-file">
          <File size={28} />
          <code>{title}</code>
        </div>
      )}
      <footer>
        <button type="button" onClick={onRemove}><Trash2 size={14} /> {REMOVE_ATTACHMENT_LABEL}</button>
        <button type="button" onClick={onClose}>{CLOSE_LABEL}</button>
      </footer>
    </FocusTrapDialog>
  );
}

export { AttachmentPreviewModal };

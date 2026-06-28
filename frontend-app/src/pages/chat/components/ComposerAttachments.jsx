import React from 'react';
import { File, X } from 'lucide-react';
import { attachmentDisplayName } from '../../../entities/client/model/composerAttachments.js';
import { composerAttachmentKey } from './composerAttachmentKey.js';
import { resolveAttachmentImageSrc } from './timelineMessageModel.js';

const PREVIEW_ATTACHMENT_LABEL = '\u9884\u89c8\u9644\u4ef6';
const REMOVE_ATTACHMENT_LABEL = '\u79fb\u9664\u9644\u4ef6';

function ComposerAttachments({ attachments, onPreview, onRemove }) {
  if (attachments.length === 0) return null;
  return (
    <div className="attachments">
      {attachments.map((item) => {
        const imageSrc = resolveAttachmentImageSrc(item);
        const label = attachmentDisplayName(item);
        return (
          <span key={composerAttachmentKey(item)} className={`attachment-pill${imageSrc ? ' attachment-pill--image' : ''}`}>
            <button type="button" className="attachment-preview" aria-label={`${PREVIEW_ATTACHMENT_LABEL} ${label}`} onClick={() => onPreview(item)}>
              {imageSrc ? <img src={imageSrc} alt="" /> : <File size={14} />}
              <span>{label}</span>
            </button>
            <button type="button" className="attachment-remove" aria-label={`${REMOVE_ATTACHMENT_LABEL} ${label}`} onClick={() => onRemove(item)}>
              <X size={12} />
            </button>
          </span>
        );
      })}
    </div>
  );
}

export { ComposerAttachments };

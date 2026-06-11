import React from 'react';
import { File, X } from 'lucide-react';
import { composerAttachmentKey } from './composerAttachmentKey.js';

function ComposerAttachments({ attachments, onPreview, onRemove }) {
  if (attachments.length === 0) return null;
  return (
    <div className="attachments">
      {attachments.map((item) => (
        <span key={composerAttachmentKey(item)} className={`attachment-pill${item.kind === 'image' ? ' attachment-pill--image' : ''}`}>
          <button type="button" className="attachment-preview" aria-label={`预览附件 ${item.name || item.path}`} onClick={() => onPreview(item)}>
            {item.kind === 'image' && item.previewUrl ? <img src={item.previewUrl} alt="" /> : <File size={14} />}
            <span>{item.name || item.path}</span>
          </button>
          <button type="button" className="attachment-remove" aria-label={`移除附件 ${item.name || item.path}`} onClick={() => onRemove(item)}>
            <X size={12} />
          </button>
        </span>
      ))}
    </div>
  );
}

export { ComposerAttachments };

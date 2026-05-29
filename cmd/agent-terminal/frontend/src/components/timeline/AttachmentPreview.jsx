import React, { useMemo } from 'react';
import * as Vue from '../../lib/vue.esm-browser.prod.js';

export function AttachmentPreview({
  attachments = [],
  attachmentApi = {},
}) {
  const imageList = useMemo(() => attachmentApi?.imageAttachments?.(attachments) || [], [attachmentApi, attachments]);
  const fileList = useMemo(() => attachmentApi?.fileAttachments?.(attachments) || [], [attachmentApi, attachments]);

  const attachmentLabel = (att) => {
    return attachmentApi?.attachmentLabel?.(att) || '';
  };

  const attachmentPreview = (att) => {
    return attachmentApi?.attachmentPreview?.(att) || '';
  };

  const attachmentType = (att) => {
    return attachmentApi?.attachmentType?.(att) || '';
  };

  const onHoverMove = (event, att) => {
    attachmentApi?.onAttachmentHoverMove?.(event, att);
  };

  const onHoverLeave = () => {
    attachmentApi?.onAttachmentHoverLeave?.();
  };

  const openLightbox = (att) => {
    attachmentApi?.openAttachmentLightbox?.(att);
  };

  return (
    <>
      {imageList.length > 0 && (
        <div className="chat-attachment-gallery">
          {imageList.map((att, idx) => (
            <button
              key={`img-${att.path || att.name || ''}-${idx}`}
              className="chat-attachment-card"
              type="button"
              title={attachmentLabel(att)}
              onMouseEnter={(e) => onHoverMove(e, att)}
              onMouseMove={(e) => onHoverMove(e, att)}
              onMouseLeave={onHoverLeave}
              onFocus={(e) => onHoverMove(e, att)}
              onBlur={onHoverLeave}
              onClick={() => openLightbox(att)}
            >
              <img
                className="chat-attachment-card__image"
                src={attachmentPreview(att)}
                alt={attachmentLabel(att)}
                loading="lazy"
              />
              <span className="chat-attachment-card__meta">
                <span className="attachment-kind">IMG</span>
                <span className="chat-attachment-card__name">{attachmentLabel(att)}</span>
              </span>
            </button>
          ))}
        </div>
      )}
      {fileList.length > 0 && (
        <div className="chat-attachment-list">
          {fileList.map((att, idx) => (
            <span
              key={`file-${att.path || att.name || ''}-${idx}`}
              className="chat-attachment-pill"
            >
              <span className="attachment-kind">{attachmentType(att)}</span>
              <span className="attachment-name">{attachmentLabel(att)}</span>
            </span>
          ))}
        </div>
      )}
    </>
  );
}

AttachmentPreview.setup = function(props) {
  const imageList = Vue.computed(() => props.attachmentApi.imageAttachments(props.attachments));
  const fileList = Vue.computed(() => props.attachmentApi.fileAttachments(props.attachments));
  
  return {
    imageList,
    fileList,
    attachmentLabel(item) { return props.attachmentApi.attachmentLabel(item); },
    attachmentPreview(item) { return props.attachmentApi.attachmentPreview(item); },
    attachmentType(item) { return props.attachmentApi.attachmentType(item); },
    onHoverMove(event, item) { props.attachmentApi.onAttachmentHoverMove(event, item); },
    onHoverLeave() { props.attachmentApi.onAttachmentHoverLeave(); },
    openLightbox(item) { props.attachmentApi.openAttachmentLightbox(item); },
  };
};

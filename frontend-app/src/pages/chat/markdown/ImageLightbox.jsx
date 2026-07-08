import React from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { firstTrimmedText } from './markdownMessageModel.js';

const PREVIEW_LABEL = '\u9884\u89c8';
const LIGHTBOX_LABEL_PREFIX = '\u56fe\u7247\u9884\u89c8\uff1a';
const CLOSE_LABEL = '\u5173\u95ed\u56fe\u7247\u9884\u89c8';

function ImageLightbox({ label, onClose, children }) {
  const displayLabel = firstTrimmedText(label, PREVIEW_LABEL);
  return createPortal(
    <dialog className="image-lightbox" open aria-label={`${LIGHTBOX_LABEL_PREFIX}${displayLabel}`}>
      <button type="button" className="image-lightbox-backdrop" aria-label={CLOSE_LABEL} onClick={onClose} />
      <section className="image-lightbox-panel">
        <header>
          <strong>{displayLabel}</strong>
          <div>
            <button type="button" aria-label={CLOSE_LABEL} onClick={onClose}><X size={16} /></button>
          </div>
        </header>
        {children}
      </section>
    </dialog>,
    document.body,
  );
}

export { ImageLightbox };

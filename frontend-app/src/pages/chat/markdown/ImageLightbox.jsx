import React from 'react';
import { Button as AriaButton, Dialog, Modal, ModalOverlay } from 'react-aria-components';
import { X } from 'lucide-react';
import { firstTrimmedText } from './markdownMessageModel.js';

const PREVIEW_LABEL = '\u9884\u89c8';
const LIGHTBOX_LABEL_PREFIX = '\u56fe\u7247\u9884\u89c8\uff1a';
const CLOSE_LABEL = '\u5173\u95ed\u56fe\u7247\u9884\u89c8';

function ImageLightbox({ label, onClose, children }) {
  const displayLabel = firstTrimmedText(label, PREVIEW_LABEL);
  return (
    <ModalOverlay
      className="image-lightbox"
      isDismissable
      isOpen
      onOpenChange={(isOpen) => {
        if (!isOpen) onClose();
      }}
    >
      <Modal className="image-lightbox-panel">
        <Dialog className="image-lightbox-dialog" aria-label={`${LIGHTBOX_LABEL_PREFIX}${displayLabel}`}>
          <header>
            <strong>{displayLabel}</strong>
            <div>
              <AriaButton slot="close" type="button" aria-label={CLOSE_LABEL}><X size={16} /></AriaButton>
            </div>
          </header>
          {children}
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}

export { ImageLightbox };

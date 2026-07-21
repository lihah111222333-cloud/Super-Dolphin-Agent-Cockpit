import React from 'react';
import { createPortal } from 'react-dom';
import { requiredOverlayRoot } from './overlayPortalRoot.js';

export function OverlayPortal({ children }) {
  return createPortal(children, requiredOverlayRoot());
}

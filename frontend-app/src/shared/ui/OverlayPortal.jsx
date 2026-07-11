import React from 'react';
import { createPortal } from 'react-dom';

const OVERLAY_ROOT_ERROR = 'Required overlay-root host is unavailable or not unique.';

// eslint-disable-next-line react-refresh/only-export-components
export function requiredOverlayRoot() {
  if (typeof document === 'undefined') {
    throw new Error(OVERLAY_ROOT_ERROR);
  }
  const hosts = document.querySelectorAll('#overlay-root');
  const host = hosts.length === 1 ? hosts[0] : null;
  if (!(host instanceof HTMLElement)) {
    throw new Error(OVERLAY_ROOT_ERROR);
  }
  return host;
}

export function OverlayPortal({ children }) {
  return createPortal(children, requiredOverlayRoot());
}

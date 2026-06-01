import React, { useCallback, useEffect, useRef } from 'react';

const FOCUSABLE_SELECTOR = [
  'a[href]',
  'button:not([disabled])',
  'textarea:not([disabled])',
  'input:not([type="hidden"]):not([disabled])',
  'select:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

function focusableElements(root) {
  if (!root) return [];
  return Array.from(root.querySelectorAll(FOCUSABLE_SELECTOR))
    .filter((element) => element && typeof element.focus === 'function' && element.getAttribute('aria-hidden') !== 'true');
}

export function FocusTrapDialog({
  ariaLabel,
  ariaLabelledBy,
  children,
  className = 'modal-box',
  closeDisabled = false,
  closeOnOverlayClick = false,
  initialFocusSelector = '',
  onClose,
  overlayClassName = 'modal-overlay',
}) {
  const dialogRef = useRef(null);
  const restoreFocusRef = useRef(null);

  useEffect(() => {
    const activeElement = typeof document !== 'undefined' ? document.activeElement : null;
    restoreFocusRef.current = activeElement && typeof activeElement.focus === 'function' ? activeElement : null;
    const timer = window.setTimeout(() => {
      const dialog = dialogRef.current;
      if (!dialog) return;
      const target = initialFocusSelector ? dialog.querySelector(initialFocusSelector) : null;
      const first = target || focusableElements(dialog)[0] || dialog;
      first.focus({ preventScroll: true });
    }, 0);
    return () => {
      window.clearTimeout(timer);
      const target = restoreFocusRef.current;
      if (target?.isConnected && typeof target.focus === 'function') {
        target.focus({ preventScroll: true });
      }
    };
  }, [initialFocusSelector]);

  const requestClose = useCallback(() => {
    if (!closeDisabled && typeof onClose === 'function') {
      onClose();
    }
  }, [closeDisabled, onClose]);

  const handleOverlayClick = useCallback((event) => {
    if (event.target === event.currentTarget && closeOnOverlayClick) {
      requestClose();
    }
  }, [closeOnOverlayClick, requestClose]);

  const handleKeyDown = useCallback((event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      requestClose();
      return;
    }
    if (event.key !== 'Tab') return;

    const dialog = dialogRef.current;
    const items = focusableElements(dialog);
    if (!dialog || items.length === 0) {
      event.preventDefault();
      dialog?.focus({ preventScroll: true });
      return;
    }

    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (event.shiftKey) {
      if (active === first || !dialog.contains(active)) {
        event.preventDefault();
        last.focus({ preventScroll: true });
      }
      return;
    }
    if (active === last || !dialog.contains(active)) {
      event.preventDefault();
      first.focus({ preventScroll: true });
    }
  }, [requestClose]);

  return (
    <div className={overlayClassName} onClick={handleOverlayClick}>
      <section
        ref={dialogRef}
        className={className}
        role="dialog"
        aria-modal="true"
        aria-label={ariaLabel}
        aria-labelledby={ariaLabelledBy}
        tabIndex={-1}
        onKeyDown={handleKeyDown}
      >
        {children}
      </section>
    </div>
  );
}

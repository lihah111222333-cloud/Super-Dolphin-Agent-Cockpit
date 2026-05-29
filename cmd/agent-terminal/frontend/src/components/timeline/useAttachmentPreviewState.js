import React, { useState, useRef, useCallback, useEffect } from 'react';

function isWailsShimDebug() {
  if (typeof window === 'undefined') return false;
  return Boolean(Reflect.get(window, '__WAILS_SHIM_DEBUG__'));
}

function checkReactActive() {
  if (typeof window !== 'undefined' && window.__VUE_SETUP_ACTIVE__) return false;
  if (typeof window !== 'undefined' && window.__REACT_APP_ACTIVE__) return true;
  const dispatcher = React?.__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED?.ReactCurrentDispatcher?.current ||
    React?.__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE?.H;
  return dispatcher !== null && dispatcher !== undefined;
}

export function useAttachmentPreviewState() {
  const isReactActive = checkReactActive();
  if (!isReactActive) {
    return {
      attachmentType: (att) => att?.kind === 'image' ? 'IMG' : 'FILE',
      attachmentPreview: (att) => att?.kind === 'image' ? (att.previewUrl || att.path || '') : '',
      attachmentLabel: (att) => (att?.name || att?.path || '').toString(),
      imageAttachments: (source) => (Array.isArray(source) ? source : source?.attachments || []).filter(att => att?.kind === 'image'),
      fileAttachments: (source) => (Array.isArray(source) ? source : source?.attachments || []).filter(att => att?.kind !== 'image'),
      onAttachmentHoverMove: () => {},
      onAttachmentHoverLeave: () => {},
      onAttachmentPreviewEnter: () => {},
      onAttachmentPreviewLeave: () => {},
      onAttachmentPreviewZoomIn: () => {},
      onAttachmentPreviewZoomOut: () => {},
      onAttachmentPreviewResetZoom: () => {},
      attachmentCanZoomOut: () => false,
      attachmentHoverStyle: () => ({}),
      attachmentHoverPreview: { value: null },
      attachmentLightbox: { value: null },
      openAttachmentLightbox: () => {},
      closeAttachmentLightbox: () => {},
      onAttachmentLightboxKeydown: () => {},
    };
  }

  const attachmentHoverHideTimerRef = useRef(0);
  const attachmentHoverShowTimerRef = useRef(0);
  const attachmentHoverPendingPreviewRef = useRef(null);


  const ATTACHMENT_HOVER_SHOW_DELAY_MS = 500;
  const ATTACHMENT_HOVER_HIDE_DELAY_MS = 800;
  const ATTACHMENT_HOVER_PREVIEW_LEAVE_DELAY_MS = 800;
  const ATTACHMENT_HOVER_ZOOM_MIN = 1;
  const ATTACHMENT_HOVER_ZOOM_STEP = 0.25;
  const ATTACHMENT_HOVER_ZOOM_MAX = 3;

  const [attachmentHoverPreview, setAttachmentHoverPreview] = useState(null);
  const [attachmentLightbox, setAttachmentLightbox] = useState(null);

  const resolveAttachmentList = useCallback((source) => {
    if (Array.isArray(source)) return source;
    return Array.isArray(source?.attachments) ? source.attachments : [];
  }, []);

  const attachmentType = useCallback((att) => {
    return att?.kind === 'image' ? 'IMG' : 'FILE';
  }, []);

  const attachmentPreview = useCallback((att) => {
    if (!att || att.kind !== 'image') return '';
    const preview = (att.previewUrl || '').toString().trim();
    if (preview) return preview;
    const path = (att.path || '').toString().trim();
    if (!path) return '';
    const lower = path.toLowerCase();
    if (lower.startsWith('http://')
      || lower.startsWith('https://')
      || lower.startsWith('data:image/')
      || lower.startsWith('file://')) {
      if (lower.startsWith('file://') && isWailsShimDebug()) {
        return '';
      }
      return path;
    }
    if (isWailsShimDebug()) {
      return '';
    }
    return encodeURI(`file://${path}`);
  }, []);

  const attachmentLabel = useCallback((att) => {
    return (att?.name || att?.path || (att?.kind === 'image' ? 'image attachment' : 'attachment')).toString();
  }, []);

  const imageAttachments = useCallback((source) => {
    return resolveAttachmentList(source).filter((att) => Boolean(attachmentPreview(att)));
  }, [resolveAttachmentList, attachmentPreview]);

  const fileAttachments = useCallback((source) => {
    return resolveAttachmentList(source).filter((att) => !attachmentPreview(att));
  }, [resolveAttachmentList, attachmentPreview]);

  const attachmentHoverPoint = useCallback((event) => {
    const targetEvent = event;
    const x = Number(targetEvent?.clientX);
    const y = Number(targetEvent?.clientY);
    if (Number.isFinite(x) && Number.isFinite(y) && (x > 0 || y > 0)) {
      return { x, y };
    }
    const currentTarget = targetEvent?.currentTarget;
    const rect = currentTarget?.getBoundingClientRect?.();
    if (rect) {
      return {
        x: rect.left + Math.min(rect.width, 32),
        y: rect.top + Math.min(rect.height, 32),
      };
    }
    return { x: 32, y: 32 };
  }, []);

  const clearAttachmentHoverHideTimer = useCallback(() => {
    if (!attachmentHoverHideTimerRef.current || typeof window === 'undefined') return;
    window.clearTimeout(attachmentHoverHideTimerRef.current);
    attachmentHoverHideTimerRef.current = 0;
  }, []);

  const clearAttachmentHoverShowTimer = useCallback(() => {
    if (attachmentHoverShowTimerRef.current && typeof window !== 'undefined') {
      window.clearTimeout(attachmentHoverShowTimerRef.current);
      attachmentHoverShowTimerRef.current = 0;
    }
    attachmentHoverPendingPreviewRef.current = null;
  }, []);

  const scheduleAttachmentHoverHide = useCallback((delayMS = ATTACHMENT_HOVER_HIDE_DELAY_MS) => {
    clearAttachmentHoverHideTimer();
    if (typeof window === 'undefined') {
      setAttachmentHoverPreview(null);
      return;
    }
    const delay = Number(delayMS);
    attachmentHoverHideTimerRef.current = window.setTimeout(() => {
      setAttachmentHoverPreview(null);
      attachmentHoverHideTimerRef.current = 0;
    }, Number.isFinite(delay) ? Math.max(0, delay) : ATTACHMENT_HOVER_HIDE_DELAY_MS);
  }, [clearAttachmentHoverHideTimer]);

  const applyAttachmentHoverPreview = useCallback((nextPreview) => {
    setAttachmentHoverPreview((prev) => {
      const prevZoomRaw = Number(prev?.zoom);
      const zoom = prev && prev.src === nextPreview.src && Number.isFinite(prevZoomRaw) && prevZoomRaw > 0
        ? prevZoomRaw
        : ATTACHMENT_HOVER_ZOOM_MIN;
      return {
        ...nextPreview,
        zoom,
      };
    });
  }, []);

  const scheduleAttachmentHoverShow = useCallback((nextPreview) => {
    attachmentHoverPendingPreviewRef.current = nextPreview;
    if (attachmentHoverShowTimerRef.current || typeof window === 'undefined') return;
    attachmentHoverShowTimerRef.current = window.setTimeout(() => {
      const pending = attachmentHoverPendingPreviewRef.current;
      attachmentHoverShowTimerRef.current = 0;
      attachmentHoverPendingPreviewRef.current = null;
      if (!pending) return;
      applyAttachmentHoverPreview(pending);
    }, ATTACHMENT_HOVER_SHOW_DELAY_MS);
  }, [applyAttachmentHoverPreview]);

  const attachmentHoverPosition = useCallback((pointX, pointY) => {
    const margin = 14;
    const offset = 18;
    const viewportWidth = window.innerWidth || 1280;
    const viewportHeight = window.innerHeight || 800;
    const maxWidthByViewport = Math.max(240, viewportWidth - margin * 2);
    const maxHeightByViewport = Math.max(220, viewportHeight - margin * 2);
    const preferredWidth = Math.round(viewportWidth * 0.68);
    const preferredHeight = Math.round(viewportHeight * 0.76);
    const previewWidth = Math.min(760, maxWidthByViewport, Math.max(320, preferredWidth));
    const previewHeight = Math.min(720, maxHeightByViewport, Math.max(260, preferredHeight));
    let left = pointX + offset;
    let top = pointY + offset;
    if (left + previewWidth > viewportWidth - margin) {
      left = Math.max(margin, pointX - previewWidth - offset);
    }
    if (top + previewHeight > viewportHeight - margin) {
      top = Math.max(margin, pointY - previewHeight - offset);
    }
    return {
      left,
      top,
      width: previewWidth,
      maxHeight: previewHeight,
    };
  }, []);

  const onAttachmentHoverMove = useCallback((event, att) => {
    const src = attachmentPreview(att);
    if (!src) {
      clearAttachmentHoverShowTimer();
      clearAttachmentHoverHideTimer();
      setAttachmentHoverPreview(null);
      return;
    }
    clearAttachmentHoverHideTimer();
    const point = attachmentHoverPoint(event);
    const pos = attachmentHoverPosition(point.x, point.y);
    const nextPreview = {
      src,
      alt: (att?.name || att?.path || 'image attachment').toString(),
      left: pos.left,
      top: pos.top,
      width: pos.width,
      maxHeight: pos.maxHeight,
    };

    setAttachmentHoverPreview((prev) => {
      if (prev && prev.src === src) {
        clearAttachmentHoverShowTimer();
        // Since we are setting inside setState callback, we return nextPreview with current zoom
        return {
          ...nextPreview,
          zoom: prev.zoom,
        };
      }
      if (prev && prev.src !== src) {
        // will reset
      }
      scheduleAttachmentHoverShow(nextPreview);
      return prev;
    });
  }, [attachmentPreview, attachmentHoverPoint, attachmentHoverPosition, clearAttachmentHoverShowTimer, clearAttachmentHoverHideTimer, scheduleAttachmentHoverShow]);

  const onAttachmentHoverLeave = useCallback(() => {
    clearAttachmentHoverShowTimer();
    scheduleAttachmentHoverHide();
  }, [clearAttachmentHoverShowTimer, scheduleAttachmentHoverHide]);

  const onAttachmentPreviewEnter = useCallback(() => {
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
  }, [clearAttachmentHoverHideTimer, clearAttachmentHoverShowTimer]);

  const onAttachmentPreviewLeave = useCallback(() => {
    scheduleAttachmentHoverHide(ATTACHMENT_HOVER_PREVIEW_LEAVE_DELAY_MS);
  }, [scheduleAttachmentHoverHide]);

  const onAttachmentPreviewZoomIn = useCallback((event) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
    setAttachmentHoverPreview((prev) => {
      if (!prev) return null;
      const zoomRaw = Number(prev.zoom);
      const zoom = Number.isFinite(zoomRaw) && zoomRaw > 0 ? zoomRaw : 1;
      const nextZoom = Math.min(
        ATTACHMENT_HOVER_ZOOM_MAX,
        Math.round((zoom + ATTACHMENT_HOVER_ZOOM_STEP) * 100) / 100,
      );
      return { ...prev, zoom: nextZoom };
    });
  }, [clearAttachmentHoverHideTimer, clearAttachmentHoverShowTimer]);

  const onAttachmentPreviewZoomOut = useCallback((event) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
    setAttachmentHoverPreview((prev) => {
      if (!prev) return null;
      const zoomRaw = Number(prev.zoom);
      const zoom = Number.isFinite(zoomRaw) && zoomRaw > 0 ? zoomRaw : ATTACHMENT_HOVER_ZOOM_MIN;
      const nextZoom = Math.max(
        ATTACHMENT_HOVER_ZOOM_MIN,
        Math.round((zoom - ATTACHMENT_HOVER_ZOOM_STEP) * 100) / 100,
      );
      return { ...prev, zoom: nextZoom };
    });
  }, [clearAttachmentHoverHideTimer, clearAttachmentHoverShowTimer]);

  const onAttachmentPreviewResetZoom = useCallback((event) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
    setAttachmentHoverPreview((prev) => {
      if (!prev) return null;
      return { ...prev, zoom: ATTACHMENT_HOVER_ZOOM_MIN };
    });
  }, [clearAttachmentHoverHideTimer, clearAttachmentHoverShowTimer]);

  const attachmentCanZoomOut = useCallback(() => {
    if (!attachmentHoverPreview) return false;
    const zoomRaw = Number(attachmentHoverPreview.zoom);
    const zoom = Number.isFinite(zoomRaw) && zoomRaw > 0 ? zoomRaw : ATTACHMENT_HOVER_ZOOM_MIN;
    return zoom > ATTACHMENT_HOVER_ZOOM_MIN;
  }, [attachmentHoverPreview]);

  const attachmentHoverStyle = useCallback(() => {
    const current = attachmentHoverPreview;
    if (!current) return null;
    const zoomRaw = Number(current.zoom);
    const zoom = Number.isFinite(zoomRaw) && zoomRaw > 0 ? zoomRaw : 1;
    const margin = 14;
    const viewportWidth = window.innerWidth || 1280;
    const viewportHeight = window.innerHeight || 800;
    const width = Math.min(
      Math.max(220, Math.round(Number(current.width || 0) * zoom)),
      Math.max(220, viewportWidth - margin * 2),
    );
    const maxHeight = Math.min(
      Math.max(180, Math.round(Number(current.maxHeight || 0) * zoom)),
      Math.max(180, viewportHeight - margin * 2),
    );
    let left = Number(current.left || margin);
    let top = Number(current.top || margin);
    if (left + width > viewportWidth - margin) {
      left = Math.max(margin, viewportWidth - margin - width);
    }
    if (top + maxHeight > viewportHeight - margin) {
      top = Math.max(margin, viewportHeight - margin - maxHeight);
    }
    return {
      left: `${Math.round(left)}px`,
      top: `${Math.round(top)}px`,
      width: `${width}px`,
      maxHeight: `${maxHeight}px`,
    };
  }, [attachmentHoverPreview]);

  const openAttachmentLightbox = useCallback((att) => {
    const src = attachmentPreview(att);
    if (!src) return;
    clearAttachmentHoverShowTimer();
    clearAttachmentHoverHideTimer();
    setAttachmentHoverPreview(null);
    setAttachmentLightbox({
      src,
      alt: attachmentLabel(att),
      path: (att?.path || att?.name || '').toString(),
    });
  }, [attachmentPreview, attachmentLabel, clearAttachmentHoverShowTimer, clearAttachmentHoverHideTimer]);

  const closeAttachmentLightbox = useCallback(() => {
    setAttachmentLightbox(null);
  }, []);

  const onAttachmentLightboxKeydown = useCallback((event) => {
    const targetEvent = event;
    if ((targetEvent?.key || '').toString() === 'Escape') {
      targetEvent.preventDefault?.();
      closeAttachmentLightbox();
    }
  }, [closeAttachmentLightbox]);

  useEffect(() => {
    return () => {
      clearAttachmentHoverHideTimer();
      clearAttachmentHoverShowTimer();
    };
  }, [clearAttachmentHoverHideTimer, clearAttachmentHoverShowTimer]);

  return {
    attachmentType,
    attachmentPreview,
    attachmentLabel,
    imageAttachments,
    fileAttachments,
    onAttachmentHoverMove,
    onAttachmentHoverLeave,
    onAttachmentPreviewEnter,
    onAttachmentPreviewLeave,
    onAttachmentPreviewZoomIn,
    onAttachmentPreviewZoomOut,
    onAttachmentPreviewResetZoom,
    attachmentCanZoomOut,
    attachmentHoverStyle,
    attachmentHoverPreview,
    attachmentLightbox,
    openAttachmentLightbox,
    closeAttachmentLightbox,
    onAttachmentLightboxKeydown,
  };
}

import { onBeforeUnmount, ref } from '../../../lib/vue.esm-browser.prod.js';

type AttachmentItem = {
  kind?: string;
  name?: string;
  path?: string;
  previewUrl?: string;
};

type HoverPreviewState = {
  src: string;
  alt: string;
  left: number;
  top: number;
  width: number;
  maxHeight: number;
  zoom: number;
} | null;

type LightboxState = {
  src: string;
  alt: string;
  path: string;
} | null;

type HoverPoint = { x: number; y: number };
type HoverPosition = { left: number; top: number; width: number; maxHeight: number };

function isWailsShimDebug(): boolean {
  if (typeof window === 'undefined') return false;
  return Boolean(Reflect.get(window as unknown as Record<string, unknown>, '__WAILS_SHIM_DEBUG__'));
}

export function useAttachmentPreviewState() {
  let attachmentHoverHideTimer = 0;
  let attachmentHoverShowTimer = 0;
  let attachmentHoverPendingPreview: Omit<NonNullable<HoverPreviewState>, 'zoom'> | null = null;
  const ATTACHMENT_HOVER_SHOW_DELAY_MS = 500;
  const ATTACHMENT_HOVER_HIDE_DELAY_MS = 800;
  const ATTACHMENT_HOVER_PREVIEW_LEAVE_DELAY_MS = 800;
  const ATTACHMENT_HOVER_ZOOM_MIN = 1;
  const ATTACHMENT_HOVER_ZOOM_STEP = 0.25;
  const ATTACHMENT_HOVER_ZOOM_MAX = 3;
  const attachmentHoverPreview: { value: HoverPreviewState } = ref(null);
  const attachmentLightbox: { value: LightboxState } = ref(null);

  function resolveAttachmentList(source: AttachmentItem[] | { attachments?: AttachmentItem[] } | null | undefined): AttachmentItem[] {
    if (Array.isArray(source)) return source;
    return Array.isArray(source?.attachments) ? source.attachments : [];
  }

  function attachmentType(att: AttachmentItem): string {
    return att?.kind === 'image' ? 'IMG' : 'FILE';
  }

  function attachmentPreview(att: AttachmentItem): string {
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
  }

  function attachmentLabel(att: AttachmentItem): string {
    return (att?.name || att?.path || (att?.kind === 'image' ? 'image attachment' : 'attachment')).toString();
  }

  function imageAttachments(source: AttachmentItem[] | { attachments?: AttachmentItem[] } | null | undefined): AttachmentItem[] {
    return resolveAttachmentList(source).filter((att) => Boolean(attachmentPreview(att)));
  }

  function fileAttachments(source: AttachmentItem[] | { attachments?: AttachmentItem[] } | null | undefined): AttachmentItem[] {
    return resolveAttachmentList(source).filter((att) => !attachmentPreview(att));
  }

  function attachmentHoverPoint(event: Event): HoverPoint {
    const targetEvent = event as MouseEvent;
    const x = Number(targetEvent?.clientX);
    const y = Number(targetEvent?.clientY);
    if (Number.isFinite(x) && Number.isFinite(y) && (x > 0 || y > 0)) {
      return { x, y };
    }
    const currentTarget = targetEvent?.currentTarget as HTMLElement | null;
    const rect = currentTarget?.getBoundingClientRect?.();
    if (rect) {
      return {
        x: rect.left + Math.min(rect.width, 32),
        y: rect.top + Math.min(rect.height, 32),
      };
    }
    return { x: 32, y: 32 };
  }

  function clearAttachmentHoverHideTimer(): void {
    if (!attachmentHoverHideTimer || typeof window === 'undefined') return;
    window.clearTimeout(attachmentHoverHideTimer);
    attachmentHoverHideTimer = 0;
  }

  function clearAttachmentHoverShowTimer(): void {
    if (attachmentHoverShowTimer && typeof window !== 'undefined') {
      window.clearTimeout(attachmentHoverShowTimer);
      attachmentHoverShowTimer = 0;
    }
    attachmentHoverPendingPreview = null;
  }

  function scheduleAttachmentHoverHide(delayMS = ATTACHMENT_HOVER_HIDE_DELAY_MS): void {
    clearAttachmentHoverHideTimer();
    if (typeof window === 'undefined') {
      attachmentHoverPreview.value = null;
      return;
    }
    const delay = Number(delayMS);
    attachmentHoverHideTimer = window.setTimeout(() => {
      attachmentHoverPreview.value = null;
      attachmentHoverHideTimer = 0;
    }, Number.isFinite(delay) ? Math.max(0, delay) : ATTACHMENT_HOVER_HIDE_DELAY_MS);
  }

  function applyAttachmentHoverPreview(nextPreview: Omit<NonNullable<HoverPreviewState>, 'zoom'>): void {
    const prev = attachmentHoverPreview.value;
    const prevZoomRaw = Number(prev?.zoom);
    const zoom = prev && prev.src === nextPreview.src && Number.isFinite(prevZoomRaw) && prevZoomRaw > 0
      ? prevZoomRaw
      : ATTACHMENT_HOVER_ZOOM_MIN;
    attachmentHoverPreview.value = {
      ...nextPreview,
      zoom,
    };
  }

  function scheduleAttachmentHoverShow(nextPreview: Omit<NonNullable<HoverPreviewState>, 'zoom'>): void {
    attachmentHoverPendingPreview = nextPreview;
    if (attachmentHoverShowTimer || typeof window === 'undefined') return;
    attachmentHoverShowTimer = window.setTimeout(() => {
      const pending = attachmentHoverPendingPreview;
      attachmentHoverShowTimer = 0;
      attachmentHoverPendingPreview = null;
      if (!pending) return;
      applyAttachmentHoverPreview(pending);
    }, ATTACHMENT_HOVER_SHOW_DELAY_MS);
  }

  function attachmentHoverPosition(pointX: number, pointY: number): HoverPosition {
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
  }

  function onAttachmentHoverMove(event: Event, att: AttachmentItem): void {
    const src = attachmentPreview(att);
    if (!src) {
      clearAttachmentHoverShowTimer();
      clearAttachmentHoverHideTimer();
      attachmentHoverPreview.value = null;
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
    const prev = attachmentHoverPreview.value;
    if (prev && prev.src === src) {
      clearAttachmentHoverShowTimer();
      applyAttachmentHoverPreview(nextPreview);
      return;
    }
    if (prev && prev.src !== src) {
      attachmentHoverPreview.value = null;
    }
    scheduleAttachmentHoverShow(nextPreview);
  }

  function onAttachmentHoverLeave(): void {
    clearAttachmentHoverShowTimer();
    scheduleAttachmentHoverHide();
  }

  function onAttachmentPreviewEnter(): void {
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
  }

  function onAttachmentPreviewLeave(): void {
    scheduleAttachmentHoverHide(ATTACHMENT_HOVER_PREVIEW_LEAVE_DELAY_MS);
  }

  function onAttachmentPreviewZoomIn(event: Event): void {
    const targetEvent = event as MouseEvent;
    targetEvent?.preventDefault?.();
    targetEvent?.stopPropagation?.();
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
    const current = attachmentHoverPreview.value;
    if (!current) return;
    const zoomRaw = Number(current.zoom);
    const zoom = Number.isFinite(zoomRaw) && zoomRaw > 0 ? zoomRaw : 1;
    const nextZoom = Math.min(
      ATTACHMENT_HOVER_ZOOM_MAX,
      Math.round((zoom + ATTACHMENT_HOVER_ZOOM_STEP) * 100) / 100,
    );
    attachmentHoverPreview.value = { ...current, zoom: nextZoom };
  }

  function onAttachmentPreviewZoomOut(event: Event): void {
    const targetEvent = event as MouseEvent;
    targetEvent?.preventDefault?.();
    targetEvent?.stopPropagation?.();
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
    const current = attachmentHoverPreview.value;
    if (!current) return;
    const zoomRaw = Number(current.zoom);
    const zoom = Number.isFinite(zoomRaw) && zoomRaw > 0 ? zoomRaw : ATTACHMENT_HOVER_ZOOM_MIN;
    const nextZoom = Math.max(
      ATTACHMENT_HOVER_ZOOM_MIN,
      Math.round((zoom - ATTACHMENT_HOVER_ZOOM_STEP) * 100) / 100,
    );
    attachmentHoverPreview.value = { ...current, zoom: nextZoom };
  }

  function onAttachmentPreviewResetZoom(event: Event): void {
    const targetEvent = event as MouseEvent;
    targetEvent?.preventDefault?.();
    targetEvent?.stopPropagation?.();
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
    const current = attachmentHoverPreview.value;
    if (!current) return;
    attachmentHoverPreview.value = { ...current, zoom: ATTACHMENT_HOVER_ZOOM_MIN };
  }

  function attachmentCanZoomOut(): boolean {
    const current = attachmentHoverPreview.value;
    if (!current) return false;
    const zoomRaw = Number(current.zoom);
    const zoom = Number.isFinite(zoomRaw) && zoomRaw > 0 ? zoomRaw : ATTACHMENT_HOVER_ZOOM_MIN;
    return zoom > ATTACHMENT_HOVER_ZOOM_MIN;
  }

  function attachmentHoverStyle(): Record<string, string> | null {
    const current = attachmentHoverPreview.value;
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
  }

  function openAttachmentLightbox(att: AttachmentItem): void {
    const src = attachmentPreview(att);
    if (!src) return;
    clearAttachmentHoverShowTimer();
    clearAttachmentHoverHideTimer();
    attachmentHoverPreview.value = null;
    attachmentLightbox.value = {
      src,
      alt: attachmentLabel(att),
      path: (att?.path || att?.name || '').toString(),
    };
  }

  function closeAttachmentLightbox(): void {
    attachmentLightbox.value = null;
  }

  function onAttachmentLightboxKeydown(event: Event): void {
    const targetEvent = event as KeyboardEvent;
    if ((targetEvent?.key || '').toString() === 'Escape') {
      targetEvent.preventDefault?.();
      closeAttachmentLightbox();
    }
  }

  onBeforeUnmount(() => {
    clearAttachmentHoverHideTimer();
    clearAttachmentHoverShowTimer();
    attachmentHoverPreview.value = null;
    attachmentLightbox.value = null;
  });

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

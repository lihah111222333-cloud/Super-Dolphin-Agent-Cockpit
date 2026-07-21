const OVERLAY_ROOT_ERROR = 'Required overlay-root host is unavailable or not unique.';

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

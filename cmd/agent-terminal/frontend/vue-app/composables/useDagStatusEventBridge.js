import { watch } from '../../lib/vue.esm-browser.prod.js';

export function requireDagStatusEventPayload(event) {
  if (!event) return null;
  if (!Object.prototype.hasOwnProperty.call(event, 'payload') || !event.payload || typeof event.payload !== 'object') {
    throw new Error('dag status event payload is required');
  }
  return event.payload;
}

export function useDagStatusEventBridge(props, dagDetail) {
  if (!dagDetail || typeof dagDetail.handleStatusEvent !== 'function') {
    throw new Error('dag status event handler is required');
  }
  watch(
    () => props.statusEvent,
    (event) => {
      const payload = requireDagStatusEventPayload(event);
      if (payload) dagDetail.handleStatusEvent(payload);
    },
    { flush: 'sync' },
  );
}

import { watch } from '../../lib/vue.esm-browser.prod.js';

export function requireDagNodeStatusPayload(payload, payloadMessage = 'dag status event payload is required') {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error(payloadMessage);
  const dagKey = (payload.dag_key || payload.dagKey || '').toString().trim();
  if (!dagKey) throw new Error('dag status event dag key is required');
  const nodeKey = (payload.node_key || payload.nodeKey || '').toString().trim();
  if (!nodeKey) throw new Error('dag status event node key is required');
  const runKey = (payload.run_key || payload.runKey || '').toString().trim();
  const runID = Number(payload.run_id ?? payload.runId ?? 0);
  if (!runKey && (!Number.isFinite(runID) || runID <= 0)) throw new Error('dag status event run identity is required');
  const status = (payload.new_status || payload.newStatus || payload.status || '').toString().trim();
  if (!status) throw new Error('dag status event status is required');
  return payload;
}

export function requireDagStatusEventPayload(event) {
  if (!event) throw new Error('dag status event payload is required');
  if (!Object.prototype.hasOwnProperty.call(event, 'payload') || !event.payload || typeof event.payload !== 'object') {
    throw new Error('dag status event payload is required');
  }
  return requireDagNodeStatusPayload(event.payload);
}

export function useDagStatusEventBridge(props, dagDetail) {
  if (!dagDetail || typeof dagDetail.handleStatusEvent !== 'function') {
    throw new Error('dag status event handler is required');
  }
  let processed = 0;
  watch(
    () => props.statusEvents,
    (events) => {
      const list = Array.isArray(events) ? events : [];
      if (list.length < processed) processed = 0;
      for (; processed < list.length; processed += 1) {
        dagDetail.handleStatusEvent(requireDagStatusEventPayload(list[processed]));
      }
    },
    { immediate: true, flush: 'sync' },
  );
}

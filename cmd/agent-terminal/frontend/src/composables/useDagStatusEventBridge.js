import { watch } from '../../lib/vue.esm-browser.prod.js';

function requiredPayloadString(payload, field, message) {
  if (!Object.prototype.hasOwnProperty.call(payload, field)) throw new Error(message);
  const raw = payload[field];
  if (raw === null || raw === undefined) throw new Error(message);
  const value = raw.toString().trim();
  if (!value) throw new Error(message);
  return value;
}

export function requireDagNodeStatusPayload(payload, payloadMessage = 'dag status event payload is required') {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error(payloadMessage);
  const dagKey = requiredPayloadString(payload, 'dag_key', 'dag status event dag key is required');
  const nodeKey = requiredPayloadString(payload, 'node_key', 'dag status event node key is required');
  const status = requiredPayloadString(payload, 'new_status', 'dag status event status is required');
  const runKey = Object.prototype.hasOwnProperty.call(payload, 'run_key') && payload.run_key !== null && payload.run_key !== undefined ? payload.run_key.toString().trim() : '';
  const runID = Object.prototype.hasOwnProperty.call(payload, 'run_id') ? Number(payload.run_id) : 0;
  if (!runKey && (!Number.isFinite(runID) || runID <= 0)) throw new Error('dag status event run identity is required');
  const normalized = { ...payload, dag_key: dagKey, node_key: nodeKey, new_status: status };
  if (runKey) normalized.run_key = runKey;
  if (Number.isFinite(runID) && runID > 0) normalized.run_id = runID;
  return normalized;
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

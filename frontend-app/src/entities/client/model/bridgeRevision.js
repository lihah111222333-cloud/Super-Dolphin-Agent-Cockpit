export const ACTIVE_PROMPT_PREF_KEY = 'settings.activePromptKey';

const PROMPT_REVISION_EVENTS = new Set(['prompts/changed', 'prompt-assets/changed', 'ui/prompts/changed']);
const BRIDGE_REVISION_EVENTS = Object.freeze([
  Object.freeze({ key: 'skillRevision', events: new Set(['skills/changed']) }),
  Object.freeze({ key: 'sharedFilesRevision', events: new Set(['ui/shared-files/changed', 'shared-files/changed', 'shared_file/changed']) }),
  Object.freeze({ key: 'memoryRevision', events: new Set(['ui/memory/changed', 'memory/changed']) }),
  Object.freeze({ key: 'workflowRevision', events: new Set(['task/node/statuschanged', 'cron/job/runstatechanged', 'task/dag/changed', 'dags/changed']) }),
]);

function normalizeString(value) {
  return (value || '').toString().trim();
}

function requiredDagStatusPayloadString(payload, field, message) {
  if (!Object.prototype.hasOwnProperty.call(payload, field)) throw new Error(message);
  const value = normalizeString(payload[field]);
  if (!value) throw new Error(message);
  return value;
}

export function requireDagNodeStatusPayload(payload) {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('dag status event payload is required');
  requiredDagStatusPayloadString(payload, 'dag_key', 'dag status event dag key is required');
  requiredDagStatusPayloadString(payload, 'node_key', 'dag status event node key is required');
  requiredDagStatusPayloadString(payload, 'new_status', 'dag status event status is required');
  const runKey = Object.prototype.hasOwnProperty.call(payload, 'run_key') ? normalizeString(payload.run_key) : '';
  const runID = Object.prototype.hasOwnProperty.call(payload, 'run_id') ? Number(payload.run_id) : 0;
  if (!runKey && (!Number.isFinite(runID) || runID <= 0)) throw new Error('dag status event run identity is required');
}

export function bridgeRevisionKey(eventName, payload = {}) {
  if (
    PROMPT_REVISION_EVENTS.has(eventName) ||
    (eventName === 'ui/preferences/changed' && normalizeString(payload.key) === ACTIVE_PROMPT_PREF_KEY)
  ) {
    return 'promptRevision';
  }
  if (eventName === 'task/node/statuschanged') requireDagNodeStatusPayload(payload);
  const match = BRIDGE_REVISION_EVENTS.find((entry) => entry.events.has(eventName));
  return match?.key || '';
}

export function isDagNodeStatusBridgeEvent(evt) {
  return normalizeString(evt?.method || evt?.type).toLowerCase() === 'task/node/statuschanged';
}

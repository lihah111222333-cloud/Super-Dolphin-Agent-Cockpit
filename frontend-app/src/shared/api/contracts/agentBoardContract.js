import { hasOwn } from '../backendResponseValidatorShared.js';

const AGENT_BOARD_STATUS_VALUES = new Set([
  'provisioning', 'idle', 'turn_queued', 'turn_starting', 'turn_running',
  'awaiting_user_input', 'recovering', 'stopping', 'stopped', 'failed',
]);
const AGENT_BOARD_OUTCOME_KINDS = new Set(['success', 'failure', 'stopped']);
const BOARD_VIEW_KEYS = new Set(['id', 'threadId', 'parentAgentId', 'name', 'assignment', 'progress', 'outcome']);
const ASSIGNMENT_KEYS = new Set(['title', 'description', 'assignedAt']);
const PROGRESS_KEYS = new Set(['status', 'currentStep', 'completedSteps', 'totalSteps', 'updatedAt']);
const OUTCOME_KEYS = new Set(['kind', 'summary', 'reason', 'code', 'recoverable', 'completedAt']);

/** @param {unknown} value @param {string} label @returns {Record<string, unknown>} */
function record(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${label} must be an object`);
  }
  return /** @type {Record<string, unknown>} */ (value);
}

/** @param {Record<string, unknown>} value @param {Set<string>} allowed @param {Set<string>} required @param {string} label */
function exactKeys(value, allowed, required, label) {
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) throw new TypeError(`${label}.${key} is not allowed`);
  }
  for (const key of required) {
    if (!hasOwn(value, key)) throw new TypeError(`${label}.${key} is required`);
  }
}

/** @param {unknown} value @param {string} label */
function nonBlank(value, label) {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new TypeError(`${label} must be a non-blank string`);
  }
  return value.trim();
}

/** @param {unknown} value @param {string} label */
function optionalString(value, label) {
  if (value === undefined) return '';
  if (typeof value !== 'string') throw new TypeError(`${label} must be a string`);
  return value;
}

/** @param {unknown} value @param {string} label */
function timestamp(value, label) {
  const text = nonBlank(value, label);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(text)) {
    throw new TypeError(`${label} must be an ISO timestamp`);
  }
  return text;
}

/** @param {unknown} value @param {string} label */
function normalizeAssignment(value, label) {
  const source = record(value, label);
  exactKeys(source, ASSIGNMENT_KEYS, ASSIGNMENT_KEYS, label);
  return {
    title: nonBlank(source.title, `${label}.title`),
    description: nonBlank(source.description, `${label}.description`),
    assignedAt: timestamp(source.assignedAt, `${label}.assignedAt`),
  };
}

/** @param {unknown} value @param {string} label */
function nullableStepCount(value, label) {
  if (value === null) return null;
  if (typeof value !== 'number' || !Number.isInteger(value) || value < 0) {
    throw new TypeError(`${label} must be null or a non-negative integer`);
  }
  return value;
}

/** @param {unknown} value @param {string} label */
function normalizeProgress(value, label) {
  const source = record(value, label);
  exactKeys(source, PROGRESS_KEYS, PROGRESS_KEYS, label);
  const status = nonBlank(source.status, `${label}.status`);
  if (!AGENT_BOARD_STATUS_VALUES.has(status)) throw new TypeError(`${label}.status is invalid`);
  if (source.currentStep !== null && (typeof source.currentStep !== 'string' || source.currentStep.trim() === '')) {
    throw new TypeError(`${label}.currentStep must be null or a non-blank string`);
  }
  const completedSteps = nullableStepCount(source.completedSteps, `${label}.completedSteps`);
  const totalSteps = nullableStepCount(source.totalSteps, `${label}.totalSteps`);
  if ((completedSteps === null) !== (totalSteps === null)) {
    throw new TypeError(`${label} step counts must be provided together`);
  }
  if (completedSteps !== null && totalSteps !== null && completedSteps > totalSteps) {
    throw new TypeError(`${label}.completedSteps must not exceed totalSteps`);
  }
  return {
    status,
    currentStep: source.currentStep,
    completedSteps,
    totalSteps,
    updatedAt: timestamp(source.updatedAt, `${label}.updatedAt`),
  };
}

/** @param {unknown} value @param {string} label */
function normalizeOutcome(value, label) {
  if (value === null) return null;
  const source = record(value, label);
  exactKeys(source, OUTCOME_KEYS, new Set(['kind', 'recoverable', 'completedAt']), label);
  const kind = nonBlank(source.kind, `${label}.kind`);
  if (!AGENT_BOARD_OUTCOME_KINDS.has(kind)) throw new TypeError(`${label}.kind is invalid`);
  const summary = optionalString(source.summary, `${label}.summary`);
  const reason = optionalString(source.reason, `${label}.reason`);
  const code = optionalString(source.code, `${label}.code`);
  if (kind === 'success' && summary.trim() === '') throw new TypeError(`${label}.summary is required for success`);
  if (kind !== 'success' && reason.trim() === '') throw new TypeError(`${label}.reason is required for ${kind}`);
  if (source.recoverable !== null && typeof source.recoverable !== 'boolean') {
    throw new TypeError(`${label}.recoverable must be boolean or null`);
  }
  return {
    kind,
    summary,
    reason,
    code,
    recoverable: source.recoverable,
    completedAt: timestamp(source.completedAt, `${label}.completedAt`),
  };
}

/** @param {unknown} value @param {string} [label] */
function normalizeBoardView(value, label = 'agent') {
  const source = record(value, label);
  exactKeys(source, BOARD_VIEW_KEYS, new Set(['id', 'threadId', 'name', 'assignment', 'progress', 'outcome']), label);
  const parentAgentId = source.parentAgentId;
  if (hasOwn(source, 'parentAgentId') && typeof parentAgentId !== 'string') {
    throw new TypeError(`${label}.parentAgentId must be a string`);
  }
  return {
    id: nonBlank(source.id, `${label}.id`),
    threadId: nonBlank(source.threadId, `${label}.threadId`),
    parentAgentId: typeof parentAgentId === 'string' ? parentAgentId.trim() : '',
    name: nonBlank(source.name, `${label}.name`),
    assignment: normalizeAssignment(source.assignment, `${label}.assignment`),
    progress: normalizeProgress(source.progress, `${label}.progress`),
    outcome: normalizeOutcome(source.outcome, `${label}.outcome`),
  };
}

/** @param {unknown} value @param {string} [label] */
function normalizeSnapshotAgent(value, label = 'agent') {
  const source = record(value, label);
  return normalizeBoardView({
    id: source.id,
    threadId: source.thread_id,
    ...(hasOwn(source, 'parentAgentId') ? { parentAgentId: source.parentAgentId } : {}),
    name: source.name,
    assignment: source.assignment,
    progress: source.progress,
    outcome: source.outcome,
  }, label);
}

export {
  AGENT_BOARD_STATUS_VALUES,
  normalizeBoardView,
  normalizeSnapshotAgent,
};

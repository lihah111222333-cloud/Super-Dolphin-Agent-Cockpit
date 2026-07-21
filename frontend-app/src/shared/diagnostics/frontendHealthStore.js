// @ts-check

import { requiredAppStoragePort } from '../api/browser/browserStorage.js';
import { parse as parseLosslessJSON } from 'lossless-json';

const FRONTEND_HEALTH_STORAGE_KEY = 'super-dolphin.frontend-health.v1';
const FRONTEND_HEALTH_LIMIT = 100;
const HEALTH_RECORD_KEYS = Object.freeze([
  'actionId', 'code', 'diagnosticId', 'firstOccurredAt', 'lastOccurredAt', 'message', 'occurrences', 'title',
]);
const HEALTH_TIME_FORMATTER = new Intl.DateTimeFormat('sv-SE', {
  day: '2-digit', hour: '2-digit', hour12: false, minute: '2-digit', month: '2-digit', second: '2-digit', year: 'numeric',
});

/**
 * @typedef {{
 *   actionId: string,
 *   code: string,
 *   diagnosticId: string,
 *   firstOccurredAt: string,
 *   lastOccurredAt: string,
 *   message: string,
 *   occurrences: number,
 *   title: string,
 * }} FrontendHealthRecord
 * @typedef {{ code: string, diagnosticId: string, message: string, title: string }} SafePublicError
 * @typedef {{ actionId: string, publicError: SafePublicError }} FrontendHealthFailure
 * @typedef {{ status: 'available' } | ({ status: 'failed' } & SafePublicError)} FrontendHealthPersistence
 * @typedef {{ records: readonly FrontendHealthRecord[], persistence: FrontendHealthPersistence }} FrontendHealthState
 * @typedef {{ get: (key: string) => string | null, set: (key: string, value: string) => unknown, remove: (key: string) => unknown }} HealthStoragePort
 */

/** @returns {string} */
function currentHealthTimestamp() {
  return HEALTH_TIME_FORMATTER.format().replace(' ', 'T');
}

/** @param {unknown} value @returns {value is Record<string, unknown>} */
function plainObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

/** @param {unknown} record @returns {FrontendHealthRecord} */
function assertSafeHealthRecord(record) {
  if (!plainObject(record)) throw new TypeError('frontend health record must be an object');
  const byName = (/** @type {string} */ left, /** @type {string} */ right) => left.localeCompare(right);
  const keys = Object.keys(record).sort(byName);
  if (keys.join('\n') !== [...HEALTH_RECORD_KEYS].sort(byName).join('\n')) {
    throw new TypeError('frontend health record fields are invalid');
  }
  for (const key of ['actionId', 'code', 'diagnosticId', 'firstOccurredAt', 'lastOccurredAt', 'message', 'title']) {
    if (typeof record[key] !== 'string' || !record[key].trim()) throw new TypeError(`frontend health ${key} is required`);
  }
  if (!Number.isInteger(record.occurrences) || /** @type {number} */ (record.occurrences) < 1) {
    throw new TypeError('frontend health occurrences must be a positive integer');
  }
  return /** @type {FrontendHealthRecord} */ (record);
}

/** @param {string | null} raw @returns {FrontendHealthRecord[]} */
function parseStoredRecords(raw) {
  if (raw === null) return [];
  const parsed = parseLosslessJSON(raw, null, { parseNumber: Number });
  if (!Array.isArray(parsed)) throw new TypeError('frontend health storage must be an array');
  if (parsed.length > FRONTEND_HEALTH_LIMIT) throw new TypeError('frontend health storage exceeds its limit');
  return parsed.map((record) => ({ ...assertSafeHealthRecord(record) }));
}

/** @param {FrontendHealthRecord} record @returns {string} */
export function frontendHealthIdentity(record) {
  return `${record.actionId}\n${record.code}\n${record.message}`;
}

/** @param {FrontendHealthRecord[]} records @param {FrontendHealthRecord} incoming @returns {FrontendHealthRecord[]} */
function mergedHealthRecords(records, incoming) {
  const identity = frontendHealthIdentity(incoming);
  const index = records.findIndex((record) => frontendHealthIdentity(record) === identity);
  if (index < 0) return [incoming, ...records].slice(0, FRONTEND_HEALTH_LIMIT);
  const previous = records[index];
  const merged = {
    ...previous,
    diagnosticId: incoming.diagnosticId,
    lastOccurredAt: incoming.lastOccurredAt,
    occurrences: previous.occurrences + 1,
  };
  return [merged, ...records.slice(0, index), ...records.slice(index + 1)].slice(0, FRONTEND_HEALTH_LIMIT);
}

let internalDiagnosticSequence = 0;
/** @returns {string} */
function internalHealthDiagnosticId() {
  internalDiagnosticSequence += 1;
  return `frontend-health-${internalDiagnosticSequence}`;
}

/** @returns {SafePublicError} */
function persistencePublicError() {
  const diagnosticId = internalHealthDiagnosticId();
  return Object.freeze({
    code: 'HEALTH_PERSISTENCE_FAILED',
    title: 'Health 持久化异常',
    message: 'Health 记录当前只能保留在本次运行中，请复制诊断 ID 后重试。',
    diagnosticId,
  });
}

/** @param {HealthStoragePort} storage @param {FrontendHealthRecord[]} records @returns {FrontendHealthPersistence} */
function persistHealthRecords(storage, records) {
  try {
    storage.set(FRONTEND_HEALTH_STORAGE_KEY, JSON.stringify(records));
    return { status: 'available' };
  } catch {
    return { status: 'failed', ...persistencePublicError() };
  }
}

/** @param {HealthStoragePort} storage @returns {FrontendHealthPersistence} */
function clearPersistedHealth(storage) {
  try {
    storage.remove(FRONTEND_HEALTH_STORAGE_KEY);
    return { status: 'available' };
  } catch {
    return { status: 'failed', ...persistencePublicError() };
  }
}

/** @returns {HealthStoragePort} */
function unavailableHealthStorage() {
  const fail = () => { throw new Error('frontend health storage is unavailable'); };
  return { get: fail, set: fail, remove: fail };
}

/**
 * @param {{ now?: () => string, storage: HealthStoragePort }} options
 */
export function createFrontendHealthStore({ now = currentHealthTimestamp, storage }) {
  if (!storage || typeof storage.get !== 'function' || typeof storage.set !== 'function' || typeof storage.remove !== 'function') {
    throw new TypeError('frontend health storage port is required');
  }
  /** @type {FrontendHealthRecord[]} */
  let records = [];
  /** @type {FrontendHealthPersistence} */
  let persistence = { status: 'available' };
  try {
    records = parseStoredRecords(storage.get(FRONTEND_HEALTH_STORAGE_KEY));
  } catch {
    persistence = { status: 'failed', ...persistencePublicError() };
  }
  /** @type {Set<() => void>} */
  const listeners = new Set();
  const emit = () => listeners.forEach((listener) => listener());
  /** @returns {FrontendHealthState} */
  const getState = () => Object.freeze({ records: Object.freeze([...records]), persistence });
  /** @param {FrontendHealthFailure} failure @param {boolean} persist */
  const recordInternal = ({ actionId, publicError }, persist) => {
    const occurredAt = now();
    const incoming = assertSafeHealthRecord({
      actionId,
      code: publicError.code,
      diagnosticId: publicError.diagnosticId,
      firstOccurredAt: occurredAt,
      lastOccurredAt: occurredAt,
      message: publicError.message,
      occurrences: 1,
      title: publicError.title,
    });
    records = mergedHealthRecords(records, incoming);
    if (persist && persistence.status === 'available') persistence = persistHealthRecords(storage, records);
    emit();
    return Object.freeze({ record: incoming, persisted: persistence.status === 'available' && persist });
  };
  return Object.freeze({
    clear() {
      records = [];
      persistence = clearPersistedHealth(storage);
      emit();
      return Object.freeze({ persisted: persistence.status === 'available' });
    },
    getSnapshot: () => [...records],
    getState,
    /** @param {FrontendHealthFailure} failure */
    record(failure) {
      return recordInternal(failure, true);
    },
    /** @param {FrontendHealthFailure} failure */
    recordInMemory(failure) {
      return recordInternal(failure, false);
    },
    /** @param {() => void} listener */
    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('frontend health listener is required');
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  });
}

/** @type {ReturnType<typeof createFrontendHealthStore> | undefined} */
let persistentStore;
function defaultPersistentStore() {
  if (!persistentStore) {
    let storage;
    try {
      storage = requiredAppStoragePort('frontend health storage');
    } catch {
      storage = unavailableHealthStorage();
    }
    persistentStore = createFrontendHealthStore({ storage });
  }
  return persistentStore;
}

/** @param {FrontendHealthFailure} failure */
export function recordFrontendHealth(failure) {
  return defaultPersistentStore().record(failure);
}

/**
 * This is the single finite terminal state for a reporting sink failure. It is
 * deliberately memory-only because retrying the failed persistence path here
 * would recurse.
 * @param {FrontendHealthFailure} failure
 */
export function recordFrontendHealthFailureState(failure) {
  return defaultPersistentStore().recordInMemory(failure);
}

/** @returns {FrontendHealthRecord[]} */
export function frontendHealthSnapshot() {
  return defaultPersistentStore().getSnapshot();
}

/** @returns {FrontendHealthState} */
export function frontendHealthStateSnapshot() {
  return defaultPersistentStore().getState();
}

/** @param {() => void} listener */
export function subscribeFrontendHealth(listener) {
  return defaultPersistentStore().subscribe(listener);
}

export function clearFrontendHealth() {
  return defaultPersistentStore().clear();
}

export function resetFrontendHealthForTest() {
  persistentStore = undefined;
  internalDiagnosticSequence = 0;
}

export { FRONTEND_HEALTH_STORAGE_KEY, FRONTEND_HEALTH_LIMIT };

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

function currentHealthTimestamp() {
  return HEALTH_TIME_FORMATTER.format().replace(' ', 'T');
}

function plainObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function assertSafeHealthRecord(record) {
  if (!plainObject(record)) throw new TypeError('frontend health record must be an object');
  const byName = (left, right) => left.localeCompare(right);
  const keys = Object.keys(record).sort(byName);
  if (keys.join('\n') !== [...HEALTH_RECORD_KEYS].sort(byName).join('\n')) {
    throw new TypeError('frontend health record fields are invalid');
  }
  for (const key of ['actionId', 'code', 'diagnosticId', 'firstOccurredAt', 'lastOccurredAt', 'message', 'title']) {
    if (typeof record[key] !== 'string' || !record[key].trim()) throw new TypeError(`frontend health ${key} is required`);
  }
  if (!Number.isInteger(record.occurrences) || record.occurrences < 1) {
    throw new TypeError('frontend health occurrences must be a positive integer');
  }
  return record;
}

function parseStoredRecords(raw) {
  if (raw === null) return [];
  const parsed = parseLosslessJSON(raw, null, { parseNumber: Number });
  if (!Array.isArray(parsed)) throw new TypeError('frontend health storage must be an array');
  if (parsed.length > FRONTEND_HEALTH_LIMIT) throw new TypeError('frontend health storage exceeds its limit');
  return parsed.map((record) => ({ ...assertSafeHealthRecord(record) }));
}

function healthSignature(record) {
  return `${record.actionId}\n${record.code}\n${record.message}`;
}

function mergedHealthRecords(records, incoming) {
  const signature = healthSignature(incoming);
  const index = records.findIndex((record) => healthSignature(record) === signature);
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

export function createFrontendHealthStore({ now = currentHealthTimestamp, storage }) {
  if (!storage || typeof storage.get !== 'function' || typeof storage.set !== 'function' || typeof storage.remove !== 'function') {
    throw new TypeError('frontend health storage port is required');
  }
  let records = parseStoredRecords(storage.get(FRONTEND_HEALTH_STORAGE_KEY));
  const listeners = new Set();
  const emit = () => listeners.forEach((listener) => listener());
  const persist = () => storage.set(FRONTEND_HEALTH_STORAGE_KEY, JSON.stringify(records));
  return Object.freeze({
    clear() {
      records = [];
      storage.remove(FRONTEND_HEALTH_STORAGE_KEY);
      emit();
    },
    getSnapshot: () => records,
    record({ actionId, publicError }) {
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
      emit();
      persist();
      return incoming;
    },
    subscribe(listener) {
      if (typeof listener !== 'function') throw new TypeError('frontend health listener is required');
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  });
}

let persistentStore;
let persistentStoreFailureDiagnosticId = '';
const emergencyStorage = new Map();
const diagnosticCauses = new Map();
let lastResortRecords = [];
const lastResortListeners = new Set();
const emitLastResort = () => lastResortListeners.forEach((listener) => listener());
const emergencyStore = createFrontendHealthStore({
  storage: {
    get: (key) => emergencyStorage.get(key) ?? null,
    set: (key, value) => emergencyStorage.set(key, value),
    remove: (key) => emergencyStorage.delete(key),
  },
});

function defaultPersistentStore() {
  if (!persistentStore) {
    persistentStore = createFrontendHealthStore({ storage: requiredAppStoragePort('frontend health storage') });
  }
  return persistentStore;
}

function recordPersistentStoreFailure(error) {
  if (persistentStoreFailureDiagnosticId) return;
  if (typeof globalThis.crypto?.randomUUID !== 'function') throw error;
  const diagnosticId = globalThis.crypto.randomUUID();
  persistentStoreFailureDiagnosticId = diagnosticId;
  retainDiagnosticCause(diagnosticId, error);
  emergencyStore.record({
    actionId: 'frontend-health.persistence',
    publicError: {
      code: 'HEALTH_PERSISTENCE_FAILED',
      title: 'Health 持久化异常',
      message: 'Health 持久记录不可用，本次运行中的诊断仍可查看。',
      diagnosticId,
    },
  });
}

export function recordFrontendHealth(failure) {
  return defaultPersistentStore().record(failure);
}

export function recordEmergencyFrontendHealth(failure) {
  return emergencyStore.record(failure);
}

export function recordLastResortFrontendHealth({ actionId, publicError }) {
  try {
    let occurredAt = 'time-unavailable';
    try {
      occurredAt = currentHealthTimestamp();
    } catch {
      // The explicit marker keeps the final in-memory record finite when time formatting itself is unavailable.
    }
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
    lastResortRecords = mergedHealthRecords(lastResortRecords, incoming);
    emitLastResort();
    return incoming;
  } catch {
    return undefined;
  }
}

export function frontendHealthSnapshot() {
  let persisted = [];
  try {
    persisted = defaultPersistentStore().getSnapshot();
  } catch (error) {
    recordPersistentStoreFailure(error);
  }
  return [...lastResortRecords, ...emergencyStore.getSnapshot(), ...persisted];
}

export function subscribeFrontendHealth(listener) {
  let unsubscribePersistent = () => undefined;
  try {
    unsubscribePersistent = defaultPersistentStore().subscribe(listener);
  } catch (error) {
    recordPersistentStoreFailure(error);
  }
  const unsubscribeEmergency = emergencyStore.subscribe(listener);
  lastResortListeners.add(listener);
  return () => {
    unsubscribePersistent();
    unsubscribeEmergency();
    lastResortListeners.delete(listener);
  };
}

export function clearFrontendHealth() {
  lastResortRecords = [];
  emitLastResort();
  emergencyStore.clear();
  try {
    defaultPersistentStore().clear();
    persistentStoreFailureDiagnosticId = '';
  } catch (error) {
    recordPersistentStoreFailure(error);
  }
}

export function retainDiagnosticCause(diagnosticId, cause) {
  diagnosticCauses.set(diagnosticId, cause);
}

export function diagnosticCauseForTest(diagnosticId) {
  return diagnosticCauses.get(diagnosticId);
}

export function resetFrontendHealthForTest() {
  persistentStore = undefined;
  persistentStoreFailureDiagnosticId = '';
  emergencyStore.clear();
  emergencyStorage.clear();
  diagnosticCauses.clear();
  lastResortRecords = [];
  lastResortListeners.clear();
}

export { FRONTEND_HEALTH_STORAGE_KEY, FRONTEND_HEALTH_LIMIT };

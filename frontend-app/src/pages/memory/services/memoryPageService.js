import * as memoryService from '../../../services/modules/memoryService.js';

const MEMORY_TARGETS = new Set(['private', 'team']);

function assertPlainObject(value, message) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(message);
  }
  return value;
}

function hasOwn(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function normalizeRequiredString(value, field) {
  if (typeof value !== 'string') throw new Error(`${field} is required`);
  const normalized = value.trim();
  if (!normalized) throw new Error(`${field} is required`);
  return normalized;
}

function normalizeTarget(value, field = 'target') {
  const target = normalizeRequiredString(value, field);
  if (!MEMORY_TARGETS.has(target)) throw new Error(`${field} must be private or team`);
  return target;
}

function memoryEntryPayload(params) {
  const payload = assertPlainObject(params, 'memory entry params are required');
  return {
    ...payload,
    cwd: normalizeRequiredString(payload.cwd, 'cwd'),
    target: normalizeTarget(payload.target),
    path: normalizeRequiredString(payload.path, 'path'),
  };
}

function memoryUpsertPayload(params) {
  const payload = assertPlainObject(params, 'memory upsert params are required');
  if (!hasOwn(payload, 'existingPath')) throw new Error('path is required');
  return {
    ...payload,
    cwd: normalizeRequiredString(payload.cwd, 'cwd'),
    target: normalizeTarget(payload.target),
    existingPath: typeof payload.existingPath === 'string' ? payload.existingPath.trim() : '',
    name: normalizeRequiredString(payload.name, 'name'),
    description: normalizeRequiredString(payload.description, 'description'),
    type: normalizeRequiredString(payload.type, 'type'),
    content: normalizeRequiredString(payload.content, 'content'),
  };
}

function autoDreamIntentPayload(params) {
  const payload = assertPlainObject(params, 'memory auto dream intent params are required');
  if (typeof payload.enabled !== 'boolean') throw new Error('enabled is required');
  return {
    ...payload,
    cwd: normalizeRequiredString(payload.cwd, 'cwd'),
  };
}

function memoryPairPayload(params) {
  const payload = assertPlainObject(params, 'memory similarity params are required');
  const normalized = {
    ...payload,
    cwd: normalizeRequiredString(payload.cwd, 'cwd'),
    targetA: normalizeTarget(payload.targetA, 'targetA'),
    pathA: normalizeRequiredString(payload.pathA, 'pathA'),
    targetB: normalizeTarget(payload.targetB, 'targetB'),
    pathB: normalizeRequiredString(payload.pathB, 'pathB'),
  };
  if (normalized.targetA === normalized.targetB && normalized.pathA === normalized.pathB) {
    throw new Error('source and target memory identity must be different');
  }
  return normalized;
}

function consolidationStatusPayload(params) {
  const payload = assertPlainObject(params, 'memory consolidation status params are required');
  return {
    ...payload,
    cwd: normalizeRequiredString(payload.cwd, 'cwd'),
    jobId: normalizeRequiredString(payload.jobId, 'jobId'),
  };
}

function consolidationStartPayload(params) {
  const payload = assertPlainObject(params, 'memory consolidation params are required');
  return {
    ...payload,
    cwd: normalizeRequiredString(payload.cwd, 'cwd'),
  };
}

function createMemoryPageService(api = memoryService) {
  const service = {
    fetchMemoryDashboard(cwd, options) {
      return api.fetchMemoryDashboard(normalizeRequiredString(cwd, 'cwd'), options);
    },
    loadDashboard(cwd, options) {
      return service.fetchMemoryDashboard(cwd, options);
    },
    getMemoryEntry(params) {
      return api.getMemoryEntry(memoryEntryPayload(params));
    },
    upsertMemoryEntry(params) {
      return api.upsertMemoryEntry(memoryUpsertPayload(params));
    },
    deleteMemoryEntry(params) {
      return api.deleteMemoryEntry(memoryEntryPayload(params));
    },
    setMemoryAutoDreamIntent(params) {
      return api.setMemoryAutoDreamIntent(autoDreamIntentPayload(params));
    },
    mergeMemoryEntries(params) {
      return api.mergeMemoryEntries(memoryPairPayload(params));
    },
    ignoreMemorySimilarity(params) {
      return api.ignoreMemorySimilarity(memoryPairPayload(params));
    },
    startConsolidateMemorySimilarities(params) {
      return api.startConsolidateMemorySimilarities(consolidationStartPayload(params));
    },
    getMemoryConsolidationStatus(params) {
      return api.getMemoryConsolidationStatus(consolidationStatusPayload(params));
    },
  };

  service.fetchMemoryDashboard.withSignal = (cwd, signal) => service.fetchMemoryDashboard(cwd, { signal });
  return service;
}

const memoryPageService = createMemoryPageService();

const {
  deleteMemoryEntry,
  fetchMemoryDashboard,
  getMemoryConsolidationStatus,
  getMemoryEntry,
  ignoreMemorySimilarity,
  mergeMemoryEntries,
  setMemoryAutoDreamIntent,
  startConsolidateMemorySimilarities,
  upsertMemoryEntry,
} = memoryPageService;

export {
  createMemoryPageService,
  deleteMemoryEntry,
  fetchMemoryDashboard,
  getMemoryConsolidationStatus,
  getMemoryEntry,
  ignoreMemorySimilarity,
  memoryPageService,
  mergeMemoryEntries,
  setMemoryAutoDreamIntent,
  startConsolidateMemorySimilarities,
  upsertMemoryEntry,
};

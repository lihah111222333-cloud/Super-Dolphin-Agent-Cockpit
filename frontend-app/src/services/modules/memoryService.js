import {
  deleteMemoryEntry as deleteMemoryEntryBackend,
  getMemoryConsolidationStatus as getMemoryConsolidationStatusBackend,
  getMemoryEntry as getMemoryEntryBackend,
  getMemorySnapshot as getMemorySnapshotBackend,
  ignoreMemorySimilarity as ignoreMemorySimilarityBackend,
  mergeMemoryEntries as mergeMemoryEntriesBackend,
  setMemoryAutoDreamIntent as setMemoryAutoDreamIntentBackend,
  startConsolidateMemorySimilarities as startConsolidateMemorySimilaritiesBackend,
  upsertMemoryEntry as upsertMemoryEntryBackend,
} from '../../shared/api/backendApi.js';
import { normalizeMemorySnapshot } from '../../adapters/memoryAdapter.js';
import { DEFAULT_REQUEST_TIMEOUT_MS, runServiceRequest, withRequestTimeout } from '../apiClient.js';

async function fetchMemoryDashboard(cwd) {
  return runServiceRequest(async () => {
    const response = await withRequestTimeout(
      getMemorySnapshotBackend({ cwd }),
      DEFAULT_REQUEST_TIMEOUT_MS,
      '记忆中心加载超时，请检查记忆数据或后端状态。',
    );
    return normalizeMemorySnapshot(response);
  }, '加载记忆中心失败');
}

async function getMemoryEntry(params) {
  return runServiceRequest(() => getMemoryEntryBackend(params), '加载记忆失败');
}

async function upsertMemoryEntry(params) {
  return runServiceRequest(() => upsertMemoryEntryBackend(params), '保存记忆失败');
}

async function deleteMemoryEntry(params) {
  return runServiceRequest(() => deleteMemoryEntryBackend(params), '删除记忆失败');
}

async function setMemoryAutoDreamIntent(params) {
  return runServiceRequest(() => setMemoryAutoDreamIntentBackend(params), '切换自动沉淀失败');
}

async function mergeMemoryEntries(params) {
  return runServiceRequest(() => mergeMemoryEntriesBackend(params), '整合记忆失败');
}

async function ignoreMemorySimilarity(params) {
  return runServiceRequest(() => ignoreMemorySimilarityBackend(params), '忽略相似记忆失败');
}

async function startConsolidateMemorySimilarities(params) {
  return runServiceRequest(() => startConsolidateMemorySimilaritiesBackend(params), '启动智能整合失败');
}

async function getMemoryConsolidationStatus(params) {
  return runServiceRequest(() => getMemoryConsolidationStatusBackend(params), '查询智能整合状态失败');
}

export {
  deleteMemoryEntry,
  fetchMemoryDashboard,
  getMemoryConsolidationStatus,
  getMemoryEntry,
  ignoreMemorySimilarity,
  mergeMemoryEntries,
  setMemoryAutoDreamIntent,
  startConsolidateMemorySimilarities,
  upsertMemoryEntry,
};

import { afterEach, expect, it, vi } from 'vitest';
import { fetchMemoryDashboard } from './memoryService.js';
import { getMemorySnapshot as getMemorySnapshotBackend } from '../../shared/api/backendApi.js';

vi.mock('../../shared/api/backendApi.js', () => ({
  deleteMemoryEntry: vi.fn(),
  getMemoryConsolidationStatus: vi.fn(),
  getMemoryEntry: vi.fn(),
  getMemorySnapshot: vi.fn(),
  ignoreMemorySimilarity: vi.fn(),
  mergeMemoryEntries: vi.fn(),
  setMemoryAutoDreamIntent: vi.fn(),
  startConsolidateMemorySimilarities: vi.fn(),
  upsertMemoryEntry: vi.fn(),
}));

function memoryDashboardResponse(overrides = {}) {
  // 与真实 facade（backendApi.getMemorySnapshot → validateMemorySnapshotResponse）的输出一致：
  // schema parse + transform 之后的扁平形态 { overview, entries }，不再是原始 wire 的 private/team 结构。
  return {
    overview: {
      scan: overrides.scan || {},
    },
    entries: [],
  };
}

afterEach(() => {
  vi.clearAllMocks();
});

it('rejects canceled dashboard loads before starting the backend scan', async () => {
  getMemorySnapshotBackend.mockResolvedValue(memoryDashboardResponse());
  const controller = new AbortController();
  controller.abort();

  await expect(fetchMemoryDashboard('/repo/app', { signal: controller.signal })).rejects.toMatchObject({
    code: 'memory_scan_canceled',
  });
  expect(getMemorySnapshotBackend).not.toHaveBeenCalled();
});

it('uses AbortSignal and preserves memory scan truncation metadata', async () => {
  const controller = new AbortController();
  getMemorySnapshotBackend.mockResolvedValue(memoryDashboardResponse({
    scan: {
      reason: 'memory_scan_truncated',
      truncated: true,
    },
  }));

  await expect(fetchMemoryDashboard.withSignal('/repo/app', controller.signal)).resolves.toMatchObject({
    overview: {
      scan: {
        reason: 'memory_scan_truncated',
        truncated: true,
      },
    },
    entries: [],
  });
  expect(getMemorySnapshotBackend).toHaveBeenCalledWith({ cwd: '/repo/app' });
});

it('returns the facade-normalized snapshot by reference without a second parse', async () => {
  // facade（backendApi.getMemorySnapshot）已完成 validateMemorySnapshotResponse 的
  // parse + transform；service 层必须原样透传（引用相等证明没有二次 normalize）。
  const facadeSnapshot = memoryDashboardResponse();
  getMemorySnapshotBackend.mockResolvedValue(facadeSnapshot);

  await expect(fetchMemoryDashboard('/repo/app')).resolves.toBe(facadeSnapshot);
});

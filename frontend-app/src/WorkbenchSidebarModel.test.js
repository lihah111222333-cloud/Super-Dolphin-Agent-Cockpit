import { describe, expect, it } from 'vitest';
import { sidebarSnapshotThreads } from './WorkbenchSidebarModel.js';

describe('WorkbenchSidebarModel thread timestamps', () => {
  it('maps persisted numeric milliseconds to ISO without decimal stringification', () => {
    const [thread, stringifiedThread] = sidebarSnapshotThreads({
      threads: [
        { id: 'thread-1', cwd: '/repo/app', updated_at: 1784719357000 },
        { id: 'thread-2', cwd: '/repo/app', updatedAt: '1784719357000' },
      ],
    });

    expect(thread.updatedAt).toBe('2026-07-22T11:22:37.000Z');
    expect(stringifiedThread.updatedAt).toBe('2026-07-22T11:22:37.000Z');
  });

  it('preserves an ISO timestamp from a canonical sidebar source', () => {
    const [thread] = sidebarSnapshotThreads({
      threads: [{ id: 'thread-1', updatedAt: '2026-07-22T11:22:37.000Z' }],
    });

    expect(thread.updatedAt).toBe('2026-07-22T11:22:37.000Z');
  });

  it('rejects persisted seconds and invalid timestamp text', () => {
    expect(() => sidebarSnapshotThreads({ threads: [{ id: 'seconds', updated_at: 1784719357 }] }))
      .toThrow('sidebar thread updatedAt 必须是毫秒时间戳：1784719357');
    expect(() => sidebarSnapshotThreads({ threads: [{ id: 'invalid', updated_at: 'broken' }] }))
      .toThrow('sidebar thread updatedAt 时间戳无效：broken');
  });
});

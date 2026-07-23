import { describe, expect, it } from 'vitest';
import { normalizeMemorySnapshot } from './memoryAdapter.js';
import { parseMemorySnapshotResponse } from '../shared/api/backendSchemas.js';

const memoryOverview = { writeAvailable: true, health: { similarGroups: [] } };
const privateMemoryEntry = { path: 'u.md', type: 'user', name: 'User Memory', updated_at: '2026-07-08' };
const teamMemoryEntry = { path: 'p.md', type: 'project', title: 'Project Memory' };

function memorySnapshot(privateEntries, teamEntries) {
  return {
    overview: memoryOverview,
    private: { entries: privateEntries },
    team: { entries: teamEntries },
  };
}

describe('memoryAdapter', () => {
  it('normalizes memory snapshot entries at the schema boundary', () => {
    const result = parseMemorySnapshotResponse(memorySnapshot([privateMemoryEntry], [teamMemoryEntry]));

    expect(result).toEqual({
      overview: memoryOverview,
      entries: [
        expect.objectContaining({
          target: 'private',
          path: 'u.md',
          category: 'preference',
          updatedAt: '2026-07-08',
        }),
        expect.objectContaining({
          target: 'team',
          path: 'p.md',
          category: 'project',
          name: 'Project Memory',
        }),
      ],
    });
  });

  it('rejects malformed memory sections instead of normalizing them to empty entries', () => {
    expect(() => normalizeMemorySnapshot({
      overview: { writeAvailable: true },
      private: null,
      team: { entries: [] },
    })).toThrow(/memory private entries must be an array/);
  });

  it('fails fast when memory section entries are null instead of arrays', () => {
    // Go 生产端（loadUIMemoryScope）保证 entries 始终为数组；
    // null 属于非法 wire 形状，必须在 schema 边界 fail-fast，不得静默归一为空列表。
    expect(() => normalizeMemorySnapshot({
      overview: { writeAvailable: true },
      private: { entries: null },
      team: { entries: [] },
    })).toThrow(/memory private entries must be an array/);
    expect(() => normalizeMemorySnapshot({
      overview: { writeAvailable: true },
      private: { entries: [] },
      team: { entries: null },
    })).toThrow(/memory team entries must be an array/);
  });

  it('fails fast when memory section entries are missing entirely', () => {
    expect(() => normalizeMemorySnapshot({
      overview: { writeAvailable: true },
      private: {},
      team: { entries: [] },
    })).toThrow(/memory private entries must be an array/);
    expect(() => normalizeMemorySnapshot({
      overview: { writeAvailable: true },
      private: { entries: [] },
      team: {},
    })).toThrow(/memory team entries must be an array/);
    expect(() => normalizeMemorySnapshot({
      overview: { writeAvailable: true },
      private: { entries: [] },
      team: null,
    })).toThrow(/memory team entries must be an array/);
  });

  it('rejects malformed memory entries at the schema boundary', () => {
    expect(() => parseMemorySnapshotResponse(memorySnapshot([{ ...privateMemoryEntry, path: '' }], []))).toThrow('memory private entry 0 path is required');

    expect(() => parseMemorySnapshotResponse(memorySnapshot([], [{ ...teamMemoryEntry, type: '' }]))).toThrow('memory team entry 0 type is unsupported: (empty)');

    expect(() => parseMemorySnapshotResponse(memorySnapshot([{ path: 'u.md', type: 'user' }], []))).toThrow('memory private entry 0 name is required');
  });

  it('preserves the stable non-Git capability reason and rejects incomplete capability shapes', () => {
    const unavailable = parseMemorySnapshotResponse({
      ...memorySnapshot([], []),
      overview: { writeAvailable: false, unavailableReason: 'git_repository_required' },
    });
    expect(unavailable.overview).toEqual({ writeAvailable: false, unavailableReason: 'git_repository_required' });
    expect(() => parseMemorySnapshotResponse({ ...memorySnapshot([], []), overview: { writeAvailable: false } }))
      .toThrow(/required when memory writes are unavailable/);
  });
});

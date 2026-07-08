import { describe, expect, it } from 'vitest';
import { normalizeMemorySnapshot } from './memoryAdapter.js';
import { parseMemorySnapshotResponse } from '../shared/api/backendSchemas.js';

const memoryOverview = { health: { similarGroups: [] } };
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
      overview: {},
      private: null,
      team: { entries: [] },
    })).toThrow(/memory private entries must be an array/);
  });

  it('rejects malformed memory entries at the schema boundary', () => {
    expect(() => parseMemorySnapshotResponse(memorySnapshot([{ ...privateMemoryEntry, path: '' }], []))).toThrow('memory private entry 0 path is required');

    expect(() => parseMemorySnapshotResponse(memorySnapshot([], [{ ...teamMemoryEntry, type: '' }]))).toThrow('memory team entry 0 type is unsupported: (empty)');

    expect(() => parseMemorySnapshotResponse(memorySnapshot([{ path: 'u.md', type: 'user' }], []))).toThrow('memory private entry 0 name is required');
  });
});

import { describe, expect, it, vi } from 'vitest';
import { ActivityHeightSession } from './activityHeightSession.js';

describe('ActivityHeightSession', () => {
  it('is an independent session-local transaction with owner filtering and rollback', () => {
    const commit = vi.fn();
    const preview = vi.fn();
    const session = new ActivityHeightSession({ commit, preview });
    const owner = {};
    session.begin(owner, { coordinate: 500, max: 286, min: 64, pointerId: 4, value: 64 });
    expect(session.move(owner, { coordinate: 400, max: 286, min: 64, pointerId: 99 })).toBe(false);
    expect(session.move(owner, { coordinate: 400, max: 286, min: 64, pointerId: 4 })).toBe(true);
    expect(preview).toHaveBeenLastCalledWith(164, expect.objectContaining({ phase: 'move' }));
    session.cancel(owner, { pointerId: 4, reason: 'blur' });
    expect(preview).toHaveBeenLastCalledWith(64, expect.objectContaining({ phase: 'cancel' }));
    expect(commit).not.toHaveBeenCalled();
  });

  it('commits using the latest viewport metrics and ignores no-move up', () => {
    const commit = vi.fn();
    const session = new ActivityHeightSession({ commit, preview: vi.fn() });
    const owner = {};
    session.begin(owner, { coordinate: 500, max: 400, min: 64, pointerId: 4, value: 200 });
    session.commit(owner, { pointerId: 4 });
    expect(commit).not.toHaveBeenCalled();

    session.begin(owner, { coordinate: 500, max: 400, min: 64, pointerId: 5, value: 200 });
    session.migrateViewport({ max: 220, min: 64 });
    session.move(owner, { coordinate: 300, pointerId: 5 });
    session.commit(owner, { pointerId: 5 });
    expect(commit).toHaveBeenCalledWith(220);
  });
});

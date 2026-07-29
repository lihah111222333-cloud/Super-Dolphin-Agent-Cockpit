import { describe, expect, it, vi } from 'vitest';
import { WidthSession } from './widthSession.js';

function widthHarness() {
  const railCommit = vi.fn();
  const rightCommit = vi.fn();
  const preview = vi.fn();
  return {
    preview,
    railCommit,
    rightCommit,
    session: new WidthSession({
      commitTargets: { rail: railCommit, right: rightCommit },
      preview,
    }),
  };
}

describe('WidthSession', () => {
  it('routes rail and right commits to distinct targets', () => {
    const rail = widthHarness();
    const railOwner = {};
    rail.session.begin(railOwner, { coordinate: 100, kind: 'rail', max: 460, min: 280, pointerId: 1, value: 340 });
    rail.session.move(railOwner, { coordinate: 130, max: 460, min: 280, pointerId: 1 });
    rail.session.commit(railOwner, { pointerId: 1 });
    expect(rail.railCommit).toHaveBeenCalledWith(370);
    expect(rail.rightCommit).not.toHaveBeenCalled();

    const right = widthHarness();
    const rightOwner = {};
    right.session.begin(rightOwner, { coordinate: 100, kind: 'right', max: 600, min: 0, pointerId: 2, value: 380 });
    right.session.move(rightOwner, { coordinate: 40, max: 600, min: 0, pointerId: 2 });
    right.session.commit(rightOwner, { pointerId: 2 });
    expect(right.rightCommit).toHaveBeenCalledWith(440);
    expect(right.railCommit).not.toHaveBeenCalled();
  });

  it('ignores foreign pointers and a repeated begin while active', () => {
    const harness = widthHarness();
    const owner = {};
    const foreignOwner = {};
    expect(harness.session.begin(owner, { coordinate: 100, kind: 'right', max: 600, min: 0, pointerId: 7, value: 380 })).toBe(true);
    expect(harness.session.begin(owner, { coordinate: 100, kind: 'rail', max: 460, min: 280, pointerId: 8, value: 340 })).toBe(false);
    expect(harness.session.move(foreignOwner, { coordinate: 0, max: 600, min: 0, pointerId: 7 })).toBe(false);
    expect(harness.session.move(owner, { coordinate: 0, max: 600, min: 0, pointerId: 8 })).toBe(false);
    expect(harness.session.commit(owner, { pointerId: 8 })).toBe(false);
    expect(harness.session.active).toBe(true);
  });

  it('does not write a preference on no-move up', () => {
    const harness = widthHarness();
    const owner = {};
    harness.session.begin(owner, { coordinate: 100, kind: 'right', max: 600, min: 0, pointerId: 1, value: 380 });
    expect(harness.session.commit(owner, { pointerId: 1 })).toBe(true);
    expect(harness.rightCommit).not.toHaveBeenCalled();
    expect(harness.session.active).toBe(false);
  });

  it.each(['pointercancel', 'blur', 'close', 'unmount'])('rolls back on %s', (reason) => {
    const harness = widthHarness();
    const owner = {};
    harness.session.begin(owner, { coordinate: 100, kind: 'right', max: 600, min: 0, pointerId: 1, value: 380 });
    harness.session.move(owner, { coordinate: 0, max: 600, min: 0, pointerId: 1 });
    expect(harness.session.cancel(owner, { pointerId: 1, reason })).toBe(true);
    expect(harness.preview).toHaveBeenLastCalledWith(380, expect.objectContaining({ phase: 'cancel' }));
    expect(harness.rightCommit).not.toHaveBeenCalled();
    expect(harness.session.active).toBe(false);
  });

  it('uses the latest metrics after viewport migration', () => {
    const harness = widthHarness();
    const owner = {};
    harness.session.begin(owner, { coordinate: 100, kind: 'right', max: 600, min: 0, pointerId: 1, value: 380 });
    harness.session.migrateViewport({ max: 420, min: 0 });
    harness.session.move(owner, { coordinate: 0, pointerId: 1 });
    harness.session.commit(owner, { pointerId: 1 });
    expect(harness.rightCommit).toHaveBeenCalledWith(420);
  });
});

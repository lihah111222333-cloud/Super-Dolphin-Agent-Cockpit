import { describe, expect, it } from 'vitest';
import { createThreadOpenCoordinator } from './threadOpenCoordinator.js';

describe('threadOpenCoordinator', () => {
  it('uses monotonic intent identity instead of target equality for repeated selections', () => {
    const coordinator = createThreadOpenCoordinator();

    const firstA = coordinator.begin('thread-a');
    const middleB = coordinator.begin('thread-b');
    const latestA = coordinator.begin('thread-a');

    expect(firstA).toEqual(expect.objectContaining({ targetThreadId: 'thread-a' }));
    expect(middleB).toEqual(expect.objectContaining({ targetThreadId: 'thread-b' }));
    expect(latestA).toEqual(expect.objectContaining({ targetThreadId: 'thread-a' }));
    expect(latestA.selectionIntentId).toBeGreaterThan(middleB.selectionIntentId);
    expect(middleB.selectionIntentId).toBeGreaterThan(firstA.selectionIntentId);
    expect(coordinator.isCurrent(firstA)).toBe(false);
    expect(coordinator.isCurrent(middleB)).toBe(false);
    expect(coordinator.isCurrent(latestA)).toBe(true);
    expect(coordinator.canReleaseTarget(firstA)).toBe(false);
    expect(coordinator.canReleaseTarget(middleB)).toBe(true);
    expect(coordinator.canReleaseTarget(latestA)).toBe(true);
  });

  it('only cancels the matching current intent and supports explicit invalidation', () => {
    const coordinator = createThreadOpenCoordinator();
    const stale = coordinator.begin('thread-a');
    const current = coordinator.begin('thread-b');

    expect(coordinator.cancel(stale)).toBe(false);
    expect(coordinator.isCurrent(current)).toBe(true);
    expect(coordinator.cancel(current)).toBe(true);
    expect(coordinator.isCurrent(current)).toBe(false);

    const next = coordinator.begin('thread-c');
    coordinator.invalidate();
    expect(coordinator.isCurrent(next)).toBe(false);
  });
});

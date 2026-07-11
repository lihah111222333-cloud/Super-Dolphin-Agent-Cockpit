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

  it('conditionally begins a real target while the captured generation is unchanged', () => {
    const coordinator = createThreadOpenCoordinator();
    const snapshot = coordinator.capture?.();

    const intent = coordinator.beginIfUnchanged?.(snapshot, 'thread-a');

    expect(intent).toEqual(expect.objectContaining({ targetThreadId: 'thread-a' }));
    expect(coordinator.isCurrent(intent)).toBe(true);
  });

  it('invalidates a captured generation after a newer begin', () => {
    const coordinator = createThreadOpenCoordinator();
    const snapshot = coordinator.capture?.();

    coordinator.begin('thread-a');

    expect(coordinator.beginIfUnchanged?.(snapshot, 'thread-b')).toBeNull();
  });

  it('invalidates a captured generation after a successful cancel', () => {
    const coordinator = createThreadOpenCoordinator();
    const current = coordinator.begin('thread-a');
    const snapshot = coordinator.capture?.();

    expect(coordinator.cancel(current)).toBe(true);
    expect(coordinator.beginIfUnchanged?.(snapshot, 'thread-b')).toBeNull();
  });

  it('keeps a captured generation valid after a failed cancel', () => {
    const coordinator = createThreadOpenCoordinator();
    coordinator.begin('thread-a');
    const snapshot = coordinator.capture?.();

    expect(coordinator.cancel(Object.freeze({}))).toBe(false);
    expect(coordinator.beginIfUnchanged?.(snapshot, 'thread-b')).toEqual(
      expect.objectContaining({ targetThreadId: 'thread-b' }),
    );
  });

  it('invalidates a captured generation when the current intent is invalidated', () => {
    const coordinator = createThreadOpenCoordinator();
    coordinator.begin('thread-a');
    const snapshot = coordinator.capture?.();

    coordinator.invalidate();

    expect(coordinator.beginIfUnchanged?.(snapshot, 'thread-b')).toBeNull();
  });

  it('invalidates a captured generation even when invalidate starts with no current intent', () => {
    const coordinator = createThreadOpenCoordinator();
    const snapshot = coordinator.capture?.();

    coordinator.invalidate();

    expect(coordinator.beginIfUnchanged?.(snapshot, 'thread-a')).toBeNull();
  });
});

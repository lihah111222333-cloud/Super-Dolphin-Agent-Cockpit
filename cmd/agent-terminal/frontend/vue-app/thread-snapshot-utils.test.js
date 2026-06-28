// @ts-nocheck
import { describe, it, expect } from 'vitest';
import { isShallowObjectEqual, freezeTimelineItemsAtomic } from './stores/thread-snapshot-utils.js';

describe('isShallowObjectEqual — array-aware comparison', () => {
  it('returns true for objects with identical attachments arrays (different references)', () => {
    const left = { id: 'item-1', kind: 'user', text: '截图', attachments: [{ kind: 'image', name: 'screenshot.png', path: '/tmp/a.png', previewUrl: '/tmp/a.png' }] };
    const right = { id: 'item-1', kind: 'user', text: '截图', attachments: [{ kind: 'image', name: 'screenshot.png', path: '/tmp/a.png', previewUrl: '/tmp/a.png' }] };
    expect(isShallowObjectEqual(left, right)).toBe(true);
  });

  it('returns false for objects with different attachments content', () => {
    const left = { id: 'item-1', attachments: [{ kind: 'image', name: 'a.png' }] };
    const right = { id: 'item-1', attachments: [{ kind: 'image', name: 'b.png' }] };
    expect(isShallowObjectEqual(left, right)).toBe(false);
  });

  it('returns false for objects with different attachments length', () => {
    const left = { id: 'item-1', attachments: [{ kind: 'image' }] };
    const right = { id: 'item-1', attachments: [{ kind: 'image' }, { kind: 'file' }] };
    expect(isShallowObjectEqual(left, right)).toBe(false);
  });

  it('returns true for objects with empty attachments arrays', () => {
    const left = { id: 'item-1', attachments: [] };
    const right = { id: 'item-1', attachments: [] };
    expect(isShallowObjectEqual(left, right)).toBe(true);
  });

  it('returns false when one side has attachments array and other does not', () => {
    const left = { id: 'item-1', attachments: [{ kind: 'image' }] };
    const right = { id: 'item-1' };
    expect(isShallowObjectEqual(left, right)).toBe(false);
  });

  it('still works for plain shallow objects without arrays', () => {
    expect(isShallowObjectEqual({ a: 1, b: 'x' }, { a: 1, b: 'x' })).toBe(true);
    expect(isShallowObjectEqual({ a: 1 }, { a: 2 })).toBe(false);
  });
});

describe('freezeTimelineItemsAtomic — attachments dedup', () => {
  it('reports changed=false when timeline items with attachments are content-equal', () => {
    const current = Object.freeze([
      Object.freeze({ id: 'u1', kind: 'user', text: '截图', attachments: Object.freeze([Object.freeze({ kind: 'image', name: 'screenshot.png', path: '/tmp/a.png' })]) }),
    ]);
    const source = [
      { id: 'u1', kind: 'user', text: '截图', attachments: [{ kind: 'image', name: 'screenshot.png', path: '/tmp/a.png' }] },
    ];
    const result = freezeTimelineItemsAtomic(source, current);
    expect(result.changed).toBe(false);
    expect(result.items).toBe(current);
  });

  it('reports changed=true when attachment content differs', () => {
    const current = [
      Object.freeze({ id: 'u1', kind: 'user', text: '截图', attachments: Object.freeze([Object.freeze({ kind: 'image', name: 'old.png' })]) }),
    ];
    const source = [
      { id: 'u1', kind: 'user', text: '截图', attachments: [{ kind: 'image', name: 'new.png' }] },
    ];
    const result = freezeTimelineItemsAtomic(source, current);
    expect(result.changed).toBe(true);
  });
});

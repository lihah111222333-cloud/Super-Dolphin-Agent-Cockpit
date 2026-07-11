import { describe, expect, it } from 'vitest';
import {
  createScrollIntentState,
  reduceScrollIntent,
  shouldFollowTimeline,
} from './scrollIntentModel.js';

describe('scrollIntentModel', () => {
  it('starts sticky and resets to sticky for a new thread or an explicit send', () => {
    const initial = createScrollIntentState('thread-a');
    expect(initial).toEqual(expect.objectContaining({ mode: 'sticky', threadId: 'thread-a' }));

    const reading = reduceScrollIntent(initial, { type: 'wheel', deltaX: 0, deltaY: -24, ctrlKey: false });
    expect(reading.mode).toBe('reading');
    expect(reduceScrollIntent(reading, { type: 'message-sent' }).mode).toBe('sticky');
    expect(reduceScrollIntent(reading, { type: 'thread-changed', threadId: 'thread-b' })).toEqual(
      expect.objectContaining({ mode: 'sticky', threadId: 'thread-b' }),
    );
  });

  it('leaves sticky for vertical upward wheel intent but ignores zoom and horizontal wheel input', () => {
    const sticky = createScrollIntentState('thread-a');

    expect(reduceScrollIntent(sticky, { type: 'wheel', deltaX: 0, deltaY: -8, ctrlKey: false }).mode).toBe('reading');
    expect(reduceScrollIntent(sticky, { type: 'wheel', deltaX: 0, deltaY: -8, ctrlKey: true }).mode).toBe('sticky');
    expect(reduceScrollIntent(sticky, { type: 'wheel', deltaX: 20, deltaY: -4, ctrlKey: false }).mode).toBe('sticky');
  });

  it('leaves sticky for upward touch and reading-navigation keys outside editable targets', () => {
    const sticky = createScrollIntentState('thread-a');

    expect(reduceScrollIntent(sticky, { type: 'touch', direction: 'up' }).mode).toBe('reading');
    expect(reduceScrollIntent(sticky, { type: 'key', key: 'PageUp', targetEditable: false }).mode).toBe('reading');
    expect(reduceScrollIntent(sticky, { type: 'key', key: 'Home', targetEditable: false }).mode).toBe('reading');
    expect(reduceScrollIntent(sticky, { type: 'key', key: 'ArrowUp', targetEditable: true }).mode).toBe('sticky');
  });

  it('tracks threshold departure and re-enters sticky at the bottom or through explicit navigation', () => {
    const sticky = createScrollIntentState('thread-a');
    const reading = reduceScrollIntent(sticky, { type: 'scroll-position', nearBottom: false });
    expect(reading.mode).toBe('reading');
    expect(reduceScrollIntent(reading, { type: 'scroll-position', nearBottom: true }).mode).toBe('sticky');
    expect(reduceScrollIntent(reading, { type: 'key', key: 'End', targetEditable: false }).mode).toBe('sticky');
    expect(reduceScrollIntent(reading, { type: 'explicit-bottom' }).mode).toBe('sticky');
  });

  it('allows streaming, load, mutation, and resize corrections only while sticky', () => {
    const sticky = createScrollIntentState('thread-a');
    const reading = reduceScrollIntent(sticky, { type: 'scroll-position', nearBottom: false });

    for (const source of ['streaming', 'load', 'mutation', 'resize']) {
      expect(shouldFollowTimeline(sticky, source)).toBe(true);
      expect(shouldFollowTimeline(reading, source)).toBe(false);
    }
  });
});

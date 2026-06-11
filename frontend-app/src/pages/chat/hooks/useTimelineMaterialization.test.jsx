import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useTimelineMaterialization } from './useTimelineMaterialization.js';

function createMessages(count, prefix = 'message') {
  return Array.from({ length: count }, (_, index) => ({ id: `${prefix}-${index}`, text: `${prefix} ${index}` }));
}

function renderTimelineMaterialization(overrides = {}) {
  return renderHook((props) => useTimelineMaterialization(props), {
    initialProps: {
      activeThreadId: 'thread-a',
      introMode: false,
      messages: createMessages(120, 'a'),
      timelineContentBlocked: false,
      ...overrides,
    },
  });
}

describe('useTimelineMaterialization', () => {
  it('materializes the newest timeline messages first', () => {
    const { result } = renderTimelineMaterialization();

    expect(result.current.hiddenOlderCount).toBe(40);
    expect(result.current.visibleMessages).toHaveLength(80);
    expect(result.current.visibleMessages[0].id).toBe('a-40');
    expect(result.current.visibleMessages.at(-1).id).toBe('a-119');
  });

  it('reveals older messages without exceeding the available timeline', () => {
    const { result } = renderTimelineMaterialization({ messages: createMessages(100, 'a') });

    expect(result.current.hiddenOlderCount).toBe(20);

    act(() => {
      result.current.revealOlder();
    });

    expect(result.current.hiddenOlderCount).toBe(0);
    expect(result.current.visibleMessages).toHaveLength(100);
    expect(result.current.visibleMessages[0].id).toBe('a-0');
  });

  it('resets the materialized window when the thread changes', () => {
    const { result, rerender } = renderTimelineMaterialization();

    act(() => {
      result.current.revealOlder();
    });
    expect(result.current.hiddenOlderCount).toBe(0);

    rerender({
      activeThreadId: 'thread-b',
      introMode: false,
      messages: createMessages(120, 'b'),
      timelineContentBlocked: false,
    });

    expect(result.current.hiddenOlderCount).toBe(40);
    expect(result.current.visibleMessages[0].id).toBe('b-40');
  });
});

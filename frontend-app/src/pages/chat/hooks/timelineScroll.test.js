import { describe, expect, it, vi } from 'vitest';
import {
  isTimelineNearBottom,
  requestTimelineBottomScroll,
  scrollTimelineElementToBottom,
} from './timelineScroll.js';

describe('timelineScroll', () => {
  it('detects whether the timeline is close enough to the bottom', () => {
    expect(isTimelineNearBottom(null)).toBe(true);
    expect(isTimelineNearBottom({ scrollHeight: 300, clientHeight: 400, scrollTop: 0 })).toBe(true);
    expect(isTimelineNearBottom({ scrollHeight: 1000, clientHeight: 400, scrollTop: 552 })).toBe(true);
    expect(isTimelineNearBottom({ scrollHeight: 1000, clientHeight: 400, scrollTop: 551 })).toBe(false);
  });

  it('scrolls instantly or delegates smooth scrolling to the element', () => {
    const instant = { scrollHeight: 900, scrollTop: 0 };
    scrollTimelineElementToBottom(instant);
    expect(instant.scrollTop).toBe(900);

    const smooth = { scrollHeight: 700, scrollTo: vi.fn() };
    scrollTimelineElementToBottom(smooth, true);
    expect(smooth.scrollTo).toHaveBeenCalledWith({ top: 700, behavior: 'smooth' });
  });

  it('requests bottom scroll on the next animation frame when available', () => {
    const callback = vi.fn();
    const original = window.requestAnimationFrame;
    window.requestAnimationFrame = vi.fn((fn) => {
      fn();
      return 1;
    });
    try {
      const frameId = requestTimelineBottomScroll(callback);
      expect(callback).toHaveBeenCalledTimes(1);
      expect(window.requestAnimationFrame).toHaveBeenCalledTimes(1);
      expect(frameId).toBe(1);
    } finally {
      window.requestAnimationFrame = original;
    }
  });
});

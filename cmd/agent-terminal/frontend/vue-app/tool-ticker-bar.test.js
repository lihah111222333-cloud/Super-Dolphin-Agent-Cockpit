// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ToolTickerBar } from './components/timeline/ToolTickerBar.ts';

let frameQueue = [];

beforeEach(() => {
  frameQueue = [];
  vi.stubGlobal('window', {
    matchMedia: vi.fn(() => ({ matches: false })),
    requestAnimationFrame: vi.fn((cb) => {
      frameQueue.push(cb);
      return frameQueue.length;
    }),
  });
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('ToolTickerBar', () => {
  it('queues animation frames and advances the viewport when resumed', () => {
    const vm = ToolTickerBar.setup({ text: 'tool output', visible: true });
    vm.toolTickerViewportRef.value = { scrollLeft: 0, scrollWidth: 320, clientWidth: 120 };
    frameQueue.length = 0;

    vm.resumeToolTicker();
    expect(frameQueue.length).toBe(1);

    frameQueue.shift()();
    expect(vm.toolTickerViewportRef.value.scrollLeft).toBeGreaterThan(0);
  });

  it('cancels the current animation frame when paused', () => {
    const vm = ToolTickerBar.setup({ text: 'tool output', visible: true });
    vm.toolTickerViewportRef.value = { scrollLeft: 0, scrollWidth: 320, clientWidth: 120 };
    frameQueue.length = 0;

    vm.resumeToolTicker();
    vm.pauseToolTicker();

    expect(globalThis.cancelAnimationFrame).toHaveBeenCalled();
  });

  it('does not schedule animation when reduced motion is preferred', () => {
    globalThis.window.matchMedia.mockReturnValueOnce({ matches: true });
    const vm = ToolTickerBar.setup({ text: 'tool output', visible: true });
    vm.toolTickerViewportRef.value = { scrollLeft: 0, scrollWidth: 320, clientWidth: 120 };
    frameQueue.length = 0;

    vm.resumeToolTicker();

    expect(frameQueue.length).toBe(0);
  });
});

// @ts-nocheck
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { createStreamingMarkdownStateResolver } from './utils/assistant-markdown-streaming.js';

// Stub for environment missing rAF
if (typeof globalThis.requestAnimationFrame === 'undefined') {
  globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 16);
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
}

describe('assistant-markdown-streaming guard (TDD)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('normal streaming updates should flush state via scheduled frames', async () => {
    const renderBody = vi.fn((text) => `<p>${text}</p>`);
    const onFlush = vi.fn();
    const onStall = vi.fn();

    const resolve = createStreamingMarkdownStateResolver(renderBody, onFlush, onStall);

    const state1 = resolve({ id: 'msg-1', text: 'Hello', done: false });
    expect(state1.text).toBe('Hello'); // First frame is displayed synchronously

    // Provide second chunk
    resolve({ id: 'msg-1', text: 'Hello World', done: false });

    // Advance 16ms (1 frame delay via setTimeout since requestAnimationFrame is not native here)
    await vi.advanceTimersByTimeAsync(16);
    expect(onFlush).toHaveBeenCalledTimes(1);

    const flushedState = resolve({ id: 'msg-1', text: 'Hello World', done: false });
    expect(flushedState.text).toBe('Hello World');
  });

  it('stale guard MUST automatically flush pending items if network connection drops or done:true is lost', async () => {
    // Override rAF to simulate a frozen main thread / background tab
    const originalRaf = globalThis.requestAnimationFrame;
    globalThis.requestAnimationFrame = () => {}; // Never fire!

    try {
      const renderBody = vi.fn((text) => `<p>${text}</p>`);
      const onFlush = vi.fn();
      const onStall = vi.fn();

      const resolve = createStreamingMarkdownStateResolver(renderBody, onFlush, onStall);

      // Provide a chunk
      resolve({ id: 'msg-2', text: '# Part 1', done: false });
      
      // Simulate frame flush working once before freeze
      originalRaf(() => resolve({ id: 'msg-2', text: '# Part 1\n# Part 2', done: false }));
      
      // Don't wait for 16ms frame, jump ahead! 
      // Wait for the staleGuardTimer which triggers after 200ms
      await vi.advanceTimersByTimeAsync(250);

      // The stale guard timer should have fired
      expect(onStall).toHaveBeenCalledWith(expect.objectContaining({
        reason: 'stale_guard_fired',
      }));

      // Even without explicitly resolving a done:true, the guard should have flushed the latest text
      const finalState = resolve({ id: 'msg-2', text: '# Part 1\n# Part 2', done: false });
      expect(finalState.text).toBe('# Part 1\n# Part 2');
      
      resolve.dispose();
    } finally {
      globalThis.requestAnimationFrame = originalRaf;
    }
  });
});

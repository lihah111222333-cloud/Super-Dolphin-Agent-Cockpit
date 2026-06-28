// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { observeContainerWidth, disconnectContainerObserver } from './services/pretext-layout.js';

describe('pretext-layout requestAnimationFrame guards', () => {
  beforeEach(() => {
    vi.stubGlobal('document', {
      body: { innerHTML: '' },
      querySelector: vi.fn(() => null),
    });
  });

  afterEach(() => {
    disconnectContainerObserver();
    if (typeof document !== 'undefined' && document?.body) document.body.innerHTML = '';
    vi.unstubAllGlobals();
  });

  it('does not throw when requestAnimationFrame is unavailable', () => {
    vi.stubGlobal('requestAnimationFrame', undefined);
    vi.stubGlobal('cancelAnimationFrame', undefined);

    expect(() => observeContainerWidth()).not.toThrow();
  });

  it('disconnect cleanup tolerates missing cancelAnimationFrame after a scheduled retry', () => {
    const raf = vi.fn(() => 1);
    vi.stubGlobal('requestAnimationFrame', raf);
    vi.stubGlobal('cancelAnimationFrame', undefined);

    expect(() => observeContainerWidth()).not.toThrow();
    expect(raf).toHaveBeenCalledTimes(1);
    expect(() => disconnectContainerObserver()).not.toThrow();
  });
});

// @ts-nocheck
// Hot log-level switching: cross-tab sync (`storage` event), in-process
// subscribers (`onLogLevelChange`), devtools `window.AOLog.setLevel`.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { onLogLevelChange, readLogLevel, setLogLevel } from './services/log.js';

describe('log level subscriber', () => {
  let unsubs;

  beforeEach(() => {
    unsubs = [];
    setLogLevel('info'); // reset to known baseline
  });

  afterEach(() => {
    for (const fn of unsubs) {
      try { fn(); } catch { /* ignore */ }
    }
  });

  it('fires subscribers when setLogLevel changes the level', () => {
    const seen = [];
    unsubs.push(onLogLevelChange((lvl) => seen.push(lvl)));

    setLogLevel('debug');

    expect(seen).toEqual(['debug']);
    expect(readLogLevel()).toBe('debug');
  });

  it('does not fire when the new level equals the previous one', () => {
    const seen = [];
    unsubs.push(onLogLevelChange((lvl) => seen.push(lvl)));

    setLogLevel('info'); // already info from beforeEach
    expect(seen).toEqual([]);
  });

  it('rejects unknown levels and does not fire subscribers', () => {
    const seen = [];
    unsubs.push(onLogLevelChange((lvl) => seen.push(lvl)));

    const ok = setLogLevel('verbose-nope');

    expect(ok).toBe(false);
    expect(seen).toEqual([]);
    expect(readLogLevel()).toBe('info');
  });

  it('returns an unsubscribe function that detaches the callback', () => {
    const seen = [];
    const off = onLogLevelChange((lvl) => seen.push(lvl));

    setLogLevel('warn');
    off();
    setLogLevel('error');

    expect(seen).toEqual(['warn']);
  });

  it('survives a faulty subscriber without poisoning the others', () => {
    const seen = [];
    unsubs.push(onLogLevelChange(() => { throw new Error('boom'); }));
    unsubs.push(onLogLevelChange((lvl) => seen.push(lvl)));

    setLogLevel('debug');

    expect(seen).toEqual(['debug']);
  });

  it('ignores onLogLevelChange when callback is not a function', () => {
    const off = onLogLevelChange(null);
    expect(typeof off).toBe('function');
    // Should not throw
    off();
  });
});

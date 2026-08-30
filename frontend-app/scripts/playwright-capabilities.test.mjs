import { describe, expect, it } from 'vitest';

import { ffmpegAvailable, resolvePlaywrightVideoMode } from '../playwright-capabilities.mjs';

describe('Playwright media capability contract', () => {
  it('disables video explicitly when ffmpeg is unavailable', () => {
    expect(resolvePlaywrightVideoMode({}, () => false)).toBe(false);
  });

  it('retains failure video when a runnable ffmpeg is available', () => {
    expect(resolvePlaywrightVideoMode({}, () => true)).toBe('retain-on-failure');
  });

  it('fails fast when video is explicitly required without ffmpeg', () => {
    expect(() => resolvePlaywrightVideoMode({ SUPER_DOLPHIN_PLAYWRIGHT_VIDEO: '1' }, () => false))
      .toThrow(/requires an executable ffmpeg/);
  });

  it('honors an explicit video disable without probing the host', () => {
    let probes = 0;
    expect(resolvePlaywrightVideoMode({ SUPER_DOLPHIN_PLAYWRIGHT_VIDEO: 'off' }, () => { probes += 1; return true; })).toBe(false);
    expect(probes).toBe(0);
  });

  it('requires configured ffmpeg paths to exist and execute', () => {
    const env = { SUPER_DOLPHIN_FFMPEG_BIN: '/tmp/ffmpeg' };
    expect(ffmpegAvailable(env, (candidate) => candidate === '/tmp/ffmpeg', () => true)).toBe(true);
    expect(ffmpegAvailable(env, () => false, () => true)).toBe(false);
    expect(ffmpegAvailable(env, () => true, () => false)).toBe(false);
  });
});

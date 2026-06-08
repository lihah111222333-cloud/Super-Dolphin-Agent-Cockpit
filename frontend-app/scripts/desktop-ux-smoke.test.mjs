import { describe, expect, it } from 'vitest';

import {
  buildDesktopUXEnv,
  desktopUXSmokeConfig,
  parseHostPort,
  resolveChromeExecutable,
} from './desktop-ux-smoke.mjs';

describe('desktop UX smoke command', () => {
  it('fails fast when neither explicit Chrome nor system Chrome is available', () => {
    expect(() => resolveChromeExecutable({}, () => false))
      .toThrow('PLAYWRIGHT_CHROMIUM_EXECUTABLE is required');
  });

  it('prefers explicit Playwright Chrome executable', () => {
    expect(resolveChromeExecutable({ PLAYWRIGHT_CHROMIUM_EXECUTABLE: '/opt/chrome' }, () => false))
      .toBe('/opt/chrome');
  });

  it('uses independent ports and a short Postgres socket path', () => {
    const config = desktopUXSmokeConfig({
      PLAYWRIGHT_CHROMIUM_EXECUTABLE: '/opt/chrome',
      SUPER_DOLPHIN_PLAYWRIGHT_HTTP_ADDR: '127.0.0.1:4613',
      SUPER_DOLPHIN_PLAYWRIGHT_VITE_URL: 'http://127.0.0.1:5276',
      SUPER_DOLPHIN_PLAYWRIGHT_CTL_ADDR: '127.0.0.1:8193',
      SUPER_DOLPHIN_PLAYWRIGHT_POSTGRES_PORT: '56434',
    }, '/repo/app');

    expect(config.httpAddr).toBe('127.0.0.1:4613');
    expect(config.viteURL).toBe('http://127.0.0.1:5276');
    expect(config.ctlAddr).toBe('127.0.0.1:8193');
    expect(config.postgresPort).toBe(56434);
    expect(config.postgresRuntimeDir).toMatch(/^\/tmp\/sd-pw-pg-56434-/);
    expect(config.postgresRuntimeDir.length).toBeLessThan(60);
  });

  it('builds the run-new-ui-desktop environment for UX smoke', () => {
    const config = desktopUXSmokeConfig({ PLAYWRIGHT_CHROMIUM_EXECUTABLE: '/opt/chrome' }, '/repo/app');
    const env = buildDesktopUXEnv(config, { PATH: '/bin' });

    expect(env.SUPER_DOLPHIN_HTTP_ADDR).toBe('127.0.0.1:4513');
    expect(env.VITE_DEV_URL).toBe('http://127.0.0.1:5176');
    expect(env.FRONTEND_DEVSERVER_URL).toBe('http://127.0.0.1:5176');
    expect(env.GO_AGENT_CTL_RPC_ADDR).toBe('127.0.0.1:8093');
    expect(env.SUPER_DOLPHIN_LOCAL_POSTGRES_PORT).toBe('55434');
    expect(env.SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR).toMatch(/^\/tmp\/sd-pw-pg-55434-/);
    expect(env.PLAYWRIGHT_CHROMIUM_EXECUTABLE).toBe('/opt/chrome');
    expect(env.SUPER_DOLPHIN_DESKTOP_UX_BASE_URL).toBe('http://127.0.0.1:5176');
  });

  it('parses host and port pairs', () => {
    expect(parseHostPort('127.0.0.1:4513')).toEqual({ host: '127.0.0.1', port: 4513 });
    expect(() => parseHostPort('127.0.0.1')).toThrow('host:port is required');
  });
});

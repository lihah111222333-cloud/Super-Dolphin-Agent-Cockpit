import { beforeEach, describe, expect, it, vi } from 'vitest';

describe('Tencent Cloud RUM monitoring', () => {
  beforeEach(() => {
    vi.resetModules();
  });

  it('does not instantiate Aegis when Tencent RUM is not configured', async () => {
    const { initTencentRum } = await import('./tencentRum.js');
    const loadAegis = vi.fn();

    expect(initTencentRum({}, loadAegis)).toBeNull();

    expect(loadAegis).not.toHaveBeenCalled();
  });

  it('fails fast when Tencent RUM is explicitly enabled without an application id', async () => {
    const { initTencentRum } = await import('./tencentRum.js');

    expect(() => initTencentRum({ VITE_TENCENT_RUM_ENABLED: 'true' }))
      .toThrow('VITE_TENCENT_RUM_ID is required when Tencent RUM is enabled');
  });

  it('initializes Aegis with performance collection only when trace urls are not configured', async () => {
    const Aegis = vi.fn();
    const loadAegis = vi.fn().mockResolvedValue({ default: Aegis });
    const { initTencentRum } = await import('./tencentRum.js');

    await initTencentRum({
      VITE_TENCENT_RUM_ID: 'rum-app-id',
      VITE_TENCENT_RUM_UIN: 'user-123',
      VITE_TENCENT_RUM_HOST_URL: 'https://tamaegis.com',
    }, loadAegis);

    expect(loadAegis).toHaveBeenCalledTimes(1);
    expect(Aegis).toHaveBeenCalledWith(expect.objectContaining({
      id: 'rum-app-id',
      uin: 'user-123',
      hostUrl: 'https://tamaegis.com',
      reportApiSpeed: {
        urlHandler: expect.any(Function),
      },
      reportAssetSpeed: true,
      spa: true,
      api: {},
      urlHandler: expect.any(Function),
    }));
    expect(Aegis.mock.calls[0][0].api).not.toHaveProperty('injectTraceHeader');
  });

  it('initializes Aegis with explicit trace header injection whitelist', async () => {
    const Aegis = vi.fn();
    const loadAegis = vi.fn().mockResolvedValue({ default: Aegis });
    const { initTencentRum } = await import('./tencentRum.js');

    await initTencentRum({
      VITE_TENCENT_RUM_ID: 'rum-app-id',
      VITE_TENCENT_RUM_TRACE_HEADER: 'traceparent',
      VITE_TENCENT_RUM_TRACE_URLS: 'regex:^https://api\\.example\\.com/,/api',
      VITE_TENCENT_RUM_TRACE_IGNORE_URLS: 'regex:/auth/token$',
    }, loadAegis);

    expect(loadAegis).toHaveBeenCalledTimes(1);
    expect(Aegis).toHaveBeenCalledWith(expect.objectContaining({
      api: {
        injectTraceHeader: 'traceparent',
        injectTraceUrls: [expect.any(RegExp), '/api'],
        injectTraceIgnoreUrls: [expect.any(RegExp), expect.any(RegExp)],
        reqHeaders: ['traceparent'],
      },
      urlHandler: expect.any(Function),
    }));
    expect(Aegis.mock.calls[0][0].api.injectTraceUrls[0]).toEqual(/^https:\/\/api\.example\.com\//);
    expect(Aegis.mock.calls[0][0].api.injectTraceIgnoreUrls[0]).toEqual(/\/auth\/token$/);
    expect(Aegis.mock.calls[0][0].api.injectTraceIgnoreUrls[1].test('https://rumt-zh.com/speed')).toBe(true);
  });

  it('fails fast on invalid trace URL regex patterns', async () => {
    const { buildTencentRumConfig } = await import('./tencentRum.js');

    expect(() => buildTencentRumConfig({
      VITE_TENCENT_RUM_ID: 'rum-app-id',
      VITE_TENCENT_RUM_TRACE_URLS: 'regex:[',
    })).toThrow('VITE_TENCENT_RUM_TRACE_URLS contains invalid regex');
  });

  it('sanitizes sensitive and local URL details before reporting', async () => {
    const { buildTencentRumConfig } = await import('./tencentRum.js');

    const config = buildTencentRumConfig({ VITE_TENCENT_RUM_ID: 'rum-app-id' });

    expect(config.reportApiSpeed.urlHandler('https://example.test/home/alice/project/123456789?access_token=abc&id_token=jwt&api_key=secret&auth=bearer#frag'))
      .toBe('https://example.test/<local-path>?access_token=<redacted>&id_token=<redacted>&api_key=<redacted>&auth=<redacted>');
    expect(config.urlHandler('file:///C:/Users/Alice/project/123456789?refresh_token=abc'))
      .toBe('file:///<local-path>?refresh_token=<redacted>');
  });

  it('keeps a single Aegis instance promise per page session', async () => {
    const Aegis = vi.fn();
    const loadAegis = vi.fn().mockResolvedValue({ default: Aegis });
    const { initTencentRum } = await import('./tencentRum.js');

    const first = initTencentRum({ VITE_TENCENT_RUM_ID: 'rum-app-id' }, loadAegis);
    const second = initTencentRum({ VITE_TENCENT_RUM_ID: 'rum-app-id' }, loadAegis);
    await Promise.all([first, second]);

    expect(loadAegis).toHaveBeenCalledTimes(1);
    expect(Aegis).toHaveBeenCalledTimes(1);
  });
});

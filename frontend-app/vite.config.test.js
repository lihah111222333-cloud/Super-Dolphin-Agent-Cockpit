import { readFileSync } from 'node:fs';
import process from 'node:process';
import { describe, expect, it } from 'vitest';
import {
  ATH_BUILD_IDENTITY_HEADER,
  ATH_HEALTH_PATH,
  ATH_NONCE_HEADER,
  ATH_SOURCE_ROOT_HEADER,
  agenticHarnessIdentityPlugin,
  createFrontendViteConfig,
} from './vite.config.js';

const packageJson = JSON.parse(readFileSync('package.json', 'utf8'));

describe('frontend vite dev proxy', () => {
  it('proxies generated image assets to the Go asset server', () => {
    const backendAddr = process.env.SUPER_DOLPHIN_HTTP_ADDR || '127.0.0.1:4512';
    const config = createFrontendViteConfig(process.env, { command: 'serve', mode: 'development' });

    expect(config.server.proxy['/generated-image']).toEqual({
      target: `http://${backendAddr}`,
    });
    expect(config.server.proxy['/local-image']).toEqual({
      target: `http://${backendAddr}`,
    });
  });

  it('passes the dev session token through the websocket proxy', () => {
    const proxy = createFrontendViteConfig({
      SUPER_DOLPHIN_HTTP_ADDR: '127.0.0.1:4512',
      GO_AGENT_CTL_SESSION_TOKEN: 'dev-token',
    }).server.proxy['/wails/ws'];

    expect(proxy).toEqual({
      target: 'ws://127.0.0.1:4512',
      ws: true,
      headers: { Cookie: 'super_dolphin_wails_ws=dev-token' },
    });
  });

  it('serves the development Wails runtime shim as a module request', () => {
    const config = createFrontendViteConfig({}, { command: 'serve', mode: 'development' });
    const plugin = config.plugins.find((item) => item?.name === 'super-dolphin-dev-wails-runtime');
    const handlers = [];
    const headers = {};
    let body = '';
    let nextCalled = false;
    const response = {
      statusCode: 0,
      setHeader(name, value) {
        headers[name] = value;
      },
      end(value) {
        body = String(value);
      },
    };

    plugin.configureServer({
      middlewares: {
        use(handler) {
          handlers.push(handler);
        },
      },
    });
    handlers[0]({ url: '/wails/runtime.js?import' }, response, () => {
      nextCalled = true;
    });

    expect(nextCalled).toBe(false);
    expect(response.statusCode).toBe(200);
    expect(headers['Content-Type']).toBe('text/javascript; charset=utf-8');
    expect(headers['Cache-Control']).toBe('no-store');
    expect(body).toContain('/wails/ws');
    expect(body).toContain('__WAILS_SHIM_DEBUG__');
  });
});

describe('frontend vite watch config', () => {
  it('enables polling by default for direct npm run dev', () => {
    expect(packageJson.scripts.dev).toBe('vite --host 127.0.0.1 --port 5175 --strictPort');
    expect(createFrontendViteConfig({}).server.watch.usePolling).toBe(true);
  });

  it('allows explicitly disabling polling for native fs events', () => {
    expect(createFrontendViteConfig({ SUPER_DOLPHIN_VITE_USE_POLLING: '0' }).server.watch.usePolling).toBe(false);
    expect(createFrontendViteConfig({ CHOKIDAR_USEPOLLING: 'false' }).server.watch.usePolling).toBe(false);
  });

  it('fails fast for invalid watch booleans and conflicts', () => {
    expect(() => createFrontendViteConfig({ SUPER_DOLPHIN_VITE_USE_POLLING: 'sometimes' })).toThrow(
      /SUPER_DOLPHIN_VITE_USE_POLLING must be a boolean/,
    );
    expect(() => createFrontendViteConfig({ CHOKIDAR_USEPOLLING: 'sometimes' })).toThrow(
      /CHOKIDAR_USEPOLLING must be a boolean/,
    );
    expect(() => createFrontendViteConfig({
      SUPER_DOLPHIN_VITE_USE_POLLING: '0',
      CHOKIDAR_USEPOLLING: '1',
    })).toThrow(/conflicting frontend watch config/);
  });
});

describe('frontend vite build budget', () => {
  it('builds isolated normal and Recovery entry points', () => {
    const input = createFrontendViteConfig({}).build.rolldownOptions.input;
    expect(input.main).toMatch(/index\.html$/);
    expect(input.recovery).toMatch(/recovery\.html$/);
  });

  it('keeps the lazy mermaid parser bundle under the configured warning limit', () => {
    expect(createFrontendViteConfig({}).build.chunkSizeWarningLimit).toBe(650);
  });
});

describe('frontend vite ui test MCP gate', () => {
  it('allows the UI test MCP flag for dev and test config', () => {
    expect(() => createFrontendViteConfig(
      { VITE_SUPER_DOLPHIN_UI_TEST_MCP: '1' },
      { command: 'serve', mode: 'development' },
    )).not.toThrow();
    expect(() => createFrontendViteConfig(
      { VITE_SUPER_DOLPHIN_UI_TEST_MCP: '1' },
      { command: 'serve', mode: 'test' },
    )).not.toThrow();
  });

  it('rejects the UI test MCP flag for production builds', () => {
    expect(() => createFrontendViteConfig(
      { VITE_SUPER_DOLPHIN_UI_TEST_MCP: '1' },
      { command: 'build', mode: 'production' },
    )).toThrow(/VITE_SUPER_DOLPHIN_UI_TEST_MCP/);
    expect(() => createFrontendViteConfig(
      { VITE_SUPER_DOLPHIN_UI_TEST_MCP: '0', NODE_ENV: 'production' },
      { command: 'serve', mode: 'development' },
    )).toThrow(/production builds/);
  });
});

describe('frontend vite agentic harness identity gate', () => {
  const harnessEnv = {
    ATH_TARGET_NONCE: 'A'.repeat(43),
    SUPER_DOLPHIN_ATH_SOURCE_ROOT: '/tmp/super-dolphin-source',
    SUPER_DOLPHIN_ATH_BUILD_IDENTITY: 'git:0123456789abcdef',
  };

  it('serves exact nonce and identity only on the harness health path', () => {
    const plugin = agenticHarnessIdentityPlugin(harnessEnv, { command: 'serve', mode: 'development' });
    const handlers = [];
    const headers = {};
    let body = '';
    plugin.configureServer({ middlewares: { use(handler) { handlers.push(handler); } } });
    const response = {
      statusCode: 0,
      setHeader(name, value) { headers[name] = value; },
      end(value) { body = String(value); },
    };
    handlers[0]({ url: ATH_HEALTH_PATH }, response, () => {
      throw new Error('health middleware unexpectedly delegated');
    });
    expect(response.statusCode).toBe(200);
    expect(headers[ATH_NONCE_HEADER]).toBe(harnessEnv.ATH_TARGET_NONCE);
    expect(headers[ATH_SOURCE_ROOT_HEADER]).toBe(harnessEnv.SUPER_DOLPHIN_ATH_SOURCE_ROOT);
    expect(headers[ATH_BUILD_IDENTITY_HEADER]).toBe(harnessEnv.SUPER_DOLPHIN_ATH_BUILD_IDENTITY);
    expect(JSON.parse(body)).toEqual({
      ok: true,
      sourceRoot: harnessEnv.SUPER_DOLPHIN_ATH_SOURCE_ROOT,
      buildIdentity: harnessEnv.SUPER_DOLPHIN_ATH_BUILD_IDENTITY,
    });
  });

  it('is absent normally and fails fast for partial, malformed, or production identity', () => {
    expect(agenticHarnessIdentityPlugin({}, { command: 'serve', mode: 'development' })).toBeUndefined();
    expect(() => agenticHarnessIdentityPlugin(
      { ATH_TARGET_NONCE: harnessEnv.ATH_TARGET_NONCE },
      { command: 'serve', mode: 'development' },
    )).toThrow(/SUPER_DOLPHIN_ATH_SOURCE_ROOT/);
    expect(() => agenticHarnessIdentityPlugin(
      { ...harnessEnv, ATH_TARGET_NONCE: 'short' },
      { command: 'serve', mode: 'development' },
    )).toThrow(/256-bit base64url/);
    expect(() => agenticHarnessIdentityPlugin(
      harnessEnv,
      { command: 'build', mode: 'production' },
    )).toThrow(/dev\/test-only/);
  });
});

import { readFileSync } from 'node:fs';
import process from 'node:process';
import { describe, expect, it } from 'vitest';
import { createFrontendViteConfig } from './vite.config.js';

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

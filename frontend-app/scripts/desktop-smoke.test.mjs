import { describe, expect, it } from 'vitest';

import {
  buildFrontendFailureEvent,
  buildJSONRPCRequest,
  buildThreadStartParams,
  buildTurnInterruptParams,
  buildWebSocketURL,
  packageScriptIncludesSmoke,
  smokeConfig,
} from './desktop-smoke.mjs';

describe('desktop smoke command', () => {
  it('normalizes the desktop HTTP address to the Wails websocket route', () => {
    expect(buildWebSocketURL('127.0.0.1:4512')).toBe('ws://127.0.0.1:4512/wails/ws');
    expect(buildWebSocketURL('http://127.0.0.1:4512')).toBe('ws://127.0.0.1:4512/wails/ws');
    expect(buildWebSocketURL('wss://example.test/custom')).toBe('wss://example.test/custom');
  });

  it('builds json-rpc websocket requests with object params', () => {
    expect(buildJSONRPCRequest(7, 'ui/sidebar/get', { cwd: '/repo/app' })).toEqual({
      jsonrpc: '2.0',
      id: 7,
      method: 'ui/sidebar/get',
      params: { cwd: '/repo/app' },
    });
  });

  it('keeps provider-spawning turn smoke opt-in', () => {
    expect(smokeConfig({}, '/repo/app').runTurnPath).toBe(false);
    expect(smokeConfig({ SUPER_DOLPHIN_DESKTOP_SMOKE_TURN: '1' }, '/repo/app').runTurnPath).toBe(true);
  });

  it('carries the accepted turn identity and a unique request id into interrupt', () => {
    expect(buildTurnInterruptParams(
      'thread-1',
      { turn_id: 'turn-1' },
      () => 'request-1',
    )).toEqual({
      thread_id: 'thread-1',
      expected_turn_id: 'turn-1',
      request_id: 'request-1',
      source: 'desktop_smoke',
    });
    expect(() => buildTurnInterruptParams('thread-1', {}, () => 'request-1')).toThrow(/turn_id/);
    expect(() => buildTurnInterruptParams('thread-1', { turn_id: 'turn-1' }, () => '')).toThrow(/request_id/);
    expect(() => buildTurnInterruptParams('', { turn_id: 'turn-1' }, () => 'request-1')).toThrow(/thread_id/);
  });

  it('prepares the Go embed frontend by default', () => {
    expect(smokeConfig({}, '/repo/app').skipFrontendBuild).toBe(false);
    expect(smokeConfig({ SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD: '1' }, '/repo/app').skipFrontendBuild).toBe(true);
  });

  it('uses defer_spawn for the default thread/start smoke path', () => {
    const config = smokeConfig({ SUPER_DOLPHIN_DESKTOP_SMOKE_PROVIDER: 'codex' }, '/repo/app');
    const payload = buildThreadStartParams(config);
    expect(payload).toEqual(expect.objectContaining({
      cwd: '/repo/app',
      provider: 'codex',
      defer_spawn: true,
      tool_surface_mode: 'chat',
    }));
    expect(payload).not.toHaveProperty('optimisticUserMessage');
    expect(payload).not.toHaveProperty('skipInitialRuntimeSync');
  });

  it('uses the named npm script for the runnable smoke command', async () => {
    await expect(packageScriptIncludesSmoke()).resolves.toBe(true);
  });

  it('builds a strict frontend ingest failure event', () => {
    expect(buildFrontendFailureEvent()).toEqual(expect.objectContaining({
      phase: 'frontend.rpc.failed',
      method: 'thread/start',
      status: 'error',
      metadata: { component: 'desktop-smoke' },
    }));
  });
});

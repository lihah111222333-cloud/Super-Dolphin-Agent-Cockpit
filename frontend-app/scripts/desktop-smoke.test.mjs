import { describe, expect, it } from 'vitest';

import {
  buildFrontendFailureEvent,
  buildJSONRPCRequest,
  buildThreadStartParams,
  buildTurnInterruptParams,
  buildWebSocketOptions,
  buildWebSocketURL,
  openWSRPC,
  packageScriptIncludesSmoke,
  runDesktopSmoke,
  runTurnSmoke,
  smokeConfig,
} from './desktop-smoke.mjs';

describe('desktop smoke command', () => {
  it('normalizes the desktop HTTP address to the Wails websocket route', () => {
    expect(buildWebSocketURL('127.0.0.1:4512')).toBe('ws://127.0.0.1:4512/wails/ws');
    expect(buildWebSocketURL('http://127.0.0.1:4512')).toBe('ws://127.0.0.1:4512/wails/ws');
    expect(buildWebSocketURL('wss://example.test/custom')).toBe('wss://example.test/custom');
  });

  it('builds same-origin websocket headers with the explicit Wails token', () => {
    expect(buildWebSocketOptions('ws://127.0.0.1:4512/wails/ws', 'token-1')).toEqual({
      headers: {
        Cookie: 'super_dolphin_wails_ws=token-1',
        Origin: 'http://127.0.0.1:4512',
      },
    });
    expect(() => buildWebSocketOptions('ws://127.0.0.1:4512/wails/ws', '')).toThrow(/non-empty/);
    expect(() => buildWebSocketOptions('ws://127.0.0.1:4512/wails/ws', 'bad;token')).toThrow(/cookie-safe/);
  });

  it('binds one generated websocket token into the desktop smoke config', () => {
    expect(smokeConfig({}, '/repo/app', () => 'generated-token').wsToken).toBe('generated-token');
    expect(smokeConfig({ SUPER_DOLPHIN_WAILS_WS_TOKEN: 'configured-token' }, '/repo/app', () => 'unused').wsToken)
      .toBe('configured-token');
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

  it.each([
    [{ ok: false, accepted: true }, /ok=true/],
    [{ ok: true, accepted: false }, /accepted=true/],
    [{ ok: false, accepted: false, errorCode: 'TARGET_CHANGED' }, /TARGET_CHANGED/],
    [{ ok: false, accepted: false, errorCode: 'NOT_APPLIED' }, /NOT_APPLIED/],
  ])('rejects a non-accepted turn interrupt result %#', async (interruptResult, errorPattern) => {
    const client = {
      request: (method) => Promise.resolve(
        method === 'turn/start'
          ? { turn_id: 'turn-1' }
          : interruptResult,
      ),
    };

    await expect(runTurnSmoke(client, { cwd: '/repo/app' }, 'thread-1')).rejects.toThrow(errorPattern);
  });

  it('propagates an interrupt transport timeout instead of reporting smoke success', async () => {
    const timeout = new Error('turn/interrupt timed out');
    const client = {
      request: (method) => (
        method === 'turn/start'
          ? Promise.resolve({ turn_id: 'turn-1' })
          : Promise.reject(timeout)
      ),
    };

    await expect(runTurnSmoke(client, { cwd: '/repo/app' }, 'thread-1')).rejects.toBe(timeout);
  });

  it('accepts only an explicit successful interrupt result', async () => {
    const client = {
      request: (method) => Promise.resolve(
        method === 'turn/start'
          ? { turn_id: 'turn-1' }
          : { ok: true, accepted: true },
      ),
    };

    await expect(runTurnSmoke(client, { cwd: '/repo/app' }, 'thread-1')).resolves.toBeUndefined();
  });

  it.each([
    ['rejected handshake', new Error('websocket connection failed (handshake details redacted)')],
    ['handshake timeout', new Error('websocket open timed out')],
  ])('always terminates the desktop tree after %s', async (_name, failure) => {
    const child = { pid: 1234, exitCode: null, signalCode: null };
    const stopCalls = [];
    await expect(runDesktopSmoke({
      ...smokeConfig({}, '/repo/app'),
      skipFrontendBuild: true,
    }, {
      spawn: () => child,
      waitForHTTP: async () => {},
      openWSRPC: async () => { throw failure; },
      stopDesktop: async (target) => { stopCalls.push(target); },
    })).rejects.toBe(failure);
    expect(stopCalls).toEqual([child]);
  });

  it.each([
    ['rejection', true],
    ['timeout', false],
  ])('explicitly terminates the websocket after handshake %s', async (_name, rejectHandshake) => {
    let terminated = 0;
    class FakeWebSocket {
      addEventListener(type, listener) {
        if (rejectHandshake && type === 'error') queueMicrotask(listener);
      }

      terminate() {
        terminated += 1;
      }
    }
    await expect(openWSRPC('ws://127.0.0.1:4512/wails/ws', 'token', 5, FakeWebSocket))
      .rejects.toThrow(rejectHandshake ? /details redacted/ : /timed out/);
    expect(terminated).toBe(1);
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

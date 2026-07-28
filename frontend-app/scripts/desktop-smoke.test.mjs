import { EventEmitter } from 'node:events';
import { describe, expect, it } from 'vitest';

import {
  buildFrontendFailureEvent,
  buildJSONRPCRequest,
  buildThreadStartParams,
  buildTurnInterruptParams,
  buildWebSocketOptions,
  buildWebSocketURL,
  desktopRunnerInvocation,
  desktopSpawnOptions,
  packageScriptIncludesSmoke,
  runDesktopSmoke,
  smokeConfig,
  stopDesktop,
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

  it('fails the real Wails WS smoke when thread/start projects invalid Agent progress', async () => {
    const invalidSidebar = { agents: [{
      id: 'agent-1', name: 'worker', thread_id: 'thread-1',
      assignment: { title: '任务', description: '验证 bootstrap', assignedAt: '2026-07-28T16:00:00Z' },
      progress: { status: '', currentStep: null, completedSteps: null, totalSteps: null, updatedAt: '0001-01-01T00:00:00Z' },
      outcome: null,
    }] };
    let sidebarReads = 0;
    const client = {
      close() {},
      async request(method) {
        if (method === 'ui/sidebar/get') return sidebarReads++ === 0 ? { agents: [] } : invalidSidebar;
        if (method === 'thread/start') return { threadId: 'thread-1' };
        return {};
      },
    };
    const config = { ...smokeConfig({}, '/repo/app', () => 'token'), skipFrontendBuild: true };
    const spawn = () => Object.assign(new EventEmitter(), { pid: 1234, exitCode: null, signalCode: null });
    await expect(runDesktopSmoke(config, {
      spawn, waitForHTTP: async () => {}, openWSRPC: async () => client, stopDesktop: async () => {},
    })).rejects.toThrow(/progress\.status/);
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

  it('uses Xvfb only for an explicit Linux CI smoke', () => {
    const linuxCI = smokeConfig({ CI: 'true' }, '/repo/app', () => 'token', 'linux');
    expect(linuxCI.headlessDisplay).toBe(true);
    expect(desktopRunnerInvocation({ ...linuxCI, runner: '/repo/app/run-new-ui-desktop.sh' })).toEqual({
      command: 'xvfb-run',
      args: ['-a', '/repo/app/run-new-ui-desktop.sh'],
    });

    const localLinux = smokeConfig({}, '/repo/app', () => 'token', 'linux');
    const darwinCI = smokeConfig({ CI: 'true' }, '/repo/app', () => 'token', 'darwin');
    expect(localLinux.headlessDisplay).toBe(false);
    expect(darwinCI.headlessDisplay).toBe(false);
    expect(desktopRunnerInvocation({ ...darwinCI, runner: '/repo/app/run-new-ui-desktop.sh' })).toEqual({
      command: '/repo/app/run-new-ui-desktop.sh',
      args: [],
    });
  });

  it('isolates the desktop wrapper in a process group on POSIX', () => {
    const config = smokeConfig({ CI: 'true' }, '/repo/app', () => 'token', 'linux');
    expect(desktopSpawnOptions(config, 'linux').detached).toBe(true);
    expect(desktopSpawnOptions(config, 'darwin').detached).toBe(true);
    expect(desktopSpawnOptions(config, 'win32').detached).toBe(false);
  });

  it('terminates the desktop process group and any lingering descendants', async () => {
    const child = Object.assign(new EventEmitter(), {
      pid: 1234,
      exitCode: null,
      signalCode: null,
    });
    const calls = [];
    await stopDesktop(child, {
      terminateProcessTree: async (target, options) => {
        calls.push({ target, options });
      },
    });
    expect(calls).toEqual([{ target: child, options: { killGraceMs: 10_000 } }]);
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

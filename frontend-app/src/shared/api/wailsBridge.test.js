import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { beginTextClipboardWrite, copyTextToClipboard } from './wailsBridge.js';

const runtimeModule = 'http://127.0.0.1:5175/wails/runtime.js';

function captureBridgeLogs(registerBridgeLogStore) {
  const logs = [];
  const write = (level) => (event, fields) => logs.push({ level, event, fields });
  registerBridgeLogStore({
    debug: write('debug'),
    error: write('error'),
    info: write('info'),
    warn: write('warn'),
  });
  return logs;
}

describe('wails bridge clipboard helpers', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__WAILS_SHIM_DEBUG__;
    delete document.execCommand;
  });

  it('starts a clipboard write synchronously and commits text after async data is ready', async () => {
    let copiedText = '';
    class TestClipboardItem {
      constructor(items) {
        this.items = items;
      }

      getType(type) {
        return this.items[type];
      }
    }
    class TestBlob {
      constructor(parts, options = {}) {
        this.parts = parts;
        this.type = options.type || '';
      }
    }
    const write = vi.fn(async ([item]) => {
      const blob = await item.getType('text/plain');
      copiedText = blob.parts.join('');
    });
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { write },
    });
    vi.stubGlobal('ClipboardItem', TestClipboardItem);
    vi.stubGlobal('Blob', TestBlob);

    const prepared = beginTextClipboardWrite();

    expect(write).toHaveBeenCalledTimes(1);
    await expect(prepared.commit('thread info')).resolves.toBe(true);
    expect(copiedText).toBe('thread info');
  });

  it('falls back to a focused textarea copy when async clipboard is unavailable', async () => {
    window.__WAILS_SHIM_DEBUG__ = true;
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    });
    document.execCommand = vi.fn(() => true);

    await expect(copyTextToClipboard('fallback text')).resolves.toBe(true);

    expect(document.execCommand).toHaveBeenCalledWith('copy');
    expect(document.querySelector('textarea')).toBeNull();
  });

  it('throws concrete clipboard failures instead of returning false', async () => {
    window.__WAILS_SHIM_DEBUG__ = true;
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: vi.fn().mockRejectedValue(new Error('The request is not allowed')),
      },
    });
    document.execCommand = vi.fn(() => false);

    await expect(copyTextToClipboard('failfast text')).rejects.toThrow(
      "clipboard copy failed: browser clipboard.writeText failed: The request is not allowed; document.execCommand fallback failed: document.execCommand('copy') returned false",
    );
  });
});

describe('wails bridge warning logs', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.doUnmock(runtimeModule);
  });

  it('reports a failed backend RPC once as api.rpc.failed', async () => {
    const error = new Error('backend unavailable');
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('thread/config/get', { threadId: 'thread-1' })).rejects.toThrow('backend unavailable');

    const errorEvents = logs.filter((entry) => entry.level === 'error').map((entry) => entry.event);
    expect(errorEvents).toEqual(['api.rpc.failed']);
    expect(errorEvents).not.toContain('bridge.call.failed');
  });

  it('records failed backend RPC details with a serializable error message', async () => {
    const error = new Error('backend unavailable');
    error.code = 'ECONNREFUSED';
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('thread/config/get', { threadId: 'thread-1' })).rejects.toThrow('backend unavailable');

    const failure = logs.find((entry) => entry.event === 'api.rpc.failed');
    expect(failure.fields).toEqual(expect.objectContaining({
      method: 'thread/config/get',
      error: expect.objectContaining({
        message: 'backend unavailable',
        code: 'ECONNREFUSED',
      }),
    }));
    expect(JSON.stringify(failure.fields)).toContain('backend unavailable');
  });

  it('records a compact backend RPC return preview on successful calls', async () => {
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockResolvedValue({ ok: true, tool: 'mcp__lsp__grep', result: { total: 3 } }) },
      Events: { On: vi.fn() },
    }));
    const { callAPI, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(callAPI('tools/call', { name: 'mcp__lsp__grep' })).resolves.toEqual({
      ok: true,
      tool: 'mcp__lsp__grep',
      result: { total: 3 },
    });

    const done = logs.find((entry) => entry.event === 'api.rpc.done');
    expect(done.fields).toEqual(expect.objectContaining({
      method: 'tools/call',
      result_preview: expect.stringContaining('"total":3'),
    }));
  });

  it('keeps bridge.call.failed for non-RPC bridge binding failures', async () => {
    const error = new Error('native binding unavailable');
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { getBuildInfo, registerBridgeLogStore } = await import('./wailsBridge.js');
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(getBuildInfo()).rejects.toThrow('native binding unavailable');

    const errorEvents = logs.filter((entry) => entry.level === 'error').map((entry) => entry.event);
    expect(errorEvents).toEqual(['bridge.call.failed']);
  });
});

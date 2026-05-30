// @ts-nocheck
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, test, vi, beforeEach, afterEach } from 'vitest';

const bridgePath = fileURLToPath(new URL('./wailsBridge.js', import.meta.url));

const runtimeMock = vi.hoisted(() => ({
  byId: vi.fn(),
  eventsOn: vi.fn(),
}));

let callAPI, normalizeRuntimeEventEnvelope, saveTextFile;

describe('Wails Bridge and API tests', () => {
  beforeEach(async () => {
    vi.resetModules();
    runtimeMock.byId.mockReset();
    runtimeMock.eventsOn.mockReset();

    // Set standard mock of /wails/runtime.js for each test
    vi.doMock('/wails/runtime.js', () => ({
      Call: { ByID: runtimeMock.byId },
      Events: {
        On: runtimeMock.eventsOn,
        Off: vi.fn(),
      },
    }), { virtual: true });

    vi.stubGlobal('window', {
      __WAILS_SHIM_DEBUG__: true,
      location: { pathname: '/test-route' },
    });

    const bridge = await import('./wailsBridge.js');
    callAPI = bridge.callAPI;
    normalizeRuntimeEventEnvelope = bridge.normalizeRuntimeEventEnvelope;
    saveTextFile = bridge.saveTextFile;
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('keeps the Wails runtime import out of Vite import analysis', () => {
    const source = readFileSync(bridgePath, 'utf8');
    expect(source).toContain('@vite-ignore');
    expect(source).not.toContain("import('/wails/runtime.js')");
    expect(source).toContain('resolveRuntimeModuleSpecifier()');
    expect(source).toContain('window.location.origin');
  });

  test('ships the dev runtime shim from public/wails/runtime.js', () => {
    const runtimePath = fileURLToPath(new URL('../../../public/wails/runtime.js', import.meta.url));
    const source = readFileSync(runtimePath, 'utf8');

    expect(source).toContain('Development Wails runtime shim');
    expect(source).toContain("const WS_PATH = '/wails/ws'");
    expect(source).toContain('ByID: callByID');
  });

  describe('callAPI validation and error handling (fail-fast)', () => {
    test('rejects non-object params with TypeError', async () => {
      await expect(callAPI('test/method', 'string')).rejects.toThrow(TypeError);
      await expect(callAPI('test/method', 123)).rejects.toThrow(TypeError);
      await expect(callAPI('test/method', [])).rejects.toThrow(TypeError);
      await expect(callAPI('test/method', true)).rejects.toThrow(TypeError);
    });

    test('accepts valid object params or null/undefined', async () => {
      runtimeMock.byId.mockResolvedValue({ ok: true });

      await expect(callAPI('test/method', { foo: 'bar' })).resolves.toEqual({ ok: true });
      await expect(callAPI('test/method', null)).resolves.toEqual({ ok: true });
      await expect(callAPI('test/method', undefined)).resolves.toEqual({ ok: true });
    });

    test('fails fast (throws Error) if method is missing or empty', async () => {
      await expect(callAPI('')).rejects.toThrow('callAPI method must be a non-empty string');
      await expect(callAPI('   ')).rejects.toThrow('callAPI method must be a non-empty string');
      await expect(callAPI(null)).rejects.toThrow('callAPI method must be a non-empty string');
      await expect(callAPI(undefined)).rejects.toThrow('callAPI method must be a non-empty string');
    });

    test('fails fast (throws Error) when Wails runtime is not ready', async () => {
      // Mock runtime module loading to return object without Call.ByID
      vi.doMock('/wails/runtime.js', () => ({
        Call: {},
        Events: {},
      }), { virtual: true });

      vi.resetModules();
      const bridge = await import('./wailsBridge.js');
      const callAPINoRuntime = bridge.callAPI;

      await expect(callAPINoRuntime('test/method', {})).rejects.toThrow('Wails runtime bridge not ready');
    });

    test('saveTextFile fails fast (throws Error) if defaultFilename is missing', async () => {
      await expect(saveTextFile({ defaultPath: '/tmp' })).rejects.toThrow('saveTextFile defaultFilename is required');
      await expect(saveTextFile({ defaultFilename: '' })).rejects.toThrow('saveTextFile defaultFilename is required');
    });
  });

  describe('unique _aoRequestId injection', () => {
    test('generates incrementing unique _aoRequestId for each call', async () => {
      runtimeMock.byId.mockResolvedValue({ ok: true });

      await callAPI('test/method1', { val: 1 });
      await callAPI('test/method2', { val: 2 });

      expect(runtimeMock.byId).toHaveBeenCalledTimes(2);

      // Verify first call payload had _aoRequestId: 1
      const call1Payload = runtimeMock.byId.mock.calls[0][2];
      expect(call1Payload).toMatchObject({
        val: 1,
        _aoClientKind: 'web-debug-shim',
        _aoClientRoute: '/test-route',
        _aoRequestId: 1,
      });

      // Verify second call payload had _aoRequestId: 2
      const call2Payload = runtimeMock.byId.mock.calls[1][2];
      expect(call2Payload).toMatchObject({
        val: 2,
        _aoClientKind: 'web-debug-shim',
        _aoClientRoute: '/test-route',
        _aoRequestId: 2,
      });
    });
  });

  describe('preservation of 19-digit IDs', () => {
    test('preserves 19-digit nanosecond integer IDs as strings in raw JSON event data', () => {
      // Normally, JSON.parse('{"id":1778748074684743001}') parses to 1778748074684743000 due to JS float precision.
      // Our regex replacement wraps it in quotes first.

      const evt = {
        name: 'test-event',
        data: '{"id":1778748074684743001,"list":[1778748074684743002],"nested":{"agent_id":1778748074684743003}}',
      };

      const normalized = normalizeRuntimeEventEnvelope(evt);

      expect(normalized.id).toBe('1778748074684743001');
      expect(normalized.list[0]).toBe('1778748074684743002');
      expect(normalized.nested.agent_id).toBe('1778748074684743003');
    });

    test('leaves existing string IDs or small safe integers untouched', () => {
      const evt = {
        name: 'test-event',
        data: '{"id":"already-string-123","safe_num":42,"normal_str":"hello"}',
      };

      const normalized = normalizeRuntimeEventEnvelope(evt);

      expect(normalized.id).toBe('already-string-123');
      expect(normalized.safe_num).toBe(42);
      expect(normalized.normal_str).toBe('hello');
    });
  });
});

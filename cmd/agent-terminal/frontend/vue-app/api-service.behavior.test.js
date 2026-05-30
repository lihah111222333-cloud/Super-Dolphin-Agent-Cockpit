// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const runtimeMock = vi.hoisted(() => ({
  byId: vi.fn(),
}));

vi.mock('/wails/runtime.js', () => ({
  Call: { ByID: runtimeMock.byId },
  Events: {
    On: vi.fn(),
    Off: vi.fn(),
  },
}), { virtual: true });

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
  registerLogBridgeSink: vi.fn(),
}));

import {
  callAPI,
  copyTextToClipboard,
  normalizeRuntimeEventEnvelope,
  readDroppedTextFiles,
  resolveThreadIdentity,
  saveTextFile,
  selectProjectDir,
} from './services/api.js';

beforeEach(() => {
  runtimeMock.byId.mockReset();
  vi.stubGlobal('window', { __WAILS_SHIM_DEBUG__: true, location: { pathname: '/test' } });
  vi.stubGlobal('navigator', { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
  vi.stubGlobal('document', {
    body: {
      appendChild: vi.fn(),
      removeChild: vi.fn(),
    },
    createElement: vi.fn(() => ({
      style: {},
      select: vi.fn(),
    })),
    execCommand: vi.fn(() => true),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});


describe('api service behavior', () => {
  it('normalizes raw and Wails-style runtime event envelopes', () => {
    expect(normalizeRuntimeEventEnvelope({ type: 'raw' })).toEqual({ type: 'raw' });
    expect(normalizeRuntimeEventEnvelope({ name: 'evt', data: '{"type":"bridge"}' })).toEqual({ type: 'bridge' });
    expect(normalizeRuntimeEventEnvelope({ name: 'evt', data: { type: 'agent' } })).toEqual({ type: 'agent' });
    expect(normalizeRuntimeEventEnvelope({ name: 'evt', data: '' })).toEqual({});
  });

  it('rejects non-object RPC params before touching the runtime bridge', async () => {
    await expect(callAPI('thread/list', [])).rejects.toThrow('callAPI params must be an object');
    await expect(callAPI('thread/list', 'bad')).rejects.toThrow('callAPI params must be an object');
  });

  it('falls back to RPC project directory selection when direct binding is unavailable', async () => {
    runtimeMock.byId
      .mockRejectedValueOnce(new Error('binding missing'))
      .mockResolvedValueOnce({ path: '/tmp/from-rpc' });

    await expect(selectProjectDir()).resolves.toBe('/tmp/from-rpc');

    expect(runtimeMock.byId).toHaveBeenNthCalledWith(1, 3694631468);
    expect(runtimeMock.byId).toHaveBeenNthCalledWith(
      2,
      2963398832,
      'ui/selectProjectDir',
      expect.objectContaining({
        defaultPath: '',
        _aoClientKind: 'web-debug-shim',
        _aoClientRoute: '/test',
      }),
    );
  });

  it('reads recently dropped text files through the RPC bridge', async () => {
    runtimeMock.byId.mockResolvedValueOnce({
      files: [{ path: '/tmp/notes.md', name: 'notes.md', text: 'hello', sizeBytes: 5 }],
    });

    await expect(readDroppedTextFiles(['/tmp/notes.md'], 'prompt-intent-drop-zone')).resolves.toEqual([
      { path: '/tmp/notes.md', name: 'notes.md', text: 'hello', sizeBytes: 5 },
    ]);
    expect(runtimeMock.byId).toHaveBeenCalledWith(
      2963398832,
      'ui/readDroppedTextFiles',
      expect.objectContaining({
        files: ['/tmp/notes.md'],
        targetId: 'prompt-intent-drop-zone',
        _aoClientKind: 'web-debug-shim',
        _aoClientRoute: '/test',
      }),
    );
  });

  it('saves text files through the native save RPC', async () => {
    runtimeMock.byId.mockResolvedValueOnce({ path: '/tmp/final-report.md' });

    await expect(saveTextFile({
      defaultPath: '/repo',
      defaultFilename: 'final-report.md',
      content: '# Final report\nready',
    })).resolves.toBe('/tmp/final-report.md');

    expect(runtimeMock.byId).toHaveBeenCalledWith(
      2963398832,
      'ui/saveTextFile',
      expect.objectContaining({
        defaultPath: '/repo',
        defaultFilename: 'final-report.md',
        content: '# Final report\nready',
        _aoClientKind: 'web-debug-shim',
        _aoClientRoute: '/test',
      }),
    );
  });

  it('copies text through browser clipboard in debug shim mode', async () => {
    await expect(copyTextToClipboard('hello')).resolves.toBe(true);
    expect(globalThis.navigator.clipboard.writeText).toHaveBeenCalledWith('hello');
  });
  it('falls back to execCommand when clipboard api is unavailable', async () => {
    vi.stubGlobal('navigator', {});
    await expect(copyTextToClipboard('fallback')).resolves.toBe(true);
    expect(globalThis.document.execCommand).toHaveBeenCalledWith('copy');
  });


  it('returns an empty identity object for blank thread ids', async () => {
    await expect(resolveThreadIdentity('   ')).resolves.toEqual({});
  });
});

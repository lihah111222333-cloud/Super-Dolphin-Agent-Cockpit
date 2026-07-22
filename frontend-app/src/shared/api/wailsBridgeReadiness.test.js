import { beforeEach, describe, expect, it, vi } from 'vitest';

const runtimeModule = '/wails/runtime.js';

function resetWailsRuntimeMocks() {
  vi.resetModules();
  vi.doUnmock(runtimeModule);
}

describe('wails frontend readiness handshake', () => {
  beforeEach(resetWailsRuntimeMocks);

  it('commits the current epoch only after a successful Wails ByID probe', async () => {
    const byID = vi.fn()
      .mockResolvedValueOnce({ epoch: 7 })
      .mockResolvedValueOnce({ epoch: 7 });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { reportFrontendReadiness } = await import('./wails/wailsBridgeRpc.js');

    await expect(reportFrontendReadiness({ documentRef: { readyState: 'complete' } })).resolves.toBe(7);

    const readinessCalls = byID.mock.calls.filter(([, method]) => method === 'ui/frontend/readiness');
    expect(readinessCalls).toHaveLength(2);
    expect(readinessCalls[0][2]).toEqual(expect.objectContaining({ phase: 'probe' }));
    expect(readinessCalls[1][2]).toEqual(expect.objectContaining({ phase: 'commit', epoch: 7 }));
  });

  it('does not commit when the Wails bridge probe fails', async () => {
    const byID = vi.fn().mockRejectedValue(new Error('bridge unavailable'));
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { reportFrontendReadiness } = await import('./wails/wailsBridgeRpc.js');

    await expect(reportFrontendReadiness({ documentRef: { readyState: 'complete' } })).rejects.toThrow('bridge unavailable');

    const readinessCalls = byID.mock.calls.filter(([, method]) => method === 'ui/frontend/readiness');
    expect(readinessCalls).toHaveLength(1);
    expect(readinessCalls[0][2]).toEqual(expect.objectContaining({ phase: 'probe' }));
  });

  it('rejects unknown readiness response fields before committing', async () => {
    const byID = vi.fn().mockResolvedValue({ epoch: 7, unexpected: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { reportFrontendReadiness } = await import('./wails/wailsBridgeRpc.js');

    await expect(reportFrontendReadiness({ documentRef: { readyState: 'complete' } }))
      .rejects.toThrow('frontend readiness probe response must contain only epoch');

    const readinessCalls = byID.mock.calls.filter(([, method]) => method === 'ui/frontend/readiness');
    expect(readinessCalls).toHaveLength(1);
    expect(readinessCalls[0][2]).toEqual(expect.objectContaining({ phase: 'probe' }));
  });

  it('rejects unknown readiness fields in the commit response', async () => {
    const byID = vi.fn()
      .mockResolvedValueOnce({ epoch: 7 })
      .mockResolvedValueOnce({ epoch: 7, unexpected: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { reportFrontendReadiness } = await import('./wails/wailsBridgeRpc.js');

    await expect(reportFrontendReadiness({ documentRef: { readyState: 'complete' } }))
      .rejects.toThrow('frontend readiness commit response must contain only epoch');

    const readinessCalls = byID.mock.calls.filter(([, method]) => method === 'ui/frontend/readiness');
    expect(readinessCalls).toHaveLength(2);
    expect(readinessCalls[1][2]).toEqual(expect.objectContaining({ phase: 'commit', epoch: 7 }));
  });

  it('does not call the bridge before the page load boundary', async () => {
    const byID = vi.fn();
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { reportFrontendReadiness } = await import('./wails/wailsBridgeRpc.js');

    await expect(reportFrontendReadiness({ documentRef: { readyState: 'loading' } }))
      .rejects.toThrow('frontend page load is required before readiness');
    expect(byID).not.toHaveBeenCalled();
  });
});

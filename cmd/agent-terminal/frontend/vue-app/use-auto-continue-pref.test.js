// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./services/api.js', () => ({ callAPI: vi.fn() }));
vi.mock('./services/log.js', () => ({ logWarn: vi.fn(), logInfo: vi.fn(), logDebug: vi.fn(), logError: vi.fn() }));

const { callAPI } = await import('./services/api.js');
const { logWarn } = await import('./services/log.js');
const {
  isValidAutoContinuePref,
  loadAutoContinuePref,
  saveAutoContinuePref,
  useAutoContinuePref,
  _resetAutoContinuePrefForTest,
} = await import('./composables/useAutoContinuePref.js');

beforeEach(() => {
  vi.mocked(callAPI).mockReset();
  vi.mocked(logWarn).mockReset();
  _resetAutoContinuePrefForTest();
});
afterEach(() => { vi.restoreAllMocks(); });

describe('isValidAutoContinuePref', () => {
  it('accepts booleans', () => {
    expect(isValidAutoContinuePref(true)).toBe(true);
    expect(isValidAutoContinuePref(false)).toBe(true);
  });
  it('rejects non-booleans', () => {
    expect(isValidAutoContinuePref(null)).toBe(false);
    expect(isValidAutoContinuePref(undefined)).toBe(false);
    expect(isValidAutoContinuePref(0)).toBe(false);
    expect(isValidAutoContinuePref(1)).toBe(false);
    expect(isValidAutoContinuePref('true')).toBe(false);
    expect(isValidAutoContinuePref({})).toBe(false);
    expect(isValidAutoContinuePref([])).toBe(false);
  });
});

describe('loadAutoContinuePref', () => {
  it('returns default (true) when callAPI returns null', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce(null);
    const result = await loadAutoContinuePref();
    expect(result).toBe(true);
  });
  it('uses preference value when valid (false)', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce(false);
    const result = await loadAutoContinuePref();
    expect(result).toBe(false);
  });
  it('uses preference value when valid (true)', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce(true);
    const result = await loadAutoContinuePref();
    expect(result).toBe(true);
  });
  it('falls back to default and warns when preference invalid (non-boolean)', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce('yes');
    const result = await loadAutoContinuePref();
    expect(result).toBe(true);
    expect(logWarn).toHaveBeenCalledWith('ui', 'autoContinuePref.invalid', { value: 'yes' });
  });
  it('falls back to default and warns when callAPI rejects', async () => {
    vi.mocked(callAPI).mockRejectedValueOnce(new Error('network down'));
    const result = await loadAutoContinuePref();
    expect(result).toBe(true);
    expect(logWarn).toHaveBeenCalledWith('ui', 'autoContinuePref.load_failed', { error: 'network down' });
  });
  it('is idempotent (memoized): second call does not re-fetch', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce(false);
    await loadAutoContinuePref();
    await loadAutoContinuePref();
    expect(callAPI).toHaveBeenCalledTimes(1);
  });
});

describe('saveAutoContinuePref', () => {
  it('rejects invalid values without calling API', async () => {
    await expect(saveAutoContinuePref('true')).rejects.toThrow();
    await expect(saveAutoContinuePref(1)).rejects.toThrow();
    await expect(saveAutoContinuePref(null)).rejects.toThrow();
    expect(callAPI).not.toHaveBeenCalled();
  });
  it('persists valid values and updates the shared ref', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce(undefined);
    await saveAutoContinuePref(false);
    expect(callAPI).toHaveBeenCalledWith('ui/preferences/set', { key: 'taskHandoff.autoContinueOnAlert', value: false });
    const ref = useAutoContinuePref();
    expect(ref.value).toBe(false);
  });
});

describe('useAutoContinuePref', () => {
  it('returns the shared module ref so settings save reflects in consumers', async () => {
    const ref1 = useAutoContinuePref();
    const ref2 = useAutoContinuePref();
    expect(ref1).toBe(ref2);
    vi.mocked(callAPI).mockResolvedValueOnce(undefined);
    await saveAutoContinuePref(false);
    expect(ref1.value).toBe(false);
    expect(ref2.value).toBe(false);
  });
  it('default value before any save is true', () => {
    const ref = useAutoContinuePref();
    expect(ref.value).toBe(true);
  });
});

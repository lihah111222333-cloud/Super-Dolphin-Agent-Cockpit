// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./services/api.js', () => ({ callAPI: vi.fn() }));
vi.mock('./services/log.js', () => ({ logWarn: vi.fn(), logInfo: vi.fn(), logDebug: vi.fn(), logError: vi.fn() }));

const { callAPI } = await import('./services/api.js');
const {
  isValidThresholds,
  loadContextUsageThresholds,
  saveContextUsageThresholds,
  useContextUsageThresholds,
  _resetContextUsageThresholdsForTest,
} = await import('./composables/useContextUsageThresholds.js');

beforeEach(() => {
  vi.mocked(callAPI).mockReset();
  _resetContextUsageThresholdsForTest();
});
afterEach(() => { vi.restoreAllMocks(); });

describe('isValidThresholds', () => {
  it('rejects non-arrays', () => {
    expect(isValidThresholds(null)).toBe(false);
    expect(isValidThresholds('70,85,95')).toBe(false);
    expect(isValidThresholds({})).toBe(false);
  });
  it('rejects wrong length', () => {
    expect(isValidThresholds([])).toBe(false);
    expect(isValidThresholds([70, 85])).toBe(false);
    expect(isValidThresholds([70, 80, 90, 95])).toBe(false);
  });
  it('rejects out-of-range values', () => {
    expect(isValidThresholds([0, 50, 95])).toBe(false);
    expect(isValidThresholds([70, 85, 100])).toBe(false);
    expect(isValidThresholds([-10, 50, 90])).toBe(false);
    expect(isValidThresholds([70, 85, NaN])).toBe(false);
  });
  it('rejects non-strict-ascending', () => {
    expect(isValidThresholds([85, 70, 95])).toBe(false);
    expect(isValidThresholds([70, 70, 95])).toBe(false);
    expect(isValidThresholds([70, 95, 85])).toBe(false);
  });
  it('accepts valid 3-tuple in (0,100) ascending', () => {
    expect(isValidThresholds([70, 85, 95])).toBe(true);
    expect(isValidThresholds([10, 50, 90])).toBe(true);
    expect(isValidThresholds(['70', '85', '95'])).toBe(true); // numeric coercion ok
  });
});

describe('loadContextUsageThresholds', () => {
  it('returns defaults when callAPI returns null', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce(null);
    const result = await loadContextUsageThresholds();
    expect(result).toEqual([70, 85, 95]);
  });
  it('uses preference value when valid', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce([60, 75, 90]);
    const result = await loadContextUsageThresholds();
    expect(result).toEqual([60, 75, 90]);
  });
  it('falls back to defaults when preference invalid', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce([200, 5, 50]);
    const result = await loadContextUsageThresholds();
    expect(result).toEqual([70, 85, 95]);
  });
  it('falls back to defaults when callAPI rejects', async () => {
    vi.mocked(callAPI).mockRejectedValueOnce(new Error('network down'));
    const result = await loadContextUsageThresholds();
    expect(result).toEqual([70, 85, 95]);
  });
  it('is idempotent (memoized): second call does not re-fetch', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce([60, 75, 90]);
    await loadContextUsageThresholds();
    await loadContextUsageThresholds();
    expect(callAPI).toHaveBeenCalledTimes(1);
  });
});

describe('saveContextUsageThresholds', () => {
  it('rejects invalid values without calling API', async () => {
    await expect(saveContextUsageThresholds([200, 5, 50])).rejects.toThrow();
    expect(callAPI).not.toHaveBeenCalled();
  });
  it('persists valid values and updates the shared ref', async () => {
    vi.mocked(callAPI).mockResolvedValueOnce(undefined);
    await saveContextUsageThresholds([60, 75, 90]);
    expect(callAPI).toHaveBeenCalledWith('ui/preferences/set', { key: 'contextUsageAlerts.thresholds', value: [60, 75, 90] });
    const ref = useContextUsageThresholds();
    expect(ref.value).toEqual([60, 75, 90]);
  });
});

describe('useContextUsageThresholds', () => {
  it('returns the shared module ref so settings save reflects in consumers', async () => {
    const ref1 = useContextUsageThresholds();
    const ref2 = useContextUsageThresholds();
    expect(ref1).toBe(ref2);
    vi.mocked(callAPI).mockResolvedValueOnce(undefined);
    await saveContextUsageThresholds([55, 70, 88]);
    expect(ref1.value).toEqual([55, 70, 88]);
    expect(ref2.value).toEqual([55, 70, 88]);
  });
});

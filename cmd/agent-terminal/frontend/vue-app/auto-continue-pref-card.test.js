// @ts-nocheck
// 纯 setup 单元测试（无 DOM 依赖）
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ref } from '../lib/vue.esm-browser.prod.js';

vi.mock('./services/log.js', () => ({ logWarn: vi.fn(), logInfo: vi.fn(), logDebug: vi.fn(), logError: vi.fn() }));
vi.mock('./composables/useAutoContinuePref.js', () => {
  const r = ref(true);
  return {
    useAutoContinuePref: () => r,
    loadAutoContinuePref: vi.fn().mockResolvedValue(true),
    saveAutoContinuePref: vi.fn().mockResolvedValue(undefined),
    isValidAutoContinuePref: () => true,
    _setAutoContinuePrefForTest: (v) => { r.value = v; },
  };
});

const prefMod = await import('./composables/useAutoContinuePref.js');
const { logWarn } = await import('./services/log.js');
const { AutoContinuePrefCard } = await import('./components/AutoContinuePrefCard.js');

beforeEach(() => {
  vi.mocked(prefMod.saveAutoContinuePref).mockReset().mockResolvedValue(undefined);
  vi.mocked(logWarn).mockReset();
  prefMod._setAutoContinuePrefForTest(true); // R3 fix：明确重置 default
});
afterEach(() => { vi.restoreAllMocks(); });

describe('AutoContinuePrefCard · setup', () => {
  it('exposes enabledRef wired to useAutoContinuePref', () => {
    const exposed = AutoContinuePrefCard.setup();
    expect(exposed.enabledRef.value).toBe(true);
    prefMod._setAutoContinuePrefForTest(false);
    expect(exposed.enabledRef.value).toBe(false);
  });

  it('saving and error are reactive refs initially false / empty', () => {
    const { saving, error } = AutoContinuePrefCard.setup();
    expect(saving.value).toBe(false);
    expect(error.value).toBe('');
  });
});

describe('AutoContinuePrefCard · onToggle', () => {
  it('calls saveAutoContinuePref with checkbox checked value', async () => {
    const { onToggle } = AutoContinuePrefCard.setup();
    await onToggle({ target: { checked: false } });
    expect(prefMod.saveAutoContinuePref).toHaveBeenCalledWith(false);
  });

  it('passes true when checkbox checked', async () => {
    const { onToggle } = AutoContinuePrefCard.setup();
    await onToggle({ target: { checked: true } });
    expect(prefMod.saveAutoContinuePref).toHaveBeenCalledWith(true);
  });

  it('toggles saving flag during async save', async () => {
    let release;
    vi.mocked(prefMod.saveAutoContinuePref).mockImplementation(() => new Promise((r) => { release = r; }));
    const { onToggle, saving } = AutoContinuePrefCard.setup();
    const promise = onToggle({ target: { checked: false } });
    expect(saving.value).toBe(true);
    release();
    await promise;
    expect(saving.value).toBe(false);
  });

  it('records error on save failure and logs warning', async () => {
    vi.mocked(prefMod.saveAutoContinuePref).mockRejectedValueOnce(new Error('boom'));
    const { onToggle, error, saving } = AutoContinuePrefCard.setup();
    await onToggle({ target: { checked: false } });
    expect(error.value).toBe('boom');
    expect(saving.value).toBe(false);
    expect(logWarn).toHaveBeenCalledWith('ui', 'autoContinuePrefCard.save_failed', expect.objectContaining({
      value: false, error: 'boom',
    }));
  });

  it('clears previous error on successful save', async () => {
    vi.mocked(prefMod.saveAutoContinuePref).mockRejectedValueOnce(new Error('first fail'));
    const { onToggle, error } = AutoContinuePrefCard.setup();
    await onToggle({ target: { checked: false } });
    expect(error.value).toBe('first fail');
    vi.mocked(prefMod.saveAutoContinuePref).mockResolvedValueOnce(undefined);
    await onToggle({ target: { checked: true } });
    expect(error.value).toBe('');
  });
});

describe('AutoContinuePrefCard · component shape', () => {
  it('template references checkbox testid + role', () => {
    expect(AutoContinuePrefCard.template).toContain('auto-continue-pref-card');
    expect(AutoContinuePrefCard.template).toContain('auto-continue-pref-checkbox');
    expect(AutoContinuePrefCard.template).toContain('@change="onToggle"');
  });

  it('mentions the boundary 不影响普通对话 in template', () => {
    expect(AutoContinuePrefCard.template).toContain('不影响普通对话');
  });
});

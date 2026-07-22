import { describe, expect, it, vi } from 'vitest';
import { createShellLayoutStore } from './useShellLayoutStore.js';
import { rightPanelWidthSchema } from './shellLayoutSchema.js';
import shellLayoutStoreSource from './useShellLayoutStore.js?raw';

function createStorage(initialValue = null) {
  let storedValue = initialValue;
  return {
    get: vi.fn(() => storedValue),
    set: vi.fn((_key, value) => { storedValue = value; }),
    remove: vi.fn(() => { storedValue = null; }),
    value: () => storedValue,
  };
}

function expectRightPanelWidthValidationFailure(run) {
  let failure;
  try {
    run();
  }
  catch (error) {
    failure = error;
  }
  expect(failure).toMatchObject({
    name: 'ShellLayoutValidationError',
    code: 'shell_layout.invalid_right_panel_width',
  });
}

describe('createShellLayoutStore', () => {
  it('uses the injected scalar port without JSON parsing or a global singleton contract', () => {
    expect(shellLayoutStoreSource).not.toContain('JSON.parse');
    expect(shellLayoutStoreSource).not.toContain('window.localStorage');
  });

  it('treats a missing key as first-run and persists the schema initial value', () => {
    const storage = createStorage();
    const store = createShellLayoutStore({ storage });

    expect(storage.get).toHaveBeenCalledExactlyOnceWith(rightPanelWidthSchema.key);
    expect(storage.set).toHaveBeenCalledExactlyOnceWith(
      rightPanelWidthSchema.key,
      rightPanelWidthSchema.serialize(rightPanelWidthSchema.initialValue),
    );
    expect(storage.remove).not.toHaveBeenCalled();
    expect(store.getState().rightPanelWidth).toBe(rightPanelWidthSchema.initialValue);
    expect(store.getState().resetShellLayout).toBeUndefined();
  });

  it('persists a strict scalar before state changes and roundtrips it into a new store', () => {
    const storage = createStorage();
    const firstStore = createShellLayoutStore({ storage });

    firstStore.getState().setRightPanelWidth(480.5);
    expect(storage.value()).toBe('480.5');
    expect(firstStore.getState().rightPanelWidth).toBe(480.5);

    const secondStore = createShellLayoutStore({ storage });
    expect(secondStore.getState().rightPanelWidth).toBe(480.5);
    expect(storage.remove).not.toHaveBeenCalled();
  });

  it.each([
    '',
    '0380',
    '-1',
    'NaN',
    'Infinity',
    '1e3',
    '380px',
    String(Number.MAX_SAFE_INTEGER + 1),
  ])('blocks invalid existing scalar %j without mutating or removing it', (invalidValue) => {
    const storage = createStorage(invalidValue);

    expectRightPanelWidthValidationFailure(() => createShellLayoutStore({ storage }));
    expect(storage.value()).toBe(invalidValue);
    expect(storage.set).not.toHaveBeenCalled();
    expect(storage.remove).not.toHaveBeenCalled();
  });

  it('propagates storage get failure before any mutation', () => {
    const storage = createStorage();
    storage.get.mockImplementation(() => { throw new Error('layout read failed'); });

    expect(() => createShellLayoutStore({ storage })).toThrow('layout read failed');
    expect(storage.set).not.toHaveBeenCalled();
    expect(storage.remove).not.toHaveBeenCalled();
  });

  it('blocks first-run initialization when the initial scalar cannot be persisted', () => {
    const storage = createStorage();
    storage.set.mockImplementation(() => { throw new Error('layout write failed'); });

    expect(() => createShellLayoutStore({ storage })).toThrow('layout write failed');
    expect(storage.remove).not.toHaveBeenCalled();
  });

  it('keeps state unchanged when a later persistence write fails', () => {
    const storage = createStorage('380');
    const store = createShellLayoutStore({ storage });
    storage.set.mockImplementation(() => { throw new Error('layout write failed'); });

    expect(() => store.getState().setRightPanelWidth(480)).toThrow('layout write failed');
    expect(store.getState().rightPanelWidth).toBe(380);
    expect(storage.value()).toBe('380');
    expect(storage.remove).not.toHaveBeenCalled();
  });

  it.each([
    ['get', undefined],
    ['set', null],
    ['remove', 'not-a-function'],
  ])('fails fast before initialization when storage.%s is not a function', (method, invalidValue) => {
    const storage = createStorage('380');
    const originalGet = storage.get;
    storage[method] = invalidValue;

    expect(() => createShellLayoutStore({ storage })).toThrow();
    expect(originalGet).not.toHaveBeenCalled();
  });
});

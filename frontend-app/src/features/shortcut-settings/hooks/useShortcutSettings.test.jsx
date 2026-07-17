import React from 'react';
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { defineAppCommandRegistry } from '../../../app/commands/appCommandRegistry.js';
import { useShortcutSettings } from './useShortcutSettings.js';

const registry = defineAppCommandRegistry([
  {
    id: 'chat.new',
    labelKey: 'commands.chat.new',
    helpKey: 'commands.chat.newHelp',
    section: 'chat',
    defaultShortcut: { key: 'n', mod: true },
  },
  {
    id: 'settings.open',
    labelKey: 'commands.settings.open',
    helpKey: 'commands.settings.openHelp',
    section: 'navigation',
    defaultShortcut: { key: ',', mod: true },
  },
]);

const copy = Object.freeze({
  labels: Object.freeze({
    'commands.chat.new': 'New chat',
    'commands.settings.open': 'Open settings',
  }),
  help: Object.freeze({
    'commands.chat.newHelp': 'Start an empty conversation',
    'commands.settings.openHelp': 'Open application preferences',
  }),
});

const ctrlShortcut = (key) => ({ key, meta: false, ctrl: true, alt: false, shift: false });

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

function hookProps(overrides = {}) {
  return {
    cwd: '/repo/a',
    registry,
    copy,
    platform: 'linux',
    getPreference: vi.fn().mockResolvedValue({}),
    setPreference: vi.fn().mockResolvedValue({ ok: true }),
    ...overrides,
  };
}

describe('useShortcutSettings', () => {
  it.each([
    ['getPreference', undefined, vi.fn()],
    ['setPreference', vi.fn(), undefined],
  ])('throws when the injected %s dependency is missing', (_name, getPreference, setPreference) => {
    expect(() => renderHook(() => useShortcutSettings(hookProps({ getPreference, setPreference })))).toThrow();
  });

  it('keeps an empty cwd explicitly unavailable without reading or writing preferences', async () => {
    const getPreference = vi.fn();
    const setPreference = vi.fn();
    const { result } = renderHook(() => useShortcutSettings(hookProps({
      cwd: '',
      getPreference,
      setPreference,
    })));

    expect(result.current.status).toBe('unavailable');
    expect(result.current.validatedOverrides).toBeUndefined();
    await expect(result.current.save()).rejects.toThrow('shortcut settings are unavailable without cwd');
    await expect(result.current.reset()).rejects.toThrow('shortcut settings are unavailable without cwd');
    expect(getPreference).not.toHaveBeenCalled();
    expect(setPreference).not.toHaveBeenCalled();
  });

  it('rejects a non-empty untrimmed cwd', () => {
    expect(() => renderHook(() => useShortcutSettings(hookProps({ cwd: ' /repo/a' })))).toThrow(
      'shortcut settings cwd is required',
    );
  });

  it('rejects a stale load after cwd generation changes', async () => {
    const first = deferred();
    const getPreference = vi.fn(({ cwd }) => (
      cwd === '/repo/a'
        ? first.promise
        : Promise.resolve({ 'chat.new': ctrlShortcut('m') })
    ));
    const props = hookProps({ getPreference });
    const { result, rerender } = renderHook(
      ({ options }) => useShortcutSettings(options),
      { initialProps: { options: props } },
    );

    rerender({ options: { ...props, cwd: '/repo/b' } });
    await waitFor(() => expect(result.current.status).toBe('ready'));
    expect(result.current.validatedOverrides).toEqual({ 'chat.new': ctrlShortcut('m') });

    await act(async () => first.resolve({ 'chat.new': ctrlShortcut('x') }));
    expect(result.current.validatedOverrides).toEqual({ 'chat.new': ctrlShortcut('m') });
  });

  it('preserves the edited draft when save fails', async () => {
    const setPreference = vi.fn().mockRejectedValue(new Error('disk unavailable'));
    const options = hookProps({ setPreference });
    const { result } = renderHook(() => useShortcutSettings(options));
    await waitFor(() => expect(result.current.status).toBe('ready'));

    act(() => result.current.setDraftBinding('chat.new', ctrlShortcut('x')));
    await act(async () => {
      await expect(result.current.save()).rejects.toThrow('disk unavailable');
    });

    expect(result.current.draftOverrides).toEqual({ 'chat.new': ctrlShortcut('x') });
    expect(result.current.error).toBe('保存快捷键设置失败，请重试。');
  });

  it('reads after write and rebuilds validated overrides from the returned persisted value', async () => {
    const getPreference = vi.fn()
      .mockResolvedValueOnce({})
      .mockResolvedValueOnce({ 'chat.new': ctrlShortcut('m') });
    const setPreference = vi.fn().mockResolvedValue({ ok: true });
    const { result } = renderHook(() => useShortcutSettings(hookProps({ getPreference, setPreference })));
    await waitFor(() => expect(result.current.status).toBe('ready'));

    act(() => result.current.setDraftBinding('chat.new', ctrlShortcut('x')));
    await act(async () => result.current.save());

    expect(setPreference).toHaveBeenCalledWith({
      cwd: '/repo/a',
      key: 'settings.shortcuts.bindings',
      value: { 'chat.new': ctrlShortcut('x') },
    });
    expect(getPreference).toHaveBeenLastCalledWith({ cwd: '/repo/a', key: 'settings.shortcuts.bindings' });
    expect(result.current.validatedOverrides).toEqual({ 'chat.new': ctrlShortcut('m') });
    expect(result.current.draftOverrides).toEqual({ 'chat.new': ctrlShortcut('m') });
  });

  it('resets by writing an empty object and reading the authoritative value back', async () => {
    const getPreference = vi.fn()
      .mockResolvedValueOnce({ 'chat.new': ctrlShortcut('m') })
      .mockResolvedValueOnce({});
    const setPreference = vi.fn().mockResolvedValue({ ok: true });
    const { result } = renderHook(() => useShortcutSettings(hookProps({ getPreference, setPreference })));
    await waitFor(() => expect(result.current.status).toBe('ready'));

    await act(async () => result.current.reset());

    expect(setPreference).toHaveBeenCalledWith({
      cwd: '/repo/a',
      key: 'settings.shortcuts.bindings',
      value: {},
    });
    expect(result.current.validatedOverrides).toEqual({});
    expect(result.current.draftOverrides).toEqual({});
  });
});

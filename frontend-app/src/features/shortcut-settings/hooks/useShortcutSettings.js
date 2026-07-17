import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { runBackgroundAction, runUIAction } from '../../../shared/ui/runUIAction.js';

import { projectShortcutSettings, SHORTCUT_PREFERENCE_KEY, validateShortcutOverrides } from '../model/shortcutSettingsModel.js';

function assertShortcutSettingsDependencies({ cwd, registry, getPreference, setPreference }) {
  if (typeof cwd !== 'string' || cwd.trim() !== cwd) {
    throw new Error('shortcut settings cwd is required');
  }
  if (!Array.isArray(registry)) throw new Error('shortcut settings registry is required');
  if (typeof getPreference !== 'function') throw new Error('shortcut settings getPreference is required');
  if (typeof setPreference !== 'function') throw new Error('shortcut settings setPreference is required');
}

function preferenceValue(value) {
  if (value === null) return {};
  return value;
}

async function loadShortcutSettings(params) {
  const {
    cwd, generation, generationRef, getPreference, platform, registry,
    setDraftOverrides, setError, setStatus, setValidatedOverrides,
  } = params;
  try {
    const value = await getPreference({ cwd, key: SHORTCUT_PREFERENCE_KEY });
    const overrides = validateShortcutOverrides({ registry, overrides: preferenceValue(value), platform });
    if (generationRef.current !== generation) return;
    setValidatedOverrides(overrides);
    setDraftOverrides(overrides);
    setStatus('ready');
  } catch (loadError) {
    if (generationRef.current !== generation) return;
    setValidatedOverrides(undefined);
    setStatus('error');
    setError('加载快捷键设置失败，请重试。');
    throw loadError;
  }
}

export function useShortcutSettings(options) {
  assertShortcutSettingsDependencies(options);
  const { copy, cwd, getPreference, platform, registry, setPreference } = options;
  const generationRef = useRef(0);
  const [status, setStatus] = useState(cwd === '' ? 'unavailable' : 'loading');
  const [error, setError] = useState('');
  const [validatedOverrides, setValidatedOverrides] = useState(undefined);
  const [draftOverrides, setDraftOverrides] = useState({});

  useEffect(() => {
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    setError('');
    setValidatedOverrides(undefined);
    setDraftOverrides({});
    if (cwd === '') {
      setStatus('unavailable');
      return;
    }
    setStatus('loading');
    runBackgroundAction('settings.shortcuts.bootstrap', () => loadShortcutSettings({
      cwd, generation, generationRef, getPreference, platform, registry,
      setDraftOverrides, setError, setStatus, setValidatedOverrides,
    }));
  }, [cwd, getPreference, platform, registry]);

  const setDraftBinding = useCallback((id, shortcut) => {
    setDraftOverrides((current) => ({ ...current, [id]: shortcut }));
  }, []);

  const persist = useCallback(async (nextDraft) => {
    if (cwd === '') throw new Error('shortcut settings are unavailable without cwd');
    const generation = generationRef.current;
    setStatus('saving');
    setError('');
    try {
      const validatedDraft = validateShortcutOverrides({ registry, overrides: nextDraft, platform });
      await setPreference({ cwd, key: SHORTCUT_PREFERENCE_KEY, value: validatedDraft });
      const authoritativeValue = await getPreference({ cwd, key: SHORTCUT_PREFERENCE_KEY });
      const authoritative = validateShortcutOverrides({
        registry,
        overrides: preferenceValue(authoritativeValue),
        platform,
      });
      if (generationRef.current !== generation) return;
      setValidatedOverrides(authoritative);
      setDraftOverrides(authoritative);
      setStatus('ready');
    } catch (saveError) {
      if (generationRef.current !== generation) return;
      setStatus('error');
      setError('保存快捷键设置失败，请重试。');
      throw saveError;
    }
  }, [cwd, getPreference, platform, registry, setPreference]);

  const save = useCallback(() => runUIAction('settings.shortcuts.save', () => persist(draftOverrides)), [draftOverrides, persist]);
  const reset = useCallback(() => runUIAction('settings.shortcuts.reset', () => persist({})), [persist]);
  const commands = useMemo(() => projectShortcutSettings({
    registry,
    copy,
    platform,
    overrides: draftOverrides,
  }), [copy, draftOverrides, platform, registry]);

  return useMemo(() => ({
    commands,
    draftOverrides,
    error,
    reset,
    save,
    setDraftBinding,
    status,
    validatedOverrides,
  }), [commands, draftOverrides, error, reset, save, setDraftBinding, status, validatedOverrides]);
}

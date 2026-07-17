import { useCallback, useEffect, useRef, useState } from 'react';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { firstPresentText } from '../shared/pageShared.js';
import { settingsPageService } from './services/settingsPageService.js';
import { isCurrentPreferenceRequest, textValue } from './settingsPageRuntime.js';
import { runBackgroundAction, runUIAction } from '../../shared/ui/runUIAction.js';

const {
  copyTextToClipboard,
  getPreference,
  readConfig,
  readLspPromptHint,
  setPreference,
  writeLspPromptHint,
} = settingsPageService;

function usePromptSettings(cwd, copy) {
  const [hint, setHint] = useState('');
  const [effectiveHint, setEffectiveHint] = useState('');
  const [defaultHint, setDefaultHint] = useState('');
  const [usingDefault, setUsingDefault] = useState(true);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const [showInjected, setShowInjected] = useState(false);
  const [showInjectedSaving, setShowInjectedSaving] = useState(false);
  const [currentScopeCwd, setCurrentScopeCwd] = useState('');
  const promptLoadSeq = useRef(0);
  const visibilityLoadSeq = useRef(0);
  const nextPromptLoadRequest = useCallback(() => {
    promptLoadSeq.current += 1;
    const requestSeq = promptLoadSeq.current;
    return () => promptLoadSeq.current === requestSeq;
  }, []);
  const nextVisibilityLoadRequest = useCallback(() => {
    visibilityLoadSeq.current += 1;
    const requestSeq = visibilityLoadSeq.current;
    return () => visibilityLoadSeq.current === requestSeq;
  }, []);
  const loadPrompt = useCallback(() => runUIAction('settings.prompt.load', () => loadLspPromptState({
    copy,
    cwd,
    isCurrent: nextPromptLoadRequest(),
    setDefaultHint,
    setEffectiveHint,
    setHint,
    setLoading,
    setNotice,
    setUsingDefault,
  }), { retryable: true }), [copy, cwd, nextPromptLoadRequest]);
  const loadScope = useCallback(() => loadPromptScope(setCurrentScopeCwd), []);
  const loadVisibility = useCallback(() => loadInjectedPromptVisibility({ copy, cwd, isCurrent: nextVisibilityLoadRequest(), setNotice, setShowInjected }), [copy, cwd, nextVisibilityLoadRequest]);
  const save = useCallback(() => runUIAction('settings.prompt.save', () => saveLspPromptHintState({ copy, cwd, hint, saving, setDefaultHint, setEffectiveHint, setHint, setNotice, setSaving, setUsingDefault })), [copy, cwd, hint, saving]);
  const reset = useCallback(() => runUIAction('settings.prompt.reset', () => saveLspPromptHintState({ copy, cwd, hint: '', saving, setDefaultHint, setEffectiveHint, setHint, setNotice, setSaving, setUsingDefault })), [copy, cwd, saving]);
  const copyPrompt = useCallback(() => runUIAction('settings.prompt.copy', () => copyEffectivePromptHint(promptDisplayHint(effectiveHint, defaultHint, copy), copy, setNotice), { retryable: true }), [copy, defaultHint, effectiveHint]);
  const toggleVisibility = useCallback((event) => runUIAction(
    'settings.prompt.visibility.save',
    () => saveInjectedPromptVisibility({ copy, cwd, event, loadVisibility, saving: showInjectedSaving, setNotice, setSaving: setShowInjectedSaving, setShowInjected }),
  ), [copy, cwd, loadVisibility, showInjectedSaving]);
  useEffect(() => {
    runBackgroundAction('settings.prompt.bootstrap', () => Promise.all([loadLspPromptState({ copy, cwd, isCurrent: nextPromptLoadRequest(), setDefaultHint, setEffectiveHint, setHint, setLoading, setNotice, setUsingDefault }), loadScope(), loadVisibility()]));
  }, [copy, cwd, loadScope, loadVisibility, nextPromptLoadRequest]);
  const displayHint = promptDisplayHint(effectiveHint, defaultHint, copy);
  const empty = copy.promptCard.empty;
  const lineCount = displayHint === empty ? 0 : displayHint.split('\n').length;
  const charCount = displayHint === empty ? 0 : displayHint.length;
  return {
    charCount,
    copy: copyPrompt,
    currentScopeCwd,
    defaultHint,
    displayHint,
    effectiveHint,
    hint,
    lineCount,
    loadPrompt,
    loading,
    modeLabel: promptModeLabel(loading, usingDefault, copy),
    notice,
    reset,
    save,
    saving,
    setHint,
    showInjected,
    showInjectedSaving,
    textCopy: copy,
    toggleVisibility,
    usingDefault,
  };
}

function promptDisplayHint(effectiveHint, defaultHint, copy = APP_COPY.zh.settings) {
  const promptText = firstPresentText(effectiveHint, defaultHint);
  return promptText ? promptText.trim() : copy.promptCard.empty;
}

function promptModeLabel(loading, usingDefault, copy = APP_COPY.zh.settings) {
  if (loading) return copy.promptCard.loading;
  return usingDefault ? copy.promptCard.defaultMode : copy.promptCard.customMode;
}

function normalizePromptHintResponse(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new TypeError('prompt hint response must be an object');
  }
  for (const key of ['hint', 'defaultHint', 'overrideHint']) {
    if (typeof response[key] !== 'string') {
      throw new TypeError(`prompt hint response ${key} must be a string`);
    }
  }
  if (typeof response.usingDefault !== 'boolean') {
    throw new TypeError('prompt hint response usingDefault must be a boolean');
  }
  return response;
}

async function loadLspPromptState(state) {
  if (!state.cwd) {
    if (isCurrentPreferenceRequest(state.isCurrent)) state.setLoading(false);
    return;
  }
  state.setLoading(true);
  try {
    const res = normalizePromptHintResponse(await readLspPromptHint({ cwd: state.cwd }));
    if (!isCurrentPreferenceRequest(state.isCurrent)) return;
    state.setHint(res.overrideHint);
    state.setEffectiveHint(res.hint);
    state.setDefaultHint(res.defaultHint);
    state.setUsingDefault(res.usingDefault);
    state.setNotice({ level: 'info', message: '' });
  } catch (error) {
    if (isCurrentPreferenceRequest(state.isCurrent)) state.setNotice({ level: 'error', message: state.copy.promptCard.loadFailed });
    throw error;
  } finally {
    if (isCurrentPreferenceRequest(state.isCurrent)) state.setLoading(false);
  }
}

async function loadPromptScope(setCurrentScopeCwd) {
  try {
    const cfg = await readConfig();
    setCurrentScopeCwd(textValue(cfg?.cwd).trim());
  } catch (error) {
    setCurrentScopeCwd('');
    throw error;
  }
}

async function loadInjectedPromptVisibility({ copy, cwd, isCurrent, setNotice, setShowInjected }) {
  if (!cwd) return;
  try {
    const value = await getPreference({
      cwd,
      key: 'settings.showInjectedPromptInChat',
    });
    if (isCurrentPreferenceRequest(isCurrent)) setShowInjected(parseBoolPreference(value));
  } catch (error) {
    if (isCurrentPreferenceRequest(isCurrent)) setNotice({ level: 'error', message: copy.promptCard.loadToggleFailed });
    throw error;
  }
}

async function saveLspPromptHintState(state) {
  if (!state.cwd || state.saving) return;
  state.setSaving(true);
  try {
    const res = normalizePromptHintResponse(await writeLspPromptHint({ cwd: state.cwd, hint: state.hint }));
    state.setEffectiveHint(res.hint);
    state.setDefaultHint(res.defaultHint);
    state.setHint(res.overrideHint);
    state.setUsingDefault(res.usingDefault);
    state.setNotice({ level: 'info', message: res.usingDefault ? state.copy.promptCard.restored : state.copy.promptCard.saved });
  } catch (error) {
    state.setNotice({ level: 'error', message: state.copy.promptCard.saveFailed });
    throw error;
  } finally {
    state.setSaving(false);
  }
}

async function copyEffectivePromptHint(text, copy, setNotice) {
  if (!text || text === copy.promptCard.empty) {
    setNotice({ level: 'error', message: copy.promptCard.noCopy });
    return;
  }
  try {
    const ok = await copyTextToClipboard(text);
    setNotice({ level: ok ? 'info' : 'error', message: ok ? copy.promptCard.copied : copy.promptCard.copyFailed });
  } catch (error) {
    setNotice({ level: 'error', message: copy.promptCard.copyFailed });
    throw error;
  }
}

async function saveInjectedPromptVisibility(state) {
  const { copy, cwd, event, loadVisibility, saving, setNotice, setSaving, setShowInjected } = state;
  if (!cwd || saving) return;
  const next = event.target.checked;
  setShowInjected(next);
  setSaving(true);
  try {
    await setPreference({ cwd, key: 'settings.showInjectedPromptInChat', value: next });
    setNotice({ level: 'info', message: next ? copy.promptCard.showInjectedSaved : copy.promptCard.hideInjectedSaved });
  } catch (error) {
    setNotice({ level: 'error', message: copy.promptCard.saveToggleFailed });
    await loadVisibility();
    throw error;
  } finally {
    setSaving(false);
  }
}

function parseBoolPreference(value) {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'number') return value !== 0;
  if (typeof value !== 'string') return false;
  const normalized = value.trim().toLowerCase();
  if (['1', 'true', 'yes', 'on'].includes(normalized)) return true;
  return false;
}

export { usePromptSettings };

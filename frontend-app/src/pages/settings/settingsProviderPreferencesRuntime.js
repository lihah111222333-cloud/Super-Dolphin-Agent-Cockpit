import { useCallback, useEffect, useRef, useState } from 'react';
import { settingsPageService } from './services/settingsPageService.js';
import {
  isCurrentPreferenceRequest,
  normalizeProviderName,
  providerConfigValue,
  providerSettingKey,
  readScopedPreference,
} from './settingsPageRuntime.js';

const { setPreference } = settingsPageService;

function useProviderPreferences(cwd, activeProvider, copy) {
  const provider = normalizeProviderName(activeProvider);
  const [summaryMode, setSummaryMode] = useState('detailed');
  const [approvalMode, setApprovalMode] = useState('on-request');
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const [saving, setSaving] = useState(false);
  const loadRequestSeq = useRef(0);
  const nextLoadRequest = useCallback(() => {
    loadRequestSeq.current += 1;
    const requestSeq = loadRequestSeq.current;
    return () => loadRequestSeq.current === requestSeq;
  }, []);
  const load = useCallback(async () => {
    const isCurrent = nextLoadRequest();
    if (!cwd) return;
    const providerKey = normalizeProviderName(activeProvider);
    try {
      if (!isCurrentPreferenceRequest(isCurrent)) return;
      const [summaryValue, approvalValue] = await Promise.all([
        readScopedPreference(cwd, providerSettingKey(providerKey, 'summary')),
        readScopedPreference(cwd, providerSettingKey(providerKey, 'approvalPolicy')),
      ]);
      if (isCurrentPreferenceRequest(isCurrent)) {
        applyProviderPreferenceValues(approvalValue, setApprovalMode, setNotice, setSummaryMode, summaryValue);
      }
    } catch (error) {
      if (isCurrentPreferenceRequest(isCurrent)) {
        setProviderPreferenceLoadError(copy, error, setNotice);
      }
    }
  }, [activeProvider, copy, cwd, nextLoadRequest]);
  const save = useCallback(() => saveProviderPreferenceValues({ approvalMode, copy, cwd, provider, saving, setNotice, setSaving, summaryMode }), [approvalMode, copy, cwd, provider, saving, summaryMode]);
  useEffect(() => { void load(); }, [load]);
  return { approvalMode, load, notice, provider, save, saving, setApprovalMode, setSummaryMode, summaryMode };
}

function setProviderPreferenceLoadError(copy, error, setNotice) {
  setNotice({ level: 'error', message: copy.provider.loadPreferencesFailed + error.message });
}

function applyProviderPreferenceValues(approvalValue, setApprovalMode, setNotice, setSummaryMode, summaryValue) {
  setSummaryMode(providerConfigValue(summaryValue) || 'detailed');
  setApprovalMode(providerConfigValue(approvalValue) || 'on-request');
  setNotice({ level: 'info', message: '' });
}

async function saveProviderPreferenceValues(state) {
  const { approvalMode, copy, cwd, provider, saving, setNotice, setSaving, summaryMode } = state;
  if (!cwd || saving) return;
  setSaving(true);
  try {
    const providerKey = normalizeProviderName(provider);
    await setPreference({ cwd, key: providerSettingKey(providerKey, 'summary'), value: summaryMode });
    await setPreference({ cwd, key: providerSettingKey(providerKey, 'approvalPolicy'), value: approvalMode });
    setNotice({ level: 'info', message: copy.provider.savedPrefix + summaryMode + ' / ' + approvalMode });
  } catch (error) {
    setNotice({ level: 'error', message: copy.provider.saveFailed + error.message });
  } finally {
    setSaving(false);
  }
}

export { useProviderPreferences };

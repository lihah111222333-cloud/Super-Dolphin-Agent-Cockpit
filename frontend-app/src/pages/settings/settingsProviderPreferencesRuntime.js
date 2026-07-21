import { useCallback, useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { settingsPageService } from "./services/settingsPageService.js";
import {
  runBackgroundAction,
  runUIAction,
} from "../../shared/ui/runUIAction.js";
import {
  normalizeSettingsCwd,
  providerSettingKey,
  readScopedPreference,
} from "./settingsPageRuntime.js";
import {
  normalizeProviderName,
  providerConfigValue,
} from "./settingsProviderConfig.js";

const { setPreference } = settingsPageService;

function settingsProviderPreferencesQueryKey(cwd, activeProvider) {
  return [
    "settings",
    "provider-preferences",
    normalizeSettingsCwd(cwd),
    normalizeProviderName(activeProvider),
  ];
}

async function readProviderRuntimePreferences(cwd, activeProvider) {
  const projectCwd = normalizeSettingsCwd(cwd);
  if (!projectCwd) return { approvalValue: null, summaryValue: null };
  const providerKey = normalizeProviderName(activeProvider);
  const [summaryValue, approvalValue] = await Promise.all([
    readScopedPreference(
      projectCwd,
      providerSettingKey(providerKey, "summary"),
    ),
    readScopedPreference(
      projectCwd,
      providerSettingKey(providerKey, "approvalPolicy"),
    ),
  ]);
  return { approvalValue, summaryValue };
}

function useProviderPreferences(cwd, activeProvider, copy) {
  const provider = normalizeProviderName(activeProvider);
  const [summaryMode, setSummaryMode] = useState("detailed");
  const [approvalMode, setApprovalMode] = useState("on-request");
  const [notice, setNotice] = useState({ level: "info", message: "" });
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const preferencesQuery = useQuery({
    queryKey: settingsProviderPreferencesQueryKey(cwd, provider),
    queryFn: () =>
      runBackgroundAction("settings.provider.preferences.bootstrap", () =>
        readProviderRuntimePreferences(cwd, provider),
      ),
    enabled: Boolean(normalizeSettingsCwd(cwd) && provider),
    retry: false,
    refetchOnWindowFocus: false,
  });
  const {
    data: preferencesData,
    error: preferencesError,
    refetch,
  } = preferencesQuery;
  const load = useCallback(
    () =>
      runUIAction(
        "settings.provider.preferences.load",
        () => {
          setDirty(false);
          return refetch({ throwOnError: true });
        },
        { retryable: true },
      ),
    [refetch, setDirty],
  );
  const save = useCallback(
    () =>
      runUIAction("settings.provider.preferences.save", async () => {
        const saved = await saveProviderPreferenceValues({
          approvalMode,
          copy,
          cwd,
          provider,
          saving,
          setNotice,
          setSaving,
          summaryMode,
        });
        if (saved) setDirty(false);
      }),
    [
      approvalMode,
      copy,
      cwd,
      provider,
      saving,
      setDirty,
      setNotice,
      setSaving,
      summaryMode,
    ],
  );
  const updateSummaryMode = useCallback(
    (value) => {
      setDirty(true);
      setSummaryMode(value);
    },
    [setDirty, setSummaryMode],
  );
  const updateApprovalMode = useCallback(
    (value) => {
      setDirty(true);
      setApprovalMode(value);
    },
    [setApprovalMode, setDirty],
  );
  useEffect(() => {
    setDirty(false);
  }, [cwd, provider]);
  useEffect(() => {
    if (preferencesError) {
      setProviderPreferenceLoadError(copy, preferencesError, setNotice);
      return;
    }
    if (preferencesData && !dirty) {
      applyProviderPreferenceValues(
        preferencesData.approvalValue,
        setApprovalMode,
        setNotice,
        setSummaryMode,
        preferencesData.summaryValue,
      );
    }
  }, [copy, dirty, preferencesData, preferencesError]);
  return {
    approvalMode,
    load,
    notice,
    provider,
    save,
    saving,
    setApprovalMode: updateApprovalMode,
    setSummaryMode: updateSummaryMode,
    summaryMode,
  };
}

function setProviderPreferenceLoadError(copy, _error, setNotice) {
  setNotice({ level: "error", message: copy.provider.loadPreferencesFailed });
}

function applyProviderPreferenceValues(
  approvalValue,
  setApprovalMode,
  setNotice,
  setSummaryMode,
  summaryValue,
) {
  setSummaryMode(providerConfigValue(summaryValue) || "detailed");
  setApprovalMode(providerConfigValue(approvalValue) || "on-request");
  setNotice({ level: "info", message: "" });
}

async function saveProviderPreferenceValues(state) {
  const {
    approvalMode,
    copy,
    cwd,
    provider,
    saving,
    setNotice,
    setSaving,
    summaryMode,
  } = state;
  if (!cwd || saving) return;
  setSaving(true);
  try {
    const providerKey = normalizeProviderName(provider);
    await setPreference({
      cwd,
      key: providerSettingKey(providerKey, "summary"),
      value: summaryMode,
    });
    await setPreference({
      cwd,
      key: providerSettingKey(providerKey, "approvalPolicy"),
      value: approvalMode,
    });
    setNotice({
      level: "info",
      message: copy.provider.savedPrefix + summaryMode + " / " + approvalMode,
    });
    return true;
  } catch (error) {
    setNotice({ level: "error", message: copy.provider.saveFailed });
    throw error;
  } finally {
    setSaving(false);
  }
}

export {
  readProviderRuntimePreferences,
  settingsProviderPreferencesQueryKey,
  useProviderPreferences,
};

import { APP_COPY } from "../../shared/i18n/appI18n.js";
import { settingsPageService } from "./services/settingsPageService.js";
import {
  PROVIDER_LABELS,
  normalizeProviderEffortSetting,
  normalizeProviderModelSetting,
  normalizeProviderName,
  textValue,
} from "./settingsProviderConfig.js";
import {
  absolutePathsError,
  sandboxPreferenceValue,
} from "./settingsSandboxPreferences.js";
import { SETTINGS_KEYS } from "./settingsRuntimeConstants.js";
import {
  isCurrentPreferenceRequest,
  providerSettingKey,
  readRuntimePreferenceValues,
  readRuntimePreferenceValuesForProvider,
  requireSettingsCwd,
  settingsFormFromPreferences,
  settingsFormWithUpdate,
} from "./settingsRuntimePreferences.js";

const { setPreference } = settingsPageService;

function parsePositiveInteger(label, value, copy = APP_COPY.zh.settings) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isInteger(parsed)) {
    throw new Error(`${label} ${copy.runtime.integerSuffix}`);
  }
  return parsed;
}

function validateRuntimeThresholds(form, copy = APP_COPY.zh.settings) {
  const stallThresholdSec = parsePositiveInteger(
    copy.runtime.stallThreshold,
    form.stallThresholdSec,
    copy,
  );
  if (stallThresholdSec < 30) throw new Error(copy.runtime.minTimeout);

  const warn = parsePositiveInteger(
    copy.runtime.warnThreshold,
    form.contextWarn,
    copy,
  );
  const danger = parsePositiveInteger(
    copy.runtime.dangerThreshold,
    form.contextDanger,
    copy,
  );
  const critical = parsePositiveInteger(
    copy.runtime.criticalThreshold,
    form.contextCritical,
    copy,
  );
  if (!(warn > 0 && warn < danger && danger < critical && critical <= 100)) {
    throw new Error(copy.runtime.invalidOrder);
  }
  return { stallThresholdSec, contextThresholds: [warn, danger, critical] };
}

async function loadRuntimePreferences({ cwd, isCurrent, setError, setForm }) {
  setError("");
  if (!cwd) return;
  try {
    if (!isCurrentPreferenceRequest(isCurrent)) return;
    const values = await readRuntimePreferenceValues(cwd);
    if (isCurrentPreferenceRequest(isCurrent)) {
      setForm(settingsFormFromPreferences(values));
    }
  } catch (err) {
    if (isCurrentPreferenceRequest(isCurrent)) {
      setError("切换 Provider 失败，请重试。");
    }
    throw err;
  }
}

async function changeActiveProviderPreference(state) {
  const { copy, cwd, event, isCurrent, setError, setForm, setStatus } = state;
  const provider = normalizeProviderName(event.target.value);
  setError("");
  setStatus("");
  setForm((current) =>
    settingsFormWithUpdate(current, "activeProvider", provider),
  );
  try {
    const projectCwd = requireSettingsCwd(cwd, copy);
    if (!isCurrentPreferenceRequest(isCurrent)) return;
    await setPreference({
      cwd: projectCwd,
      key: SETTINGS_KEYS.activeProvider,
      value: provider,
    });
    const values = await readRuntimePreferenceValuesForProvider(
      projectCwd,
      provider,
    );
    if (isCurrentPreferenceRequest(isCurrent)) {
      setForm(settingsFormFromPreferences(values));
      setStatus(copy.provider.switchedTo + (PROVIDER_LABELS[provider] || provider));
    }
  } catch (err) {
    if (isCurrentPreferenceRequest(isCurrent)) {
      setError("切换 Provider 失败，请重试。");
    }
    throw err;
  }
}

async function saveRuntimePreferences({ copy, cwd, form, setError, setStatus }) {
  setError("");
  setStatus("");
  try {
    const projectCwd = requireSettingsCwd(cwd, copy);
    const { stallThresholdSec, contextThresholds } = validateRuntimeThresholds(
      form,
      copy,
    );
    await setPreference({
      cwd: projectCwd,
      key: SETTINGS_KEYS.stallThreshold,
      value: stallThresholdSec,
    });
    await setPreference({
      cwd: projectCwd,
      key: SETTINGS_KEYS.contextThresholds,
      value: contextThresholds,
    });
    setStatus(copy.runtime.saved);
  } catch (err) {
    setError("保存运行设置失败，请重试。");
    throw err;
  }
}

async function saveProviderRuntimePreferences({
  copy,
  cwd,
  form,
  setError,
  setStatus,
}) {
  setError("");
  setStatus("");
  try {
    const projectCwd = requireSettingsCwd(cwd, copy);
    const rootError =
      form.sandboxPolicy === "workspaceWrite"
        ? absolutePathsError(form.writableRoots, copy)
        : "";
    if (rootError) throw new Error(rootError);
    const provider = normalizeProviderName(form.activeProvider);
    await writeProviderRuntimePreferences(projectCwd, provider, form);
    setStatus(copy.provider.settingsSaved);
  } catch (err) {
    setError("保存 Provider 设置失败，请重试。");
    throw err;
  }
}

async function writeProviderRuntimePreferences(cwd, provider, form) {
  const providerModel = normalizeProviderModelSetting(
    provider,
    form.providerModel,
  );
  const providerEffort = normalizeProviderEffortSetting(
    provider,
    providerModel,
    form.providerEffort,
  );
  const calls = [
    setPreference({
      cwd,
      key: providerSettingKey(provider, "personality"),
      value: form.personality.trim(),
    }),
    setPreference({
      cwd,
      key: providerSettingKey(provider, "sandbox"),
      value: sandboxPreferenceValue(
        form.sandboxPolicy,
        form.writableRoots,
        form.networkAccess,
        form.readOnlyMode,
        form.readableRoots,
      ),
    }),
  ];
  if (form.providerModelExplicit || form.providerModelTouched) {
    calls.push(
      setPreference({
        cwd,
        key: providerSettingKey(provider, "model"),
        value: providerModel,
      }),
    );
  }
  if (form.providerEffortExplicit || form.providerEffortTouched) {
    calls.push(
      setPreference({
        cwd,
        key: providerSettingKey(provider, "effort"),
        value: providerEffort,
      }),
    );
  }
  if (provider === "codex") {
    calls.push(
      setPreference({
        cwd,
        key: providerSettingKey("codex", "codexHome"),
        value: codexIdentityPreferenceValue(form.codexHome),
      }),
      setPreference({
        cwd,
        key: providerSettingKey("codex", "codexInstanceKey"),
        value: codexIdentityPreferenceValue(form.codexInstanceKey),
      }),
    );
  }
  await Promise.all(calls);
}

function codexIdentityPreferenceValue(value) {
  const text = textValue(value).trim();
  return text ? text : { cleared: true };
}

export {
  changeActiveProviderPreference,
  loadRuntimePreferences,
  saveProviderRuntimePreferences,
  saveRuntimePreferences,
};

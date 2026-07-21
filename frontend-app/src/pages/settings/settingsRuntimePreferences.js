import { APP_COPY } from "../../shared/i18n/appI18n.js";
import { normalizeRuntimeProviderName } from "../../entities/client/model/helpers/providerPreferences.js";
import { firstPresentText } from "../shared/pageShared.js";
import { settingsPageService } from "./services/settingsPageService.js";
import {
  normalizeProviderEffortSetting,
  normalizeProviderModelSetting,
  normalizeProviderName,
  providerConfigValue,
  textValue,
} from "./settingsProviderConfig.js";
import {
  isPreferenceAbsent,
  isPreferenceTombstone,
  readableRootsFromPreference,
  readOnlyModeFromPreference,
  sandboxPreferenceFromRaw,
  sandboxPolicyFromPreference,
  writableRootsFromPreference,
} from "./settingsSandboxPreferences.js";
import {
  SETTINGS_DEFAULTS,
  SETTINGS_KEYS,
  SETTINGS_PROJECT_CWD_REQUIRED,
} from "./settingsRuntimeConstants.js";

const { getPreference, listDashboardLogs } = settingsPageService;

function providerSettingKey(provider, key) {
  return `settings.provider.${provider}.${key}`;
}

function stringSetting(value, fallback) {
  if (typeof value === "string" && value.trim()) return value.trim();
  return fallback;
}

async function readScopedPreference(cwd, key) {
  const scope = textValue(cwd).trim();
  if (scope) {
    const scoped = await getPreference(
      { cwd: scope, key },
      { allowTombstone: true },
    );
    if (isPreferenceTombstone(scoped)) return "";
    if (!isPreferenceAbsent(scoped)) return scoped;
  }
  const globalValue = await getPreference({ key }, { allowTombstone: true });
  if (isPreferenceTombstone(globalValue)) return "";
  return isPreferenceAbsent(globalValue) ? null : globalValue;
}

function numberSetting(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function providerNameFromPreference(value) {
  if (isPreferenceAbsent(value) || isPreferenceTombstone(value)) {
    return SETTINGS_DEFAULTS.activeProvider;
  }
  return normalizeRuntimeProviderName(value, SETTINGS_KEYS.activeProvider);
}

function normalizeContextThresholds(value) {
  if (!Array.isArray(value) || value.length < 3) {
    return SETTINGS_DEFAULTS.contextThresholds;
  }
  return [
    numberSetting(value[0], SETTINGS_DEFAULTS.contextThresholds[0]),
    numberSetting(value[1], SETTINGS_DEFAULTS.contextThresholds[1]),
    numberSetting(value[2], SETTINGS_DEFAULTS.contextThresholds[2]),
  ];
}

function requireSettingsCwd(cwd, copy = APP_COPY.zh.settings) {
  const value = textValue(cwd).trim();
  if (!value) {
    throw new Error(
      firstPresentText(copy.projectCwdRequired, SETTINGS_PROJECT_CWD_REQUIRED),
    );
  }
  return value;
}

function loadSettingsDashboardLogs() {
  return listDashboardLogs({ limit: 14 });
}

function defaultSettingsForm() {
  return {
    stallThresholdSec: String(SETTINGS_DEFAULTS.stallThresholdSec),
    contextWarn: String(SETTINGS_DEFAULTS.contextThresholds[0]),
    contextDanger: String(SETTINGS_DEFAULTS.contextThresholds[1]),
    contextCritical: String(SETTINGS_DEFAULTS.contextThresholds[2]),
    activeProvider: SETTINGS_DEFAULTS.activeProvider,
    codexHome: SETTINGS_DEFAULTS.codexHome,
    codexInstanceKey: SETTINGS_DEFAULTS.codexInstanceKey,
    providerModel: SETTINGS_DEFAULTS.providerModel,
    providerEffort: SETTINGS_DEFAULTS.providerEffort,
    personality: SETTINGS_DEFAULTS.personality,
    providerModelExplicit: false,
    providerEffortExplicit: false,
    providerModelTouched: false,
    providerEffortTouched: false,
    sandboxPolicy: SETTINGS_DEFAULTS.sandboxPolicy,
    readOnlyMode: SETTINGS_DEFAULTS.readOnlyMode,
    readableRoots: SETTINGS_DEFAULTS.readableRoots,
    writableRoots: SETTINGS_DEFAULTS.writableRoots,
    networkAccess: SETTINGS_DEFAULTS.networkAccess,
  };
}

function normalizeSettingsCwd(value) {
  const text = textValue(value).trim();
  if (!text || text === "." || text === "未选择项目") return "";
  return text;
}

function settingsFormWithUpdate(current, key, value) {
  if (key === "activeProvider") {
    const activeProvider = normalizeProviderName(value);
    const providerModel = normalizeProviderModelSetting(
      activeProvider,
      current.providerModel,
    );
    return {
      ...current,
      activeProvider,
      providerModel,
      providerEffort: normalizeProviderEffortSetting(
        activeProvider,
        providerModel,
        current.providerEffort,
      ),
      providerModelExplicit: false,
      providerEffortExplicit: false,
      providerModelTouched: false,
      providerEffortTouched: false,
    };
  }
  if (key === "providerModel") {
    const providerModel = normalizeProviderModelSetting(
      current.activeProvider,
      value,
    );
    return {
      ...current,
      providerModel,
      providerEffort: normalizeProviderEffortSetting(
        current.activeProvider,
        providerModel,
        current.providerEffort,
      ),
      providerModelTouched: true,
    };
  }
  if (key === "providerEffort") {
    return {
      ...current,
      providerEffort: normalizeProviderEffortSetting(
        current.activeProvider,
        current.providerModel,
        value,
      ),
      providerEffortTouched: true,
    };
  }
  return { ...current, [key]: value };
}

function isCurrentPreferenceRequest(isCurrent) {
  return typeof isCurrent !== "function" || isCurrent();
}

async function readRuntimePreferenceValues(cwd) {
  const [stallValue, contextValue, activeProviderValue] = await Promise.all([
    getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
    getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
    readScopedPreference(cwd, SETTINGS_KEYS.activeProvider),
  ]);
  const activeProvider = providerNameFromPreference(activeProviderValue);
  const providerValues = await readProviderPreferenceValues(cwd, activeProvider);
  return { activeProvider, contextValue, providerValues, stallValue };
}

async function readRuntimeSettingsForm(cwd) {
  const projectCwd = normalizeSettingsCwd(cwd);
  if (!projectCwd) return defaultSettingsForm();
  return settingsFormFromPreferences(
    await readRuntimePreferenceValues(projectCwd),
  );
}

async function readRuntimePreferenceValuesForProvider(cwd, activeProvider) {
  const [stallValue, contextValue, providerValues] = await Promise.all([
    getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
    getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
    readProviderPreferenceValues(cwd, activeProvider),
  ]);
  return { activeProvider, contextValue, providerValues, stallValue };
}

async function readProviderPreferenceValues(cwd, activeProvider) {
  const providerPrefix = "settings.provider." + activeProvider;
  const isCodex = activeProvider === "codex";
  return Promise.all([
    isCodex
      ? readScopedPreference(cwd, providerSettingKey("codex", "codexHome"))
      : Promise.resolve(null),
    isCodex
      ? readScopedPreference(
        cwd,
        providerSettingKey("codex", "codexInstanceKey"),
      )
      : Promise.resolve(null),
    readScopedPreference(cwd, providerPrefix + ".model"),
    readScopedPreference(cwd, providerPrefix + ".effort"),
    readScopedPreference(cwd, providerPrefix + ".personality"),
    readScopedPreference(cwd, providerPrefix + ".sandbox"),
  ]);
}

function settingsFormFromPreferences({
  activeProvider,
  contextValue,
  providerValues,
  stallValue,
}) {
  const [
    codexHome,
    codexInstanceKey,
    providerModel,
    providerEffort,
    personality,
    sandbox,
  ] = providerValues;
  const contextThresholds = normalizeContextThresholds(contextValue);
  const model = normalizeProviderModelSetting(activeProvider, providerModel);
  const sandboxPreference = sandboxPreferenceFromRaw(sandbox);
  return {
    ...defaultSettingsForm(),
    stallThresholdSec: String(
      numberSetting(stallValue, SETTINGS_DEFAULTS.stallThresholdSec),
    ),
    contextWarn: String(contextThresholds[0]),
    contextDanger: String(contextThresholds[1]),
    contextCritical: String(contextThresholds[2]),
    activeProvider,
    codexHome: stringSetting(codexHome, SETTINGS_DEFAULTS.codexHome),
    codexInstanceKey: stringSetting(
      codexInstanceKey,
      SETTINGS_DEFAULTS.codexInstanceKey,
    ),
    providerModel: model,
    providerEffort: normalizeProviderEffortSetting(
      activeProvider,
      model,
      providerEffort,
    ),
    providerModelExplicit: !isPreferenceAbsent(providerModel),
    providerEffortExplicit: !isPreferenceAbsent(providerEffort),
    providerModelTouched: false,
    providerEffortTouched: false,
    personality: providerConfigValue(personality) || SETTINGS_DEFAULTS.personality,
    sandboxPolicy: sandboxPolicyFromPreference(sandboxPreference),
    readOnlyMode: readOnlyModeFromPreference(sandboxPreference),
    readableRoots: readableRootsFromPreference(sandboxPreference),
    writableRoots: writableRootsFromPreference(sandboxPreference),
    networkAccess: Boolean(
      sandboxPreference &&
      typeof sandboxPreference === "object" &&
      sandboxPreference.networkAccess,
    ),
  };
}

export {
  defaultSettingsForm,
  isCurrentPreferenceRequest,
  loadSettingsDashboardLogs,
  normalizeSettingsCwd,
  providerSettingKey,
  readRuntimePreferenceValues,
  readRuntimePreferenceValuesForProvider,
  readRuntimeSettingsForm,
  readScopedPreference,
  requireSettingsCwd,
  settingsFormFromPreferences,
  settingsFormWithUpdate,
};

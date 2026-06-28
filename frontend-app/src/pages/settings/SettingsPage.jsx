import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Menu, Settings } from 'lucide-react';
import { useClientStore } from '../../entities/client/model/useClientStore.js';
import { PageHeader } from '../shared/pageComponents.jsx';
import { BuiltinToolsCard } from './components/BuiltinToolsCard.jsx';
import { MCPToolLifecycleCard } from './components/MCPToolLifecycleCard.jsx';
import { ModelProvidersCard } from './components/ModelProvidersCard.jsx';
import { ProviderPropertiesCard, ProviderSettingsPanel } from './components/ProviderSettingsPanels.jsx';
import { PromptSettingsCard } from './components/PromptSettingsCard.jsx';
import { AboutPanel, RuntimeSettingsPanels } from './components/SettingsSystemPanels.jsx';
import { UILogCard } from './components/UILogCard.jsx';
import { VideoSettingsCard } from './components/VideoSettingsCard.jsx';
import { checkAppUpdate, copyTextToClipboard, getBuildInfo, getPreference, getVideoApiKey, installLatestAppUpdate, listDashboardLogs, readBuiltinTools, readConfig, readLspPromptHint, setPreference, setVideoApiKey, writeBuiltinTool, writeLspPromptHint } from './services/settingsPageService.js';
import { APP_BRAND_NAME, APP_COPY } from '../../shared/i18n/appI18n.js';
import './SettingsPage.css';

const PROVIDER_LABELS = Object.freeze({
  claude: 'Claude',
  codex: 'Codex',
});

const SETTINGS_KEYS = Object.freeze({
  stallThreshold: 'stallThresholdSec',
  contextThresholds: 'contextUsageAlerts.thresholds',
  activeProvider: 'settings.provider.active',
});

const SETTINGS_PROJECT_CWD_REQUIRED = '当前项目路径为空，无法保存设置';

const SETTINGS_DEFAULTS = Object.freeze({
  stallThresholdSec: 30,
  contextThresholds: [70, 85, 95],
  activeProvider: 'codex',
  codexHome: '~/.codex',
  codexInstanceKey: 'default',
  providerModel: 'gpt-5.5',
  providerEffort: 'xhigh',
  personality: 'pragmatic',
  sandboxPolicy: 'workspaceWrite',
  readOnlyMode: 'fullAccess',
  readableRoots: '',
  writableRoots: '',
  networkAccess: false,
});

const PROVIDER_DEFAULTS = Object.freeze({
  codex: Object.freeze({ model: 'gpt-5.5', effort: 'xhigh' }),
  claude: Object.freeze({ model: 'sonnet', effort: 'high' }),
});

const CLAUDE_LONG_TO_SHORT = Object.freeze({
  'claude-opus-4-7': 'opus',
  'claude-opus-4-7[1m]': 'opus[1m]',
  'claude-haiku-4-5': 'haiku',
});

const MODEL_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'gpt-5.5', label: 'GPT-5.5' },
    { value: 'gpt-5.4', label: 'GPT-5.4' },
    { value: 'gpt-5.4-mini', label: 'GPT-5.4 Mini' },
    { value: 'gpt-5', label: 'GPT-5' },
    { value: 'codex-auto-review', label: 'Codex Auto Review' },
  ]),
  claude: Object.freeze([
    { value: 'opus', label: 'Opus 4.7' },
    { value: 'opus[1m]', label: 'Opus 4.7 [1M]' },
    { value: 'claude-opus-4-6', label: 'Opus 4.6' },
    { value: 'claude-opus-4-6[1m]', label: 'Opus 4.6 [1M]' },
    { value: 'sonnet', label: 'Sonnet 4.7' },
    { value: 'sonnet[1m]', label: 'Sonnet 4.7 [1M]' },
    { value: 'claude-sonnet-4-6', label: 'Sonnet 4.6' },
    { value: 'claude-sonnet-4-6[1m]', label: 'Sonnet 4.6 [1M]' },
    { value: 'haiku', label: 'Haiku 4.5' },
  ]),
});

const EFFORT_MODES_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'xhigh', label: '极高' },
    { value: 'high', label: '高' },
    { value: 'medium', label: '中' },
    { value: 'low', label: '低' },
    { value: 'none', label: '关闭' },
  ]),
  claude: Object.freeze([
    { value: 'max', label: 'max（仅 Opus）' },
    { value: 'high', label: 'high' },
    { value: 'medium', label: 'medium' },
    { value: 'low', label: 'low' },
  ]),
});

const PERSONALITY_OPTIONS = Object.freeze([
  { value: 'pragmatic', label: 'pragmatic（务实高效，默认）' },
  { value: 'friendly', label: 'friendly（友好气氛）' },
  { value: 'none', label: 'none（默认风格）' },
]);

function providerSettingKey(provider, key) {
  return `settings.provider.${provider}.${key}`;
}

function stringSetting(value, fallback) {
  if (typeof value === 'string' && value.trim()) return value.trim();
  return fallback;
}

function providerConfigValue(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const key of ['value', 'model', 'id', 'key', 'name']) {
      const text = providerConfigValue(value[key]);
      if (text) return text;
    }
    return '';
  }
  return (value || '').toString().trim();
}

function isPreferenceTombstone(value) {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value) && value.cleared === true);
}

function isPreferenceAbsent(value) {
  return value === null || value === undefined || (typeof value === 'string' && value.trim() === '');
}

async function readScopedPreference(cwd, key) {
  const scope = (cwd || '').toString().trim();
  if (scope) {
    const scoped = await getPreference({ cwd: scope, key });
    if (isPreferenceTombstone(scoped)) return '';
    if (!isPreferenceAbsent(scoped)) return scoped;
  }
  const globalValue = await getPreference({ key });
  if (isPreferenceTombstone(globalValue)) return '';
  return isPreferenceAbsent(globalValue) ? null : globalValue;
}

function numberSetting(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function normalizeProviderName(_value) {
  return 'codex';
}

function providerNameFromPreference(value) {
  if (isPreferenceAbsent(value) || isPreferenceTombstone(value)) return SETTINGS_DEFAULTS.activeProvider;
  const provider = providerConfigValue(value).toLowerCase();
  if (provider === 'codex') return 'codex';
  if (provider === 'claude' || provider.startsWith('claude-')) return 'claude';
  throw new Error(`invalid provider preference: ${providerConfigValue(value) || String(value)}`);
}

function providerDefaults(provider) {
  return PROVIDER_DEFAULTS[normalizeProviderName(provider)] || PROVIDER_DEFAULTS.codex;
}

function canonicalizeProviderModel(provider, value) {
  const normalized = providerConfigValue(value);
  if (normalizeProviderName(provider) !== 'claude') return normalized;
  return CLAUDE_LONG_TO_SHORT[normalized] || normalized;
}

function isClaudeOpusFamilyModel(model) {
  const normalized = providerConfigValue(model).toLowerCase();
  return normalized === 'best' || normalized.includes('opus');
}

function normalizeProviderModelSetting(provider, value) {
  return canonicalizeProviderModel(provider, value) || providerDefaults(provider).model;
}

function normalizeProviderEffortSetting(provider, model, value) {
  const normalizedProvider = normalizeProviderName(provider);
  const normalizedValue = providerConfigValue(value).toLowerCase();
  if (normalizedProvider !== 'claude') {
    if (normalizedValue === 'minimal') return 'low';
    return EFFORT_MODES_BY_PROVIDER.codex.some((item) => item.value === normalizedValue)
      ? normalizedValue
      : providerDefaults(normalizedProvider).effort;
  }
  switch (normalizedValue) {
    case 'max':
      return isClaudeOpusFamilyModel(model) ? 'max' : 'high';
    case 'high':
    case 'xhigh':
      return 'high';
    case 'medium':
      return 'medium';
    case 'low':
    case 'minimal':
      return 'low';
    default:
      return providerDefaults(normalizedProvider).effort;
  }
}

function normalizeContextThresholds(value) {
  if (!Array.isArray(value) || value.length < 3) return SETTINGS_DEFAULTS.contextThresholds;
  return [
    numberSetting(value[0], SETTINGS_DEFAULTS.contextThresholds[0]),
    numberSetting(value[1], SETTINGS_DEFAULTS.contextThresholds[1]),
    numberSetting(value[2], SETTINGS_DEFAULTS.contextThresholds[2]),
  ];
}

function requireSettingsCwd(cwd, copy = APP_COPY.zh.settings) {
  const value = (cwd || '').toString().trim();
  if (!value) throw new Error(copy.projectCwdRequired || SETTINGS_PROJECT_CWD_REQUIRED);
  return value;
}

function normalizeSandboxMode(value) {
  const mode = (value || '').toString().trim();
  if (!mode) return SETTINGS_DEFAULTS.sandboxPolicy;
  if (mode === 'workspace-write') return 'workspaceWrite';
  if (mode === 'read-only') return 'readOnly';
  if (mode === 'danger-full-access') return 'dangerFullAccess';
  if (mode === 'workspaceWrite' || mode === 'readOnly' || mode === 'dangerFullAccess') return mode;
  throw new Error(`invalid sandbox policy: ${mode}`);
}

function sandboxPreferenceFromRaw(value) {
  if (isPreferenceAbsent(value) || isPreferenceTombstone(value)) return null;
  if (typeof value === 'string') {
    const text = value.trim();
    if (!text) return null;
    if (text.startsWith('{')) {
      try {
        const parsed = JSON.parse(text);
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
          throw new Error('sandbox preference JSON must be an object');
        }
        return parsed;
      } catch (error) {
        throw new Error('加载 Sandbox 失败：' + (error?.message || error), { cause: error });
      }
    }
    return { type: normalizeSandboxMode(text) };
  }
  if (value && typeof value === 'object' && !Array.isArray(value)) return value;
  throw new Error('加载 Sandbox 失败：sandbox preference must be an object');
}

function sandboxPolicyFromPreference(value) {
  if (value && typeof value === 'object') {
    return normalizeSandboxMode(value.type || value.mode || SETTINGS_DEFAULTS.sandboxPolicy);
  }
  return SETTINGS_DEFAULTS.sandboxPolicy;
}

function writableRootsFromPreference(value) {
  if (!value || typeof value !== 'object' || !Array.isArray(value.writableRoots)) return '';
  return value.writableRoots.join('\n');
}

function readOnlyModeFromPreference(value) {
  if (!value || typeof value !== 'object' || value.type !== 'readOnly') return SETTINGS_DEFAULTS.readOnlyMode;
  const access = value.access && typeof value.access === 'object' ? value.access : {};
  return access.type === 'restricted' ? 'restricted' : SETTINGS_DEFAULTS.readOnlyMode;
}

function readableRootsFromPreference(value) {
  if (!value || typeof value !== 'object' || value.type !== 'readOnly') return '';
  const access = value.access && typeof value.access === 'object' ? value.access : {};
  const roots = Array.isArray(access.readableRoots) ? access.readableRoots : access.readable_roots;
  return Array.isArray(roots) ? roots.join('\n') : '';
}

function pathsFromTextarea(value) {
  return (value || '')
    .toString()
    .split(/\r?\n/)
    .flatMap((item) => {
      const root = item.trim();
      return root ? [root] : [];
    });
}

function absolutePathsError(value, copy = APP_COPY.zh.settings) {
  const paths = pathsFromTextarea(value);
  if (paths.length === 0) return copy.provider.missingRoot;
  const bad = paths.filter((root) => !isAbsoluteRootPath(root));
  return bad.length > 0 ? copy.provider.absolutePathRequired + bad.join(', ') : '';
}

function isAbsoluteRootPath(value) {
  const root = (value || '').toString().trim();
  return root.startsWith('/') || /^[a-zA-Z]:[\\/]/.test(root) || /^\\\\[^\\]+\\[^\\]+/.test(root);
}

function sandboxPreferenceValue(policy, writableRootsText, networkAccess, readOnlyMode, readableRootsText) {
  if (policy === 'readOnly') {
    if (readOnlyMode === 'restricted') {
      return { type: 'readOnly', access: { type: 'restricted', readableRoots: pathsFromTextarea(readableRootsText), includePlatformDefaults: true } };
    }
    return { type: 'readOnly' };
  }
  if (policy === 'dangerFullAccess') return { type: 'dangerFullAccess' };
  return {
    type: 'workspaceWrite',
    writableRoots: pathsFromTextarea(writableRootsText),
    networkAccess: Boolean(networkAccess),
  };
}

function appendCurrentOption(options, currentValue) {
  const normalized = providerConfigValue(currentValue);
  if (!normalized || options.some((option) => providerConfigValue(option.value) === normalized)) return options;
  return [...options, { value: normalized, label: normalized }];
}

const providerSettingsViewConfig = Object.freeze({
  appendCurrentOption,
  effortModesByProvider: EFFORT_MODES_BY_PROVIDER,
  isClaudeOpusFamilyModel,
  modelOptionsByProvider: MODEL_OPTIONS_BY_PROVIDER,
  normalizeProviderEffortSetting,
  personalityOptions: PERSONALITY_OPTIONS,
});

function loadSettingsDashboardLogs() {
  return listDashboardLogs({ limit: 14 });
}

function parsePositiveInteger(label, value, copy = APP_COPY.zh.settings) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isInteger(parsed)) throw new Error(`${label} ${copy.runtime.integerSuffix}`);
  return parsed;
}

function validateRuntimeThresholds(form, copy = APP_COPY.zh.settings) {
  const stallThresholdSec = parsePositiveInteger(copy.runtime.stallThreshold, form.stallThresholdSec, copy);
  if (stallThresholdSec < 30) throw new Error(copy.runtime.minTimeout);

  const warn = parsePositiveInteger(copy.runtime.warnThreshold, form.contextWarn, copy);
  const danger = parsePositiveInteger(copy.runtime.dangerThreshold, form.contextDanger, copy);
  const critical = parsePositiveInteger(copy.runtime.criticalThreshold, form.contextCritical, copy);
  if (!(warn > 0 && warn < danger && danger < critical && critical <= 100)) {
    throw new Error(copy.runtime.invalidOrder);
  }
  return { stallThresholdSec, contextThresholds: [warn, danger, critical] };
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
    providerModelExplicit: false,
    providerEffortExplicit: false,
    providerModelTouched: false,
    providerEffortTouched: false,
    personality: SETTINGS_DEFAULTS.personality,
    sandboxPolicy: SETTINGS_DEFAULTS.sandboxPolicy,
    readOnlyMode: SETTINGS_DEFAULTS.readOnlyMode,
    readableRoots: SETTINGS_DEFAULTS.readableRoots,
    writableRoots: SETTINGS_DEFAULTS.writableRoots,
    networkAccess: SETTINGS_DEFAULTS.networkAccess,
  };
}

function normalizeSettingsCwd(value) {
  const text = (value || '').toString().trim();
  if (!text || text === '.' || text === '未选择项目') return '';
  return text;
}

function SettingsPage({ copy = APP_COPY.zh.settings, projectPath }) {
  const store = useClientStore();
  const cwd = normalizeSettingsCwd(projectPath) || normalizeSettingsCwd(store.activeProject) || normalizeSettingsCwd(store.cwd);
  const runtime = useSettingsRuntime(cwd, copy);
  const provider = useProviderPreferences(cwd, runtime.form.activeProvider, copy);
  const prompt = usePromptSettings(cwd, copy);
  const builtins = useBuiltinToolsSettings(cwd, copy);
  return <SettingsPageView builtins={builtins} copy={copy} cwd={cwd} prompt={prompt} provider={provider} runtime={runtime} store={store} />;
}

function useSettingsRuntime(cwd, copy) {
  const [buildInfo, setBuildInfo] = useState(null);
  const [form, setForm] = useState(defaultSettingsForm);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [updateInfo, setUpdateInfo] = useState(null);
  const [updateBusy, setUpdateBusy] = useState(false);
  const [updateInstalling, setUpdateInstalling] = useState(false);
  const [updateNotice, setUpdateNotice] = useState({ level: 'info', message: '' });
  const preferenceRequestSeq = useRef(0);
  const nextPreferenceRequest = useCallback(() => {
    preferenceRequestSeq.current += 1;
    const requestSeq = preferenceRequestSeq.current;
    return () => preferenceRequestSeq.current === requestSeq;
  }, []);
  const refreshBuildInfo = useCallback(async () => {
    setError('');
    try {
      const info = await getBuildInfo();
      if (!info || typeof info !== 'object') throw new Error('build info response must be an object');
      setBuildInfo(info);
      setStatus(copy.buildInfoRefreshed);
    } catch (err) {
      setError(err.message || String(err));
    }
  }, [copy]);
  const loadPreferences = useCallback(() => loadRuntimePreferences({ cwd, isCurrent: nextPreferenceRequest(), setError, setForm }), [cwd, nextPreferenceRequest]);
  const updateForm = useCallback((key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => settingsFormWithUpdate(current, key, value));
  }, []);
  const changeActiveProvider = useCallback((event) => changeActiveProviderPreference({ copy, cwd, event, isCurrent: nextPreferenceRequest(), setError, setForm, setStatus }), [copy, cwd, nextPreferenceRequest]);
  const saveRuntimeSettings = useCallback(() => saveRuntimePreferences({ copy, cwd, form, setError, setStatus }), [copy, cwd, form]);
  const saveProviderSettings = useCallback(() => saveProviderRuntimePreferences({ copy, cwd, form, setError, setStatus }), [copy, cwd, form]);
  const checkForUpdate = useCallback(() => checkForAppUpdate({ copy, setUpdateBusy, setUpdateInfo, setUpdateNotice, updateBusy, updateInstalling }), [copy, updateBusy, updateInstalling]);
  const installUpdate = useCallback(() => installAvailableAppUpdate({ copy, setUpdateInfo, setUpdateInstalling, setUpdateNotice, updateInfo, updateInstalling }), [copy, updateInfo, updateInstalling]);
  useEffect(() => { void refreshBuildInfo(); }, [refreshBuildInfo]);
  useEffect(() => { void loadPreferences(); }, [loadPreferences]);
  return { buildInfo, changeActiveProvider, checkForUpdate, error, form, installUpdate, refreshBuildInfo, saveProviderSettings, saveRuntimeSettings, status, updateBusy, updateInfo, updateInstalling, updateNotice, updateForm };
}

async function checkForAppUpdate({ copy, setUpdateBusy, setUpdateInfo, setUpdateNotice, updateBusy, updateInstalling }) {
  const updateCopy = copy.update;
  if (updateBusy || updateInstalling) return;
  setUpdateInfo(null);
  setUpdateBusy(true);
  setUpdateNotice({ level: 'info', message: updateCopy.checking });
  try {
    const info = await checkAppUpdate();
    if (info?.enabled === false) {
      setUpdateInfo(null);
      setUpdateNotice({ level: 'warning', message: updateCopy.disabled });
    } else if (info?.available) {
      setUpdateInfo(info);
      setUpdateNotice({ level: 'info', message: updateCopy.found + ' ' + appUpdateVersionLabel(info, copy) });
    } else {
      setUpdateInfo(null);
      setUpdateNotice({ level: 'info', message: updateCopy.latest });
    }
  } catch (error) {
    setUpdateInfo(null);
    setUpdateNotice({ level: 'error', message: updateCopy.checkFailed + (error?.message || error) });
  } finally {
    setUpdateBusy(false);
  }
}

async function installAvailableAppUpdate({ copy, setUpdateInfo, setUpdateInstalling, setUpdateNotice, updateInfo, updateInstalling }) {
  if (!updateInfo?.available || updateInstalling) return;
  const pendingInfo = updateInfo;
  const installingMessage = appUpdateInstallingMessage(pendingInfo, copy);
  setUpdateInstalling(true);
  setUpdateInfo(null);
  setUpdateNotice({ level: 'info', message: installingMessage });
  try {
    await installLatestAppUpdate();
    setUpdateNotice({ level: 'info', message: installingMessage });
  } catch (error) {
    setUpdateInfo(pendingInfo);
    setUpdateInstalling(false);
    setUpdateNotice({ level: 'error', message: copy.update.installFailed + (error?.message || error) });
  }
}

function appUpdateVersionLabel(info, copy = APP_COPY.zh.settings) {
  const version = appUpdateConcreteVersionLabel(info) || copy.update.availableUpdate;
  const platform = (info?.platform || info?.artifact?.platform || '').toString().trim();
  return platform ? `${version} (${platform})` : version;
}

function appUpdateInstallingMessage(info, copy = APP_COPY.zh.settings) {
  const version = appUpdateConcreteVersionLabel(info);
  if (!version) return copy.update.installing;
  const platform = (info?.platform || info?.artifact?.platform || '').toString().trim();
  return copy.update.installing + ' ' + (platform ? `${version} (${platform})` : version);
}

function appUpdateConcreteVersionLabel(info) {
  return (info?.version || info?.latestVersion || info?.latest_version || '').toString().trim();
}

function appUpdateCurrentVersionLabel(buildInfo) {
  const packagedVersion = (buildInfo?.appVersion || buildInfo?.app_version || buildInfo?.updateVersion || buildInfo?.update_version || '').toString().trim();
  if (packagedVersion) return appUpdateDisplayVersion(packagedVersion);
  return (buildInfo?.version || 'unknown').toString().trim();
}

function appUpdateDisplayVersion(version) {
  const value = (version || '').toString().trim();
  if (!value) return '';
  if (/^[0-9]+(?:\.[0-9]+){1,2}(?:[-+].*)?$/.test(value)) return `v${value}`;
  return value;
}

function settingsFormWithUpdate(current, key, value) {
  if (key === 'activeProvider') {
    const activeProvider = normalizeProviderName(value);
    const providerModel = normalizeProviderModelSetting(activeProvider, current.providerModel);
    return {
      ...current,
      activeProvider,
      providerModel,
      providerEffort: normalizeProviderEffortSetting(activeProvider, providerModel, current.providerEffort),
      providerModelExplicit: false,
      providerEffortExplicit: false,
      providerModelTouched: false,
      providerEffortTouched: false,
    };
  }
  if (key === 'providerModel') {
    const providerModel = normalizeProviderModelSetting(current.activeProvider, value);
    return {
      ...current,
      providerModel,
      providerEffort: normalizeProviderEffortSetting(current.activeProvider, providerModel, current.providerEffort),
      providerModelTouched: true,
    };
  }
  if (key === 'providerEffort') {
    return {
      ...current,
      providerEffort: normalizeProviderEffortSetting(current.activeProvider, current.providerModel, value),
      providerEffortTouched: true,
    };
  }
  return { ...current, [key]: value };
}

function isCurrentPreferenceRequest(isCurrent) {
  return typeof isCurrent !== 'function' || isCurrent();
}

async function loadRuntimePreferences({ cwd, isCurrent, setError, setForm }) {
  setError('');
  if (!cwd) return;
  try {
    if (!isCurrentPreferenceRequest(isCurrent)) return;
    const values = await readRuntimePreferenceValues(cwd);
    if (isCurrentPreferenceRequest(isCurrent)) {
      setForm(settingsFormFromPreferences(values));
    }
  } catch (err) {
    if (isCurrentPreferenceRequest(isCurrent)) {
      setError(err.message || String(err));
    }
  }
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

async function readRuntimePreferenceValuesForProvider(cwd, activeProvider) {
  const [stallValue, contextValue, providerValues] = await Promise.all([
    getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
    getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
    readProviderPreferenceValues(cwd, activeProvider),
  ]);
  return { activeProvider, contextValue, providerValues, stallValue };
}

async function readProviderPreferenceValues(cwd, activeProvider) {
  const providerPrefix = 'settings.provider.' + activeProvider;
  const isCodex = activeProvider === 'codex';
  return Promise.all([
    isCodex ? readScopedPreference(cwd, providerSettingKey('codex', 'codexHome')) : Promise.resolve(null),
    isCodex ? readScopedPreference(cwd, providerSettingKey('codex', 'codexInstanceKey')) : Promise.resolve(null),
    readScopedPreference(cwd, providerPrefix + '.model'),
    readScopedPreference(cwd, providerPrefix + '.effort'),
    readScopedPreference(cwd, providerPrefix + '.personality'),
    readScopedPreference(cwd, providerPrefix + '.sandbox'),
  ]);
}

function settingsFormFromPreferences({ activeProvider, contextValue, providerValues, stallValue }) {
  const [codexHome, codexInstanceKey, providerModel, providerEffort, personality, sandbox] = providerValues;
  const contextThresholds = normalizeContextThresholds(contextValue);
  const model = normalizeProviderModelSetting(activeProvider, providerModel);
  const sandboxPreference = sandboxPreferenceFromRaw(sandbox);
  return {
    ...defaultSettingsForm(),
    stallThresholdSec: String(numberSetting(stallValue, SETTINGS_DEFAULTS.stallThresholdSec)),
    contextWarn: String(contextThresholds[0]),
    contextDanger: String(contextThresholds[1]),
    contextCritical: String(contextThresholds[2]),
    activeProvider,
    codexHome: stringSetting(codexHome, SETTINGS_DEFAULTS.codexHome),
    codexInstanceKey: stringSetting(codexInstanceKey, SETTINGS_DEFAULTS.codexInstanceKey),
    providerModel: model,
    providerEffort: normalizeProviderEffortSetting(activeProvider, model, providerEffort),
    providerModelExplicit: !isPreferenceAbsent(providerModel),
    providerEffortExplicit: !isPreferenceAbsent(providerEffort),
    providerModelTouched: false,
    providerEffortTouched: false,
    personality: providerConfigValue(personality) || SETTINGS_DEFAULTS.personality,
    sandboxPolicy: sandboxPolicyFromPreference(sandboxPreference),
    readOnlyMode: readOnlyModeFromPreference(sandboxPreference),
    readableRoots: readableRootsFromPreference(sandboxPreference),
    writableRoots: writableRootsFromPreference(sandboxPreference),
    networkAccess: Boolean(sandboxPreference && typeof sandboxPreference === 'object' && sandboxPreference.networkAccess),
  };
}

async function changeActiveProviderPreference({ copy, cwd, event, isCurrent, setError, setForm, setStatus }) {
  const provider = normalizeProviderName(event.target.value);
  setError('');
  setStatus('');
  setForm((current) => settingsFormWithUpdate(current, 'activeProvider', provider));
  try {
    const projectCwd = requireSettingsCwd(cwd, copy);
    if (!isCurrentPreferenceRequest(isCurrent)) return;
    await setPreference({ cwd: projectCwd, key: SETTINGS_KEYS.activeProvider, value: provider });
    const values = await readRuntimePreferenceValuesForProvider(projectCwd, provider);
    if (isCurrentPreferenceRequest(isCurrent)) {
      setForm(settingsFormFromPreferences(values));
      setStatus(copy.provider.switchedTo + (PROVIDER_LABELS[provider] || provider));
    }
  } catch (err) {
    if (isCurrentPreferenceRequest(isCurrent)) {
      setError(err.message || String(err));
    }
  }
}

async function saveRuntimePreferences({ copy, cwd, form, setError, setStatus }) {
  setError('');
  setStatus('');
  try {
    const projectCwd = requireSettingsCwd(cwd, copy);
    const { stallThresholdSec, contextThresholds } = validateRuntimeThresholds(form, copy);
    await setPreference({ cwd: projectCwd, key: SETTINGS_KEYS.stallThreshold, value: stallThresholdSec });
    await setPreference({ cwd: projectCwd, key: SETTINGS_KEYS.contextThresholds, value: contextThresholds });
    setStatus(copy.runtime.saved);
  } catch (err) {
    setError(err.message || String(err));
  }
}

async function saveProviderRuntimePreferences({ copy, cwd, form, setError, setStatus }) {
  setError('');
  setStatus('');
  try {
    const projectCwd = requireSettingsCwd(cwd, copy);
    const rootError = form.sandboxPolicy === 'workspaceWrite' ? absolutePathsError(form.writableRoots, copy) : '';
    if (rootError) throw new Error(rootError);
    const provider = normalizeProviderName(form.activeProvider);
    await writeProviderRuntimePreferences(projectCwd, provider, form);
    setStatus(copy.provider.settingsSaved);
  } catch (err) {
    setError(err.message || String(err));
  }
}

async function writeProviderRuntimePreferences(cwd, provider, form) {
  const providerModel = normalizeProviderModelSetting(provider, form.providerModel);
  const providerEffort = normalizeProviderEffortSetting(provider, providerModel, form.providerEffort);
  const calls = [
    setPreference({ cwd, key: providerSettingKey(provider, 'personality'), value: form.personality.trim() }),
    setPreference({ cwd, key: providerSettingKey(provider, 'sandbox'), value: sandboxPreferenceValue(form.sandboxPolicy, form.writableRoots, form.networkAccess, form.readOnlyMode, form.readableRoots) }),
  ];
  if (form.providerModelExplicit || form.providerModelTouched) {
    calls.push(setPreference({ cwd, key: providerSettingKey(provider, 'model'), value: providerModel }));
  }
  if (form.providerEffortExplicit || form.providerEffortTouched) {
    calls.push(setPreference({ cwd, key: providerSettingKey(provider, 'effort'), value: providerEffort }));
  }
  if (provider === 'codex') {
    calls.push(
      setPreference({ cwd, key: providerSettingKey('codex', 'codexHome'), value: codexIdentityPreferenceValue(form.codexHome) }),
      setPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey'), value: codexIdentityPreferenceValue(form.codexInstanceKey) }),
    );
  }
  await Promise.all(calls);
}

function codexIdentityPreferenceValue(value) {
  const text = (value || '').toString().trim();
  return text ? text : { cleared: true };
}

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
        setSummaryMode(providerConfigValue(summaryValue) || 'detailed');
        setApprovalMode(providerConfigValue(approvalValue) || 'on-request');
        setNotice({ level: 'info', message: '' });
      }
    } catch (error) {
      if (isCurrentPreferenceRequest(isCurrent)) {
        setNotice({ level: 'error', message: copy.provider.loadPreferencesFailed + error.message });
      }
    }
  }, [activeProvider, copy, cwd, nextLoadRequest]);
  const save = useCallback(() => saveProviderPreferenceValues({ approvalMode, copy, cwd, provider, saving, setNotice, setSaving, summaryMode }), [approvalMode, copy, cwd, provider, saving, summaryMode]);
  useEffect(() => { void load(); }, [load]);
  return { approvalMode, load, notice, provider, save, saving, setApprovalMode, setSummaryMode, summaryMode };
}

async function saveProviderPreferenceValues({ approvalMode, copy, cwd, provider, saving, setNotice, setSaving, summaryMode }) {
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
  const loadPrompt = useCallback(() => loadLspPromptState({ copy, cwd, setDefaultHint, setEffectiveHint, setHint, setLoading, setNotice, setUsingDefault }), [copy, cwd]);
  const loadScope = useCallback(() => loadPromptScope(setCurrentScopeCwd), []);
  const loadVisibility = useCallback(() => loadInjectedPromptVisibility({ copy, cwd, setNotice, setShowInjected }), [copy, cwd]);
  const save = useCallback(() => saveLspPromptHintState({ copy, cwd, defaultHint, hint, saving, setDefaultHint, setEffectiveHint, setHint, setNotice, setSaving, setUsingDefault }), [copy, cwd, defaultHint, hint, saving]);
  const reset = useCallback(() => saveLspPromptHintState({ copy, cwd, defaultHint, hint: '', saving, setDefaultHint, setEffectiveHint, setHint, setNotice, setSaving, setUsingDefault }), [copy, cwd, defaultHint, saving]);
  const copyPrompt = useCallback(() => copyEffectivePromptHint(promptDisplayHint(effectiveHint, defaultHint, copy), copy, setNotice), [copy, defaultHint, effectiveHint]);
  const toggleVisibility = useCallback((event) => saveInjectedPromptVisibility({ copy, cwd, event, loadVisibility, saving: showInjectedSaving, setNotice, setSaving: setShowInjectedSaving, setShowInjected }), [copy, cwd, loadVisibility, showInjectedSaving]);
  useEffect(() => { void loadPrompt(); void loadScope(); void loadVisibility(); }, [loadPrompt, loadScope, loadVisibility]);
  return promptSettingsModel({ copy: copyPrompt, currentScopeCwd, defaultHint, effectiveHint, hint, loadPrompt, loading, notice, reset, save, saving, setHint, showInjected, showInjectedSaving, textCopy: copy, toggleVisibility, usingDefault });
}

function promptSettingsModel(model) {
  const displayHint = promptDisplayHint(model.effectiveHint, model.defaultHint, model.textCopy);
  const empty = model.textCopy.promptCard.empty;
  const lineCount = displayHint === empty ? 0 : displayHint.split('\n').length;
  const charCount = displayHint === empty ? 0 : displayHint.length;
  return { ...model, charCount, displayHint, lineCount, modeLabel: promptModeLabel(model.loading, model.usingDefault, model.textCopy) };
}

function promptDisplayHint(effectiveHint, defaultHint, copy = APP_COPY.zh.settings) {
  return (effectiveHint || defaultHint || '').trim() || copy.promptCard.empty;
}

function promptModeLabel(loading, usingDefault, copy = APP_COPY.zh.settings) {
  if (loading) return copy.promptCard.loading;
  return usingDefault ? copy.promptCard.defaultMode : copy.promptCard.customMode;
}

async function loadLspPromptState(state) {
  if (!state.cwd) return;
  state.setLoading(true);
  try {
    const res = await readLspPromptHint({ cwd: state.cwd });
    state.setHint((res?.overrideHint || '').toString());
    state.setEffectiveHint((res?.hint || '').toString());
    state.setDefaultHint((res?.defaultHint || '').toString());
    state.setUsingDefault(Boolean(res?.usingDefault) || (res?.overrideHint || '').toString().trim() === '');
    state.setNotice({ level: 'info', message: '' });
  } catch (error) {
    state.setNotice({ level: 'error', message: state.copy.promptCard.loadFailed + (error?.message || error) });
  } finally {
    state.setLoading(false);
  }
}

async function loadPromptScope(setCurrentScopeCwd) {
  try {
    const cfg = await readConfig();
    setCurrentScopeCwd((cfg?.cwd || '').toString().trim());
  } catch {
    setCurrentScopeCwd('');
  }
}

async function loadInjectedPromptVisibility({ copy, cwd, setNotice, setShowInjected }) {
  if (!cwd) return;
  try {
    const value = await getPreference({ cwd, key: 'settings.showInjectedPromptInChat' });
    setShowInjected(parseBoolPreference(value));
  } catch (error) {
    setNotice({ level: 'error', message: copy.promptCard.loadToggleFailed + (error?.message || error) });
  }
}

async function saveLspPromptHintState(state) {
  if (!state.cwd || state.saving) return;
  state.setSaving(true);
  try {
    const res = await writeLspPromptHint({ cwd: state.cwd, hint: state.hint });
    state.setEffectiveHint((res?.hint || '').toString());
    state.setDefaultHint((res?.defaultHint || state.defaultHint || '').toString());
    state.setHint((res?.overrideHint || '').toString());
    state.setUsingDefault(Boolean(res?.usingDefault));
    state.setNotice({ level: 'info', message: res?.usingDefault ? state.copy.promptCard.restored : state.copy.promptCard.saved });
  } catch (error) {
    state.setNotice({ level: 'error', message: state.copy.promptCard.saveFailed + (error?.message || error) });
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
    setNotice({ level: 'error', message: copy.promptCard.copyFailedPrefix + (error?.message || error) });
  }
}

async function saveInjectedPromptVisibility({ copy, cwd, event, loadVisibility, saving, setNotice, setSaving, setShowInjected }) {
  if (!cwd || saving) return;
  const next = event.target.checked;
  setShowInjected(next);
  setSaving(true);
  try {
    await setPreference({ cwd, key: 'settings.showInjectedPromptInChat', value: next });
    setNotice({ level: 'info', message: next ? copy.promptCard.showInjectedSaved : copy.promptCard.hideInjectedSaved });
  } catch (error) {
    setNotice({ level: 'error', message: copy.promptCard.saveToggleFailed + (error?.message || error) });
    await loadVisibility();
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

function useBuiltinToolsSettings(cwd, copy) {
  const builtinsCopy = copy.builtins;
  const [tools, setTools] = useState([]);
  const [loading, setLoading] = useState(false);
  const [savingIds, setSavingIds] = useState({});
  const [expandedGroups, setExpandedGroups] = useState({});
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const applyPayload = useCallback((payload) => setTools(normalizeBuiltinTools(payload)), []);
  const load = useCallback(() => loadBuiltinTools({ applyPayload, copy, cwd, setLoading, setNotice }), [applyPayload, copy, cwd]);
  const toggleTool = useCallback((tool) => toggleBuiltinTool({ applyPayload, copy, cwd, savingIds, setNotice, setSavingIds, setTools, tool }), [applyPayload, copy, cwd, savingIds]);
  const toggleGroup = useCallback((key) => setExpandedGroups((prev) => ({ ...prev, [key]: !prev[key] })), []);
  const groups = useMemo(() => builtinToolGroups(tools, builtinsCopy), [builtinsCopy, tools]);
  useEffect(() => { void load(); }, [load]);
  return {
    expandedGroups,
    filteredCount: tools.filter((tool) => tool.replacedBy || !tool.enabled).length,
    groups,
    groupSummary: (group) => groupSummary(group, builtinsCopy),
    isOpen: (key) => Boolean(expandedGroups[key]),
    load,
    loading,
    notice,
    savingIds,
    toggleGroup,
    toggleTool,
    toolMetaText: (tool) => toolMetaText(tool, builtinsCopy),
    totalToolCount: tools.length,
    tools,
  };
}

function normalizeBuiltinTools(payload) {
  const list = Array.isArray(payload?.tools) ? payload.tools : [];
  return list.map(normalizeBuiltinTool);
}

function normalizeBuiltinTool(item) {
  return {
    id: textValue(item.id),
    label: textValue(item.label || item.id),
    description: textValue(item.description),
    enabled: Boolean(item.enabled),
    provider: textValue(item.provider || 'claude'),
    replacedBy: optionalTextValue(item.replacedBy),
    filterMode: optionalTextValue(item.filterMode),
    enforcement: optionalTextValue(item.enforcement),
  };
}

function textValue(value) {
  return (value || '').toString();
}

function optionalTextValue(value) {
  const text = textValue(value);
  return text || undefined;
}

async function loadBuiltinTools({ applyPayload, copy, cwd, setLoading, setNotice }) {
  if (!cwd) return;
  setLoading(true);
  try {
    applyPayload(await readBuiltinTools({ cwd }));
    setNotice({ level: 'info', message: '' });
  } catch (error) {
    setNotice({ level: 'error', message: copy.builtins.loadFailed + (error?.message || error) });
  } finally {
    setLoading(false);
  }
}

async function toggleBuiltinTool({ applyPayload, copy, cwd, savingIds, setNotice, setSavingIds, setTools, tool }) {
  if (!cwd || tool.replacedBy || !tool.id || savingIds[tool.id]) return;
  const nextEnabled = !tool.enabled;
  setSavingIds((prev) => ({ ...prev, [tool.id]: true }));
  setTools((prev) => prev.map((item) => (item.id === tool.id ? { ...item, enabled: nextEnabled } : item)));
  try {
    applyPayload(await writeBuiltinTool({ cwd, id: tool.id, enabled: nextEnabled }));
    setNotice({ level: 'info', message: (tool.label || tool.id) + ' ' + (nextEnabled ? copy.builtins.enabledSuffix : copy.builtins.disabledSuffix) });
  } catch (error) {
    setTools((prev) => prev.map((item) => (item.id === tool.id ? { ...item, enabled: !nextEnabled } : item)));
    setNotice({ level: 'error', message: copy.builtins.saveFailed + (error?.message || error) });
  } finally {
    setSavingIds((prev) => ({ ...prev, [tool.id]: false }));
  }
}

function builtinToolGroups(tools, copy) {
  const disabled = tools.filter((tool) => !tool.enabled || tool.replacedBy);
  return [
    builtinToolGroup('native-hard', copy.nativeHard, disabled.filter((tool) => builtinToolEnforcement(tool) === 'native-hard'), copy.nativeHardNote, copy),
    builtinToolGroup('effect-hard', copy.effectHard, disabled.filter((tool) => builtinToolEnforcement(tool) === 'effect-hard'), copy.effectHardNote, copy),
    builtinToolGroup('soft-audit', copy.softAudit, disabled.filter((tool) => builtinToolEnforcement(tool) === 'soft-audit'), copy.softAuditNote, copy),
    builtinUnfilteredGroup(tools, copy),
  ].filter(Boolean);
}

function builtinToolGroup(key, label, tools, note, copy) {
  if (tools.length === 0) return null;
  return { canToggle: true, disabledCount: tools.length, key, label: label + copy.countOpen + tools.length + copy.countClose, note, tools };
}

function builtinUnfilteredGroup(tools, copy) {
  const available = tools.filter((tool) => tool.enabled && !tool.replacedBy);
  if (!available.length) return null;
  return { canToggle: true, disabledCount: 0, key: 'unfiltered', label: copy.unfiltered + copy.countOpen + available.length + copy.countClose, tools: available };
}

function builtinToolEnforcement(tool) {
  const enforcement = (tool.enforcement || '').toString().trim();
  if (enforcement) return enforcement;
  return tool.filterMode === 'hard' ? 'native-hard' : 'soft-audit';
}

function toolStatusLabel(tool, copy) {
  if (tool.replacedBy) return copy.replaced;
  if (tool.enabled) return copy.unfiltered;
  const enforcement = builtinToolEnforcement(tool);
  if (enforcement === 'native-hard') return copy.nativeHard;
  if (enforcement === 'effect-hard') return copy.effectHard;
  return enforcement === 'soft-audit' ? copy.softAudit : copy.controlledStatus;
}

function toolMetaText(tool, copy) {
  const parts = [];
  const description = (tool.description || '').trim();
  if (description) parts.push(description);
  const provider = PROVIDER_LABELS[tool.provider] || tool.provider || '';
  if (provider) parts.push(provider);
  parts.push(toolStatusLabel(tool, copy));
  return parts.join(' · ');
}

function groupSummary(group, copy) {
  if (group.key === 'unfiltered') return copy.availableCount.replace('{count}', group.tools.length);
  return copy.controlledCount.replace('{count}', group.disabledCount);
}

function mobileAccountName(cwd, fallback = '本地用户') {
  const parts = (cwd || '').toString().split(/[\\/]/).filter(Boolean);
  return parts.at(-1) || fallback;
}

function MobileAccountPanel({ copy = APP_COPY.zh.settings, cwd, runtime }) {
  const accountName = mobileAccountName(cwd, copy.accountNameFallback);
  const provider = PROVIDER_LABELS[runtime.form.activeProvider] || runtime.form.activeProvider || 'Codex';
  return (
    <section className="settings-mobile-account" data-testid="settings-mobile-account" aria-label={copy.mobileAccount}>
      <header>
        <button type="button" aria-label={copy.menu} disabled><Menu size={18} /></button>
        <h2>{APP_BRAND_NAME}</h2>
        <div className="settings-mobile-avatar" aria-label={copy.avatar}>SY</div>
      </header>
      <div className="settings-mobile-card">
        <span>{copy.username}</span>
        <strong>{accountName}</strong>
        <small>{cwd || copy.noProject}</small>
      </div>
      <div className="settings-mobile-card">
        <span>{copy.account}</span>
        <strong>{provider}</strong>
        <small>{copy.accountDescription}</small>
      </div>
      <div className="settings-mobile-card">
        <span>{copy.settings}</span>
        <strong>{copy.runtimeConfig}</strong>
        <small>{copy.runtimeDescription}</small>
      </div>
      <div className="settings-mobile-card is-disabled">
        <span>{copy.logout}</span>
        <strong>{copy.authPending}</strong>
        <button type="button" data-testid="settings-mobile-logout-button" disabled>{copy.logoutTab}</button>
      </div>
      <nav className="settings-mobile-tabs" aria-label={copy.mobileAccount}>
        <button type="button" disabled>{copy.accountTab}</button>
        <button type="button" disabled>{copy.settingsTab}</button>
        <button type="button" disabled>{copy.logoutTab}</button>
      </nav>
    </section>
  );
}

function SettingsPageView({ builtins, copy = APP_COPY.zh.settings, cwd, prompt, provider, runtime, store }) {
  return (
    <section className="settings-page" data-testid="settings-page">
      <PageHeader icon={Settings} title={copy.title} actions={<button className="btn btn-secondary" type="button" data-testid="settings-refresh-build-button" onClick={() => void runtime.refreshBuildInfo()}>{copy.refreshBuildInfo}</button>} />
      <MobileAccountPanel copy={copy} cwd={cwd} runtime={runtime} />
      <SettingsNotices error={runtime.error} status={runtime.status} />
      <div className="panel-body" data-testid="settings-panel-body">
        <AboutPanel buildInfo={runtime.buildInfo} copy={copy} cwd={cwd} runtime={runtime} updateCurrentVersion={appUpdateCurrentVersionLabel(runtime.buildInfo)} />
        <RuntimeSettingsPanels copy={copy} runtime={runtime} />
        <ProviderSettingsPanel copy={copy} runtime={runtime} viewConfig={providerSettingsViewConfig} />
        <ProviderPropertiesCard copy={copy} provider={provider} />
        <ModelProvidersCard copy={copy} cwd={cwd} />
        <PromptSettingsCard copy={copy} prompt={prompt} />
        <BuiltinToolsCard builtins={builtins} copy={copy} />
        <MCPToolLifecycleCard copy={copy} cwd={cwd} />
        <VideoSettingsCard copy={copy} getApiKey={getVideoApiKey} setApiKey={setVideoApiKey} />
        <UILogCard copy={copy} loadLogs={loadSettingsDashboardLogs} store={store} />
      </div>
    </section>
  );
}

function SettingsNotices({ error, status }) {
  return (
    <>
      {status ? <output className="settings-page-notice settings-status">{status}</output> : null}
      {error ? <p className="settings-page-notice danger-text" role="alert">{error}</p> : null}
    </>
  );
}

export { SettingsPage };

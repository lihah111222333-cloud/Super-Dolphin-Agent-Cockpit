import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Settings } from 'lucide-react';
import { useClientStore } from '../../entities/client/model/useClientStore.js';
import { checkAppUpdate, copyTextToClipboard, getBuildInfo, getPreference, getVideoApiKey, installLatestAppUpdate, listDashboardLogs, readBuiltinTools, readConfig, readLspPromptHint, setPreference, setVideoApiKey, writeBuiltinTool, writeLspPromptHint as writeLspPromptHintBackend } from '../../shared/api/backendApi.js';
import { PageHeader } from '../shared/pageComponents.jsx';
import { BuiltinToolsCard } from './components/BuiltinToolsCard.jsx';
import { ProviderPropertiesCard, ProviderSettingsPanel } from './components/ProviderSettingsPanels.jsx';
import { PromptSettingsCard } from './components/PromptSettingsCard.jsx';
import { AboutPanel, RuntimeSettingsPanels } from './components/SettingsSystemPanels.jsx';
import { UILogCard } from './components/UILogCard.jsx';
import { VideoSettingsCard } from './components/VideoSettingsCard.jsx';

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
    { value: 'minimal', label: '极低' },
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

function requireSettingsCwd(cwd) {
  const value = (cwd || '').toString().trim();
  if (!value) throw new Error(SETTINGS_PROJECT_CWD_REQUIRED);
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

function absolutePathsError(value) {
  const paths = pathsFromTextarea(value);
  if (paths.length === 0) return '请至少填写一个绝对路径';
  const bad = paths.filter((root) => !isAbsoluteRootPath(root));
  return bad.length > 0 ? `路径必须是绝对路径：${bad.join(', ')}` : '';
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

function parsePositiveInteger(label, value) {
  const parsed = Number.parseInt(value, 10);
  if (!Number.isInteger(parsed)) throw new Error(`${label} 必须是整数`);
  return parsed;
}

function validateRuntimeThresholds(form) {
  const stallThresholdSec = parsePositiveInteger('统一超时阈值', form.stallThresholdSec);
  if (stallThresholdSec < 30) throw new Error('统一超时阈值必须大于或等于 30 秒');

  const warn = parsePositiveInteger('Warn 阈值', form.contextWarn);
  const danger = parsePositiveInteger('Danger 阈值', form.contextDanger);
  const critical = parsePositiveInteger('Critical 阈值', form.contextCritical);
  if (!(warn > 0 && warn < danger && danger < critical && critical <= 100)) {
    throw new Error('上下文阈值必须满足 0 < warn < danger < critical <= 100');
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

function SettingsPage({ projectPath }) {
  const store = useClientStore();
  const cwd = normalizeSettingsCwd(projectPath) || normalizeSettingsCwd(store.activeProject) || normalizeSettingsCwd(store.cwd);
  const runtime = useSettingsRuntime(cwd);
  const provider = useProviderPreferences(cwd, runtime.form.activeProvider);
  const prompt = usePromptSettings(cwd);
  const builtins = useBuiltinToolsSettings(cwd);
  return <SettingsPageView builtins={builtins} cwd={cwd} prompt={prompt} provider={provider} runtime={runtime} store={store} />;
}

function useSettingsRuntime(cwd) {
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
      setStatus('构建信息已刷新');
    } catch (err) {
      setError(err.message || String(err));
    }
  }, []);
  const loadPreferences = useCallback(() => loadRuntimePreferences({ cwd, isCurrent: nextPreferenceRequest(), setError, setForm }), [cwd, nextPreferenceRequest]);
  const updateForm = useCallback((key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => settingsFormWithUpdate(current, key, value));
  }, []);
  const changeActiveProvider = useCallback((event) => changeActiveProviderPreference({ cwd, event, isCurrent: nextPreferenceRequest(), setError, setForm, setStatus }), [cwd, nextPreferenceRequest]);
  const saveRuntimeSettings = useCallback(() => saveRuntimePreferences({ cwd, form, setError, setStatus }), [cwd, form]);
  const saveProviderSettings = useCallback(() => saveProviderRuntimePreferences({ cwd, form, setError, setStatus }), [cwd, form]);
  const checkForUpdate = useCallback(() => checkForAppUpdate({ setUpdateBusy, setUpdateInfo, setUpdateNotice, updateBusy, updateInstalling }), [updateBusy, updateInstalling]);
  const installUpdate = useCallback(() => installAvailableAppUpdate({ setUpdateInfo, setUpdateInstalling, setUpdateNotice, updateInfo, updateInstalling }), [updateInfo, updateInstalling]);
  useEffect(() => { void refreshBuildInfo(); }, [refreshBuildInfo]);
  useEffect(() => { void loadPreferences(); }, [loadPreferences]);
  return { buildInfo, changeActiveProvider, checkForUpdate, error, form, installUpdate, refreshBuildInfo, saveProviderSettings, saveRuntimeSettings, status, updateBusy, updateInfo, updateInstalling, updateNotice, updateForm };
}

async function checkForAppUpdate({ setUpdateBusy, setUpdateInfo, setUpdateNotice, updateBusy, updateInstalling }) {
  if (updateBusy || updateInstalling) return;
  setUpdateInfo(null);
  setUpdateBusy(true);
  setUpdateNotice({ level: 'info', message: '检查中...' });
  try {
    const info = await checkAppUpdate();
    if (info?.enabled === false) {
      setUpdateInfo(null);
      setUpdateNotice({ level: 'warning', message: '当前构建未启用应用更新' });
    } else if (info?.available) {
      setUpdateInfo(info);
      setUpdateNotice({ level: 'info', message: '发现新版本 ' + appUpdateVersionLabel(info) });
    } else {
      setUpdateInfo(null);
      setUpdateNotice({ level: 'info', message: '已是最新版本' });
    }
  } catch (error) {
    setUpdateInfo(null);
    setUpdateNotice({ level: 'error', message: '检查更新失败：' + (error?.message || error) });
  } finally {
    setUpdateBusy(false);
  }
}

async function installAvailableAppUpdate({ setUpdateInfo, setUpdateInstalling, setUpdateNotice, updateInfo, updateInstalling }) {
  if (!updateInfo?.available || updateInstalling) return;
  const pendingInfo = updateInfo;
  const installingMessage = appUpdateInstallingMessage(pendingInfo);
  setUpdateInstalling(true);
  setUpdateInfo(null);
  setUpdateNotice({ level: 'info', message: installingMessage });
  try {
    await installLatestAppUpdate();
    setUpdateNotice({ level: 'info', message: installingMessage });
  } catch (error) {
    setUpdateInfo(pendingInfo);
    setUpdateInstalling(false);
    setUpdateNotice({ level: 'error', message: '安装更新失败：' + (error?.message || error) });
  }
}

function appUpdateVersionLabel(info) {
  const version = appUpdateConcreteVersionLabel(info) || '可用更新';
  const platform = (info?.platform || info?.artifact?.platform || '').toString().trim();
  return platform ? `${version} (${platform})` : version;
}

function appUpdateInstallingMessage(info) {
  const version = appUpdateConcreteVersionLabel(info);
  if (!version) return '正在安装更新';
  const platform = (info?.platform || info?.artifact?.platform || '').toString().trim();
  return '正在安装更新 ' + (platform ? `${version} (${platform})` : version);
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

async function changeActiveProviderPreference({ cwd, event, isCurrent, setError, setForm, setStatus }) {
  const provider = normalizeProviderName(event.target.value);
  setError('');
  setStatus('');
  setForm((current) => settingsFormWithUpdate(current, 'activeProvider', provider));
  try {
    const projectCwd = requireSettingsCwd(cwd);
    if (!isCurrentPreferenceRequest(isCurrent)) return;
    await setPreference({ cwd: projectCwd, key: SETTINGS_KEYS.activeProvider, value: provider });
    const values = await readRuntimePreferenceValuesForProvider(projectCwd, provider);
    if (isCurrentPreferenceRequest(isCurrent)) {
      setForm(settingsFormFromPreferences(values));
      setStatus('Active Provider 已切换为 ' + (PROVIDER_LABELS[provider] || provider));
    }
  } catch (err) {
    if (isCurrentPreferenceRequest(isCurrent)) {
      setError(err.message || String(err));
    }
  }
}

async function saveRuntimePreferences({ cwd, form, setError, setStatus }) {
  setError('');
  setStatus('');
  try {
    const projectCwd = requireSettingsCwd(cwd);
    const { stallThresholdSec, contextThresholds } = validateRuntimeThresholds(form);
    await setPreference({ cwd: projectCwd, key: SETTINGS_KEYS.stallThreshold, value: stallThresholdSec });
    await setPreference({ cwd: projectCwd, key: SETTINGS_KEYS.contextThresholds, value: contextThresholds });
    setStatus('已保存超时与上下文使用率设置');
  } catch (err) {
    setError(err.message || String(err));
  }
}

async function saveProviderRuntimePreferences({ cwd, form, setError, setStatus }) {
  setError('');
  setStatus('');
  try {
    const projectCwd = requireSettingsCwd(cwd);
    const rootError = form.sandboxPolicy === 'workspaceWrite' ? absolutePathsError(form.writableRoots) : '';
    if (rootError) throw new Error(rootError);
    const provider = normalizeProviderName(form.activeProvider);
    await writeProviderRuntimePreferences(projectCwd, provider, form);
    setStatus('Provider 设置已保存');
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

function useProviderPreferences(cwd, activeProvider) {
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
        setNotice({ level: 'error', message: '加载 Preferences 失败: ' + error.message });
      }
    }
  }, [activeProvider, cwd, nextLoadRequest]);
  const save = useCallback(() => saveProviderPreferenceValues({ approvalMode, cwd, provider, saving, setNotice, setSaving, summaryMode }), [approvalMode, cwd, provider, saving, summaryMode]);
  useEffect(() => { void load(); }, [load]);
  return { approvalMode, load, notice, provider, save, saving, setApprovalMode, setSummaryMode, summaryMode };
}

async function saveProviderPreferenceValues({ approvalMode, cwd, provider, saving, setNotice, setSaving, summaryMode }) {
  if (!cwd || saving) return;
  setSaving(true);
  try {
    const providerKey = normalizeProviderName(provider);
    await setPreference({ cwd, key: providerSettingKey(providerKey, 'summary'), value: summaryMode });
    await setPreference({ cwd, key: providerSettingKey(providerKey, 'approvalPolicy'), value: approvalMode });
    setNotice({ level: 'info', message: '已保存：' + summaryMode + ' / ' + approvalMode });
  } catch (error) {
    setNotice({ level: 'error', message: '保存失败: ' + error.message });
  } finally {
    setSaving(false);
  }
}

function usePromptSettings(cwd) {
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
  const loadPrompt = useCallback(() => loadLspPromptState({ cwd, setDefaultHint, setEffectiveHint, setHint, setLoading, setNotice, setUsingDefault }), [cwd]);
  const loadScope = useCallback(() => loadPromptScope(setCurrentScopeCwd), []);
  const loadVisibility = useCallback(() => loadInjectedPromptVisibility({ cwd, setNotice, setShowInjected }), [cwd]);
  const save = useCallback(() => saveLspPromptHintState({ cwd, defaultHint, hint, saving, setDefaultHint, setEffectiveHint, setHint, setNotice, setSaving, setUsingDefault }), [cwd, defaultHint, hint, saving]);
  const reset = useCallback(() => saveLspPromptHintState({ cwd, defaultHint, hint: '', saving, setDefaultHint, setEffectiveHint, setHint, setNotice, setSaving, setUsingDefault }), [cwd, defaultHint, saving]);
  const copy = useCallback(() => copyEffectivePromptHint(promptDisplayHint(effectiveHint, defaultHint), setNotice), [defaultHint, effectiveHint]);
  const toggleVisibility = useCallback((event) => saveInjectedPromptVisibility({ cwd, event, loadVisibility, saving: showInjectedSaving, setNotice, setSaving: setShowInjectedSaving, setShowInjected }), [cwd, loadVisibility, showInjectedSaving]);
  useEffect(() => { void loadPrompt(); void loadScope(); void loadVisibility(); }, [loadPrompt, loadScope, loadVisibility]);
  return promptSettingsModel({ copy, currentScopeCwd, defaultHint, effectiveHint, hint, loadPrompt, loading, notice, reset, save, saving, setHint, showInjected, showInjectedSaving, toggleVisibility, usingDefault });
}

function promptSettingsModel(model) {
  const displayHint = promptDisplayHint(model.effectiveHint, model.defaultHint);
  const lineCount = displayHint === '暂无可用提示词' ? 0 : displayHint.split('\n').length;
  const charCount = displayHint === '暂无可用提示词' ? 0 : displayHint.length;
  return { ...model, charCount, displayHint, lineCount, modeLabel: promptModeLabel(model.loading, model.usingDefault) };
}

function promptDisplayHint(effectiveHint, defaultHint) {
  return (effectiveHint || defaultHint || '').trim() || '暂无可用提示词';
}

function promptModeLabel(loading, usingDefault) {
  if (loading) return '加载中...';
  return usingDefault ? '默认注入' : '自定义覆盖';
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
    state.setNotice({ level: 'error', message: '加载失败：' + (error?.message || error) });
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

async function loadInjectedPromptVisibility({ cwd, setNotice, setShowInjected }) {
  if (!cwd) return;
  try {
    const value = await getPreference({ cwd, key: 'settings.showInjectedPromptInChat' });
    setShowInjected(parseBoolPreference(value));
  } catch (error) {
    setNotice({ level: 'error', message: '加载聊天注入显示开关失败：' + (error?.message || error) });
  }
}

async function saveLspPromptHintState(state) {
  if (!state.cwd || state.saving) return;
  state.setSaving(true);
  try {
    const res = await writeLspPromptHintBackend({ cwd: state.cwd, hint: state.hint });
    state.setEffectiveHint((res?.hint || '').toString());
    state.setDefaultHint((res?.defaultHint || state.defaultHint || '').toString());
    state.setHint((res?.overrideHint || '').toString());
    state.setUsingDefault(Boolean(res?.usingDefault));
    state.setNotice({ level: 'info', message: res?.usingDefault ? '已恢复默认提示词' : '提示词已保存' });
  } catch (error) {
    state.setNotice({ level: 'error', message: '保存失败：' + (error?.message || error) });
  } finally {
    state.setSaving(false);
  }
}

async function copyEffectivePromptHint(text, setNotice) {
  if (!text || text === '暂无可用提示词') {
    setNotice({ level: 'error', message: '暂无可复制内容' });
    return;
  }
  try {
    const ok = await copyTextToClipboard(text);
    setNotice({ level: ok ? 'info' : 'error', message: ok ? '已复制生效提示词' : '复制失败' });
  } catch (error) {
    setNotice({ level: 'error', message: '复制失败：' + (error?.message || error) });
  }
}

async function saveInjectedPromptVisibility({ cwd, event, loadVisibility, saving, setNotice, setSaving, setShowInjected }) {
  if (!cwd || saving) return;
  const next = event.target.checked;
  setShowInjected(next);
  setSaving(true);
  try {
    await setPreference({ cwd, key: 'settings.showInjectedPromptInChat', value: next });
    setNotice({ level: 'info', message: next ? '聊天区已改为显示自动注入内容' : '聊天区已改为隐藏自动注入内容' });
  } catch (error) {
    setNotice({ level: 'error', message: '保存聊天注入显示开关失败：' + (error?.message || error) });
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

function useBuiltinToolsSettings(cwd) {
  const [tools, setTools] = useState([]);
  const [loading, setLoading] = useState(false);
  const [savingIds, setSavingIds] = useState({});
  const [expandedGroups, setExpandedGroups] = useState({});
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const applyPayload = useCallback((payload) => setTools(normalizeBuiltinTools(payload)), []);
  const load = useCallback(() => loadBuiltinTools({ applyPayload, cwd, setLoading, setNotice }), [applyPayload, cwd]);
  const toggleTool = useCallback((tool) => toggleBuiltinTool({ applyPayload, cwd, savingIds, setNotice, setSavingIds, setTools, tool }), [applyPayload, cwd, savingIds]);
  const toggleGroup = useCallback((key) => setExpandedGroups((prev) => ({ ...prev, [key]: !prev[key] })), []);
  const groups = useMemo(() => builtinToolGroups(tools), [tools]);
  useEffect(() => { void load(); }, [load]);
  return { expandedGroups, filteredCount: tools.filter((tool) => tool.replacedBy || !tool.enabled).length, groups, groupSummary, isOpen: (key) => Boolean(expandedGroups[key]), load, loading, notice, savingIds, toggleGroup, toggleTool, toolMetaText, totalToolCount: tools.length, tools };
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

async function loadBuiltinTools({ applyPayload, cwd, setLoading, setNotice }) {
  if (!cwd) return;
  setLoading(true);
  try {
    applyPayload(await readBuiltinTools({ cwd }));
    setNotice({ level: 'info', message: '' });
  } catch (error) {
    setNotice({ level: 'error', message: '加载失败：' + (error?.message || error) });
  } finally {
    setLoading(false);
  }
}

async function toggleBuiltinTool({ applyPayload, cwd, savingIds, setNotice, setSavingIds, setTools, tool }) {
  if (!cwd || tool.replacedBy || !tool.id || savingIds[tool.id]) return;
  const nextEnabled = !tool.enabled;
  setSavingIds((prev) => ({ ...prev, [tool.id]: true }));
  setTools((prev) => prev.map((item) => (item.id === tool.id ? { ...item, enabled: nextEnabled } : item)));
  try {
    applyPayload(await writeBuiltinTool({ cwd, id: tool.id, enabled: nextEnabled }));
    setNotice({ level: 'info', message: (tool.label || tool.id) + ' 已' + (nextEnabled ? '启用' : '禁用') });
  } catch (error) {
    setTools((prev) => prev.map((item) => (item.id === tool.id ? { ...item, enabled: !nextEnabled } : item)));
    setNotice({ level: 'error', message: '保存失败：' + (error?.message || error) });
  } finally {
    setSavingIds((prev) => ({ ...prev, [tool.id]: false }));
  }
}

function builtinToolGroups(tools) {
  const disabled = tools.filter((tool) => !tool.enabled || tool.replacedBy);
  return [
    builtinToolGroup('native-hard', '启动前已关闭', disabled.filter((tool) => builtinToolEnforcement(tool) === 'native-hard'), '模型启动前就看不到这些能力。'),
    builtinToolGroup('effect-hard', '已限制为只读', disabled.filter((tool) => builtinToolEnforcement(tool) === 'effect-hard'), 'Codex 暂不支持单独关闭这类能力，已限制为只读，避免它直接改文件或执行命令。'),
    builtinToolGroup('soft-audit', '仅提醒使用项目工具', disabled.filter((tool) => builtinToolEnforcement(tool) === 'soft-audit'), 'Codex 暂不支持可靠关闭这类能力，只能提示模型优先使用本项目工具；这不是强制拦截。'),
    builtinUnfilteredGroup(tools),
  ].filter(Boolean);
}

function builtinToolGroup(key, label, tools, note) {
  if (tools.length === 0) return null;
  return { canToggle: true, disabledCount: tools.length, key, label: label + '（' + tools.length + '）', note, tools };
}

function builtinUnfilteredGroup(tools) {
  const available = tools.filter((tool) => tool.enabled && !tool.replacedBy);
  if (!available.length) return null;
  return { canToggle: true, disabledCount: 0, key: 'unfiltered', label: '保持可用（' + available.length + '）', tools: available };
}

function builtinToolEnforcement(tool) {
  const enforcement = (tool.enforcement || '').toString().trim();
  if (enforcement) return enforcement;
  return tool.filterMode === 'hard' ? 'native-hard' : 'soft-audit';
}

function toolStatusLabel(tool) {
  if (tool.replacedBy) return '已由项目工具接管';
  if (tool.enabled) return '保持可用';
  const enforcement = builtinToolEnforcement(tool);
  if (enforcement === 'native-hard') return '启动前已关闭';
  if (enforcement === 'effect-hard') return '已限制为只读';
  return enforcement === 'soft-audit' ? '仅提醒使用项目工具' : '已管控';
}

function toolMetaText(tool) {
  const parts = [];
  const description = (tool.description || '').trim();
  if (description) parts.push(description);
  const provider = PROVIDER_LABELS[tool.provider] || tool.provider || '';
  if (provider) parts.push(provider);
  parts.push(toolStatusLabel(tool));
  return parts.join(' · ');
}

function groupSummary(group) {
  if (group.key === 'unfiltered') return '可用 ' + group.tools.length + ' 项';
  return '已管控 ' + group.disabledCount + ' 项';
}

function SettingsPageView({ builtins, cwd, prompt, provider, runtime, store }) {
  return (
    <section className="settings-page" data-testid="settings-page">
      <PageHeader icon={Settings} title="设置" actions={<button className="btn btn-secondary" type="button" data-testid="settings-refresh-build-button" onClick={() => void runtime.refreshBuildInfo()}>刷新构建信息</button>} />
      <SettingsNotices error={runtime.error} status={runtime.status} />
      <div className="panel-body" data-testid="settings-panel-body">
        <AboutPanel buildInfo={runtime.buildInfo} cwd={cwd} runtime={runtime} updateCurrentVersion={appUpdateCurrentVersionLabel(runtime.buildInfo)} />
        <RuntimeSettingsPanels runtime={runtime} />
        <ProviderSettingsPanel runtime={runtime} viewConfig={providerSettingsViewConfig} />
        <ProviderPropertiesCard provider={provider} />
        <PromptSettingsCard prompt={prompt} />
        <BuiltinToolsCard builtins={builtins} />
        <VideoSettingsCard getApiKey={getVideoApiKey} setApiKey={setVideoApiKey} />
        <UILogCard loadLogs={loadSettingsDashboardLogs} store={store} />
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

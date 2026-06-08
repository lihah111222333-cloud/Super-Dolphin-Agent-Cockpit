import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Settings } from 'lucide-react';
import { useClientStore } from '../../entities/client/model/useClientStore.js';
import { callBackend, checkAppUpdate, copyTextToClipboard, getBuildInfo, getPreference, installLatestAppUpdate, listDashboardLogs, readBuiltinTools, readConfig, readLspPromptHint, setPreference, writeBuiltinTool, writeLspPromptHint as writeLspPromptHintBackend } from '../../shared/api/backendApi.js';
import { PageHeader, Panel } from '../shared/pageComponents.jsx';

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
  const bad = paths.filter((root) => !root.startsWith('/'));
  return bad.length > 0 ? `路径必须以 / 开头：${bad.join(', ')}` : '';
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
        <AboutPanel buildInfo={runtime.buildInfo} cwd={cwd} runtime={runtime} />
        <RuntimeSettingsPanels runtime={runtime} />
        <ProviderSettingsPanel runtime={runtime} />
        <ProviderPropertiesCard provider={provider} />
        <PromptSettingsCard prompt={prompt} />
        <BuiltinToolsCard builtins={builtins} />
        <VideoSettingsCard />
        <UILogCard store={store} />
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

function AboutPanel({ buildInfo, cwd, runtime }) {
  const canInstallUpdate = Boolean(runtime.updateInfo?.available) && !runtime.updateInstalling;
  const updateCurrentVersion = appUpdateCurrentVersionLabel(buildInfo);
  return (
    <Panel title="ABOUT">
      <dl>
        <dt>版本</dt><dd>Agent Orchestrator {buildInfo?.version || 'unknown'}</dd>
        <dt>运行时</dt><dd>{buildInfo?.runtime || 'unknown'}</dd>
        <dt>构建时间</dt><dd>{buildInfo?.buildTime || 'unknown'}</dd>
        <dt>Commit</dt><dd>{buildInfo?.commit || 'unknown'}</dd>
        <dt>当前项目</dt><dd>{cwd || '未选择项目'}</dd>
      </dl>
      <div className="data-card-vue settings-update-card" data-testid="settings-update-card">
        <div className="data-row-vue">
          <strong>应用更新</strong>
          <span>当前版本 {updateCurrentVersion}</span>
        </div>
        <div className="settings-action-row settings-action-inline">
          <button className="btn btn-secondary btn-toolbar-sm" type="button" data-testid="settings-update-check-button" onClick={() => void runtime.checkForUpdate()} disabled={runtime.updateBusy || runtime.updateInstalling}>{runtime.updateBusy ? '检查中...' : '检查更新'}</button>
          {canInstallUpdate ? <button className="btn btn-primary btn-toolbar-sm" type="button" data-testid="settings-update-install-button" onClick={() => void runtime.installUpdate()} disabled={runtime.updateInstalling}>安装更新</button> : null}
        </div>
        {runtime.updateNotice.message ? <SettingsPromptNotice notice={runtime.updateNotice} testId="settings-update-notice" /> : null}
      </div>
    </Panel>
  );
}

function RuntimeSettingsPanels({ runtime }) {
  const { form, saveRuntimeSettings, updateForm } = runtime;
  return (
    <>
      <Panel title="TURN TRACKER">
        <div className="form-line">
          <label>统一超时阈值<input aria-label="统一超时阈值" data-testid="settings-stall-threshold-input" type="number" min="30" value={form.stallThresholdSec} onChange={updateForm('stallThresholdSec')} /> 秒</label>
          <button className="btn btn-primary" type="button" data-testid="settings-stall-threshold-save-button" onClick={() => void saveRuntimeSettings()}>保存超时阈值</button>
        </div>
      </Panel>
      <ContextUsagePanel form={form} onSave={saveRuntimeSettings} updateForm={updateForm} />
    </>
  );
}

function ContextUsagePanel({ form, onSave, updateForm }) {
  return (
    <Panel title="CONTEXT USAGE ALERT" data-testid="settings-ctx-thresholds-card">
      <div className="form-line">
        <label>Warn 阈值<input aria-label="Warn 阈值" type="number" min="1" max="100" value={form.contextWarn} onChange={updateForm('contextWarn')} /></label>
        <label>Danger 阈值<input aria-label="Danger 阈值" type="number" min="1" max="100" value={form.contextDanger} onChange={updateForm('contextDanger')} /></label>
        <label>Critical 阈值<input aria-label="Critical 阈值" type="number" min="1" max="100" value={form.contextCritical} onChange={updateForm('contextCritical')} /></label>
        <button className="btn btn-primary" type="button" data-testid="settings-ctx-thresholds-save-button" onClick={() => void onSave()}>保存运行阈值</button>
      </div>
    </Panel>
  );
}

function ProviderSettingsPanel({ runtime }) {
  const { changeActiveProvider, form, saveProviderSettings, updateForm } = runtime;
  return (
    <Panel title="PROVIDER">
      <ProviderSettingsForm changeActiveProvider={changeActiveProvider} form={form} updateForm={updateForm} />
      <div className="settings-actions"><button className="btn btn-primary" type="button" onClick={() => void saveProviderSettings()}>保存 Provider 设置</button></div>
    </Panel>
  );
}

function ProviderSettingsForm({ changeActiveProvider, form, updateForm }) {
  const modelOptions = appendCurrentOption(MODEL_OPTIONS_BY_PROVIDER[form.activeProvider] || MODEL_OPTIONS_BY_PROVIDER.codex, form.providerModel);
  const baseEffortOptions = EFFORT_MODES_BY_PROVIDER[form.activeProvider] || EFFORT_MODES_BY_PROVIDER.codex;
  const filteredEffortOptions = form.activeProvider === 'claude' && !isClaudeOpusFamilyModel(form.providerModel)
    ? baseEffortOptions.filter((item) => item.value !== 'max')
    : baseEffortOptions;
  const effortOptions = appendCurrentOption(filteredEffortOptions, normalizeProviderEffortSetting(form.activeProvider, form.providerModel, form.providerEffort));
  return (
    <div className="form-grid">
      <label>Active Provider<select value={form.activeProvider} onChange={changeActiveProvider}><option value="codex">Codex</option></select></label>
      <label>Provider Model<select aria-label="Provider Model" value={form.providerModel} onChange={updateForm('providerModel')}>{modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Provider Effort<select aria-label="Provider Effort" value={form.providerEffort} onChange={updateForm('providerEffort')}>{effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Personality<select aria-label="Personality" value={form.personality} onChange={updateForm('personality')}>{appendCurrentOption(PERSONALITY_OPTIONS, form.personality).map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      {form.activeProvider === 'codex' ? <label>Codex Home<input aria-label="Codex Home" value={form.codexHome} onChange={updateForm('codexHome')} /></label> : null}
      {form.activeProvider === 'codex' ? <label>Instance Key<input aria-label="Instance Key" value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label> : null}
      <label>Sandbox Policy<select aria-label="Sandbox Policy" value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
      {form.sandboxPolicy === 'readOnly' ? <label>Read Only Mode<select aria-label="Read Only Mode" value={form.readOnlyMode} onChange={updateForm('readOnlyMode')}><option value="fullAccess">fullAccess（全量只读）</option><option value="restricted">restricted（限定目录）</option></select></label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="checkbox-line"><input type="checkbox" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="wide">Writable Roots<textarea aria-label="Writable Roots" value={form.writableRoots} onChange={updateForm('writableRoots')} placeholder="每行一个绝对路径" /></label> : null}
      {form.sandboxPolicy === 'readOnly' && form.readOnlyMode === 'restricted' ? <label className="wide">Readable Roots<textarea aria-label="Readable Roots" value={form.readableRoots} onChange={updateForm('readableRoots')} placeholder="每行一个绝对路径" /></label> : null}
    </div>
  );
}

function ProviderPropertiesCard({ provider }) {
  return (
    <>
      <div className="section-header">PROPERTIES</div>
      <div className="data-card-vue" data-testid="settings-provider-sandbox-card">
        <ProviderSelectRow id="provider-summary-mode-select" label="推理摘要 (Summary)" value={provider.summaryMode} onChange={provider.setSummaryMode} options={SUMMARY_MODE_OPTIONS} />
        <ProviderSelectRow id="provider-approval-mode-select" label="审批策略 (ApprovalPolicy)" value={provider.approvalMode} onChange={provider.setApprovalMode} options={APPROVAL_MODE_OPTIONS} />
        {provider.notice.message ? <SettingsPromptNotice notice={provider.notice} className="settings-provider-notice" /> : null}
        <div className="settings-action-row settings-action-inline settings-provider-actions">
          <button type="button" className="btn btn-secondary btn-toolbar-sm" onClick={provider.load} disabled={provider.saving}>刷新</button>
          <button type="button" className="btn btn-primary btn-toolbar-sm" data-testid="provider-sandbox-save-button" onClick={provider.save} disabled={provider.saving}>{provider.saving ? '保存中...' : '保存'}</button>
        </div>
      </div>
    </>
  );
}

const SUMMARY_MODE_OPTIONS = Object.freeze([
  ['detailed', 'detailed（详细摘要，推荐）'], ['auto', 'auto（自动）'], ['concise', 'concise（简洁）'], ['none', 'none（关闭）'],
]);
const APPROVAL_MODE_OPTIONS = Object.freeze([
  ['on-request', 'on-request（按需，默认）'], ['untrusted', 'untrusted（始终询问）'], ['on-failure', 'on-failure（失败后询问）'], ['never', 'never（全部放行）'],
]);

function ProviderSelectRow({ id, label, onChange, options, value }) {
  return (
    <div className="settings-stall-row settings-provider-control-row">
      <label className="settings-stall-label" htmlFor={id}>{label}</label>
      <select id={id} className="settings-stall-input settings-provider-select" data-testid={id} value={value} onChange={(event) => onChange(event.target.value)}>
        {options.map(([optionValue, optionLabel]) => <option key={optionValue} value={optionValue}>{optionLabel}</option>)}
      </select>
    </div>
  );
}

function SettingsPromptNotice({ className = '', notice, testId = '' }) {
  return (
    <div className={'settings-prompt-notice ' + className + ' is-' + notice.level} data-testid={testId || undefined} role={notice.level === 'error' ? 'alert' : 'status'}>
      {notice.message}
    </div>
  );
}

function PromptSettingsCard({ prompt }) {
  return (
    <>
      <div className="section-header">PROMPT</div>
      <div className="data-card-vue settings-prompt-card" data-testid="settings-lsp-prompt-card">
        <PromptSummary prompt={prompt} />
        <PromptVisibilityToggle prompt={prompt} />
        <PromptTextareas prompt={prompt} />
        {prompt.notice.message ? <SettingsPromptNotice notice={prompt.notice} testId="settings-lsp-prompt-notice" /> : null}
        <PromptActions prompt={prompt} />
      </div>
    </>
  );
}

function PromptSummary({ prompt }) {
  return (
    <>
      <div className="data-row-vue"><strong>自动注入提示词 (LSP / Playwright / json-render)</strong><span>{prompt.modeLabel}</span></div>
      <div className="settings-prompt-desc">下方“生效内容”是后端每轮实际注入文本：“覆盖编辑”用于调试，留空保存可恢复默认。</div>
      <div className="settings-prompt-meta" data-testid="settings-lsp-effective-cwd">当前作用 CWD: {prompt.currentScopeCwd || '未知'}</div>
    </>
  );
}

function PromptVisibilityToggle({ prompt }) {
  return (
    <label className="settings-prompt-toggle" data-testid="settings-show-injected-toggle">
      <div className="settings-prompt-toggle-copy"><span className="settings-prompt-toggle-title">聊天区显示自动注入内容（调试）</span><span className="settings-prompt-toggle-desc">开启后将保留首发消息里的“已注入 ...”段。</span></div>
      <input type="checkbox" className="settings-prompt-toggle-input" data-testid="settings-show-injected-toggle-input" checked={prompt.showInjected} onChange={prompt.toggleVisibility} disabled={prompt.loading || prompt.showInjectedSaving} />
    </label>
  );
}

function PromptTextareas({ prompt }) {
  return (
    <>
      <div className="settings-prompt-meta">生效行数 {prompt.lineCount} · 字符 {prompt.charCount}</div>
      <label className="settings-prompt-label" htmlFor="settings-lsp-effective-output">当前生效内容（只读）</label>
      <textarea id="settings-lsp-effective-output" className="settings-prompt-textarea settings-prompt-textarea-readonly" data-testid="settings-lsp-effective-output" rows={12} value={prompt.displayHint} readOnly />
      <label className="settings-prompt-label" htmlFor="settings-lsp-prompt-input">自定义覆盖（可编辑，空=默认）</label>
      <textarea id="settings-lsp-prompt-input" className="settings-prompt-textarea" data-testid="settings-lsp-prompt-input" rows={8} value={prompt.hint} onChange={(event) => prompt.setHint(event.target.value)} placeholder={prompt.defaultHint || '请输入提示词'} disabled={prompt.loading || prompt.saving} />
    </>
  );
}

function PromptActions({ prompt }) {
  return (
    <div className="settings-action-row settings-action-inline">
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-refresh-button" onClick={prompt.loadPrompt} disabled={prompt.saving}>刷新</button>
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-copy-button" onClick={prompt.copy} disabled={prompt.loading || prompt.saving}>复制生效提示词</button>
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-reset-button" onClick={prompt.reset} disabled={prompt.loading || prompt.saving}>恢复默认</button>
      <button type="button" className="btn btn-primary btn-toolbar-sm" data-testid="settings-lsp-save-button" onClick={prompt.save} disabled={prompt.loading || prompt.saving}>{prompt.saving ? '保存中...' : '保存提示词'}</button>
    </div>
  );
}

function BuiltinToolsCard({ builtins }) {
  return (
    <>
      <div className="section-header">模型内置能力</div>
      <div className="data-card-vue" data-testid="settings-builtin-tools-card">
        <BuiltinToolsSummary builtins={builtins} />
        <BuiltinToolsContent builtins={builtins} />
        {builtins.notice.message ? <SettingsPromptNotice notice={builtins.notice} testId="settings-builtin-tools-notice" /> : null}
      </div>
    </>
  );
}

function BuiltinToolsSummary({ builtins }) {
  return (
    <>
      <div className="data-row-vue"><strong>内置能力开关</strong><span data-testid="settings-builtin-tools-summary">{builtins.loading ? '加载中...' : '已管控 ' + builtins.filteredCount + ' / ' + builtins.totalToolCount}</span></div>
      <div className="settings-prompt-desc">默认管控与本项目文件、命令、编排、计划、权限、插件管理重复，或会绕过项目治理的能力。</div>
    </>
  );
}

function BuiltinToolsContent({ builtins }) {
  if (builtins.tools.length === 0 && !builtins.loading) {
    return <div className="settings-log-empty" data-testid="settings-builtin-tools-empty">暂无可配置的内置工具</div>;
  }
  return (
    <div className="settings-builtin-tool-groups" data-testid="settings-builtin-tools-groups">
      {builtins.groups.map((group) => <BuiltinToolGroup builtins={builtins} group={group} key={group.key} />)}
    </div>
  );
}

function BuiltinToolGroup({ builtins, group }) {
  const isOpen = builtins.isOpen(group.key);
  return (
    <section className="settings-builtin-tool-group" data-testid={'settings-builtin-tool-group-' + group.key}>
      <button type="button" className="settings-builtin-tool-group-head" data-testid={'settings-builtin-tool-group-head-' + group.key} aria-expanded={isOpen ? 'true' : 'false'} onClick={() => builtins.toggleGroup(group.key)}>
        <span className={'settings-builtin-tool-group-chevron ' + (isOpen ? 'is-open' : '')}>▸</span><span className="settings-builtin-tool-group-name">{group.label}</span><span className="settings-builtin-tool-group-summary">{builtins.groupSummary(group)}</span>
      </button>
      {isOpen ? <BuiltinToolGroupBody builtins={builtins} group={group} /> : null}
    </section>
  );
}

function BuiltinToolGroupBody({ builtins, group }) {
  return (
    <div className="settings-builtin-tool-group-body">
      {group.note ? <p className="settings-builtin-tool-group-note" data-testid={'settings-builtin-tool-group-note-' + group.key}>{group.note}</p> : null}
      {group.tools.map((tool) => <BuiltinToolRow builtins={builtins} key={tool.id} tool={tool} />)}
    </div>
  );
}

function BuiltinToolRow({ builtins, tool }) {
  return (
    <label className={'settings-prompt-toggle ' + ((!tool.enabled || tool.replacedBy) ? 'is-disabled-tool' : '')} data-testid={'settings-builtin-tool-' + tool.id}>
      <div className="settings-prompt-toggle-copy"><span className="settings-prompt-toggle-title">{tool.label}</span><span className="settings-prompt-toggle-desc">{builtins.toolMetaText(tool)}</span></div>
      <input type="checkbox" className="settings-prompt-toggle-input" data-testid={'settings-builtin-tool-input-' + tool.id} checked={!tool.enabled || Boolean(tool.replacedBy)} disabled={Boolean(tool.replacedBy) || Boolean(builtins.savingIds[tool.id])} onChange={() => builtins.toggleTool(tool)} />
    </label>
  );
}

function UILogCard({ store }) {
  const [remoteLogs, setRemoteLogs] = useState([]);
  const [logError, setLogError] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const refreshLogs = useCallback(async () => {
    setRefreshing(true);
    setLogError('');
    try {
      setRemoteLogs(normalizeDashboardLogs(await listDashboardLogs({ limit: 14 })));
    } catch (error) {
      setLogError('刷新日志失败：' + (error?.message || error));
    } finally {
      setRefreshing(false);
    }
  }, []);
  const localLogs = store.logEntries ? store.logEntries.slice(0, 14) : [];
  const logList = remoteLogs.length > 0 ? remoteLogs : localLogs;
  return (
    <>
      <div className="section-header">UI LOG</div>
      <div className="data-card-vue settings-log-card" data-testid="settings-log-card">
        <div className="data-row-vue"><strong>日志级别</strong><span>{store.logLevel}</span></div>
        <UILogLevelRow store={store} />
        <div className="settings-action-row settings-log-action-row"><button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-log-refresh-button" onClick={() => { void refreshLogs(); }} disabled={refreshing}>{refreshing ? '刷新中...' : '刷新日志'}</button></div>
        {logError ? <SettingsPromptNotice notice={{ level: 'error', message: logError }} testId="settings-log-notice" /> : null}
        <UILogList logList={logList} />
      </div>
    </>
  );
}

function normalizeDashboardLogs(payload) {
  const list = Array.isArray(payload?.logs) ? payload.logs : [];
  return list.map(normalizeDashboardLogEntry);
}

function normalizeDashboardLogEntry(entry, index) {
  const scope = textValue(entry.component || entry.logger || entry.source || 'dashboard') || 'dashboard';
  const event = textValue(entry.event_type || entry.eventType || entry.message || entry.raw || `log.${entry.id || index}`);
  return {
    id: entry.id || `${scope}-${index}`,
    ts: entry.timestamp || entry.ts || entry.createdAt || entry.created_at,
    level: textValue(entry.level || 'info').toLowerCase() || 'info',
    scope,
    event,
    fields: entry,
  };
}

function UILogLevelRow({ store }) {
  return (
    <div className="settings-stall-row settings-log-control-row">
      <label className="settings-stall-label" htmlFor="settings-log-level-select">日志级别</label>
      <select id="settings-log-level-select" className="settings-stall-input settings-log-level-select" data-testid="settings-log-level-select" value={store.logLevel} onChange={(event) => store.setLogLevel(event.target.value)}>
        <option value="debug">debug（最详细）</option><option value="info">info（默认）</option><option value="warn">warn</option><option value="error">error（仅错误）</option>
      </select>
      <span className="settings-stall-unit">立即生效（跨 tab 同步）</span>
    </div>
  );
}

function UILogList({ logList }) {
  if (logList.length === 0) return <div className="settings-log-empty" data-testid="settings-log-empty">暂无日志</div>;
  return (
    <div className="settings-log-list" data-testid="settings-log-list">
      {logList.map((entry) => <UILogItem entry={entry} key={entry.seq || entry.id} />)}
    </div>
  );
}

function UILogItem({ entry }) {
  return (
    <div className="settings-log-item">
      <span className="settings-log-time">{formatLogTime(entry.ts)}</span>
      <span className={'settings-log-level is-' + entry.level}>{entry.level}</span>
      <span className="settings-log-event">{entry.scope}.{entry.event}</span>
    </div>
  );
}

function formatLogTime(value) {
  if (!value) return '--:--:--';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '--:--:--';
  return date.toLocaleTimeString('zh-CN', { hour12: false });
}

function VideoSettingsCard() {
  const [apiKey, setApiKey] = useState('');
  const [notice, setNotice] = useState(null);
  const [configured, setConfigured] = useState(false);
  const [masked, setMasked] = useState('');

  useEffect(() => {
    callBackend('ui/video/getApiKey', {}).then((res) => {
      if (res?.configured) { setConfigured(true); setMasked(res.masked); }
    }).catch((err) => {
      setNotice({ level: 'error', message: '读取视频 API Key 失败：' + (err?.message || String(err)) });
    });
  }, []);

  const save = useCallback(async () => {
    const key = apiKey.trim();
    if (!key) { setNotice({ level: 'error', message: '请输入 API Key' }); return; }
    try {
      await callBackend('ui/video/setApiKey', { apiKey: key });
      setConfigured(true);
      setMasked(key.length > 8 ? key.slice(0, 4) + '*'.repeat(key.length - 8) + key.slice(-4) : '*'.repeat(key.length));
      setApiKey('');
      setNotice({ level: 'info', message: '已保存' });
    } catch (err) {
      setNotice({ level: 'error', message: '保存失败：' + (err?.message || String(err)) });
    }
  }, [apiKey]);

  return (
    <>
      <div className="section-header">视频生成（硅基流动 Wan2.2）</div>
      <div className="data-card-vue" data-testid="settings-video-card">
        <div className="data-row-vue">
          <strong>SiliconFlow API Key</strong>
          <span>{configured ? masked : '未配置'}</span>
        </div>
        <div className="settings-stall-row">
          <label className="settings-stall-label" htmlFor="settings-sf-key">API Key</label>
          <input id="settings-sf-key" className="settings-stall-input" type="password" placeholder="sk-..." value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
        </div>
        <div className="settings-action-row">
          <button className="btn btn-primary" type="button" onClick={save}>保存</button>
          {notice ? <span className="settings-page-notice" data-testid="settings-video-notice" role={notice.level === 'error' ? 'alert' : 'status'}>{notice.message}</span> : null}
        </div>
      </div>
    </>
  );
}

export { SettingsPage };

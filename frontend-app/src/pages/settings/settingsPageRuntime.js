import { normalizeRuntimeProviderName } from '../../entities/client/model/helpers/providerPreferences.js';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { firstPresentText, parseJsonObjectValue, rawTextValue } from '../shared/pageShared.js';
import { settingsPageService } from './services/settingsPageService.js';

const {
  getPreference,
  listDashboardLogs,
  setPreference,
} = settingsPageService;

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

function textValue(value) { return value === null || value === undefined ? '' : value.toString(); }

function providerConfigValue(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const key of ['value', 'model', 'id', 'key', 'name']) {
      const text = providerConfigValue(value[key]);
      if (text) return text;
    }
    return '';
  }
  return textValue(value).trim();
}

function isPreferenceTombstone(value) {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value) && value.cleared === true);
}

function isPreferenceAbsent(value) {
  return value === null || value === undefined || (typeof value === 'string' && value.trim() === '');
}

async function readScopedPreference(cwd, key) {
  const scope = textValue(cwd).trim();
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
  return normalizeRuntimeProviderName(value, SETTINGS_KEYS.activeProvider);
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
  const value = textValue(cwd).trim();
  if (!value) throw new Error(firstPresentText(copy.projectCwdRequired, SETTINGS_PROJECT_CWD_REQUIRED));
  return value;
}

function normalizeSandboxMode(value) {
  const mode = textValue(value).trim();
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
    if (text.startsWith('{')) return parseSandboxPreferenceJson(text);
    return { type: normalizeSandboxMode(text) };
  }
  if (value && typeof value === 'object' && !Array.isArray(value)) return value;
  throw new Error('加载 Sandbox 失败：sandbox preference must be an object');
}

function parseSandboxPreferenceJson(text) {
  try {
    return parseJsonObjectValue(text, 'sandbox preference');
  } catch (error) {
    throw new Error('加载 Sandbox 失败：' + (error?.message || error), { cause: error });
  }
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
  return rawTextValue(value)
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
  const root = textValue(value).trim();
  return root.startsWith('/') || /^[a-zA-Z]:[\\/]/.test(root) || /^\\\\[^\\]+\\[^\\]+/.test(root);
}

function sandboxPreferenceValue(policy, writableRootsText, networkAccess, readOnlyMode, readableRootsText) {
  if (policy === 'readOnly') {
    if (readOnlyMode === 'restricted') return restrictedReadOnlyPreference(readableRootsText);
    return { type: 'readOnly' };
  }
  if (policy === 'dangerFullAccess') return { type: 'dangerFullAccess' };
  return {
    type: 'workspaceWrite',
    writableRoots: pathsFromTextarea(writableRootsText),
    networkAccess: Boolean(networkAccess),
  };
}

function restrictedReadOnlyPreference(readableRootsText) {
  return {
    type: 'readOnly',
    access: {
      type: 'restricted',
      readableRoots: pathsFromTextarea(readableRootsText),
      includePlatformDefaults: true,
    },
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
    stallThresholdSec: String(SETTINGS_DEFAULTS.stallThresholdSec), contextWarn: String(SETTINGS_DEFAULTS.contextThresholds[0]),
    contextDanger: String(SETTINGS_DEFAULTS.contextThresholds[1]), contextCritical: String(SETTINGS_DEFAULTS.contextThresholds[2]),
    activeProvider: SETTINGS_DEFAULTS.activeProvider, codexHome: SETTINGS_DEFAULTS.codexHome, codexInstanceKey: SETTINGS_DEFAULTS.codexInstanceKey,
    providerModel: SETTINGS_DEFAULTS.providerModel, providerEffort: SETTINGS_DEFAULTS.providerEffort, personality: SETTINGS_DEFAULTS.personality,
    providerModelExplicit: false, providerEffortExplicit: false, providerModelTouched: false, providerEffortTouched: false,
    sandboxPolicy: SETTINGS_DEFAULTS.sandboxPolicy, readOnlyMode: SETTINGS_DEFAULTS.readOnlyMode, readableRoots: SETTINGS_DEFAULTS.readableRoots,
    writableRoots: SETTINGS_DEFAULTS.writableRoots, networkAccess: SETTINGS_DEFAULTS.networkAccess,
  };
}

function normalizeSettingsCwd(value) {
  const text = textValue(value).trim();
  if (!text || text === '.' || text === '未选择项目') return '';
  return text;
}

function settingsFormWithUpdate(current, key, value) {
  if (key === 'activeProvider') {
    const activeProvider = normalizeProviderName(value);
    const providerModel = normalizeProviderModelSetting(activeProvider, current.providerModel);
    return {
      ...current, activeProvider, providerModel, providerEffort: normalizeProviderEffortSetting(activeProvider, providerModel, current.providerEffort),
      providerModelExplicit: false, providerEffortExplicit: false, providerModelTouched: false, providerEffortTouched: false,
    };
  }
  if (key === 'providerModel') {
    const providerModel = normalizeProviderModelSetting(current.activeProvider, value);
    return {
      ...current, providerModel, providerEffort: normalizeProviderEffortSetting(current.activeProvider, providerModel, current.providerEffort), providerModelTouched: true,
    };
  }
  if (key === 'providerEffort') {
    return { ...current, providerEffort: normalizeProviderEffortSetting(current.activeProvider, current.providerModel, value), providerEffortTouched: true };
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

async function changeActiveProviderPreference(state) {
  const { copy, cwd, event, isCurrent, setError, setForm, setStatus } = state;
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
  const text = textValue(value).trim();
  return text ? text : { cleared: true };
}

export {
  PROVIDER_LABELS, changeActiveProviderPreference, defaultSettingsForm, isCurrentPreferenceRequest, loadRuntimePreferences,
  loadSettingsDashboardLogs, normalizeProviderName, normalizeSettingsCwd, providerConfigValue, providerSettingKey,
  providerSettingsViewConfig, readScopedPreference, saveProviderRuntimePreferences, saveRuntimePreferences, settingsFormWithUpdate, textValue,
};

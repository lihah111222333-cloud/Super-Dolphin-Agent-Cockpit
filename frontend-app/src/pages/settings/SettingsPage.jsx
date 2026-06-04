import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Settings } from 'lucide-react';
import { useClientStore } from '../../entities/client/model/useClientStore.js';
import { callBackend, getBuildInfo, getPreference, setPreference } from '../../shared/api/backendApi.js';
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
  codexModelProvider: 'openai',
  providerModel: 'gpt-5',
  providerEffort: 'high',
  sandboxPolicy: 'workspaceWrite',
  writableRoots: '',
  networkAccess: false,
});

function providerSettingKey(provider, key) {
  return `settings.provider.${provider}.${key}`;
}

function stringSetting(value, fallback) {
  if (typeof value === 'string' && value.trim()) return value.trim();
  return fallback;
}

function numberSetting(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function normalizeProviderName(value) {
  const provider = stringSetting(value, SETTINGS_DEFAULTS.activeProvider).toLowerCase();
  return provider === 'claude' ? 'claude' : 'codex';
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

function sandboxPolicyFromPreference(value) {
  if (typeof value === 'string') return value;
  if (value && typeof value === 'object') {
    return value.type || value.mode || SETTINGS_DEFAULTS.sandboxPolicy;
  }
  return SETTINGS_DEFAULTS.sandboxPolicy;
}

function writableRootsFromPreference(value) {
  if (!value || typeof value !== 'object' || !Array.isArray(value.writableRoots)) return '';
  return value.writableRoots.join('\n');
}

function sandboxPreferenceValue(policy, writableRootsText, networkAccess) {
  if (policy === 'readOnly') return { type: 'readOnly' };
  if (policy === 'dangerFullAccess') return { type: 'dangerFullAccess' };
  const writableRoots = writableRootsText
    .split(/\r?\n/)
    .flatMap((item) => {
      const root = item.trim();
      return root ? [root] : [];
    });
  return {
    type: 'workspaceWrite',
    writableRoots,
    networkAccess: Boolean(networkAccess),
  };
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
    codexModelProvider: SETTINGS_DEFAULTS.codexModelProvider,
    providerModel: SETTINGS_DEFAULTS.providerModel,
    providerEffort: SETTINGS_DEFAULTS.providerEffort,
    sandboxPolicy: SETTINGS_DEFAULTS.sandboxPolicy,
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
  const provider = useProviderPreferences(cwd);
  const prompt = usePromptSettings(cwd);
  const builtins = useBuiltinToolsSettings(cwd);
  return <SettingsPageView builtins={builtins} cwd={cwd} prompt={prompt} provider={provider} runtime={runtime} store={store} />;
}

function useSettingsRuntime(cwd) {
  const [buildInfo, setBuildInfo] = useState(null);
  const [form, setForm] = useState(defaultSettingsForm);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
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
  const loadPreferences = useCallback(() => loadRuntimePreferences({ cwd, setError, setForm }), [cwd]);
  const updateForm = useCallback((key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => ({ ...current, [key]: value }));
  }, []);
  const saveRuntimeSettings = useCallback(() => saveRuntimePreferences({ cwd, form, setError, setStatus }), [cwd, form]);
  const saveProviderSettings = useCallback(() => saveProviderRuntimePreferences({ cwd, form, setError, setStatus }), [cwd, form]);
  useEffect(() => { void refreshBuildInfo(); }, [refreshBuildInfo]);
  useEffect(() => { void loadPreferences(); }, [loadPreferences]);
  return { buildInfo, error, form, refreshBuildInfo, saveProviderSettings, saveRuntimeSettings, status, updateForm };
}

async function loadRuntimePreferences({ cwd, setError, setForm }) {
  setError('');
  if (!cwd) return;
  try {
    const values = await readRuntimePreferenceValues(cwd);
    setForm(settingsFormFromPreferences(values));
  } catch (err) {
    setError(err.message || String(err));
  }
}

async function readRuntimePreferenceValues(cwd) {
  const [stallValue, contextValue, activeProviderValue] = await Promise.all([
    getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
    getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
    getPreference({ cwd, key: SETTINGS_KEYS.activeProvider }),
  ]);
  const activeProvider = normalizeProviderName(activeProviderValue);
  const providerPrefix = 'settings.provider.' + activeProvider;
  const providerValues = await Promise.all([
    getPreference({ cwd, key: providerSettingKey('codex', 'codexHome') }),
    getPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey') }),
    getPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider') }),
    getPreference({ cwd, key: providerPrefix + '.model' }),
    getPreference({ cwd, key: providerPrefix + '.effort' }),
    getPreference({ cwd, key: providerPrefix + '.sandbox' }),
  ]);
  return { activeProvider, contextValue, providerValues, stallValue };
}

function settingsFormFromPreferences({ activeProvider, contextValue, providerValues, stallValue }) {
  const [codexHome, codexInstanceKey, codexModelProvider, providerModel, providerEffort, sandbox] = providerValues;
  const contextThresholds = normalizeContextThresholds(contextValue);
  return {
    ...defaultSettingsForm(),
    stallThresholdSec: String(numberSetting(stallValue, SETTINGS_DEFAULTS.stallThresholdSec)),
    contextWarn: String(contextThresholds[0]),
    contextDanger: String(contextThresholds[1]),
    contextCritical: String(contextThresholds[2]),
    activeProvider,
    codexHome: stringSetting(codexHome, SETTINGS_DEFAULTS.codexHome),
    codexInstanceKey: stringSetting(codexInstanceKey, SETTINGS_DEFAULTS.codexInstanceKey),
    codexModelProvider: stringSetting(codexModelProvider, SETTINGS_DEFAULTS.codexModelProvider),
    providerModel: stringSetting(providerModel, SETTINGS_DEFAULTS.providerModel),
    providerEffort: stringSetting(providerEffort, SETTINGS_DEFAULTS.providerEffort),
    sandboxPolicy: sandboxPolicyFromPreference(sandbox),
    writableRoots: writableRootsFromPreference(sandbox),
    networkAccess: Boolean(sandbox && typeof sandbox === 'object' && sandbox.networkAccess),
  };
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
    const provider = normalizeProviderName(form.activeProvider);
    await writeProviderRuntimePreferences(projectCwd, provider, form);
    setStatus('Provider 设置已保存');
  } catch (err) {
    setError(err.message || String(err));
  }
}

async function writeProviderRuntimePreferences(cwd, provider, form) {
  await Promise.all([
    setPreference({ cwd, key: SETTINGS_KEYS.activeProvider, value: provider }),
    setPreference({ cwd, key: providerSettingKey(provider, 'model'), value: form.providerModel.trim() }),
    setPreference({ cwd, key: providerSettingKey(provider, 'effort'), value: form.providerEffort.trim() }),
    setPreference({ cwd, key: providerSettingKey(provider, 'sandbox'), value: sandboxPreferenceValue(form.sandboxPolicy, form.writableRoots, form.networkAccess) }),
    setPreference({ cwd, key: providerSettingKey('codex', 'codexHome'), value: form.codexHome.trim() }),
    setPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey'), value: form.codexInstanceKey.trim() }),
    setPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider'), value: form.codexModelProvider.trim() }),
  ]);
}

function useProviderPreferences(cwd) {
  const [summaryMode, setSummaryMode] = useState('detailed');
  const [approvalMode, setApprovalMode] = useState('on-request');
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const [saving, setSaving] = useState(false);
  const load = useCallback(async () => {
    if (!cwd) return;
    try {
      const summaryValue = await getPreference({ cwd, key: 'settings.provider.codex.summary' });
      const approvalValue = await getPreference({ cwd, key: 'settings.provider.codex.approvalPolicy' });
      setSummaryMode(summaryValue || 'detailed');
      setApprovalMode(approvalValue || 'on-request');
      setNotice({ level: 'info', message: '' });
    } catch (error) {
      setNotice({ level: 'error', message: '加载 Preferences 失败: ' + error.message });
    }
  }, [cwd]);
  const save = useCallback(() => saveProviderPreferenceValues({ approvalMode, cwd, saving, setNotice, setSaving, summaryMode }), [approvalMode, cwd, saving, summaryMode]);
  useEffect(() => { void load(); }, [load]);
  return { approvalMode, load, notice, save, saving, setApprovalMode, setSummaryMode, summaryMode };
}

async function saveProviderPreferenceValues({ approvalMode, cwd, saving, setNotice, setSaving, summaryMode }) {
  if (!cwd || saving) return;
  setSaving(true);
  try {
    await setPreference({ cwd, key: 'settings.provider.codex.summary', value: summaryMode });
    await setPreference({ cwd, key: 'settings.provider.codex.approvalPolicy', value: approvalMode });
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
  const save = useCallback(() => writeLspPromptHint({ cwd, defaultHint, hint, saving, setDefaultHint, setEffectiveHint, setHint, setNotice, setSaving, setUsingDefault }), [cwd, defaultHint, hint, saving]);
  const reset = useCallback(() => writeLspPromptHint({ cwd, defaultHint, hint: '', saving, setDefaultHint, setEffectiveHint, setHint, setNotice, setSaving, setUsingDefault }), [cwd, defaultHint, saving]);
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
    const res = await callBackend('config/lspPromptHint/read', { cwd: state.cwd });
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
    const cfg = await callBackend('config/read', {});
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

async function writeLspPromptHint(state) {
  if (!state.cwd || state.saving) return;
  state.setSaving(true);
  try {
    const res = await callBackend('config/lspPromptHint/write', { cwd: state.cwd, hint: state.hint });
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
    await navigator.clipboard.writeText(text);
    setNotice({ level: 'info', message: '已复制生效提示词' });
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
    applyPayload(await callBackend('config/builtinTools/read', { cwd }));
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
    applyPayload(await callBackend('config/builtinTools/write', { cwd, id: tool.id, enabled: nextEnabled }));
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
        <AboutPanel buildInfo={runtime.buildInfo} cwd={cwd} />
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
      {status ? <p className="settings-page-notice settings-status" role="status">{status}</p> : null}
      {error ? <p className="settings-page-notice danger-text" role="alert">{error}</p> : null}
    </>
  );
}

function AboutPanel({ buildInfo, cwd }) {
  return (
    <Panel title="ABOUT">
      <dl>
        <dt>版本</dt><dd>Agent Orchestrator {buildInfo?.version || 'unknown'}</dd>
        <dt>运行时</dt><dd>{buildInfo?.runtime || 'unknown'}</dd>
        <dt>构建时间</dt><dd>{buildInfo?.buildTime || 'unknown'}</dd>
        <dt>Commit</dt><dd>{buildInfo?.commit || 'unknown'}</dd>
        <dt>当前项目</dt><dd>{cwd || '未选择项目'}</dd>
      </dl>
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
  const { form, saveProviderSettings, updateForm } = runtime;
  return (
    <Panel title="PROVIDER">
      <ProviderSettingsForm form={form} updateForm={updateForm} />
      <div className="settings-actions"><button className="btn btn-primary" type="button" onClick={() => void saveProviderSettings()}>保存 Provider 设置</button></div>
    </Panel>
  );
}

function ProviderSettingsForm({ form, updateForm }) {
  return (
    <div className="form-grid">
      <label>Active Provider<select value={form.activeProvider} onChange={updateForm('activeProvider')}><option value="codex">Codex</option><option value="claude">Claude</option></select></label>
      <label>Provider Model<input value={form.providerModel} onChange={updateForm('providerModel')} /></label>
      <label>Provider Effort<input value={form.providerEffort} onChange={updateForm('providerEffort')} /></label>
      <label>Codex Home<input aria-label="Codex Home" value={form.codexHome} onChange={updateForm('codexHome')} /></label>
      <label>Instance Key<input aria-label="Instance Key" value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label>
      <label>Model Provider<input aria-label="Model Provider" value={form.codexModelProvider} onChange={updateForm('codexModelProvider')} /></label>
      <label>Sandbox Policy<select aria-label="Sandbox Policy" value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
      <label className="checkbox-line"><input type="checkbox" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label>
      <label className="wide">Writable Roots<textarea value={form.writableRoots} onChange={updateForm('writableRoots')} placeholder="每行一个绝对路径" /></label>
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
          <button className="btn btn-secondary btn-toolbar-sm" onClick={provider.load} disabled={provider.saving}>刷新</button>
          <button className="btn btn-primary btn-toolbar-sm" data-testid="provider-sandbox-save-button" onClick={provider.save} disabled={provider.saving}>{provider.saving ? '保存中...' : '保存'}</button>
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
      <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-refresh-button" onClick={prompt.loadPrompt} disabled={prompt.saving}>刷新</button>
      <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-copy-button" onClick={prompt.copy} disabled={prompt.loading || prompt.saving}>复制生效提示词</button>
      <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-reset-button" onClick={prompt.reset} disabled={prompt.loading || prompt.saving}>恢复默认</button>
      <button className="btn btn-primary btn-toolbar-sm" data-testid="settings-lsp-save-button" onClick={prompt.save} disabled={prompt.loading || prompt.saving}>{prompt.saving ? '保存中...' : '保存提示词'}</button>
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
  const logList = store.logEntries ? store.logEntries.slice(0, 14) : [];
  return (
    <>
      <div className="section-header">UI LOG</div>
      <div className="data-card-vue settings-log-card" data-testid="settings-log-card">
        <div className="data-row-vue"><strong>日志级别</strong><span>{store.logLevel}</span></div>
        <UILogLevelRow store={store} />
        <div className="settings-action-row settings-log-action-row"><button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-log-refresh-button">刷新日志</button></div>
        <UILogList logList={logList} />
      </div>
    </>
  );
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
  const [status, setStatus] = useState('');
  const [configured, setConfigured] = useState(false);
  const [masked, setMasked] = useState('');

  useEffect(() => {
    callBackend('ui/video/getApiKey', {}).then((res) => {
      if (res?.configured) { setConfigured(true); setMasked(res.masked); }
    }).catch(() => {});
  }, []);

  const save = useCallback(async () => {
    const key = apiKey.trim();
    if (!key) { setStatus('请输入 API Key'); return; }
    try {
      await callBackend('ui/video/setApiKey', { apiKey: key });
      setConfigured(true);
      setMasked(key.length > 8 ? key.slice(0, 4) + '*'.repeat(key.length - 8) + key.slice(-4) : '*'.repeat(key.length));
      setApiKey('');
      setStatus('已保存');
    } catch (err) {
      setStatus(err.message || '保存失败');
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
          {status ? <span className="settings-page-notice">{status}</span> : null}
        </div>
      </div>
    </>
  );
}

export { SettingsPage };

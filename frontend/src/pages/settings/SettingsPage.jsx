import React, { useCallback, useEffect, useState } from 'react';
import { Settings, Laptop, FolderClosed, ShieldCheck, Gauge } from 'lucide-react';
import { getBuildInfo, getPreference, setPreference } from '../../shared/api/backendApi';
import { useProjectStore } from '../../entities/project/model/useProjectStore';

const SETTINGS_KEYS = Object.freeze({
  stallThreshold: 'stallThresholdSec',
  contextThresholds: 'contextUsageAlerts.thresholds',
  activeProvider: 'settings.provider.active',
});

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

function normalizeSettingsCwd(value) {
  const cwd = (value || '').toString().trim();
  if (!cwd || cwd === '.') throw new Error('settings: cwd is required');
  return cwd;
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
    .map((item) => item.trim())
    .filter(Boolean);
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

export default function SettingsPage() {
  const { active: activeProject, projects, scopeCwd } = useProjectStore();
  const settingsCwd = activeProject && activeProject !== '.' ? activeProject : scopeCwd;
  const [buildInfo, setBuildInfo] = useState(null);
  const [form, setForm] = useState({
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
  });
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

  const loadPreferences = useCallback(async () => {
    setError('');
    try {
      const cwd = normalizeSettingsCwd(settingsCwd);
      const [stallValue, contextValue, activeProviderValue] = await Promise.all([
        getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
        getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
        getPreference({ cwd, key: SETTINGS_KEYS.activeProvider }),
      ]);
      const activeProvider = normalizeProviderName(activeProviderValue);
      const providerPrefix = `settings.provider.${activeProvider}`;
      const [
        codexHome,
        codexInstanceKey,
        codexModelProvider,
        providerModel,
        providerEffort,
        sandbox,
      ] = await Promise.all([
        getPreference({ cwd, key: providerSettingKey('codex', 'codexHome') }),
        getPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey') }),
        getPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider') }),
        getPreference({ cwd, key: `${providerPrefix}.model` }),
        getPreference({ cwd, key: `${providerPrefix}.effort` }),
        getPreference({ cwd, key: `${providerPrefix}.sandbox` }),
      ]);
      const contextThresholds = normalizeContextThresholds(contextValue);
      setForm({
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
      });
    } catch (err) {
      setError(err.message || String(err));
    }
  }, [settingsCwd]);

  useEffect(() => {
    void refreshBuildInfo();
  }, [refreshBuildInfo]);

  useEffect(() => {
    void loadPreferences();
  }, [loadPreferences]);

  const updateForm = (key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => ({ ...current, [key]: value }));
  };

  const saveRuntimeSettings = async () => {
    setError('');
    setStatus('');
    try {
      const cwd = normalizeSettingsCwd(settingsCwd);
      const { stallThresholdSec, contextThresholds } = validateRuntimeThresholds(form);
      await setPreference({ cwd, key: SETTINGS_KEYS.stallThreshold, value: stallThresholdSec });
      await setPreference({ cwd, key: SETTINGS_KEYS.contextThresholds, value: contextThresholds });
      setStatus('运行阈值已保存');
    } catch (err) {
      setError(err.message || String(err));
    }
  };

  const saveProviderSettings = async () => {
    setError('');
    setStatus('');
    try {
      const cwd = normalizeSettingsCwd(settingsCwd);
      const provider = normalizeProviderName(form.activeProvider);
      await setPreference({ cwd, key: SETTINGS_KEYS.activeProvider, value: provider });
      await setPreference({ cwd, key: providerSettingKey(provider, 'model'), value: form.providerModel.trim() });
      await setPreference({ cwd, key: providerSettingKey(provider, 'effort'), value: form.providerEffort.trim() });
      await setPreference({
        cwd,
        key: providerSettingKey(provider, 'sandbox'),
        value: sandboxPreferenceValue(form.sandboxPolicy, form.writableRoots, form.networkAccess),
      });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexHome'), value: form.codexHome.trim() });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexInstanceKey'), value: form.codexInstanceKey.trim() });
      await setPreference({ cwd, key: providerSettingKey('codex', 'codexModelProvider'), value: form.codexModelProvider.trim() });
      setStatus('Provider 设置已保存');
    } catch (err) {
      setError(err.message || String(err));
    }
  };

  return (
    <div className="h-full w-full flex flex-col bg-sd-bg/5 overflow-hidden">
      <div className="h-12 border-b border-sd-border/40 px-4 bg-sd-surface/25 backdrop-blur-md flex items-center justify-between select-none shrink-0">
        <span className="text-xs font-semibold text-sd-text-primary flex items-center gap-1.5">
          <Settings size={13} className="text-sd-accent" />
          <span>全局偏好与运行设置</span>
        </span>
        <button
          type="button"
          onClick={() => void refreshBuildInfo()}
          className="h-8 px-3 rounded border border-sd-border/50 bg-sd-surface/60 text-[11px] text-sd-text-primary hover:border-sd-accent/60"
        >
          刷新构建信息
        </button>
      </div>

      <div className="flex-1 overflow-y-auto p-4 flex flex-col gap-4 max-w-4xl font-mono text-xs">
        <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
          <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
            <Laptop size={13} className="text-sd-accent" />
            <span>ABOUT</span>
          </h4>
          <dl className="grid grid-cols-[150px_minmax(0,1fr)] gap-2 text-[11px] leading-normal text-sd-text-secondary">
            <div>版本</div>
            <div className="text-sd-text-primary font-bold">Agent Orchestrator {buildInfo?.version || 'unknown'}</div>
            <div>运行时</div>
            <div className="text-sd-text-primary">{buildInfo?.runtime || 'unknown'}</div>
            <div>构建时间</div>
            <div className="text-sd-text-primary">{buildInfo?.buildTime || 'unknown'}</div>
            <div>Commit</div>
            <div className="text-sd-text-primary">{buildInfo?.commit || 'unknown'}</div>
          </dl>
        </div>

        <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
          <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
            <Gauge size={13} className="text-sd-accent" />
            <span>TURN TRACKER / CONTEXT USAGE ALERT</span>
          </h4>
          <div className="grid grid-cols-4 gap-3">
            <label className="flex flex-col gap-1 text-sd-text-secondary">统一超时阈值<input className="settings-input" type="number" min="30" value={form.stallThresholdSec} onChange={updateForm('stallThresholdSec')} /></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Warn 阈值<input className="settings-input" type="number" min="1" max="100" value={form.contextWarn} onChange={updateForm('contextWarn')} /></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Danger 阈值<input className="settings-input" type="number" min="1" max="100" value={form.contextDanger} onChange={updateForm('contextDanger')} /></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Critical 阈值<input className="settings-input" type="number" min="1" max="100" value={form.contextCritical} onChange={updateForm('contextCritical')} /></label>
          </div>
          <div>
            <button type="button" onClick={() => void saveRuntimeSettings()} className="h-8 px-3 rounded border border-sd-border/50 bg-sd-surface/60 text-sd-text-primary">
              保存运行阈值
            </button>
          </div>
        </div>

        <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
          <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
            <ShieldCheck size={13} className="text-sd-accent" />
            <span>PROVIDER</span>
          </h4>
          <div className="grid grid-cols-2 gap-3">
            <label className="flex flex-col gap-1 text-sd-text-secondary">Active Provider<select className="settings-input" value={form.activeProvider} onChange={updateForm('activeProvider')}><option value="codex">Codex</option><option value="claude">Claude</option></select></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Provider Model<input className="settings-input" value={form.providerModel} onChange={updateForm('providerModel')} /></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Provider Effort<input className="settings-input" value={form.providerEffort} onChange={updateForm('providerEffort')} /></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Sandbox Policy<select className="settings-input" value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Codex Home<input className="settings-input" value={form.codexHome} onChange={updateForm('codexHome')} /></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Instance Key<input className="settings-input" value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label>
            <label className="flex flex-col gap-1 text-sd-text-secondary">Model Provider<input className="settings-input" value={form.codexModelProvider} onChange={updateForm('codexModelProvider')} /></label>
            <label className="flex items-center gap-2 text-sd-text-secondary pt-5"><input type="checkbox" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label>
            <label className="flex flex-col gap-1 text-sd-text-secondary col-span-2">Writable Roots<textarea className="settings-input min-h-20 resize-y" value={form.writableRoots} onChange={updateForm('writableRoots')} placeholder="每行一个绝对路径" /></label>
          </div>
          <div>
            <button type="button" onClick={() => void saveProviderSettings()} className="h-8 px-3 rounded border border-sd-border/50 bg-sd-surface/60 text-sd-text-primary">
              保存 Provider 设置
            </button>
          </div>
        </div>

        <div className="glass-panel p-4 flex flex-col gap-3 bg-sd-surface/30 border border-sd-border/45">
          <h4 className="text-xs font-semibold text-sd-text-primary pb-2 border-b border-sd-border/45 flex items-center gap-1.5">
            <FolderClosed size={13} className="text-sd-accent" />
            <span>当前项目路径列表</span>
          </h4>
          <div className="flex flex-col gap-2">
            <div className="flex justify-between items-center bg-sd-bg/60 border border-sd-border/50 rounded px-2.5 py-1.5">
              <span className="text-[10px] text-sd-text-secondary font-mono truncate">{settingsCwd}</span>
              <span className="text-[9px] text-sd-accent bg-sd-accent/10 border border-sd-accent/20 px-1.5 py-0.5 rounded font-semibold uppercase shrink-0">
                活动中
              </span>
            </div>
            {projects.filter((p) => p !== activeProject).map((p) => (
              <div key={p} className="flex justify-between items-center bg-sd-bg/30 border border-sd-border/40 rounded px-2.5 py-1.5">
                <span className="text-[10px] text-sd-text-secondary font-mono truncate">{p}</span>
              </div>
            ))}
          </div>
        </div>

        {status ? <p className="text-[11px] text-sd-accent">{status}</p> : null}
        {error ? <p className="text-[11px] text-red-400" role="alert">{error}</p> : null}
      </div>
    </div>
  );
}

import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Panel } from '../../shared/pageComponents.jsx';
import { applyModelProvider, listModelProviders, saveModelProviders } from '../services/settingsPageService.js';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './SettingsPageComponents.css';

const RUNTIME_VENDOR_FIELDS = new Set(['configured', 'maskedEnv', 'envStatus']);

const EMPTY_REGISTRY = Object.freeze({
  activeVendorId: '',
  vendors: Object.freeze([]),
});

// 从当前项目 cwd 加载模型厂商注册表，编辑态只留在本组件内，保存时再写回后端。
function ModelProvidersCard({ copy, cwd }) {
  const modelCopy = copy.modelProviders;
  const [registry, setRegistry] = useState(EMPTY_REGISTRY);
  const [selectedVendorId, setSelectedVendorId] = useState('');
  const [loading, setLoading] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [applying, setApplying] = useState(false);
  const [notice, setNotice] = useState({ level: 'info', message: '' });

  const applyRegistryState = useCallback((payload, preferredVendorId = '') => {
    const nextRegistry = normalizeRegistry(payload);
    setRegistry(nextRegistry);
    setSelectedVendorId(selectVendorId(nextRegistry, preferredVendorId));
  }, []);

  const load = useCallback(async () => {
    if (!cwd) {
      setRegistry(EMPTY_REGISTRY);
      setSelectedVendorId('');
      setLoaded(true);
      return;
    }
    setLoaded(false);
    setLoading(true);
    try {
      const payload = await listModelProviders({ cwd });
      applyRegistryState(payload, payload?.activeVendorId);
      setNotice({ level: 'info', message: '' });
    } catch (error) {
      setNotice({ level: 'warning', message: modelCopy.loadFailed + (error?.message || error) });
    } finally {
      setLoading(false);
      setLoaded(true);
    }
  }, [applyRegistryState, cwd, modelCopy]);

  useEffect(() => {
    void load();
  }, [load]);

  const selectedVendor = useMemo(
    () => registry.vendors.find((vendor) => vendor.id === selectedVendorId) || registry.vendors[0] || null,
    [registry.vendors, selectedVendorId],
  );

  const updateVendor = useCallback((field, value) => {
    setRegistry((current) => updateSelectedVendor(current, selectedVendorId, (vendor) => ({ ...vendor, [field]: value })));
  }, [selectedVendorId]);

  const updateNestedVendor = useCallback((group, field, value) => {
    setRegistry((current) => updateSelectedVendor(current, selectedVendorId, (vendor) => ({
      ...vendor,
      [group]: { ...(plainObject(vendor[group]) ? vendor[group] : {}), [field]: value },
    })));
  }, [selectedVendorId]);

  const save = useCallback(async () => {
    if (!cwd || saving) return;
    setSaving(true);
    try {
      await saveModelProviders({ cwd, registry: registrySavePayload(registry) });
      setNotice({ level: 'info', message: modelCopy.saved });
    } catch (error) {
      setNotice({ level: 'error', message: modelCopy.saveFailed + (error?.message || error) });
    } finally {
      setSaving(false);
    }
  }, [cwd, modelCopy, registry, saving]);

  const apply = useCallback(async () => {
    if (!cwd || applying || !selectedVendor?.configured) return;
    setApplying(true);
    try {
      const payload = await applyModelProvider({ cwd, vendorId: selectedVendor.id });
      applyRegistryState(payload, selectedVendor.id);
      setNotice({ level: 'info', message: modelCopy.applied.replace('{label}', selectedVendor.label || selectedVendor.id) });
    } catch (error) {
      setNotice({ level: 'error', message: modelCopy.applyFailed + (error?.message || error) });
    } finally {
      setApplying(false);
    }
  }, [applying, applyRegistryState, cwd, modelCopy, selectedVendor]);

  return (
    <div className="settings-model-providers" data-testid={loaded ? 'settings-model-providers-card' : undefined}>
      <Panel title={modelCopy.title}>
        <div className="settings-model-provider-list" aria-label={modelCopy.vendorList}>
          {registry.vendors.map((vendor) => (
            <button
              type="button"
              key={vendor.id}
              className={vendor.id === selectedVendor?.id ? 'is-selected' : ''}
              onClick={() => setSelectedVendorId(vendor.id)}
            >
              <strong>{vendor.label || vendor.id}</strong>
              <span>{vendorStatusLabel(vendor, registry.activeVendorId, modelCopy)}</span>
            </button>
          ))}
        </div>
        <ModelProviderDetail
          disabled={loading || saving || applying}
          modelCopy={modelCopy}
          onChange={updateVendor}
          onNestedChange={updateNestedVendor}
          vendor={selectedVendor}
          vendors={registry.vendors}
        />
        {notice.message ? <SettingsPromptNotice notice={notice} testId="settings-model-providers-notice" /> : null}
        <div className="settings-action-row settings-action-inline settings-provider-actions">
          <button type="button" className="btn btn-secondary btn-toolbar-sm" onClick={load} disabled={loading || saving || applying}>{loading ? modelCopy.loading : modelCopy.refresh}</button>
          <button type="button" className="btn btn-primary btn-toolbar-sm" onClick={save} disabled={loading || saving || applying}>{saving ? modelCopy.saving : modelCopy.save}</button>
          <button type="button" className="btn btn-primary btn-toolbar-sm" onClick={apply} disabled={loading || saving || applying || !selectedVendor?.configured}>{applying ? modelCopy.applying : modelCopy.apply}</button>
        </div>
      </Panel>
    </div>
  );
}

function ModelProviderDetail({ disabled, modelCopy, onChange, onNestedChange, vendor, vendors }) {
  if (!vendor) return <div className="settings-log-empty">{modelCopy.empty}</div>;
  return (
    <div className="settings-model-provider-detail">
      <div className="data-row-vue">
        <strong>{vendor.label || vendor.id}</strong>
        <span>{envStatusLabel(vendor, modelCopy)}</span>
      </div>
      <div className="data-row-vue">
        <strong>{modelCopy.envKey}</strong>
        <span>{vendor.envKey || modelCopy.none}</span>
      </div>
      <p className="settings-provider-note">{modelCopy.envOnly}</p>
      <div className="form-grid">
        <label className="checkbox-line"><input type="checkbox" checked={Boolean(vendor.enabled)} onChange={(event) => onChange('enabled', event.target.checked)} disabled={disabled} /> {modelCopy.enabled}</label>
        <label>{modelCopy.baseURL}<input value={vendor.baseURL} onChange={(event) => onChange('baseURL', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.envKey}<input value={vendor.envKey} onChange={(event) => onChange('envKey', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.codexModelProvider}<input value={vendor.codexModelProvider} onChange={(event) => onChange('codexModelProvider', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.defaultModel}<input aria-label={modelCopy.defaultModel} value={vendor.defaultModel} onChange={(event) => onChange('defaultModel', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.codexHome}<input value={vendor.codexHome} onChange={(event) => onChange('codexHome', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.codexInstanceKey}<input value={vendor.codexInstanceKey} onChange={(event) => onChange('codexInstanceKey', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.dailyBudget}<input type="number" value={vendor.budget.dailyUsd} onChange={(event) => onNestedChange('budget', 'dailyUsd', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.monthlyBudget}<input type="number" value={vendor.budget.monthlyUsd} onChange={(event) => onNestedChange('budget', 'monthlyUsd', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.tokenPriority}<input type="number" value={vendor.tokenPool.priority} onChange={(event) => onNestedChange('tokenPool', 'priority', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.fallbackVendor}<select value={vendor.tokenPool.fallbackVendorId} onChange={(event) => onNestedChange('tokenPool', 'fallbackVendorId', event.target.value)} disabled={disabled}>
          <option value="">{modelCopy.none}</option>
          {vendors.filter((item) => item.id !== vendor.id).map((item) => <option key={item.id} value={item.id}>{item.label || item.id}</option>)}
        </select></label>
      </div>
    </div>
  );
}

function normalizeRegistry(payload) {
  if (!plainObject(payload)) return EMPTY_REGISTRY;
  return {
    ...payload,
    activeVendorId: textValue(payload.activeVendorId),
    vendors: Array.isArray(payload.vendors) ? payload.vendors.map(normalizeVendor) : [],
  };
}

function normalizeVendor(vendor) {
  const budget = plainObject(vendor?.budget) ? vendor.budget : {};
  const tokenPool = plainObject(vendor?.tokenPool) ? vendor.tokenPool : {};
  return {
    ...vendor,
    id: textValue(vendor?.id),
    label: textValue(vendor?.label || vendor?.id),
    enabled: Boolean(vendor?.enabled),
    baseURL: textValue(vendor?.baseURL),
    envKey: textValue(vendor?.envKey),
    codexModelProvider: textValue(vendor?.codexModelProvider),
    defaultModel: textValue(vendor?.defaultModel),
    codexHome: textValue(vendor?.codexHome),
    codexInstanceKey: textValue(vendor?.codexInstanceKey),
    configured: Boolean(vendor?.configured),
    maskedEnv: textValue(vendor?.maskedEnv),
    envStatus: textValue(vendor?.envStatus),
    budget: {
      ...budget,
      dailyUsd: inputNumberValue(budget.dailyUsd),
      monthlyUsd: inputNumberValue(budget.monthlyUsd),
    },
    tokenPool: {
      ...tokenPool,
      priority: inputNumberValue(tokenPool.priority),
      fallbackVendorId: textValue(tokenPool.fallbackVendorId),
    },
  };
}

function selectVendorId(registry, preferredVendorId) {
  const preferred = textValue(preferredVendorId);
  if (preferred && registry.vendors.some((vendor) => vendor.id === preferred)) return preferred;
  if (registry.activeVendorId && registry.vendors.some((vendor) => vendor.id === registry.activeVendorId)) return registry.activeVendorId;
  return registry.vendors[0]?.id || '';
}

function updateSelectedVendor(registry, selectedVendorId, update) {
  const targetId = selectedVendorId || registry.vendors[0]?.id || '';
  return {
    ...registry,
    vendors: registry.vendors.map((vendor) => (vendor.id === targetId ? normalizeVendor(update(vendor)) : vendor)),
  };
}

function registrySavePayload(registry) {
  return {
    ...registry,
    vendors: registry.vendors.map(vendorSavePayload),
  };
}

// 保存前移除只用于展示的环境变量状态，并把预算与 token 池里的数字字段转成数字。
function vendorSavePayload(vendor) {
  const nextVendor = {};
  for (const [key, value] of Object.entries(vendor)) {
    if (!RUNTIME_VENDOR_FIELDS.has(key)) nextVendor[key] = value;
  }
  nextVendor.budget = {
    ...(plainObject(vendor.budget) ? vendor.budget : {}),
    dailyUsd: finiteNumberOrZero(vendor.budget?.dailyUsd),
    monthlyUsd: finiteNumberOrZero(vendor.budget?.monthlyUsd),
  };
  nextVendor.tokenPool = {
    ...(plainObject(vendor.tokenPool) ? vendor.tokenPool : {}),
    priority: finiteNumberOrZero(vendor.tokenPool?.priority),
    fallbackVendorId: textValue(vendor.tokenPool?.fallbackVendorId),
  };
  return nextVendor;
}

function vendorStatusLabel(vendor, activeVendorId, modelCopy) {
  const status = vendor.envStatus || (vendor.configured ? modelCopy.configured : modelCopy.missing);
  return vendor.id === activeVendorId ? status + ' / ' + modelCopy.active : status;
}

function envStatusLabel(vendor, modelCopy) {
  const status = vendor.envStatus || (vendor.configured ? modelCopy.configured : modelCopy.missing);
  return vendor.maskedEnv ? status + ' / ' + vendor.maskedEnv : status;
}

function finiteNumberOrZero(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
}

function inputNumberValue(value) {
  return value === undefined || value === null ? '' : String(value);
}

function textValue(value) {
  return (value || '').toString();
}

function plainObject(value) {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value));
}

export { ModelProvidersCard };

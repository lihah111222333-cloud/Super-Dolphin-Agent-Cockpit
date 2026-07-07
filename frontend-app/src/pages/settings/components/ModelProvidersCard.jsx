import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Panel } from '../../shared/pageComponents.jsx';
import { applyModelProvider, listModelProviders, saveModelProviders } from '../services/settingsPageService.js';
import {
  EMPTY_REGISTRY,
  envStatusLabel,
  normalizeRegistry,
  plainObject,
  registrySavePayload,
  selectVendorId,
  textValue,
  updateSelectedVendor,
  vendorStatusLabel,
} from './ModelProvidersCardModel.js';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './SettingsPageComponents.css';

function modelProvidersQueryKey(cwd) {
  return ['settings', 'modelProviders', textValue(cwd)];
}

// 从当前项目 cwd 加载模型厂商注册表，编辑态只留在本组件内，保存时再写回后端。
function ModelProvidersCard({ copy, cwd }) {
  const modelCopy = copy.modelProviders;
  const queryClient = useQueryClient();
  const currentCwd = textValue(cwd);
  const currentCwdRef = useRef(currentCwd);
  const [registryState, setRegistryState] = useState(null);
  const [selectedVendorId, setSelectedVendorId] = useState('');
  const [dirty, setDirty] = useState(false);
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const hasCurrentRegistry = Boolean(registryState && registryState.cwd === currentCwd);
  const currentRegistry = hasCurrentRegistry ? registryState.registry : EMPTY_REGISTRY;
  const queryKey = useMemo(() => modelProvidersQueryKey(currentCwd), [currentCwd]);

  useEffect(() => {
    currentCwdRef.current = currentCwd;
  }, [currentCwd]);

  const applyRegistryState = useCallback((payload, preferredVendorId = '', registryCwd = '') => {
    const nextRegistry = normalizeRegistry(payload);
    setRegistryState({ cwd: registryCwd, registry: nextRegistry });
    setSelectedVendorId(selectVendorId(nextRegistry, preferredVendorId));
  }, []);

  const registryQuery = useQuery({
    queryKey,
    queryFn: () => listModelProviders({ cwd: currentCwd }),
    enabled: Boolean(currentCwd),
    refetchOnWindowFocus: false,
    retry: false,
  });
  const saveMutation = useMutation({
    mutationFn: async ({ registry, requestCwd }) => {
      await saveModelProviders({ cwd: requestCwd, registry: registrySavePayload(registry) });
      return { registry, requestCwd };
    },
    onSuccess: ({ registry, requestCwd }) => {
      if (currentCwdRef.current !== requestCwd) return;
      queryClient.setQueryData(modelProvidersQueryKey(requestCwd), registry);
      setDirty(false);
      setNotice({ level: 'info', message: modelCopy.saved });
    },
    onError: (error, variables) => {
      if (currentCwdRef.current === variables?.requestCwd) {
        setNotice({ level: 'error', message: modelCopy.saveFailed + (error?.message || error) });
      }
    },
    retry: false,
  });
  const applyMutation = useMutation({
    mutationFn: async ({ registry, requestCwd, vendor }) => {
      let phase = 'save';
      try {
        await saveModelProviders({ cwd: requestCwd, registry: registrySavePayload(registry) });
        phase = 'apply';
        const payload = await applyModelProvider({ cwd: requestCwd, vendorId: vendor.id });
        return { payload, requestCwd, vendor };
      } catch (error) {
        if (error && typeof error === 'object') error.phase = phase;
        throw error;
      }
    },
    onSuccess: ({ payload, requestCwd, vendor }) => {
      if (currentCwdRef.current !== requestCwd) return;
      queryClient.setQueryData(modelProvidersQueryKey(requestCwd), payload);
      applyRegistryState(payload, vendor.id, requestCwd);
      setDirty(false);
      setNotice({ level: 'info', message: modelCopy.applied.replace('{label}', vendor.label || vendor.id) });
    },
    onError: (error, variables) => {
      if (currentCwdRef.current === variables?.requestCwd) {
        const prefix = error?.phase === 'save' ? modelCopy.saveFailed : modelCopy.applyFailed;
        setNotice({ level: 'error', message: prefix + (error?.message || error) });
      }
    },
    retry: false,
  });
  const loading = registryQuery.isFetching;
  const saving = saveMutation.isPending;
  const applying = applyMutation.isPending;

  useEffect(() => {
    if (!currentCwd) {
      setRegistryState(null);
      setSelectedVendorId('');
      setDirty(false);
      setNotice({ level: 'info', message: '' });
      return;
    }
    setRegistryState(null);
    setSelectedVendorId('');
    setDirty(false);
    setNotice({ level: 'info', message: '' });
  }, [currentCwd]);

  useEffect(() => {
    if (!currentCwd || dirty || !registryQuery.data) return;
    applyRegistryState(registryQuery.data, registryQuery.data?.activeVendorId, currentCwd);
    setNotice({ level: 'info', message: '' });
  }, [applyRegistryState, currentCwd, dirty, registryQuery.data]);

  useEffect(() => {
    if (!registryQuery.error) return;
    setNotice({ level: 'warning', message: modelCopy.loadFailed + (registryQuery.error?.message || registryQuery.error) });
  }, [modelCopy.loadFailed, registryQuery.error]);

  const load = useCallback(() => {
    if (!currentCwd) {
      setRegistryState(null);
      setSelectedVendorId('');
      setDirty(false);
      return;
    }
    setDirty(false);
    setNotice({ level: 'info', message: '' });
    void queryClient.invalidateQueries({ queryKey, exact: true });
  }, [currentCwd, queryClient, queryKey]);

  const selectedVendor = useMemo(
    () => currentRegistry.vendors.find((vendor) => vendor.id === selectedVendorId) || currentRegistry.vendors[0] || null,
    [currentRegistry.vendors, selectedVendorId],
  );

  const updateVendor = useCallback((field, value) => {
    setRegistryState((current) => {
      if (!current || current.cwd !== currentCwd) return current;
      setDirty(true);
      return {
        ...current,
        registry: updateSelectedVendor(current.registry, selectedVendorId, (vendor) => ({ ...vendor, [field]: value })),
      };
    });
  }, [currentCwd, selectedVendorId]);

  const updateNestedVendor = useCallback((group, field, value) => {
    setRegistryState((current) => {
      if (!current || current.cwd !== currentCwd) return current;
      setDirty(true);
      return {
        ...current,
        registry: updateSelectedVendor(current.registry, selectedVendorId, (vendor) => ({
          ...vendor,
          [group]: { ...(plainObject(vendor[group]) ? vendor[group] : {}), [field]: value },
        })),
      };
    });
  }, [currentCwd, selectedVendorId]);

  const save = useCallback(() => {
    if (!currentCwd || saving || !registryState || registryState.cwd !== currentCwd) return;
    saveMutation.mutate({ registry: registryState.registry, requestCwd: currentCwd });
  }, [currentCwd, registryState, saveMutation, saving]);

  const apply = useCallback(() => {
    if (!currentCwd || applying || !registryState || registryState.cwd !== currentCwd || !selectedVendor || !selectedVendor.enabled || !selectedVendor.configured) return;
    applyMutation.mutate({ registry: registryState.registry, requestCwd: currentCwd, vendor: selectedVendor });
  }, [applying, applyMutation, currentCwd, registryState, selectedVendor]);

  return (
    <div className="settings-model-providers" data-testid={hasCurrentRegistry ? 'settings-model-providers-card' : undefined}>
      <Panel title={modelCopy.title}>
        <div className="settings-model-provider-list" aria-label={modelCopy.vendorList}>
          {currentRegistry.vendors.map((vendor) => (
            <button
              type="button"
              key={vendor.id}
              className={vendor.id === selectedVendor?.id ? 'is-selected' : ''}
              onClick={() => setSelectedVendorId(vendor.id)}
            >
              <strong>{vendor.label || vendor.id}</strong>
              <span>{vendorStatusLabel(vendor, currentRegistry.activeVendorId, modelCopy)}</span>
            </button>
          ))}
        </div>
        <ModelProviderDetail
          disabled={loading || saving || applying || !hasCurrentRegistry}
          modelCopy={modelCopy}
          onChange={updateVendor}
          onNestedChange={updateNestedVendor}
          vendor={selectedVendor}
          vendors={currentRegistry.vendors}
        />
        {notice.message ? <SettingsPromptNotice notice={notice} testId="settings-model-providers-notice" /> : null}
        <div className="settings-action-row settings-action-inline settings-provider-actions">
          <button type="button" className="btn btn-secondary btn-toolbar-sm" onClick={load} disabled={loading || saving || applying}>{loading ? modelCopy.loading : modelCopy.refresh}</button>
          <button type="button" className="btn btn-primary btn-toolbar-sm" onClick={save} disabled={loading || saving || applying || !hasCurrentRegistry}>{saving ? modelCopy.saving : modelCopy.save}</button>
          <button type="button" className="btn btn-primary btn-toolbar-sm" onClick={apply} disabled={loading || saving || applying || !hasCurrentRegistry || !selectedVendor?.enabled || !selectedVendor?.configured}>{applying ? modelCopy.applying : modelCopy.apply}</button>
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
          {vendors.map((item) => (item.id === vendor.id ? null : <option key={item.id} value={item.id}>{item.label || item.id}</option>))}
        </select></label>
      </div>
    </div>
  );
}

export { ModelProvidersCard };

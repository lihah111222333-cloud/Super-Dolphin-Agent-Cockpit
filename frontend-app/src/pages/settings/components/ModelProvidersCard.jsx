import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { runBackgroundAction, runUIAction } from '../../../shared/ui/runUIAction.js';
import { Panel } from '../../shared/pageComponents.jsx';
import { settingsPageService } from '../services/settingsPageService.js';
import {
  EMPTY_REGISTRY,
  normalizeRegistry,
  plainObject,
  registrySavePayload,
  selectVendorId,
  textValue,
  updateSelectedVendor,
} from './ModelProvidersCardModel.js';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import { ModelProviderActions, ModelProviderDetail, ModelProviderList } from './ModelProvidersCardParts.jsx';
import './SettingsPageComponents.css';

const { applyModelProvider, listModelProviders, saveModelProviders } = settingsPageService;

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
    queryFn: () => runBackgroundAction('settings.model-providers.bootstrap', () => listModelProviders({ cwd: currentCwd })),
    enabled: Boolean(currentCwd),
    refetchOnWindowFocus: false,
    retry: false,
  });
  const saveMutation = useModelProviderSaveMutation({ currentCwdRef, modelCopy, queryClient, setDirty, setNotice });
  const applyMutation = useModelProviderApplyMutation({ applyRegistryState, currentCwdRef, modelCopy, queryClient, setDirty, setNotice });
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
    setNotice({ level: 'warning', message: modelCopy.loadFailed });
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
    return runUIAction('settings.model-providers.load', () => queryClient.invalidateQueries({ queryKey, exact: true }), { retryable: true });
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
        registry: updateSelectedVendor(current.registry, selectedVendorId, (vendor) => vendorWithValue(field, value, vendor)),
      };
    });
  }, [currentCwd, selectedVendorId]);

  const updateNestedVendor = useCallback((group, field, value) => {
    setRegistryState((current) => {
      if (!current || current.cwd !== currentCwd) return current;
      setDirty(true);
      return {
        ...current,
        registry: updateSelectedVendor(
          current.registry,
          selectedVendorId,
          (vendor) => vendorWithNestedValue(field, group, value, vendor),
        ),
      };
    });
  }, [currentCwd, selectedVendorId]);

  const save = useCallback(() => {
    if (!currentCwd || saving || !registryState || registryState.cwd !== currentCwd) return;
    return runUIAction('settings.model-providers.save', () => saveMutation.mutateAsync({ registry: registryState.registry, requestCwd: currentCwd }));
  }, [currentCwd, registryState, saveMutation, saving]);

  const apply = useCallback(() => {
    if (!currentCwd || applying || !registryState || registryState.cwd !== currentCwd || !selectedVendor || !selectedVendor.enabled || !selectedVendor.configured) return;
    return runUIAction('settings.model-providers.apply', () => applyMutation.mutateAsync({ registry: registryState.registry, requestCwd: currentCwd, vendor: selectedVendor }));
  }, [applying, applyMutation, currentCwd, registryState, selectedVendor]);

  return (
    <div className="settings-model-providers" data-testid={hasCurrentRegistry ? 'settings-model-providers-card' : undefined}>
      <Panel title={modelCopy.title}>
        <ModelProviderList
          activeVendorId={currentRegistry.activeVendorId}
          modelCopy={modelCopy}
          onSelect={setSelectedVendorId}
          selectedVendor={selectedVendor}
          vendors={currentRegistry.vendors}
        />
        <ModelProviderDetail
          disabled={loading || saving || applying || !hasCurrentRegistry}
          modelCopy={modelCopy}
          onChange={updateVendor}
          onNestedChange={updateNestedVendor}
          vendor={selectedVendor}
          vendors={currentRegistry.vendors}
        />
        {notice.message ? <SettingsPromptNotice notice={notice} testId="settings-model-providers-notice" /> : null}
        <ModelProviderActions
          applying={applying}
          canApply={hasCurrentRegistry && selectedVendor?.enabled && selectedVendor?.configured}
          canSave={hasCurrentRegistry}
          loading={loading}
          modelCopy={modelCopy}
          onApply={apply}
          onLoad={load}
          onSave={save}
          saving={saving}
        />
      </Panel>
    </div>
  );
}

function useModelProviderSaveMutation({ currentCwdRef, modelCopy, queryClient, setDirty, setNotice }) {
  return useMutation({
    mutationFn: async ({ registry, requestCwd }) => {
      await saveModelProviderRegistry(requestCwd, registry);
      return { registry, requestCwd };
    },
    onSuccess: ({ registry, requestCwd }) => {
      if (currentCwdRef.current !== requestCwd) return;
      queryClient.setQueryData(modelProvidersQueryKey(requestCwd), registry);
      setDirty(false);
      setNotice({ level: 'info', message: modelCopy.saved });
    },
    onError: (error, variables) => {
      if (currentCwdRef.current !== variables?.requestCwd) return;
      setMutationErrorNotice({ error, prefix: modelCopy.saveFailed, setNotice });
    },
    retry: false,
  });
}

function useModelProviderApplyMutation(state) {
  const { applyRegistryState, currentCwdRef, modelCopy, queryClient, setDirty, setNotice } = state;
  return useMutation({
    mutationFn: async ({ registry, requestCwd, vendor }) => {
      let phase = 'save';
      try {
        await saveModelProviderRegistry(requestCwd, registry);
        phase = 'apply';
        const payload = await applyModelProviderVendor(requestCwd, vendor.id);
        return modelProviderApplyResult(payload, requestCwd, vendor);
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
      if (currentCwdRef.current !== variables?.requestCwd) return;
      const prefix = error?.phase === 'save' ? modelCopy.saveFailed : modelCopy.applyFailed;
      setMutationErrorNotice({ error, prefix, setNotice });
    },
    retry: false,
  });
}

function setMutationErrorNotice({ error: _error, prefix, setNotice }) {
  setNotice({ level: 'error', message: prefix });
}

function saveModelProviderRegistry(cwd, registry) {
  return saveModelProviders({ cwd, registry: registrySavePayload(registry) });
}

function applyModelProviderVendor(cwd, vendorId) {
  return applyModelProvider({ cwd, vendorId });
}

function modelProviderApplyResult(payload, requestCwd, vendor) {
  return { payload, requestCwd, vendor };
}

function vendorWithValue(field, value, vendor) {
  return { ...vendor, [field]: value };
}

function vendorWithNestedValue(field, group, value, vendor) {
  const currentGroup = plainObject(vendor[group]) ? vendor[group] : {};
  return {
    ...vendor,
    [group]: { ...currentGroup, [field]: value },
  };
}

export { ModelProvidersCard };

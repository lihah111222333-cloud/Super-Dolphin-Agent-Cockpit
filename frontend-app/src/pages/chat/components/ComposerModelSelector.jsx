import React, { useEffect, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Button as AriaButton, Dialog, DialogTrigger, Popover } from 'react-aria-components';
import { ChevronDown, Zap } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { loadedModelDraft, modelSelectorDerivedState, modelSelectorSnapshot, nextModelDraft } from '../adapters/composerModelSelectorState.js';
import { runUIAction } from './chatUiActions.js';

function composerModelSelectorThreadConfigQueryKey(activeThreadId) {
  if (!activeThreadId) return ['chat', 'composerModelSelector', 'threadConfig'];
  return ['chat', 'composerModelSelector', 'threadConfig', activeThreadId];
}

function useModelSelectorController({ copy, store, activeThreadId, disabled }) {
  const [openState, setOpenState] = useState({ disabled, open: false });
  if (openState.disabled !== disabled) {
    setOpenState({ disabled, open: false });
  }
  const open = openState.disabled === disabled ? openState.open : false;
  const setOpen = (value) => {
    setOpenState((current) => ({
      disabled,
      open: typeof value === 'function' ? Boolean(value(current.open)) : Boolean(value),
    }));
  };
  const snapshot = modelSelectorSnapshot(store, activeThreadId);
  const { activeEffort, activeModel, activeThreadConfig, canOverrideThread, draftEffort, draftModel, providerKey } = snapshot;
  const [draft, setDraft] = useState({ model: draftModel, effort: draftEffort });
  const closedDraft = { model: draftModel, effort: draftEffort };
  const selectorOpen = open && !disabled;
  const selectorDraft = selectorOpen ? draft : closedDraft;

  const loadThreadConfigQuery = useQuery({
    queryKey: composerModelSelectorThreadConfigQueryKey(activeThreadId),
    queryFn: async () => (await store.loadThreadConfig?.(activeThreadId)) || null,
    enabled: false,
    retry: false,
  });
  const saveModelConfigMutation = useMutation({
    mutationFn: ({ next, threadId }) => store.saveComposerModelConfig?.({ threadId, model: next.model, effort: next.effort }),
    retry: false,
  });
  const restoreInheritanceMutation = useMutation({
    mutationFn: ({ threadId }) => store.restoreComposerModelInheritance?.({ threadId }),
    retry: false,
  });

  useEffect(() => {
    if (!selectorOpen || !loadThreadConfigQuery.data) return;
    setDraft(loadedModelDraft(loadThreadConfigQuery.data, activeModel, activeEffort));
  }, [activeEffort, activeModel, loadThreadConfigQuery.data, selectorOpen]);

  const setSelectorOpen = async (nextOpen) => {
    if (disabled && nextOpen) return;
    setDraft({ model: draftModel, effort: draftEffort });
    setOpen(nextOpen);
    if (!nextOpen || !activeThreadId) return;
    await loadThreadConfigQuery.refetch({ throwOnError: true });
  };

  const saveModelConfig = async (patch) => {
    const next = nextModelDraft(providerKey, selectorDraft, patch, activeModel);
    setDraft(next);
    await saveModelConfigMutation.mutateAsync({ next, threadId: activeThreadId });
  };

  const restoreInheritance = async () => {
    const restored = await restoreInheritanceMutation.mutateAsync({ threadId: activeThreadId });
    if (restored) void setSelectorOpen(false);
  };
  const derivedState = modelSelectorDerivedState({ activeEffort, activeModel, activeThreadConfig, canOverrideThread, copy, disabled, draft: selectorDraft, providerKey, store, activeThreadId });

  return {
    ...derivedState,
    open: selectorOpen,
    restoreInheritance,
    saveModelConfig,
    setSelectorOpen,
    selectorBusy: derivedState.selectorBusy || loadThreadConfigQuery.isFetching || saveModelConfigMutation.isPending || restoreInheritanceMutation.isPending,
  };
}

function ComposerModelSelector({ copy = APP_COPY.zh.chat, store, activeThreadId, disabled = false }) {
  const controller = useModelSelectorController({ copy, store, activeThreadId, disabled });

  return (
    <div className="composer-model-wrap">
      <DialogTrigger isOpen={controller.open} onOpenChange={(open) => runUIAction(() => controller.setSelectorOpen(open))}>
        <ModelSelectorButton copy={copy} controller={controller} />
        {controller.open ? <ModelSelectorDropdown copy={copy} controller={controller} /> : null}
      </DialogTrigger>
    </div>
  );
}

function ModelSelectorButton({ copy, controller }) {
  const selectorTitle = controller.disabled
    ? copy.projectActionBlocked
    : (controller.canOverrideThread ? copy.threadModelConfig : copy.globalModelConfig);
  return (
    <AriaButton
      type="button"
      className="composer-model"
      aria-label={copy.selectModel}
      aria-expanded={controller.open}
      aria-haspopup="dialog"
      aria-busy={controller.selectorBusy}
      title={selectorTitle}
      isDisabled={controller.disabled}
    >
      {controller.providerKey === 'codex' ? <Zap size={14} aria-hidden="true" /> : null}
      <span>{controller.label}</span>
      <ChevronDown size={12} />
    </AriaButton>
  );
}

function ModelSelectorDropdown({ copy, controller }) {
  const optionDisabled = controller.disabled || controller.selectorBusy;
  return (
    <Popover className="model-dropdown" placement="top end">
      <Dialog aria-label={copy.modelConfig} className="model-dropdown-dialog">
        <label>
          <span>{copy.model}</span>
          <select aria-label={copy.model} value={controller.selectModelValue} disabled={optionDisabled} onChange={(event) => runUIAction(() => controller.saveModelConfig({ model: event.target.value }))}>
            {controller.canOverrideThread ? <option value="">{controller.inheritModelLabel}</option> : null}
            {controller.modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
          </select>
        </label>
        <label>
          <span>{copy.effort}</span>
          <select aria-label={copy.reasoningEffort} value={controller.selectEffortValue} disabled={optionDisabled} onChange={(event) => runUIAction(() => controller.saveModelConfig({ effort: event.target.value }))}>
            {controller.canOverrideThread ? <option value="">{controller.inheritEffortLabel}</option> : null}
            {controller.effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
          </select>
        </label>
        {controller.canOverrideThread && !controller.inherited ? (
          <button type="button" className="model-inherit" disabled={optionDisabled} onClick={() => runUIAction(controller.restoreInheritance)}>
            {copy.inheritGlobal}
          </button>
        ) : null}
      </Dialog>
    </Popover>
  );
}

export { ComposerModelSelector };

import React, { useEffect, useRef, useState } from 'react';
import { Button as AriaButton, Dialog, DialogTrigger, Popover } from 'react-aria-components';
import { ChevronDown, Zap } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { loadedModelDraft, modelSelectorDerivedState, modelSelectorSnapshot, nextModelDraft } from '../adapters/composerModelSelectorState.js';
import { runUIAction } from '../model/chatUiActions.js';

function useModelSelectorController({ copy, store, activeThreadId, disabled }) {
  const [openState, setOpenState] = useState({ disabled, open: false });
  const mountedRef = useRef(false);
  const loadRequestRef = useRef(0);
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

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      loadRequestRef.current += 1;
    };
  }, []);

  const setSelectorOpen = async (nextOpen) => {
    if (disabled && nextOpen) return;
    const requestID = loadRequestRef.current + 1;
    loadRequestRef.current = requestID;
    setDraft({ model: draftModel, effort: draftEffort });
    if (!nextOpen) {
      setOpen(false);
      return;
    }
    const hasLoadedCatalog = Array.isArray(activeThreadConfig?.availableModels) && activeThreadConfig.availableModels.length > 0;
    setOpen(hasLoadedCatalog);
    if (!activeThreadId) return;
    const loaded = await store.loadThreadConfig?.(activeThreadId);
    if (!mountedRef.current || loadRequestRef.current !== requestID || !loaded) return;
    setDraft(loadedModelDraft(loaded, activeModel, activeEffort));
    setOpen(true);
  };

  const saveModelConfig = async (patch) => {
    const next = nextModelDraft(providerKey, selectorDraft, patch, activeModel);
    setDraft(next);
    await store.saveComposerModelConfig?.({ threadId: activeThreadId, model: next.model, effort: next.effort });
  };

  const restoreInheritance = async () => {
    const restored = await store.restoreComposerModelInheritance?.({ threadId: activeThreadId });
    if (restored) void setSelectorOpen(false);
  };

  return {
    ...modelSelectorDerivedState({ activeEffort, activeModel, activeThreadConfig, canOverrideThread, copy, disabled, draft: selectorDraft, providerKey, store, activeThreadId }),
    open: selectorOpen,
    restoreInheritance,
    saveModelConfig,
    setSelectorOpen,
  };
}

function ComposerModelSelector({ copy = APP_COPY.zh.chat, store, activeThreadId, disabled = false }) {
  const controller = useModelSelectorController({ copy, store, activeThreadId, disabled });

  return (
    <div className="composer-model-wrap">
      <DialogTrigger isOpen={controller.open} onOpenChange={(open) => runUIAction('composer.model.selector', () => controller.setSelectorOpen(open))}>
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
          <select aria-label={copy.model} value={controller.selectModelValue} disabled={optionDisabled} onChange={(event) => runUIAction('settings.model.save', () => controller.saveModelConfig({ model: event.target.value }))}>
            {controller.canOverrideThread ? <option value="">{controller.inheritModelLabel}</option> : null}
            {controller.modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
          </select>
        </label>
        <label>
          <span>{copy.effort}</span>
          <select aria-label={copy.reasoningEffort} value={controller.selectEffortValue} disabled={optionDisabled} onChange={(event) => runUIAction('settings.model.save', () => controller.saveModelConfig({ effort: event.target.value }))}>
            {controller.canOverrideThread ? <option value="">{controller.inheritEffortLabel}</option> : null}
            {controller.effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
          </select>
        </label>
        {controller.canOverrideThread && !controller.inherited ? (
          <button type="button" className="model-inherit" disabled={optionDisabled} onClick={() => runUIAction('settings.model.restore', controller.restoreInheritance)}>
            {copy.inheritGlobal}
          </button>
        ) : null}
      </Dialog>
    </Popover>
  );
}

export { ComposerModelSelector };

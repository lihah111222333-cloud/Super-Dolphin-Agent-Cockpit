import React, { useEffect, useRef, useState } from 'react';
import { ChevronDown, Zap } from 'lucide-react';
import { loadedModelDraft, modelSelectorDerivedState, modelSelectorSnapshot, nextModelDraft } from '../adapters/composerModelSelectorState.js';
import { runUIAction } from './chatUiActions.js';

function useModelSelectorController({ store, activeThreadId, disabled, wrapRef }) {
  const [open, setOpen] = useState(false);
  const snapshot = modelSelectorSnapshot(store, activeThreadId);
  const { activeEffort, activeModel, activeThreadConfig, canOverrideThread, draftEffort, draftModel, providerKey } = snapshot;
  const [draft, setDraft] = useState({ model: draftModel, effort: draftEffort });
  const closedDraft = { model: draftModel, effort: draftEffort };
  const selectorOpen = open && !disabled;
  useEffect(() => { if (disabled && open) setOpen(false); }, [disabled, open]);
  const selectorDraft = selectorOpen ? draft : closedDraft;

  useEffect(() => {
    if (!selectorOpen) return undefined;
    const onPointerDown = (event) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target)) setOpen(false);
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () => document.removeEventListener('pointerdown', onPointerDown, true);
  }, [selectorOpen, wrapRef]);

  const openSelector = async () => {
    if (disabled) return;
    const nextOpen = !selectorOpen;
    setDraft({ model: draftModel, effort: draftEffort });
    setOpen(nextOpen);
    if (!nextOpen || !activeThreadId) return;
    let cancelled = false;
    const loaded = await store.loadThreadConfig?.(activeThreadId);
    if (cancelled || !loaded) return;
    setDraft(loadedModelDraft(loaded, activeModel, activeEffort));
    return () => { cancelled = true; };
  };

  const saveModelConfig = async (patch) => {
    const next = nextModelDraft(providerKey, selectorDraft, patch, activeModel);
    setDraft(next);
    await store.saveComposerModelConfig?.({ threadId: activeThreadId, model: next.model, effort: next.effort });
  };

  const restoreInheritance = async () => {
    const restored = await store.restoreComposerModelInheritance?.({ threadId: activeThreadId });
    if (restored) setOpen(false);
  };

  return {
    ...modelSelectorDerivedState({ activeEffort, activeModel, activeThreadConfig, canOverrideThread, disabled, draft: selectorDraft, providerKey, store, activeThreadId }),
    open: selectorOpen,
    openSelector,
    restoreInheritance,
    saveModelConfig,
  };
}

function ComposerModelSelector({ store, activeThreadId, disabled = false }) {
  const wrapRef = useRef(null);
  const controller = useModelSelectorController({ store, activeThreadId, disabled, wrapRef });

  return (
    <div className="composer-model-wrap" ref={wrapRef}>
      <ModelSelectorButton controller={controller} />
      {controller.open ? <ModelSelectorDropdown controller={controller} /> : null}
    </div>
  );
}

function ModelSelectorButton({ controller }) {
  return (
    <button
      type="button"
      className="composer-model"
      aria-label="选择模型"
      aria-expanded={controller.open}
      aria-haspopup="dialog"
      aria-busy={controller.selectorBusy}
      title={controller.selectorTitle}
      disabled={controller.disabled}
      onClick={() => runUIAction(controller.openSelector)}
    >
      {controller.providerKey === 'codex' ? <Zap size={14} aria-hidden="true" /> : null}
      <span>{controller.label}</span>
      <ChevronDown size={12} />
    </button>
  );
}

function ModelSelectorDropdown({ controller }) {
  const optionDisabled = controller.disabled || controller.selectorBusy;
  return (
    <dialog className="model-dropdown" open aria-label="模型配置">
      <label>
        <span>模型</span>
        <select aria-label="模型" value={controller.selectModelValue} disabled={optionDisabled} onChange={(event) => runUIAction(() => controller.saveModelConfig({ model: event.target.value }))}>
          {controller.canOverrideThread ? <option value="">{controller.inheritModelLabel}</option> : null}
          {controller.modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
      </label>
      <label>
        <span>强度</span>
        <select aria-label="推理强度" value={controller.selectEffortValue} disabled={optionDisabled} onChange={(event) => runUIAction(() => controller.saveModelConfig({ effort: event.target.value }))}>
          {controller.canOverrideThread ? <option value="">{controller.inheritEffortLabel}</option> : null}
          {controller.effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
        </select>
      </label>
      {controller.canOverrideThread && !controller.inherited ? (
        <button type="button" className="model-inherit" disabled={optionDisabled} onClick={() => runUIAction(controller.restoreInheritance)}>
          继承全局
        </button>
      ) : null}
    </dialog>
  );
}

export { ComposerModelSelector };

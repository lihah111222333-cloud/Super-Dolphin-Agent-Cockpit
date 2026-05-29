import React, { useCallback, useMemo } from 'react';
import * as Vue from '../lib/vue.esm-browser.prod.js';
import { useStore } from 'zustand';
import { logDebug } from '../services/log.js';
import { composerStoreVanilla } from '../stores/composer.js';
import { useComposerDragDrop } from '../composables/useComposerDragDrop.js';
import { useComposerInterrupt } from '../composables/useComposerInterrupt.js';
import { useComposerThreadConfig } from '../composables/useComposerThreadConfig.js';
import { useComposerTextarea } from '../composables/useComposerTextarea.js';
import { useVueSetup, val } from '../utils/vue-compat.js';

export { applyComposerTextareaAutoHeight } from '../utils/composer-textarea-height.js';

function getCompactResultToneClass(compactResultText, compactResultTone) {
  if (!compactResultText) return '';
  const tone = (compactResultTone || '').toString().trim().toLowerCase();
  if (tone === 'success') return 'is-success';
  if (tone === 'error') return 'is-error';
  return '';
}

function getCompactTitle(canCompact, compacting, tokenTooltip) {
  if (!canCompact) return '当前 agent 不支持上下文压缩';
  if (compacting) return '正在暂停并压缩上下文，等待压缩返回结果';
  if (tokenTooltip) return `压缩上下文\n\n${tokenTooltip}`;
  return '压缩上下文';
}

function getCompactAriaLabel(canCompact, compacting, tokenTooltip) {
  if (!canCompact) return '当前 agent 不支持上下文压缩';
  if (compacting) return '正在暂停并压缩上下文，等待压缩返回结果';
  if (tokenTooltip) return `压缩上下文，${tokenTooltip}`;
  return '压缩上下文';
}

function useComposerRuntimeState(composer) {
  const subscribedComposerState = useStore(composerStoreVanilla);
  return composer?.state || subscribedComposerState;
}

function useLegacyComposerEmit(handlers) {
  const {
    onSend,
    onInterrupt,
    onCompactProps,
    onUpdateThreadConfigModel,
    onUpdateThreadConfigEffort,
    onSaveThreadConfig,
    onRestoreThreadConfigInherit,
    onOpenForkDraft,
  } = handlers;
  return useCallback((name, ...args) => {
    if (name === 'send') onSend?.(...args);
    else if (name === 'interrupt') onInterrupt?.(...args);
    else if (name === 'compact') onCompactProps?.(...args);
    else if (name === 'update-thread-config-model') onUpdateThreadConfigModel?.(...args);
    else if (name === 'update-thread-config-effort') onUpdateThreadConfigEffort?.(...args);
    else if (name === 'save-thread-config') onSaveThreadConfig?.(...args);
    else if (name === 'restore-thread-config-inherit') onRestoreThreadConfigInherit?.(...args);
    else if (name === 'open-fork-draft') onOpenForkDraft?.(...args);
  }, [onSend, onInterrupt, onCompactProps, onUpdateThreadConfigModel, onUpdateThreadConfigEffort, onSaveThreadConfig, onRestoreThreadConfigInherit, onOpenForkDraft]);
}

const EMPTY_THREAD_CONFIG_META = Object.freeze({
  override: Object.freeze({}),
  effective: Object.freeze({}),
});

function makeStableRefCallback(vm, refKey) {
  return (el) => {
    const target = vm?.[refKey];
    if (!target || typeof target !== 'object' || !('value' in target)) return;
    const next = el || null;
    if (target.value !== next) {
      target.value = next;
    }
  };
}

export function ComposerBar(props) {
  const {
    composer,
    disabled = false,
    sendDisabled = false,
    threadId = '',
    interruptible = false,
    compacting = false,
    canCompact = true,
    compactResultText = '',
    compactResultTone = '',
    compactSuccessCount = 0,
    tokenInline = '',
    tokenTooltip = '',
    tokenLevel = 'normal',
    isCmd = false,
    threadConfigProvider = '',
    threadConfigSupportsOverride = false,
    threadConfigDraftModel = '',
    threadConfigDraftEffort = '',
    threadConfigLoading = false,
    threadConfigSaving = false,
    threadConfigNotice = '',
    threadConfigNoticeLevel = 'info',
    threadConfigMeta = EMPTY_THREAD_CONFIG_META,
    onSend,
    onInterrupt,
    onCompact: onCompactProps,
    onUpdateThreadConfigModel,
    onUpdateThreadConfigEffort,
    onSaveThreadConfig,
    onRestoreThreadConfigInherit,
    onOpenForkDraft,
  } = props;

  const composerState = useComposerRuntimeState(composer);
  const legacyEmit = useLegacyComposerEmit({
    onSend,
    onInterrupt,
    onCompactProps,
    onUpdateThreadConfigModel,
    onUpdateThreadConfigEffort,
    onSaveThreadConfig,
    onRestoreThreadConfigInherit,
    onOpenForkDraft,
  });

  const setupProps = useMemo(() => ({
    ...props,
    composer,
    disabled,
    sendDisabled,
    threadId,
    interruptible,
    compacting,
    canCompact,
    compactResultText,
    compactResultTone,
    compactSuccessCount,
    tokenInline,
    tokenTooltip,
    tokenLevel,
    isCmd,
    threadConfigProvider,
    threadConfigSupportsOverride,
    threadConfigDraftModel,
    threadConfigDraftEffort,
    threadConfigLoading,
    threadConfigSaving,
    threadConfigNotice,
    threadConfigNoticeLevel,
    threadConfigMeta,
  }), [
    props,
    composer,
    disabled,
    sendDisabled,
    threadId,
    interruptible,
    compacting,
    canCompact,
    compactResultText,
    compactResultTone,
    compactSuccessCount,
    tokenInline,
    tokenTooltip,
    tokenLevel,
    isCmd,
    threadConfigProvider,
    threadConfigSupportsOverride,
    threadConfigDraftModel,
    threadConfigDraftEffort,
    threadConfigLoading,
    threadConfigSaving,
    threadConfigNotice,
    threadConfigNoticeLevel,
    threadConfigMeta,
  ]);

  const vm = useVueSetup(ComposerBar.setup, setupProps, legacyEmit);

  const dropActive = Boolean(val(vm.dropActive));
  const interruptPending = Boolean(val(vm.interruptPending));
  const threadConfigOpen = Boolean(val(vm.threadConfigOpen));
  const threadConfigDropdownStyle = val(vm.threadConfigDropdownStyle) || {};
  const threadConfigVisible = Boolean(val(vm.threadConfigVisible));
  const threadConfigEditable = Boolean(val(vm.threadConfigEditable));
  const threadConfigInherited = Boolean(val(vm.threadConfigInherited));
  const threadConfigSummaryLabel = val(vm.threadConfigSummaryLabel) || '';
  const threadConfigInheritModelLabel = val(vm.threadConfigInheritModelLabel) || '默认';
  const threadConfigInheritEffortLabel = val(vm.threadConfigInheritEffortLabel) || '默认';
  const threadConfigModelOptions = Array.isArray(val(vm.threadConfigModelOptions)) ? val(vm.threadConfigModelOptions) : [];
  const threadConfigEffortOptions = Array.isArray(val(vm.threadConfigEffortOptions)) ? val(vm.threadConfigEffortOptions) : [];
  const threadConfigInlineNotice = val(vm.threadConfigInlineNotice) || '';
  const threadConfigInlineNoticeColor = val(vm.threadConfigInlineNoticeColor) || 'var(--text-muted)';
  const isPauseMode = typeof vm.isPauseMode === 'function' ? vm.isPauseMode() : false;
  const hasReadyInput = typeof vm.hasReadyInput === 'function' ? vm.hasReadyInput() : false;
  const compactResultToneClass = typeof vm.compactResultToneClass === 'function'
    ? vm.compactResultToneClass()
    : getCompactResultToneClass(compactResultText, compactResultTone);
  const compactTitle = useMemo(() => getCompactTitle(canCompact, compacting, tokenTooltip), [canCompact, compacting, tokenTooltip]);
  const compactAriaLabel = useMemo(() => getCompactAriaLabel(canCompact, compacting, tokenTooltip), [canCompact, compacting, tokenTooltip]);
  const setThreadConfigWrapRef = useMemo(() => makeStableRefCallback(vm, 'threadConfigWrapRef'), [vm]);
  const setThreadConfigTriggerRef = useMemo(() => makeStableRefCallback(vm, 'threadConfigTriggerRef'), [vm]);

  const handleTextareaKeyDown = useCallback((e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      vm.onSend?.(e);
    } else if (e.key === 'Escape') {
      vm.onEscape?.(e);
    }
  }, [vm]);

  return (
    <div
      id="chat-input-bar"
      className={`chat-input-vue ${dropActive ? 'drop-active' : ''}`}
      data-testid="composer-bar"
      data-file-drop-target=""
      style={{ position: 'relative' }}
      onDragEnter={vm.onDragEnter}
      onDragOver={vm.onDragOver}
      onDragLeave={vm.onDragLeave}
      onDrop={vm.onDrop}
    >
      {compacting && <div className="agent-loading-bar" />}
      {dropActive && <div className="composer-drop-hint" aria-live="polite">松开即可添加附件</div>}
      
      {composerState?.attachments && composerState.attachments.length > 0 && (
        <div className="chat-attachment-list composer-attachments">
          {composerState.attachments.map((att, idx) => (
            <span
              key={`${att.path}-${idx}`}
              className={`chat-attachment-pill ${att.kind === 'image' && att.previewUrl ? 'chat-attachment-pill--image' : ''}`}
            >
              {att.kind === 'image' && att.previewUrl && (
                <img className="chat-attachment-pill__thumb" src={att.previewUrl} alt={att.name} loading="lazy" />
              )}
              <span className="attachment-kind">{att.kind === 'image' ? 'IMG' : 'FILE'}</span>
              <span className="attachment-name">{att.name}</span>
              <button className="attachment-remove" onClick={() => vm.onRemoveAttachment?.(idx)} aria-label="移除附件">×</button>
            </span>
          ))}
        </div>
      )}

      <div id="input-row" className="chat-input-row-vue" data-testid="composer-input-row">
        <button
          id="btnAttach"
          className="btn btn-secondary"
          data-testid="composer-attach-button"
          onClick={vm.onAttach}
          disabled={composerState?.attaching || disabled}
        >
          {composerState?.attaching ? '选择中...' : '附件'}
        </button>
        <textarea
          id="chatInput"
          ref={vm.setComposerInputRef}
          data-testid="composer-input"
          rows={2}
          value={composerState?.text || ''}
          placeholder="输入给 Agent 的内容，Enter 发送，Shift+Enter 换行"
          disabled={disabled}
          onChange={(e) => {
            if (composer?.state) {
              composer.state.text = e.target.value;
            }
            vm.onInput?.();
          }}
          onPaste={vm.onPaste}
          onCompositionStart={vm.onCompositionStart}
          onCompositionEnd={vm.onCompositionEnd}
          onKeyDown={handleTextareaKeyDown}
        />

        <div className="composer-top-actions" style={{ alignItems: 'flex-end' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '4px', alignItems: 'flex-end', marginLeft: 'auto' }}>
            <div style={{ display: 'flex', gap: '4px', alignItems: 'center' }}>
              <button
                type="button"
                className="composer-token-chip composer-fork-chip"
                data-testid="composer-fork-button"
                disabled={disabled || !threadId}
                title={!threadId ? '选中一个会话后才能继承新建' : '以当前会话为背景新建一个继承对话'}
                onClick={() => onOpenForkDraft?.()}
                style={{
                  cursor: 'pointer',
                  display: 'inline-flex',
                  justifyContent: 'center',
                  alignItems: 'center',
                  border: '1px solid transparent',
                  background: 'rgba(255, 255, 255, 0.04)',
                  transition: 'background 0.2s',
                  boxSizing: 'border-box',
                  width: '22px',
                  height: '22px',
                  padding: 0
                }}
              >
                <svg viewBox="0 0 24 24" aria-hidden="true" style={{ width: '11px', height: '11px', opacity: 0.7 }}>
                  <path d="M6 5v6a3 3 0 0 0 3 3h9M18 14l-3-3m3 3l-3 3" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </button>
              
              <button
                className={`composer-token-chip ${compacting ? 'loading' : ''} ${compactResultToneClass}`}
                data-testid="composer-compact-button"
                type="button"
                title={compactTitle}
                aria-label={compactAriaLabel}
                disabled={disabled || !threadId || compacting || !canCompact}
                onClick={vm.onCompact}
                style={{
                  cursor: 'pointer',
                  display: 'inline-flex',
                  justifyContent: 'center',
                  alignItems: 'center',
                  border: '1px solid transparent',
                  background: 'rgba(255, 255, 255, 0.04)',
                  transition: 'background 0.2s',
                  boxSizing: 'border-box'
                }}
              >
                <svg className="composer-compact-icon" viewBox="0 0 24 24" aria-hidden="true" style={{ width: '13px', height: '13px', opacity: 0.6, marginRight: '4px', marginTop: '-1px' }}>
                  <path
                    d="M9 5l-4 4 4 4M15 5l4 4-4 4M9 19l-4-4 4-4M15 19l4-4-4-4"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.9"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
                {compactResultText && !compacting ? (
                  <span style={{ color: 'inherit' }}>{compactResultText}</span>
                ) : compacting ? (
                  <span className="loading-shimmer">更新中…</span>
                ) : tokenInline ? (
                  <span className={`composer-token-inline ${tokenLevel && tokenLevel !== 'normal' ? `is-token-${tokenLevel}` : ''}`}>
                    {tokenInline}
                    {compactSuccessCount > 0 && (
                      <span style={{ color: 'var(--success, #4ade80)', marginLeft: '4px', fontWeight: 600 }}>{compactSuccessCount}</span>
                    )}
                  </span>
                ) : compactSuccessCount > 0 ? (
                  <span style={{ color: 'var(--success, #4ade80)', fontWeight: 600 }}>{compactSuccessCount}</span>
                ) : null}
              </button>
            </div>

            {threadConfigVisible && threadConfigEditable && (
              <div
                className="composer-thread-config-wrap"
                ref={setThreadConfigWrapRef}
                style={{ position: 'relative', display: 'flex' }}
              >
                <button
                  className={`composer-token-chip composer-thread-config-btn ${threadConfigOpen ? 'active' : ''}`}
                  type="button"
                  ref={setThreadConfigTriggerRef}
                  onClick={vm.toggleThreadConfig}
                  disabled={threadConfigLoading}
                  aria-label="线程执行配置"
                  title={threadConfigLoading ? '加载中...' : '线程执行配置'}
                  style={{
                    cursor: 'pointer',
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    border: '1px solid transparent',
                    background: 'rgba(255, 255, 255, 0.04)',
                    transition: 'background 0.2s',
                    boxSizing: 'border-box',
                    height: '22px'
                  }}
                >
                  <svg className="composer-compact-icon" viewBox="0 0 24 24" aria-hidden="true" style={{ width: '13px', height: '13px', opacity: 0.6, marginRight: '2px', marginTop: '-1px' }}>
                    <path d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
                  </svg>
                  <span>{threadConfigLoading ? '加载中...' : threadConfigSummaryLabel}</span>
                  <svg className="project-selector-chevron" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ marginLeft: '4px', opacity: 0.5, width: '10px', height: '10px', marginTop: '-1px' }}>
                    <path d="M4 6l4 4 4-4" />
                  </svg>
                </button>
                
                {threadConfigOpen && (
                  <div
                    className="project-dropdown composer-thread-config-dropdown"
                    style={threadConfigDropdownStyle}
                    onClick={(e) => e.stopPropagation()}
                  >
                    <div style={{ padding: '10px 14px', display: 'flex', flexDirection: 'column', gap: '10px' }}>
                      <label className="thread-config-field" style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                        <span style={{ fontSize: '11px', color: 'var(--text-muted)', fontWeight: 500 }}>Model</span>
                        <select
                          className="settings-stall-input"
                          data-testid="thread-config-model-select"
                          style={{ width: '100%', height: '28px', background: 'var(--card)', border: '1px solid var(--border)', borderRadius: '4px', color: 'var(--text)', fontSize: '12px', padding: '0 6px' }}
                          value={threadConfigDraftModel || ''}
                          disabled={threadConfigSaving}
                          onChange={(e) => vm.onModelSelectChange?.(e.target.value)}
                        >
                          <option value="">{threadConfigInheritModelLabel}</option>
                          {threadConfigModelOptions.map((m) => (
                            <option key={m.value} value={m.value}>{m.label}</option>
                          ))}
                        </select>
                      </label>
                      <label className="thread-config-field" style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                        <span style={{ fontSize: '11px', color: 'var(--text-muted)', fontWeight: 500 }}>Effort</span>
                        <select
                          className="settings-stall-input"
                          data-testid="thread-config-effort-select"
                          style={{ width: '100%', height: '28px', background: 'var(--card)', border: '1px solid var(--border)', borderRadius: '4px', color: 'var(--text)', fontSize: '12px', padding: '0 6px' }}
                          value={threadConfigDraftEffort || ''}
                          disabled={threadConfigSaving}
                          onChange={(e) => vm.onEffortSelectChange?.(e.target.value)}
                        >
                          <option value="">{threadConfigInheritEffortLabel}</option>
                          {threadConfigEffortOptions.map((m) => (
                            <option key={m.value} value={m.value}>{m.label}</option>
                          ))}
                        </select>
                      </label>
                    </div>
                    {!threadConfigInherited && (
                      <div style={{ padding: '6px 14px 10px', display: 'flex', justifyContent: 'center' }}>
                        <button
                          className="btn btn-ghost btn-xs"
                          data-testid="thread-config-restore-button"
                          disabled={threadConfigSaving}
                          onClick={vm.restoreThreadConfig}
                          style={{ fontSize: '11px', opacity: 0.7 }}
                        >
                          替换：继承全局
                        </button>
                      </div>
                    )}
                  </div>
                )}
                {threadConfigInlineNotice && (
                  <div
                    className="composer-thread-config-notice"
                    title={threadConfigInlineNotice}
                    style={{ fontSize: '11px', lineHeight: '1.35', minHeight: '16px', textAlign: 'right', color: threadConfigInlineNoticeColor }}
                  >
                    {threadConfigInlineNotice}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>

        <div className="composer-action-stack">
          <button
            id="btnSend"
            className={`btn btn-primary ${isPauseMode ? 'btn-stop' : ''}`}
            data-testid="composer-send-button"
            disabled={disabled || (isPauseMode && interruptPending) || (!isPauseMode && !hasReadyInput)}
            aria-label={isPauseMode ? '中断' : '发送'}
            onClick={vm.onPrimaryAction}
          >
            {isPauseMode ? (
              <span className="btn-stop-icon" aria-hidden="true" />
            ) : (
              <svg className="btn-send-icon" viewBox="0 0 24 24" aria-hidden="true">
                <path
                  d="M12 17V7M7.5 11.5L12 7l4.5 4.5"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2.2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}

ComposerBar.props = {
  composer: { type: Object, default: () => ({}) },
  disabled: { type: Boolean, default: false },
  sendDisabled: { type: Boolean, default: false },
  threadId: { type: String, default: '' },
  interruptible: { type: Boolean, default: false },
  compacting: { type: Boolean, default: false },
  canCompact: { type: Boolean, default: true },
  compactResultText: { type: String, default: '' },
  compactResultTone: { type: String, default: '' },
  compactSuccessCount: { type: Number, default: 0 },
  tokenInline: { type: String, default: '' },
  tokenTooltip: { type: String, default: '' },
  tokenLevel: { type: String, default: 'normal' },
  isCmd: { type: Boolean, default: false },
  threadConfigProvider: { type: String, default: '' },
  threadConfigSupportsOverride: { type: Boolean, default: false },
  threadConfigDraftModel: { type: String, default: '' },
  threadConfigDraftEffort: { type: String, default: '' },
  threadConfigLoading: { type: Boolean, default: false },
  threadConfigSaving: { type: Boolean, default: false },
  threadConfigNotice: { type: String, default: '' },
  threadConfigNoticeLevel: { type: String, default: 'info' },
  threadConfigMeta: { type: Object, default: () => ({ override: {}, effective: {} }) },
};

ComposerBar.emits = [
  'send', 'interrupt', 'compact',
  'update-thread-config-model', 'update-thread-config-effort', 'save-thread-config', 'restore-thread-config-inherit',
  'open-fork-draft',
];

ComposerBar.template = `
  <div data-testid="composer-bar">
    <div class="chat-input-row-vue" data-testid="composer-input-row">
      <textarea :ref="setComposerInputRef" :disabled="disabled"></textarea>
      <button @click="onCompact"></button>
      <button @click="onAttach"></button>
      <button @click="onRemoveAttachment(idx)"></button>
      <button @click="onPrimaryAction"></button>
      <div class="composer-thread-config-notice"></div>
      <button @keydown.esc.exact="onEscape"></button>
    </div>
  </div>
`;

ComposerBar.setup = function(props, { emit }) {
  const textarea = useComposerTextarea();
  const dragDrop = useComposerDragDrop(props);
  const interrupt = useComposerInterrupt(props, emit, {
    hasReadyInput,
    onSend,
  });
  const threadConfig = useComposerThreadConfig(props, emit);

  const {
    isComposing,
    syncComposerInputHeight,
    setComposerInputRef,
    onInput,
    onCompositionStart,
    onCompositionEnd,
  } = textarea;

  const { resetDropState } = dragDrop;
  const { pauseAcknowledged, resetInterruptState } = interrupt;
  const { onThreadConfigClickOutside } = threadConfig;

  const threadConfigInlineNotice = Vue.computed(() => {
    if (!threadConfig.threadConfigVisible.value || !threadConfig.threadConfigEditable.value) {
      return '';
    }
    const explicitNotice = (props.threadConfigNotice || '').toString().trim();
    return explicitNotice || '';
  });

  const threadConfigInlineNoticeColor = Vue.computed(() => {
    const hasExplicitNotice = Boolean((props.threadConfigNotice || '').toString().trim());
    if (!hasExplicitNotice) {
      return 'var(--text-muted)';
    }
    const level = (props.threadConfigNoticeLevel || 'info').toString().trim().toLowerCase();
    if (level === 'error') return 'var(--error, #f87171)';
    if (level === 'warning') return 'var(--warning, #fbbf24)';
    return 'var(--info, #60a5fa)';
  });

  function hasReadyInput() {
    if (props.disabled || props.sendDisabled) return false;
    return Boolean(val(props.composer?.canSend));
  }

  function onPaste(event) {
    logDebug('ui', 'composerBar.paste', {});
    props.composer.handlePaste(event);
  }

  function onSend(event) {
    const keyCode = Number(event?.keyCode || event?.which || 0);
    const key = (event?.key || '').toString();
    const imeLikely = event?.isComposing || isComposing.value || keyCode === 229 || key === 'Process' || key === 'Unidentified';
    if (event?.type === 'keydown' && imeLikely) {
      logDebug('ui', 'composerBar.send.blockedByComposition', {
        key_code: keyCode,
        key,
        composing: Boolean(event?.isComposing || isComposing.value),
      });
      return;
    }
    if (!hasReadyInput()) {
      logDebug('ui', 'composerBar.send.skipped.noInput', {
        trigger: event?.type || '',
      });
      return;
    }
    if (event?.type === 'keydown' && typeof event.preventDefault === 'function') {
      event.preventDefault();
    }
    pauseAcknowledged.value = false;
    logDebug('ui', 'composerBar.send.click', {
      disabled: props.disabled,
    });
    emit('send');
  }

  function onCompact() {
    if (props.disabled) return;
    if (props.compacting) return;
    if (!props.canCompact) return;
    if (!(props.threadId || '').toString().trim()) return;
    emit('compact');
  }

  function compactResultToneClass() {
    if (!props.compactResultText) return '';
    const tone = (props.compactResultTone || '').toString().trim().toLowerCase();
    if (tone === 'success') return 'is-success';
    if (tone === 'error') return 'is-error';
    return '';
  }

  function onAttach() {
    logDebug('ui', 'composerBar.attach.click', {
      disabled: props.disabled || props.composer.state.attaching,
    });
    props.composer.attachByPicker();
  }

  function onRemoveAttachment(index) {
    logDebug('ui', 'composerBar.attachment.remove', { index });
    props.composer.removeAttachment(index);
  }

  Vue.watch(
    () => props.threadId,
    (next, prev) => {
      const nextID = (next || '').toString();
      const prevID = (prev || '').toString();
      if (nextID === prevID) return;
      resetInterruptState();
      isComposing.value = false;
      resetDropState();
    },
  );

  Vue.watch(
    () => props.composer?.state?.text,
    () => {
      Vue.nextTick(() => syncComposerInputHeight());
    },
    { immediate: true },
  );

  Vue.onUpdated(() => {
    if (pauseAcknowledged.value && hasReadyInput()) {
      pauseAcknowledged.value = false;
    }
    syncComposerInputHeight();
  });

  Vue.onMounted(() => {
    dragDrop.bindNativeFileDrop();
    if (typeof window?.addEventListener === 'function') window.addEventListener('resize', syncComposerInputHeight);
    document.addEventListener('click', onThreadConfigClickOutside, true);
    Vue.nextTick(() => syncComposerInputHeight());
  });

  Vue.onBeforeUnmount(() => {
    dragDrop.unbindNativeFileDrop();
    if (typeof window?.removeEventListener === 'function') window.removeEventListener('resize', syncComposerInputHeight);
    document.removeEventListener('click', onThreadConfigClickOutside, true);
    resetInterruptState();
    resetDropState();
  });

  return {
    isComposing,
    setComposerInputRef,
    hasReadyInput,
    onPaste,
    onInput,
    onCompositionStart,
    onCompositionEnd,
    onSend,
    onCompact,
    compactResultToneClass,
    onAttach,
    onRemoveAttachment,
    threadConfigInlineNotice,
    threadConfigInlineNoticeColor,

    ...interrupt,
    ...dragDrop,
    ...threadConfig,
  };
};

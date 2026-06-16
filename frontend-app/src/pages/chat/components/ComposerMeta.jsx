import React from 'react';
import { ArrowUp, CircleStop, Mic, Plus, SlidersHorizontal } from 'lucide-react';
import { ComposerModelSelector } from './ComposerModelSelector.jsx';
import { runUIAction } from './chatUiActions.js';

function ComposerMeta({
  canInterrupt,
  canSend,
  canUseProjectActions: _canUseProjectActions,
  modelThreadId,
  projectActionBlocked,
  projectActionBlockedTitle,
  selectFiles,
  sendMessage,
  showProviderToggle: _,
  showProjectSelector: _showProjectSelector = false,
  store,
}) {
  const primaryActionLabel = canInterrupt ? '中断当前执行' : '发送消息';
  const primaryActionTitle = canInterrupt ? '中断当前执行' : undefined;
  const primaryActionClass = `send${canInterrupt ? ' send--interrupt' : ''}`;
  const primaryActionDisabled = canInterrupt ? false : !canSend;
  const onPrimaryAction = () => {
    if (canInterrupt) {
      runUIAction(() => store.interruptActiveThread?.());
      return;
    }
    if (canSend) runUIAction(() => sendMessage());
  };
  return (
    <div className="composer-meta">
      <button
        type="button"
        className="composer-icon-action composer-attach"
        aria-label="添加文件"
        title={projectActionBlocked ? projectActionBlockedTitle : '添加文件'}
        disabled={projectActionBlocked}
        onClick={() => {
          if (!projectActionBlocked) runUIAction(() => selectFiles());
        }}
      >
        <Plus size={20} />
      </button>
      <button
        type="button"
        className="composer-custom"
        aria-label="自定义配置"
        title="自定义配置待后端接入"
        disabled
      >
        <SlidersHorizontal size={17} />
        <span>自定义</span>
      </button>
      <div className="composer-actions">
        <ComposerModelSelector store={store} activeThreadId={modelThreadId} disabled={projectActionBlocked} />
        <button type="button" className="composer-voice" aria-label="语音输入" title="语音输入待后端接入" disabled>
          <Mic size={20} aria-hidden="true" />
        </button>
        <button type="button" className={primaryActionClass} aria-label={primaryActionLabel} title={primaryActionTitle} disabled={primaryActionDisabled} onClick={onPrimaryAction}>
          {canInterrupt ? <CircleStop size={18} /> : <ArrowUp size={22} />}
        </button>
      </div>
    </div>
  );
}

export { ComposerMeta };

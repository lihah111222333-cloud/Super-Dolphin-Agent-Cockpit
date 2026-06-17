import React from 'react';
import { ArrowUp, CircleStop, Plus } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { ComposerModelSelector } from './ComposerModelSelector.jsx';
import { runUIAction } from './chatUiActions.js';

function ComposerMeta({
  copy = APP_COPY.zh.chat,
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
  const primaryActionLabel = canInterrupt ? copy.interrupt : copy.sendMessage;
  const primaryActionTitle = canInterrupt ? copy.interrupt : undefined;
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
        aria-label={copy.addFile}
        title={projectActionBlocked ? projectActionBlockedTitle : copy.addFile}
        disabled={projectActionBlocked}
        onClick={() => {
          if (!projectActionBlocked) runUIAction(() => selectFiles());
        }}
      >
        <Plus size={20} />
      </button>
      <div className="composer-actions">
        <ComposerModelSelector copy={copy} store={store} activeThreadId={modelThreadId} disabled={projectActionBlocked} />
        <button type="button" className={primaryActionClass} aria-label={primaryActionLabel} title={primaryActionTitle} disabled={primaryActionDisabled} onClick={onPrimaryAction}>
          {canInterrupt ? <CircleStop size={18} /> : <ArrowUp size={22} />}
        </button>
      </div>
    </div>
  );
}

export { ComposerMeta };

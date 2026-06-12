import React from 'react';
import { ArrowUp, CircleStop, GitBranch, Paperclip } from 'lucide-react';
import { ComposerModelSelector } from './ComposerModelSelector.jsx';
import { ProjectSelector } from './ProjectSelector.jsx';
import { runUIAction } from './chatUiActions.js';

function ComposerMeta({
  canInterrupt,
  canSend,
  canUseProjectActions,
  modelThreadId,
  projectPath,
  projectActionBlocked,
  projectActionBlockedTitle,
  selectFiles,
  sendMessage,
  showProviderToggle: _,
  showProjectSelector = false,
  store,
}) {
  const canForkThread = canUseProjectActions && Boolean(store.hasActiveThreadActions?.());
  const forkBlockedTitle = projectActionBlocked ? projectActionBlockedTitle : '当前没有可继承的会话';
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
      {showProjectSelector ? <ProjectSelector store={store} projectPath={projectPath} /> : null}
      <button
        type="button"
        className="composer-attach"
        aria-label="添加文件"
        title={projectActionBlocked ? projectActionBlockedTitle : '添加文件'}
        disabled={projectActionBlocked}
        onClick={() => {
          if (!projectActionBlocked) runUIAction(() => selectFiles());
        }}
      >
        <Paperclip size={18} />
        <span>添加附件</span>
      </button>
      <button
        type="button"
        className="composer-attach composer-fork"
        aria-label="继承当前对话"
        title={canForkThread ? '继承当前对话' : forkBlockedTitle}
        disabled={!canForkThread}
        onClick={() => {
          if (canForkThread) runUIAction(() => store.openForkDraft?.());
        }}
      >
        <GitBranch size={16} />
      </button>
      <div className="composer-actions">
        <ComposerModelSelector store={store} activeThreadId={modelThreadId} disabled={projectActionBlocked} />
        <button type="button" className={primaryActionClass} aria-label={primaryActionLabel} title={primaryActionTitle} disabled={primaryActionDisabled} onClick={onPrimaryAction}>
          {canInterrupt ? <CircleStop size={18} /> : <ArrowUp size={22} />}
        </button>
      </div>
    </div>
  );
}

export { ComposerMeta };

import { useRef, useState } from 'react';
import { Button as AriaButton, Menu, MenuItem, MenuTrigger, Popover } from 'react-aria-components';
import { CheckCircle2, CircleStop, Copy, GitBranch, MoreHorizontal, PanelRight, PanelTopOpen, RefreshCw } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { activeThreadForStore, displayThreadName } from '../adapters/threadStateAdapter.js';
import { ProjectSelector } from './ProjectSelector.jsx';
import { runUIAction } from '../model/chatUiActions.js';
import { chatHeaderFeedbackForStore } from '../model/chatHeaderModel.js';

function restoreTriggerFocus(ref) {
  const focus = () => ref.current?.focus?.();
  if (typeof globalThis.queueMicrotask === 'function') {
    globalThis.queueMicrotask(focus);
    return;
  }
  focus();
}

function ChatPageHeader({ copy = APP_COPY.zh.chat, store, projectPath, rightPanelOpen, setRightPanelOpen }) {
  const [actionsOpen, setActionsOpen] = useState(false);
  const actionsButtonRef = useRef(null);
  const canUseThreadActions = Boolean(store?.hasActiveThreadActions?.());
  const canInterruptThread = Boolean(store?.hasInterruptibleThreadAction?.());
  const canForceCompleteThread = typeof store?.hasForceCompleteThreadAction === 'function'
    ? Boolean(store.hasForceCompleteThreadAction())
    : canInterruptThread;
  const feedback = chatHeaderFeedbackForStore(store);
  const recoveryRequesting = Boolean(feedback?.recoveryRequesting);
  const activeThread = activeThreadForStore(store);
  const title = store?.activeThreadId && activeThread ? displayThreadName(activeThread) : '聊天页面';
  const setActionsMenuOpen = (isOpen) => {
    setActionsOpen(isOpen);
    if (!isOpen) restoreTriggerFocus(actionsButtonRef);
  };
  return (
    <header className="chat-page-header">
      <div className="chat-page-title">
        <h1>{title}</h1>
        <MenuTrigger isOpen={actionsOpen} onOpenChange={setActionsMenuOpen}>
          <AriaButton
            ref={actionsButtonRef}
            type="button"
            className={`chat-more-button ${actionsOpen ? 'active' : ''}`}
            aria-label="聊天操作"
            title="聊天操作"
            aria-expanded={actionsOpen}
          >
            <MoreHorizontal size={24} aria-hidden="true" />
          </AriaButton>
          <ChatActionsMenu
            copy={copy}
            canForceCompleteThread={canForceCompleteThread}
            canInterruptThread={canInterruptThread}
            canUseThreadActions={canUseThreadActions}
            recoveryRequesting={recoveryRequesting}
            projectPath={projectPath}
            rightPanelOpen={rightPanelOpen}
            setRightPanelOpen={setRightPanelOpen}
            store={store}
          />
        </MenuTrigger>
      </div>
      <div className="chat-header-tools" aria-label="聊天视图工具">
        <button
          type="button"
          className="chat-header-tool"
          aria-label="布局视图"
          title={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
          aria-pressed={rightPanelOpen}
          onClick={() => setRightPanelOpen?.((prev) => !prev)}
        >
          <PanelRight size={22} aria-hidden="true" />
        </button>
      </div>
      <button
        type="button"
        className="chat-sidepanel-shortcut"
        aria-label={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        title={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        aria-pressed={rightPanelOpen}
        onClick={() => setRightPanelOpen?.((prev) => !prev)}
      />
      <div className="chat-legacy-actions" aria-label="聊天操作">
        <button
          type="button"
          className="icon-btn"
          aria-label="新窗口（独立进程）"
          title="新窗口（独立进程）"
          onClick={() => runUIAction('workspace.window.open', () => store.openNewWindow?.())}
        >
          <PanelTopOpen size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canUseThreadActions ? '复制当前线程' : '复制当前线程（不可用）'}
          title={canUseThreadActions ? '复制当前线程' : '请先选择会话'}
          disabled={!canUseThreadActions}
          onClick={() => runUIAction('thread.info.copy', () => store.copyActiveThreadInfo?.())}
        >
          <Copy size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canInterruptThread ? '停止' : '停止（不可用）'}
          title={canInterruptThread ? '中断当前执行' : '无运行中任务'}
          disabled={!canInterruptThread}
          onClick={() => runUIAction('thread.interrupt', () => store.interruptActiveThread?.())}
        >
          <CircleStop size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canForceCompleteThread ? '强制完成' : '强制完成（不可用）'}
          title={canForceCompleteThread ? '强制完成当前执行' : '无运行中任务'}
          disabled={!canForceCompleteThread}
          onClick={() => runUIAction('thread.force-complete', () => store.forceCompleteActiveThread?.())}
        >
          <CheckCircle2 size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={recoveryRequesting ? '正在恢复' : (canUseThreadActions ? '进程恢复' : '请先选择会话')}
          title={recoveryRequesting ? '恢复请求处理中' : (canUseThreadActions ? '手动杀进程并恢复连接' : '请先选择会话')}
          disabled={!canUseThreadActions || recoveryRequesting}
          onClick={() => runUIAction('thread.recover', () => store.recoverActiveThread?.())}
        >
          <RefreshCw size={14} />
        </button>
      </div>
      {feedback?.bootstrapRecovery ? (
        <div className="chat-header-feedback">
          <output className={`action-feedback ${feedback.tone || 'info'}`} data-testid="chat-action-feedback">
            {feedback.message}
          </output>
          <button
            type="button"
            className="btn secondary chat-bootstrap-retry"
            aria-label={feedback.retrying ? '正在重新连接后端' : '重新连接后端'}
            disabled={feedback.retrying}
            onClick={() => runUIAction('app.bootstrap', () => store.bootstrap())}
          >
            {feedback.retrying ? '连接中…' : '重新连接'}
          </button>
        </div>
      ) : null}
    </header>
  );
}

function ChatActionsMenu({
  copy = APP_COPY.zh.chat,
  canForceCompleteThread,
  canInterruptThread,
  canUseThreadActions,
  recoveryRequesting,
  projectPath,
  rightPanelOpen,
  setRightPanelOpen,
  store,
}) {
  const runAction = (key) => {
    switch (String(key)) {
      case 'new-window':
        runUIAction('workspace.window.open', () => store.openNewWindow?.());
        break;
      case 'copy-thread':
        runUIAction('thread.info.copy', () => store.copyActiveThreadInfo?.());
        break;
      case 'fork-thread':
        runUIAction('thread.fork.open', () => store.openForkDraft?.());
        break;
      case 'interrupt-thread':
        runUIAction('thread.interrupt', () => store.interruptActiveThread?.());
        break;
      case 'force-complete-thread':
        runUIAction('thread.force-complete', () => store.forceCompleteActiveThread?.());
        break;
      case 'recover-thread':
        runUIAction('thread.recover', () => store.recoverActiveThread?.());
        break;
      case 'toggle-runtime-panel':
        runUIAction('runtime.panel.toggle', () => setRightPanelOpen?.((prev) => !prev));
        break;
      default:
        throw new Error(`Unknown chat header action: ${String(key)}`);
    }
  };
  return (
    <Popover className="chat-actions-menu" data-testid="chat-actions-menu" placement="bottom start">
      {store?.activeThreadId ? (
        <div className="chat-actions-project">
          <ProjectSelector copy={copy} store={store} projectPath={projectPath} />
        </div>
      ) : null}
      <Menu aria-label="聊天操作" className="chat-actions-menu-list" onAction={runAction}>
        <ChatActionMenuItem
          id="new-window"
        icon={PanelTopOpen}
        label="新窗口（独立进程）"
      />
        <ChatActionMenuItem
          id="copy-thread"
        icon={Copy}
        label={canUseThreadActions ? '复制当前线程' : '复制当前线程（不可用）'}
        disabled={!canUseThreadActions}
      />
        <ChatActionMenuItem
          id="fork-thread"
        icon={GitBranch}
        label={canUseThreadActions ? '继承当前对话' : '继承当前对话（不可用）'}
        disabled={!canUseThreadActions}
      />
        <ChatActionMenuItem
          id="interrupt-thread"
        icon={CircleStop}
        label={canInterruptThread ? '停止' : '停止（不可用）'}
        disabled={!canInterruptThread}
      />
        <ChatActionMenuItem
          id="force-complete-thread"
        icon={CheckCircle2}
        label={canForceCompleteThread ? '强制完成' : '强制完成（不可用）'}
        disabled={!canForceCompleteThread}
      />
        <ChatActionMenuItem
          id="recover-thread"
        icon={RefreshCw}
        label={recoveryRequesting ? '正在恢复' : (canUseThreadActions ? '进程恢复' : '请先选择会话')}
        disabled={!canUseThreadActions || recoveryRequesting}
      />
        <ChatActionMenuItem
          id="toggle-runtime-panel"
        icon={PanelTopOpen}
        label={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
      />
      </Menu>
    </Popover>
  );
}

function ChatActionMenuItem({ disabled = false, icon: Icon, id, label }) {
  return (
    <MenuItem id={id} className="chat-action-menu-item" isDisabled={disabled} textValue={label}>
      <Icon size={16} aria-hidden="true" />
      <span>{label}</span>
    </MenuItem>
  );
}

export { ChatPageHeader };

import { useCallback, useEffect, useRef, useState } from 'react';
import { CheckCircle2, CircleStop, Copy, GitBranch, MoreHorizontal, PanelRight, PanelTopOpen, RefreshCw } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { activeThreadForStore, displayThreadName } from '../adapters/threadStateAdapter.js';
import { ProjectSelector } from './ProjectSelector.jsx';
import { runUIAction } from './chatUiActions.js';
import { chatHeaderFeedbackForStore } from './chatHeaderModel.js';

function ChatPageHeader({ copy = APP_COPY.zh.chat, store, projectPath, rightPanelOpen, setRightPanelOpen }) {
  const [actionsOpen, setActionsOpen] = useState(false);
  const actionsButtonRef = useRef(null);
  const actionsMenuRef = useRef(null);
  const canUseThreadActions = Boolean(store?.hasActiveThreadActions?.());
  const canInterruptThread = Boolean(store?.hasInterruptibleThreadAction?.());
  const canForceCompleteThread = typeof store?.hasForceCompleteThreadAction === 'function'
    ? Boolean(store.hasForceCompleteThreadAction())
    : canInterruptThread;
  const feedback = chatHeaderFeedbackForStore(store);
  const activeThread = activeThreadForStore(store);
  const title = store?.activeThreadId && activeThread ? displayThreadName(activeThread) : '聊天页面';
  useEffect(() => {
    if (!actionsOpen) return undefined;
    const closeOnPointerDown = (event) => {
      if (actionsMenuRef.current?.contains(event.target)) return;
      if (actionsButtonRef.current?.contains(event.target)) return;
      setActionsOpen(false);
    };
    const closeOnEscape = (event) => {
      if (event.key !== 'Escape') return;
      setActionsOpen(false);
      actionsButtonRef.current?.focus?.();
    };
    window.addEventListener('pointerdown', closeOnPointerDown);
    window.addEventListener('keydown', closeOnEscape);
    return () => {
      window.removeEventListener('pointerdown', closeOnPointerDown);
      window.removeEventListener('keydown', closeOnEscape);
    };
  }, [actionsOpen]);
  const runMenuAction = useCallback((action, { close = true } = {}) => {
    if (close) setActionsOpen(false);
    runUIAction(action);
  }, []);
  return (
    <header className="chat-page-header">
      <div className="chat-page-title">
        <h1>{title}</h1>
        <button
          ref={actionsButtonRef}
          type="button"
          className={`chat-more-button ${actionsOpen ? 'active' : ''}`}
          aria-label="聊天操作"
          title="聊天操作"
          aria-haspopup="menu"
          aria-expanded={actionsOpen}
          onClick={() => setActionsOpen((current) => !current)}
        >
          <MoreHorizontal size={24} aria-hidden="true" />
        </button>
      </div>
      {actionsOpen ? (
        <ChatActionsMenu
          copy={copy}
          canForceCompleteThread={canForceCompleteThread}
          canInterruptThread={canInterruptThread}
          canUseThreadActions={canUseThreadActions}
          menuRef={actionsMenuRef}
          projectPath={projectPath}
          rightPanelOpen={rightPanelOpen}
          runMenuAction={runMenuAction}
          setRightPanelOpen={setRightPanelOpen}
          store={store}
        />
      ) : null}
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
          onClick={() => runUIAction(() => store.openNewWindow?.())}
        >
          <PanelTopOpen size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canUseThreadActions ? '复制当前线程' : '复制当前线程（不可用）'}
          title={canUseThreadActions ? '复制当前线程' : '请先选择会话'}
          disabled={!canUseThreadActions}
          onClick={() => runUIAction(() => store.copyActiveThreadInfo?.())}
        >
          <Copy size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canInterruptThread ? '停止' : '停止（不可用）'}
          title={canInterruptThread ? '中断当前执行' : '无运行中任务'}
          disabled={!canInterruptThread}
          onClick={() => runUIAction(() => store.interruptActiveThread?.())}
        >
          <CircleStop size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canForceCompleteThread ? '强制完成' : '强制完成（不可用）'}
          title={canForceCompleteThread ? '强制完成当前执行' : '无运行中任务'}
          disabled={!canForceCompleteThread}
          onClick={() => runUIAction(() => store.forceCompleteActiveThread?.())}
        >
          <CheckCircle2 size={14} />
        </button>
        <button
          type="button"
          className="icon-btn"
          aria-label={canUseThreadActions ? '进程恢复' : '请先选择会话'}
          title={canUseThreadActions ? '手动杀进程并恢复连接' : '请先选择会话'}
          disabled={!canUseThreadActions}
          onClick={() => runUIAction(() => store.recoverActiveThread?.())}
        >
          <RefreshCw size={14} />
        </button>
      </div>
      {feedback?.message ? (
        <output className={`action-feedback ${feedback.tone || 'info'}`} data-testid="chat-action-feedback">
          {feedback.message}
        </output>
      ) : null}
    </header>
  );
}

function ChatActionsMenu({
  copy = APP_COPY.zh.chat,
  canForceCompleteThread,
  canInterruptThread,
  canUseThreadActions,
  menuRef,
  projectPath,
  rightPanelOpen,
  runMenuAction,
  setRightPanelOpen,
  store,
}) {
  const toggleRuntimePanel = () => setRightPanelOpen?.((prev) => !prev);
  return (
    <div ref={menuRef} className="chat-actions-menu" data-testid="chat-actions-menu" role="menu" aria-label="聊天操作">
      {store?.activeThreadId ? (
        <div className="chat-actions-project">
          <ProjectSelector copy={copy} store={store} projectPath={projectPath} />
        </div>
      ) : null}
      <ChatActionMenuButton
        icon={PanelTopOpen}
        label="新窗口（独立进程）"
        onClick={() => runMenuAction(() => store.openNewWindow?.())}
      />
      <ChatActionMenuButton
        icon={Copy}
        label={canUseThreadActions ? '复制当前线程' : '复制当前线程（不可用）'}
        disabled={!canUseThreadActions}
        onClick={() => runMenuAction(() => store.copyActiveThreadInfo?.())}
      />
      <ChatActionMenuButton
        icon={GitBranch}
        label={canUseThreadActions ? '继承当前对话' : '继承当前对话（不可用）'}
        disabled={!canUseThreadActions}
        onClick={() => runMenuAction(() => store.openForkDraft?.())}
      />
      <ChatActionMenuButton
        icon={CircleStop}
        label={canInterruptThread ? '停止' : '停止（不可用）'}
        disabled={!canInterruptThread}
        onClick={() => runMenuAction(() => store.interruptActiveThread?.())}
      />
      <ChatActionMenuButton
        icon={CheckCircle2}
        label={canForceCompleteThread ? '强制完成' : '强制完成（不可用）'}
        disabled={!canForceCompleteThread}
        onClick={() => runMenuAction(() => store.forceCompleteActiveThread?.())}
      />
      <ChatActionMenuButton
        icon={RefreshCw}
        label={canUseThreadActions ? '进程恢复' : '请先选择会话'}
        disabled={!canUseThreadActions}
        onClick={() => runMenuAction(() => store.recoverActiveThread?.())}
      />
      <ChatActionMenuButton
        icon={PanelTopOpen}
        label={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
        onClick={() => runMenuAction(toggleRuntimePanel)}
      />
    </div>
  );
}

function ChatActionMenuButton({ disabled = false, icon: Icon, label, onClick }) {
  return (
    <button type="button" className="chat-action-menu-item" disabled={disabled} onClick={onClick}>
      <Icon size={16} aria-hidden="true" />
      <span>{label}</span>
    </button>
  );
}

export { ChatPageHeader };

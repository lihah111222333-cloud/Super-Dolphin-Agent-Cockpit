import React, { useMemo } from 'react';
import { ProjectSelect } from '../ProjectSelect.jsx';

export function ChatToolbar({
  isCmd = false,
  activeStatus = '',
  displayStatusText = '',
  activeStatusMeta = '',
  useClaudeProvider = false,
  providerPreferenceReady = true,
  providerPreferenceError = '',
  selectedThreadId = '',
  threadConfigProvider = '',
  threadConfigSupportsOverride = false,
  threadConfigDraftModel = '',
  threadConfigDraftEffort = '',
  threadConfigLoading = false,
  threadConfigSaving = false,
  threadConfigNotice = '',
  threadConfigNoticeLevel = 'info',
  threadConfigMeta = { override: {}, effective: {} },

  canInterrupt = false,
  recoveringSelected = false,
  copyButtonLabel = '',
  projectOptions = [],
  activeProject = '.',
  layoutMode = '',
  cmdCardCols = 3,
  windowCwd = '',
  cwdDisplay = '',

  onUpdateProject,
  onAddProject,
  onRemoveProject,
  onSetCmdLayout,
  onSetCmdCardCols,
  onCopyThreadInfo,
  onStopSelected,
  onToggleProviderMode,
  onLaunchOne,
  onRecoverSelected,
}) {
  const providerToggleLabel = useMemo(() => {
    if (!providerPreferenceReady) return 'Provider';
    return useClaudeProvider ? 'Claude' : 'Codex';
  }, [providerPreferenceReady, useClaudeProvider]);

  const launchAgentLabel = '新对话';
  const launchAgentTitle = '新对话：发送第一条消息时才会创建会话';

  return (
    <div className="chat-toolbar unified-toolbar" style={{ position: 'relative' }} data-testid="chat-toolbar">
      <ProjectSelect
        modelValue={activeProject}
        options={projectOptions}
        onUpdateModelValue={onUpdateProject}
        onAddProject={onAddProject}
        onRemoveProject={onRemoveProject}
      />

      {isCmd && (
        <div className="layout-switch">
          <button 
            className={`btn btn-ghost btn-xs ${layoutMode === 'overview' ? 'active' : ''}`} 
            onClick={() => onSetCmdLayout && onSetCmdLayout('overview')}
          >
            A 紧凑
          </button>
          <button 
            className={`btn btn-ghost btn-xs ${layoutMode === 'chat' ? 'active' : ''}`} 
            onClick={() => onSetCmdLayout && onSetCmdLayout('chat')}
          >
            B 对话
          </button>
          <button 
            className={`btn btn-ghost btn-xs ${layoutMode === 'mix' ? 'active' : ''}`} 
            onClick={() => onSetCmdLayout && onSetCmdLayout('mix')}
          >
            C 混合
          </button>
        </div>
      )}

      {isCmd && (
        <div className="layout-switch">
          <button 
            className={`btn btn-ghost btn-xs ${cmdCardCols === 2 ? 'active' : ''}`} 
            onClick={() => onSetCmdCardCols && onSetCmdCardCols(2)}
          >
            2列
          </button>
          <button 
            className={`btn btn-ghost btn-xs ${cmdCardCols === 3 ? 'active' : ''}`} 
            onClick={() => onSetCmdCardCols && onSetCmdCardCols(3)}
          >
            3列
          </button>
        </div>
      )}

      {!isCmd && selectedThreadId && (
        <button
          className="btn btn-ghost btn-xs chat-toolbar-icon-btn"
          aria-label={copyButtonLabel}
          title={copyButtonLabel}
          onClick={onCopyThreadInfo}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <rect x="9" y="7" width="10" height="13" rx="2.2"></rect>
            <path d="M15 7V5.8A1.8 1.8 0 0 0 13.2 4H6.8A1.8 1.8 0 0 0 5 5.8V16.2A1.8 1.8 0 0 0 6.8 18H9"></path>
            <path d="M12 11.5h4"></path>
            <path d="M12 15h4"></path>
          </svg>
        </button>
      )}

      {!isCmd && selectedThreadId && (
        <button
          className="btn btn-ghost btn-xs chat-toolbar-icon-btn"
          disabled={!canInterrupt}
          aria-label={canInterrupt ? '停止' : '当前没有可中断任务'}
          title={canInterrupt ? '中断当前执行' : '当前没有可中断任务'}
          onClick={onStopSelected}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="12" cy="12" r="8"></circle>
            <rect x="9" y="9" width="6" height="6" rx="1.2"></rect>
          </svg>
        </button>
      )}

      <label
        className={`provider-toggle ${useClaudeProvider ? 'active' : ''}`}
        data-testid="provider-toggle"
        title="切换 Claude / Codex provider"
      >
        <input
          type="checkbox"
          checked={useClaudeProvider}
          disabled={!providerPreferenceReady}
          onChange={onToggleProviderMode}
          className="provider-toggle-input"
        />
        <span className="provider-toggle-track">
          <span className="provider-toggle-thumb"></span>
        </span>
        <span className="provider-toggle-label">{providerToggleLabel}</span>
      </label>

      <button
        className="btn launch-agent-icon-btn"
        data-testid="launch-agent-button"
        aria-label={launchAgentLabel}
        title={launchAgentTitle}
        onClick={onLaunchOne}
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M4 20h4l10-10a2.2 2.2 0 0 0-3.1-3.1L4.9 16.8 4 20z"></path>
          <path d="M13.8 7.2l3 3"></path>
        </svg>
      </button>

      {!isCmd && (
        <button
          className="btn btn-ghost btn-xs btn-warning chat-toolbar-icon-btn"
          data-testid="recover-agent-button"
          disabled={recoveringSelected || !selectedThreadId}
          aria-label={recoveringSelected ? '恢复中' : (selectedThreadId ? '进程恢复' : '请先选择会话')}
          title={recoveringSelected ? '进程恢复中…' : (selectedThreadId ? '手动杀进程并恢复连接' : '请先选择会话')}
          onClick={onRecoverSelected}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M20 12a8 8 0 1 1-2.3-5.6"></path>
            <path d="M20 4v5h-5"></path>
            <path d="M12 9v6"></path>
            <path d="M9 12h6"></path>
          </svg>
        </button>
      )}

      {/* flex spacer */}
      <div className="toolbar-filler" aria-hidden="true"></div>

      {/* CWD 图标徽章 */}
      {windowCwd && (
        <div
          className="cwd-badge"
          title={cwdDisplay || windowCwd}
          aria-label="当前工作目录"
        >
          <svg className="cwd-badge-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"></path>
          </svg>
          <span className="cwd-badge-text">
            {windowCwd.split('/').filter(Boolean).pop() || windowCwd}
          </span>
        </div>
      )}
    </div>
  );
}

ChatToolbar.components = { ProjectSelect };
ChatToolbar.props = {
  isCmd: { default: false },
  activeStatus: { default: '' },
  displayStatusText: { default: '' },
  activeStatusMeta: { default: '' },
  useClaudeProvider: { default: false },
  providerPreferenceReady: { default: true },
  providerPreferenceError: { default: '' },
  selectedThreadId: { default: '' },
  threadConfigProvider: { default: '' },
  threadConfigSupportsOverride: { default: false },
  threadConfigDraftModel: { default: '' },
  threadConfigDraftEffort: { default: '' },
  threadConfigLoading: { default: false },
  threadConfigSaving: { default: false },
  threadConfigNotice: { default: '' },
  threadConfigNoticeLevel: { default: 'info' },
  threadConfigMeta: { default: () => ({ override: {}, effective: {} }) },
  canInterrupt: { default: false },
  recoveringSelected: { default: false },
  copyButtonLabel: { default: '' },
  projectOptions: { default: () => [] },
  activeProject: { default: '.' },
  layoutMode: { default: '' },
  cmdCardCols: { default: 3 },
  windowCwd: { default: '' },
  cwdDisplay: { default: '' },
};
ChatToolbar.emits = [
  'update-project',
  'add-project',
  'remove-project',
  'set-cmd-layout',
  'set-cmd-card-cols',
  'copy-thread-info',
  'stop-selected',
  'toggle-provider-mode',
  'launch-one',
  'recover-selected',
  'update-thread-config-model',
  'update-thread-config-effort',
  'save-thread-config',
  'restore-thread-config-inherit',
];
ChatToolbar.template = `
  <div data-testid="chat-toolbar">
    <div data-testid="provider-toggle">
      <input class="provider-toggle-input" :disabled="!providerPreferenceReady" />
    </div>
    <button data-testid="launch-agent-button"></button>
    <button data-testid="recover-agent-button"></button>
    <ProjectSelect />
  </div>
`;

export default ChatToolbar;

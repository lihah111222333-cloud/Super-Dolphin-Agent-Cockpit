// @ts-nocheck
import { describe, expect, it } from 'vitest';

import { ChatToolbar } from './components/unified-chat/ChatToolbar.js';
import { ThreadRailSidePanel } from './components/unified-chat/ThreadRailSidePanel.js';
import { CmdCardGrid } from './components/unified-chat/CmdCardGrid.js';
import { CmdOverviewPanel } from './components/unified-chat/CmdOverviewPanel.js';
import { WorkspaceChatPanel } from './components/unified-chat/WorkspaceChatPanel.js';
import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

function expectTemplateContainsAll(template, markers) {
  for (const marker of markers) {
    expect(template).toContain(marker);
  }
}

describe('UnifiedChatPage template contract', () => {
  it('keeps the root anchors stable after component extraction', () => {
    const template = (UnifiedChatPage.template || '').toString();

    expectTemplateContainsAll(template, [
      'data-testid="chat-page"',
      '<ChatToolbar',
      '<ThreadRailSidePanel',
      '<CmdCardGrid',
      '<CmdOverviewPanel',
      '<WorkspaceChatPanel',
      ':thread-config-notice="threadConfigUi.notice"',
      ':thread-config-notice-level="threadConfigUi.noticeLevel"',
      'data-testid="chat-send-failure-notice"',
      'class="thread-rail-resizer"',
      'class="workspace-bottom-row"',
      ':send-disabled="activeThreadSendBlocked"',
    ]);
    expect(template).not.toContain(':disabled="!selectedThreadId && !providerPreferenceReady"');
    const removedManualTaskEvent = '@' + ['promote', 'task'].join('-');
    const removedManualTaskErrorProp = ':' + ['promote', 'task', 'error'].join('-');
    expect(template).not.toContain(removedManualTaskEvent);
    expect(template).not.toContain(':promoting-task');
    expect(template).not.toContain(removedManualTaskErrorProp);
    expect(template).not.toContain('task-handoff-strip');
    expect(template).not.toContain('taskHandoffVisible');
    expect(template).not.toContain('任务模式');
    expect(template).not.toContain('任务接力摘要');
  });

  it('keeps the main layout spine ordering stable', () => {
    const template = (UnifiedChatPage.template || '').toString();

    const chatToolbar = template.indexOf('<ChatToolbar');
    const unifiedMain = template.indexOf('<div class="unified-main">');
    const threadRail = template.indexOf('<ThreadRailSidePanel');
    const threadRailResizer = template.indexOf('class="thread-rail-resizer"');
    const unifiedCenter = template.indexOf('<div class="unified-center">');
    const workspaceArea = template.indexOf('<div v-if="showWorkspace" class="workspace-area">');
    const agentWorkspace = template.indexOf('<div ref="workspaceRef" id="agent-workspace" class="chat-workspace with-diff">');
    const workspaceChatPanel = template.indexOf('<WorkspaceChatPanel');
    const panelResizer = template.indexOf('<div class="panel-resizer"');
    const workspaceRightCol = template.indexOf('<div class="workspace-right-col"');

    expect(chatToolbar).toBeGreaterThan(-1);
    expect(unifiedMain).toBeGreaterThan(chatToolbar);
    expect(threadRail).toBeGreaterThan(unifiedMain);
    expect(threadRailResizer).toBeGreaterThan(threadRail);
    expect(unifiedCenter).toBeGreaterThan(threadRailResizer);
    expect(workspaceArea).toBeGreaterThan(unifiedCenter);
    expect(agentWorkspace).toBeGreaterThan(workspaceArea);
    expect(workspaceChatPanel).toBeGreaterThan(agentWorkspace);
    expect(panelResizer).toBeGreaterThan(workspaceChatPanel);
    expect(workspaceRightCol).toBeGreaterThan(panelResizer);

    expectTemplateContainsAll(template, [
      'aria-label="调整会话列表宽度"',
      '@mousedown="onThreadRailResizeStart"',
      'id="agent-workspace" class="chat-workspace with-diff"',
    ]);
  });

  it('keeps ChatToolbar component anchors and emit contract stable', () => {
    const template = (ChatToolbar.template || '').toString();

    expect(ChatToolbar.name).toBe('ChatToolbar');
    expect(ChatToolbar.components).toHaveProperty('ProjectSelect');
    expect(Object.keys(ChatToolbar.props)).toEqual(expect.arrayContaining([
      'threadConfigProvider',
      'threadConfigSupportsOverride',
      'threadConfigDraftModel',
      'threadConfigDraftEffort',
      'threadConfigLoading',
      'threadConfigSaving',
      'threadConfigNotice',
      'threadConfigNoticeLevel',
      'threadConfigMeta',
      'providerPreferenceReady',
    ]));
    expect(ChatToolbar.emits).toEqual([
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
    ]);
    expectTemplateContainsAll(template, [
      'data-testid="chat-toolbar"',
      'data-testid="provider-toggle"',
      'data-testid="launch-agent-button"',
      'class="provider-toggle-input"',
      'data-testid="recover-agent-button"',
      '<ProjectSelect',
    ]);
    expect(template).not.toContain(':disabled="!providerPreferenceReady"');
  });

  it('keeps ThreadRailSidePanel anchors and rename emit contract stable', () => {
    const template = (ThreadRailSidePanel.template || '').toString();

    expect(ThreadRailSidePanel.name).toBe('ThreadRailSidePanel');
    expect(ThreadRailSidePanel.emits).toEqual([
      'open-new-window',
      'toggle-archived-thread-list',
      'select-thread',
      'toggle-thread-pin',
      'toggle-thread-archive',
      'begin-inline-rename',
      'submit-inline-rename',
      'handle-inline-rename-enter',
      'cancel-inline-rename',
      'handle-inline-rename-blur',
      'update-editing-alias',
      'delete-stale-threads',
    ]);
    expectTemplateContainsAll(template, [
      'data-testid="thread-rail"',
      'data-testid="new-window-btn"',
      'data-testid="thread-archive-toggle"',
      'data-testid="thread-empty-state"',
      'data-testid="thread-list"',
      'class="thread-rail-alias-input"',
      'data-rename-save-button-for',
    ]);
  });

  it('keeps CmdCardGrid and CmdOverviewPanel contracts stable', () => {
    const gridTemplate = (CmdCardGrid.template || '').toString();
    const overviewTemplate = (CmdOverviewPanel.template || '').toString();

    expect(CmdCardGrid.name).toBe('CmdCardGrid');
    expect(CmdCardGrid.emits).toEqual(['select-thread', 'load-card-history', 'rename-card', 'stop-card']);
    expectTemplateContainsAll(gridTemplate, [
      'class="cmd-card-grid"',
      'class="cmd-thread-card"',
      'class="cmd-thread-actions"',
      'class="cmd-thread-diff"',
    ]);

    expect(CmdOverviewPanel.name).toBe('CmdOverviewPanel');
    expect(CmdOverviewPanel.emits).toEqual(['select-thread']);
    expectTemplateContainsAll(overviewTemplate, [
      'class="agent-overview-panel"',
      'class="overview-metrics"',
      'class="overview-recent"',
      'class="recent-chip"',
    ]);
  });

  it('keeps WorkspaceChatPanel anchors and emit contract stable', () => {
    const template = (WorkspaceChatPanel.template || '').toString();

    expect(WorkspaceChatPanel.name).toBe('WorkspaceChatPanel');
    expect(WorkspaceChatPanel.components).toHaveProperty('ChatTimeline');
    expect(WorkspaceChatPanel.emits).toEqual(['dismiss-pinned-plan', 'file-ref-click', 'citation-click', 'scroll-to-bottom', 'scroll-to-top']);
    expectTemplateContainsAll(template, [
      'id="chat-panel"',
      'data-testid="chat-empty-state"',
      '<ChatTimeline',
      ':empty-text="emptyText"',
    ]);

  });
});

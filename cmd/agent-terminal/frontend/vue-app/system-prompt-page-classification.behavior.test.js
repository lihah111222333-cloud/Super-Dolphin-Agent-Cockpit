// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  copyTextToClipboard: vi.fn(),
  onFilesDropped: vi.fn(() => () => {}),
  readDroppedTextFiles: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  copyTextToClipboard: apiMock.copyTextToClipboard,
  onFilesDropped: apiMock.onFilesDropped,
  readDroppedTextFiles: apiMock.readDroppedTextFiles,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { SystemPromptPage } from './pages/SystemPromptPage.js';
import { PROMPT_ASSET_TABS, PROMPT_SCOPE_FILTERS, PROMPT_STATUS_FILTERS } from './pages/SystemPromptPage.helpers.js';

function createPage() {
  const props = {
    projectStore: { state: { active: '/test-repo' } },
    threadStore: null,
    windowCwd: '/fallback-cwd',
  };
  return { props, vm: SystemPromptPage.setup(props) };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.copyTextToClipboard.mockReset();
  apiMock.onFilesDropped.mockReset().mockReturnValue(() => {});
  apiMock.readDroppedTextFiles.mockReset();
});

describe('SystemPromptPage asset classification', () => {
  it('template exposes asset tabs instead of occupation role categories', () => {
    expect(SystemPromptPage.template).toContain('AI 能力与资料');
    expect(SystemPromptPage.template).toContain('data-testid="sp-asset-tabs"');
    expect(PROMPT_ASSET_TABS.map(tab => tab.label)).toEqual(['全部', '专家能力', '参考资料', '默认规则', '待确认']);
    expect(SystemPromptPage.template).toContain('data-testid="sp-scope-filter"');
    expect(PROMPT_SCOPE_FILTERS.map(item => item.label)).toEqual(['全部范围', '这个项目', '全局可用']);
    expect(SystemPromptPage.template).toContain('data-testid="sp-status-filter"');
    expect(PROMPT_STATUS_FILTERS.map(item => item.label)).toEqual(['全部状态', '启用中', '已停用']);
    expect(SystemPromptPage.template).toContain('添加给 AI 的内容');
    expect(SystemPromptPage.template).not.toContain('System 提示词管理');
    expect(SystemPromptPage.template).not.toContain('新建角色');
    expect(SystemPromptPage.template).not.toContain('角色');
  });

  it('ordinary editor header uses scope instead of legacy role categories', () => {
    expect(SystemPromptPage.template).toContain("form.scope === 'global' ? '全局可用' : '这个项目'");
    expect(SystemPromptPage.template).not.toContain('roles.find');
  });

  it('filteredCards filters by asset type tab', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        { id: 'p1', name: 'Legacy Expert', content: 'a', agentType: 'coder' },
        { id: 'p2', name: 'Knowledge', content: 'b', agentType: 'main', tags: '["intent:recall"]' },
        { id: 'p3', name: 'Rule', content: 'c', agent_key: 'default_rule', tags: '["intent:default_rule"]' },
        {
          id: 'p4',
          name: 'Pending',
          content: 'd',
          agentType: 'main',
          tags: '["intent:expert"]',
          state: 'pending_confirm',
          draft_status: 'ready_to_save',
        },
      ],
    });

    const { vm } = createPage();
    await vm.loadPrompts();

    expect(vm.filteredCards.value).toHaveLength(4);

    vm.switchTab('expert');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Legacy Expert']);

    vm.switchTab('recall');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Knowledge']);

    vm.switchTab('default_rule');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Rule']);

    vm.switchTab('pending');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Pending']);
  });

  it('filteredCards applies scope and enabled filters after asset tab', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        { id: 'p1', name: 'Project Expert', content: 'a', agentType: 'main', scope: 'project', enabled: true },
        { id: 'p2', name: 'Global Expert', content: 'b', agentType: 'main', scope: 'global', enabled: true },
        { id: 'p3', name: 'Disabled Expert', content: 'c', agentType: 'main', scope: 'project', enabled: false },
        { id: 'p4', name: 'Global Knowledge', content: 'd', tags: '["intent:recall"]', scope: 'global', enabled: true },
      ],
    });

    const { vm } = createPage();
    await vm.loadPrompts();

    vm.switchTab('expert');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Project Expert', 'Global Expert', 'Disabled Expert']);

    vm.switchScopeFilter('global');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Global Expert']);

    vm.switchScopeFilter('all');
    vm.switchStatusFilter('disabled');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Disabled Expert']);

    vm.switchStatusFilter('enabled');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Project Expert', 'Global Expert']);
  });

  it('pending drafts are not treated as disabled saved assets', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      prompts: [
        {
          id: 'draft-1',
          name: 'Pending Draft',
          tags: '["intent:recall"]',
          state: 'pending_confirm',
          draft_status: 'ready_to_save',
          enabled: false,
        },
        { id: 'disabled-1', name: 'Disabled Expert', enabled: false },
      ],
    });

    const { vm } = createPage();
    await vm.loadPrompts();

    vm.switchStatusFilter('disabled');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Disabled Expert']);

    vm.switchStatusFilter('enabled');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual([]);

    vm.switchTab('pending');
    expect(vm.filteredCards.value.map(card => card.name)).toEqual(['Pending Draft']);
  });
});

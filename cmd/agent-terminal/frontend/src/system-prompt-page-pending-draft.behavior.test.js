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

function createPage() {
  const props = {
    projectStore: { state: { active: '/test-repo' } },
    threadStore: null,
    windowCwd: '/fallback-cwd',
  };
  return { props, vm: SystemPromptPage.setup(props) };
}

function pendingDraftCard() {
  return {
    id: 'intent/recall/ready',
    draftKey: 'intent/recall/ready',
    draftStatus: 'ready_to_save',
    name: '价格表资料',
    assetType: 'recall',
    isPendingDraft: true,
    scope: 'global',
    card: {
      kind: 'recall',
      title: '价格表资料',
      summary: '价格表说明',
      recall_body: 'A 套餐 100 元',
      hit_examples: ['查询价格'],
      miss_examples: ['写代码'],
    },
    issues: [],
  };
}

function pendingActionsTemplateBlock() {
  const template = SystemPromptPage.template;
  const start = template.indexOf('<div v-if="item.isPendingDraft" class="sp-card-actions">');
  const end = template.indexOf('<div v-else class="sp-card-actions">', start);
  expect(start).toBeGreaterThanOrEqual(0);
  expect(end).toBeGreaterThan(start);
  return template.slice(start, end);
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.copyTextToClipboard.mockReset();
  apiMock.onFilesDropped.mockReset().mockReturnValue(() => {});
  apiMock.readDroppedTextFiles.mockReset();
});

describe('SystemPromptPage pending draft cards', () => {
  it('refreshes pending draft cards when the wizard creates a draft', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ prompts: [pendingDraftCard()] });
    const { vm } = createPage();

    expect(SystemPromptPage.template).toContain('@drafted="loadPrompts"');
    await vm.loadPrompts();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-assets/list', { cwd: '/test-repo' });
    expect(vm.promptCards.value).toHaveLength(1);
    expect(vm.promptCards.value[0].isPendingDraft).toBe(true);
  });

  it('only continue confirmation or discard', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ ok: true })
      .mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();
    const pending = pendingDraftCard();

    vm.continuePendingDraft(pending);
    expect(vm.intentWizardOpen.value).toBe(true);
    expect(vm.pendingDraftForWizard.value).toMatchObject({
      draft_key: 'intent/recall/ready',
      scope: 'global',
      card: { title: '价格表资料' },
    });

    await vm.discardPendingDraft(pending);
    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-intents/discard', {
      draft_key: 'intent/recall/ready',
      cwd: '/test-repo',
    });
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('prompt-assets/list', { cwd: '/test-repo' });
    expect(vm.notice.message).toContain('已丢弃');
    const pendingBlock = pendingActionsTemplateBlock();
    expect(pendingBlock).toContain('继续确认');
    expect(pendingBlock).toContain('丢弃');
    expect(pendingBlock).toContain('sp-pending-continue-btn');
    expect(pendingBlock).toContain('sp-pending-discard-btn');
    expect(pendingBlock).not.toContain('sp-edit-btn');
    expect(pendingBlock).not.toContain('sp-copy-btn');
    expect(pendingBlock).not.toContain('sp-set-launch-btn');
    expect(pendingBlock).not.toContain('sp-clear-launch-btn');
    expect(pendingBlock).not.toContain('sp-delete-btn');
  });

  it('does not route pending drafts through saved prompt actions', async () => {
    const { vm } = createPage();
    const pending = pendingDraftCard();

    vm.openEdit(pending);
    await vm.copyPromptContent(pending);
    await vm.setLaunchPrompt(pending);
    await vm.deletePrompt(pending);

    expect(vm.editorOpen.value).toBe(false);
    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(apiMock.copyTextToClipboard).not.toHaveBeenCalled();
    expect(vm.notice.message).toContain('待确认');
  });
});

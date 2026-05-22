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
import { normalizePromptList } from './pages/SystemPromptPage.helpers.js';

function createPage() {
  const props = {
    projectStore: { state: { active: '/test-repo' } },
    threadStore: null,
    windowCwd: '/fallback-cwd',
  };
  return { props, vm: SystemPromptPage.setup(props) };
}

function templateBetween(startToken, endToken) {
  const start = SystemPromptPage.template.indexOf(startToken);
  expect(start).toBeGreaterThanOrEqual(0);
  const end = SystemPromptPage.template.indexOf(endToken, start);
  expect(end).toBeGreaterThan(start);
  return SystemPromptPage.template.slice(start, end);
}

function ordinaryEditorTemplate() {
  return templateBetween('data-testid="sp-editor-basic"', 'data-testid="sp-advanced-debug"');
}

function advancedDebugTemplate() {
  return templateBetween('data-testid="sp-advanced-debug"', 'data-testid="sp-editor-notice"');
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
  apiMock.copyTextToClipboard.mockReset();
  apiMock.onFilesDropped.mockReset().mockReturnValue(() => {});
  apiMock.readDroppedTextFiles.mockReset();
});

describe('SystemPromptPage prompt asset scope', () => {
  it('normalizes backend scope and hides internal scope tags', () => {
    const cards = normalizePromptList([
      {
        id: 'global/pricing',
        name: '价格表资料',
        scope: 'global',
        tags: '["scope.global","scope.cwd:/other","intent:recall","pricing"]',
      },
      {
        id: 'project/db',
        name: '数据库规则',
        scope: 'project',
        tags: '["scope.cwd:/repo","intent:default_rule","database"]',
      },
    ]);

    expect(cards[0].scope).toBe('global');
    expect(cards[0].tags).toEqual(['pricing']);
    expect(cards[1].scope).toBe('project');
    expect(cards[1].tags).toEqual(['database']);
    expect(SystemPromptPage.template).toContain('全局可用');
  });

  it('keeps ordinary edit surface small and removes raw/internal fields', () => {
    const ordinary = ordinaryEditorTemplate();

    expect(ordinary).toContain('data-testid="sp-name-input"');
    expect(ordinary).toContain('data-testid="sp-desc-input"');
    expect(ordinary).toContain('data-testid="sp-when-to-use-input"');
    expect(ordinary).toContain('data-testid="sp-execution-input"');
    expect(ordinary).toContain('data-testid="sp-preview-input"');
    expect(ordinary).toContain('data-testid="sp-enabled-checkbox"');
    expect(ordinary).toContain('可用范围：{{ form.scope');
    expect(ordinary).toContain('data-testid="sp-editor-scope-group"');
    expect(ordinary).toContain('data-testid="sp-editor-scope-project"');
    expect(ordinary).toContain('data-testid="sp-editor-scope-global"');
    expect(ordinary).toContain('说明：只在当前项目的对话中使用。');
    expect(SystemPromptPage.template).not.toContain('scope.global');
    expect(SystemPromptPage.template).not.toContain('全局启用');
    expect(ordinary).not.toContain('data-testid="sp-agent-key-select"');
    expect(ordinary).not.toContain('data-testid="sp-tags-input"');
    expect(ordinary).not.toContain('data-testid="sp-match-when-input"');
    expect(ordinary).not.toContain('data-testid="sp-priority-input"');
    expect(ordinary).not.toContain('<sections-editor');
    expect(ordinary).not.toContain('data-testid="sp-content-input"');
    expect(SystemPromptPage.template).toContain('{{ saveButtonCopy(fallbackMode, saving) }}');
  });

  it('keeps advanced routing and section controls only inside explicit debug panel', () => {
    const advanced = advancedDebugTemplate();

    expect(advanced).toContain('高级调试');
    expect(advanced).toContain('v-if="advancedDebugAvailable"');
    expect(advanced).toContain('data-testid="sp-agent-key-select"');
    expect(advanced).toContain('data-testid="sp-tags-input"');
    expect(advanced).toContain('data-testid="sp-match-when-input"');
    expect(advanced).toContain('data-testid="sp-priority-input"');
    expect(advanced).toContain('<sections-editor');
    expect(advanced).toContain('v-if="advancedDebugOpen"');
    expect(advanced).toContain(':visible="advancedDebugOpen"');
  });

  it('openEdit populates scope from item', () => {
    const { vm } = createPage();

    vm.openEdit({ id: 'x1', name: 'Test', content: 'Body', description: 'Desc', enabled: false, scope: 'global' });

    expect(vm.editorOpen.value).toBe(true);
    expect(vm.form.scope).toBe('global');
  });

  it('savePrompt sends the selected scope', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ prompt: { id: 'global-prompt', name: 'Global Prompt' } })
      .mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();

    vm.editorMode.value = 'create';
    vm.editorOpen.value = true;
    vm.form.name = 'Global Prompt';
    vm.form.scope = 'global';
    await vm.savePrompt();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/write', expect.objectContaining({
      name: 'Global Prompt',
      scope: 'global',
      cwd: '/test-repo',
    }));
  });

  it('deletePrompt sends the item scope for global assets', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ ok: true })
      .mockResolvedValueOnce({ prompts: [] });
    const { vm } = createPage();

    await vm.deletePrompt({ id: 'main/global', name: 'Global Prompt', scope: 'global' });

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompts/delete', {
      id: 'main/global',
      scope: 'global',
      cwd: '/test-repo',
    });
  });
 }
);

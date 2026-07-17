import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { assertPreferenceResponseShape } from '../../shared/api/preferenceResponseGuards.js';
import { PromptPageView } from './PromptPageView.jsx';

const backend = vi.hoisted(() => ({
  commitPromptIntent: vi.fn(),
  copyTextToClipboard: vi.fn(),
  deletePrompt: vi.fn(),
  discardPromptIntent: vi.fn(),
  draftPromptIntent: vi.fn(),
  dryRunPromptIntent: vi.fn(),
  getDashboardPrompts: vi.fn(),
  getPersonalizationProfile: vi.fn(),
  getPreference: vi.fn(),
  getPrompt: vi.fn(),
  listPromptSections: vi.fn(),
  listPromptAssets: vi.fn(),
  savePersonalizationProfile: vi.fn(),
  setPreference: vi.fn(),
  writePromptSection: vi.fn(),
  writePrompt: vi.fn(),
  deletePromptSection: vi.fn(),
}));

const validatedPreferenceReader = vi.hoisted(() => vi.fn());

vi.mock('../../pages/prompts/services/promptPageService.js', () => ({
  ...backend,
  getPreference: validatedPreferenceReader,
}));
vi.mock('../../shared/api/backendApi.js', () => ({
  getPreference: backend.getPreference,
}));

function renderPromptPage(props = {}) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return {
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <PromptPageView projectPath="/repo/app" {...props} />
      </QueryClientProvider>,
    ),
  };
}

function mockPromptList() {
  backend.listPromptAssets.mockResolvedValue({
    prompts: [
      {
        id: 'main/reviewer',
        name: '代码审查专家',
        description: '审查代码质量',
        when_to_use: '用户要求代码审查时使用',
        content: '先检查阻塞问题',
        agentType: 'coder',
        createdAt: '2026-07-11T00:00:00Z',
        updatedAt: '2026-07-11T00:00:00Z',
        tags: ['intent:expert', 'review'],
        enabled: true,
        scope: 'project',
        priority: 5,
      },
    ],
  });
  backend.getPreference.mockResolvedValue('');
  backend.getPersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.savePersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.writePrompt.mockResolvedValue({});
  backend.getPrompt.mockResolvedValue({ prompt: { content: '先检查阻塞问题' } });
  backend.copyTextToClipboard.mockResolvedValue(true);
  backend.dryRunPromptIntent.mockResolvedValue({ would_use: true, action: 'expert', reasons: ['matched'] });
  backend.getDashboardPrompts.mockResolvedValue({ prompts: [] });
  backend.listPromptSections.mockResolvedValue({ sections: [] });
  backend.writePromptSection.mockResolvedValue({ ok: true });
  backend.deletePromptSection.mockResolvedValue({ ok: true });
}

function mockPendingDraftPrompt(overrides = {}) {
  const name = overrides.name || '代码审查专家';
  const content = overrides.content || '先检查阻塞问题';
  const scope = overrides.scope || 'project';
  backend.listPromptAssets.mockResolvedValue({
    prompts: [
      {
        id: overrides.id || 'draft/reviewer',
        name,
        description: overrides.description || '',
        content,
        agentType: 'main',
        when_to_use: '',
        createdAt: '2026-07-11T00:00:00Z',
        updatedAt: '2026-07-11T00:00:00Z',
        draft_key: overrides.draftKey || 'intent/expert/review',
        draft_status: overrides.status || 'ready_to_save',
        state: 'pending_confirm',
        tags: overrides.tags || ['intent:expert'],
        scope,
        enabled: true,
        card: overrides.card || {
          kind: 'expert',
          scope,
          title: name,
          output: content,
          hit_examples: [],
          miss_examples: [],
        },
        issues: overrides.issues || [],
      },
    ],
  });
}

function canonicalPromptWireItem(overrides = {}) {
  return {
    id: 'main/canonical-wire',
    name: '规范提示词',
    content: '严格解析',
    description: '完整后端 wire shape',
    agentType: 'coder',
    when_to_use: '验证响应契约时',
    createdAt: '2026-07-11T00:00:00Z',
    updatedAt: '2026-07-11T00:00:00Z',
    enabled: true,
    scope: 'project',
    tags: ['intent:expert'],
    ...overrides,
  };
}

function dashboardPromptWireItem(overrides = {}) {
  return {
    id: 17,
    prompt_key: 'legacy/prompt',
    title: '旧提示词',
    agent_key: 'main',
    tool_name: '',
    prompt_text: 'legacy readonly data',
    when_to_use: '',
    variables: {},
    tags: ['intent:expert', 'scope.cwd:/repo/app'],
    enabled: true,
    manually_edited: false,
    priority: 0,
    created_by: '',
    updated_by: '',
    created_at: '2026-07-11T00:00:00Z',
    updated_at: '2026-07-11T00:00:00Z',
    description: '',
    ...overrides,
  };
}

function promptItemWithout(field) {
  const item = canonicalPromptWireItem();
  delete item[field];
  return item;
}

async function openPendingDraftWizard(overrides) {
  mockPendingDraftPrompt(overrides);
  renderPromptPage();
  fireEvent.click(await screen.findByRole('button', { name: '继续确认' }));
  return screen.findByRole('dialog', { name: '添加给 AI 的内容' });
}

beforeEach(() => {
  vi.clearAllMocks();
  mockPromptList();
  validatedPreferenceReader.mockImplementation(async (payload) => {
    const value = await backend.getPreference(payload);
    assertPreferenceResponseShape(payload.key, value);
    return value;
  });
});

afterEach(() => {
  cleanup();
  delete window.__SUPER_DOLPHIN_PROMPT_DEBUG__;
  vi.restoreAllMocks();
});

describe('PromptPageView module', () => {
  it('exports the prompt page view component', () => {
    expect(PromptPageView).toBeTypeOf('function');
  });

  it('keeps advanced debug disabled when browser storage is unavailable', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });

    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));

    expect(await screen.findByRole('dialog', { name: '编辑提示词' })).toBeInTheDocument();
    expect(screen.queryByLabelText('match_when JSON')).not.toBeInTheDocument();
  });

  it('closes the editor through React Aria modal dismissal', async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));

    const dialog = await screen.findByRole('dialog', { name: '编辑提示词' });
    fireEvent.keyDown(dialog, { key: 'Escape' });

    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '编辑提示词' })).not.toBeInTheDocument();
    });
  });

  it('loads and saves personalization profile', async () => {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [
        canonicalPromptWireItem({ id: 'main/role', name: '代码审查专家', tags: ['intent:expert'], content: 'review' }),
        canonicalPromptWireItem({ id: 'recall/vue', name: 'Vue 规范', tags: ['intent:recall'], content: 'vue' }),
        canonicalPromptWireItem({ id: 'rule/default', name: '默认规则', agentType: 'default_rule', tags: ['intent:default_rule'], content: 'rule', scope: 'global' }),
        canonicalPromptWireItem({ id: 'draft/profile', name: '待确认角色', draft_key: 'draft-profile', draft_status: 'ready_to_save', state: 'pending_confirm', tags: ['intent:expert'], content: 'draft', enabled: false }),
      ],
    });
    backend.getPersonalizationProfile.mockResolvedValue({
      profile: {
        displayName: '小海',
        role: '后端工程师',
        background: '熟悉 Go',
        customInstructions: '回答要直接',
      },
    });
    backend.savePersonalizationProfile.mockResolvedValue({
      profile: {
        displayName: '小海',
        role: '架构师',
        background: '熟悉 Go',
        customInstructions: '回答要直接',
      },
    });

    renderPromptPage();

    expect(await screen.findByRole('heading', { name: '个性化' })).toBeInTheDocument();
    expect(screen.getByText('管理您的身份信息以及 燧元 的记忆内容')).toBeInTheDocument();
    const overview = screen.getByLabelText('个性化概览');
    const metricValue = (label) => {
      const term = Array.from(overview.querySelectorAll('dt')).find((node) => node.textContent === label);
      expect(term).toBeTruthy();
      return term.nextElementSibling;
    };
    await waitFor(() => expect(metricValue('定制角色')).toHaveTextContent('1'));
    expect(metricValue('知识')).toHaveTextContent('1');
    expect(metricValue('默认规则')).toHaveTextContent('1');
    expect(metricValue('待确认')).toHaveTextContent('1');
    await waitFor(() => expect(backend.getPersonalizationProfile).toHaveBeenCalledWith({ cwd: '/repo/app' }));
    expect(within(overview).getByLabelText('昵称')).toHaveValue('小海');
    expect(within(overview).getByLabelText('职业')).toHaveValue('后端工程师');
    expect(within(overview).getByLabelText('更多关于您的信息')).toHaveValue('熟悉 Go');
    expect(within(overview).getByLabelText('自定义指令')).toHaveValue('回答要直接');

    fireEvent.change(within(overview).getByLabelText('职业'), { target: { value: '架构师' } });
    fireEvent.click(within(overview).getByRole('button', { name: '保存个人资料' }));

    await waitFor(() => expect(backend.savePersonalizationProfile).toHaveBeenCalledWith({
      cwd: '/repo/app',
      profile: {
        displayName: '小海',
        role: '架构师',
        background: '熟悉 Go',
        customInstructions: '回答要直接',
      },
    }));
    expect(await screen.findByText('个人资料已保存')).toBeInTheDocument();
  });

  it('does not publish profile success after save response validation rejects', async () => {
    backend.savePersonalizationProfile.mockRejectedValueOnce(new TypeError(
      'ui/personalization/profile/save response personalization profile response.profile.background Expected string, received array',
    ));

    renderPromptPage();

    const overview = await screen.findByLabelText('个性化概览');
    fireEvent.change(within(overview).getByLabelText('职业'), { target: { value: '架构师' } });
    fireEvent.click(within(overview).getByRole('button', { name: '保存个人资料' }));

    expect(await screen.findByText('个人资料保存失败，请重试。')).toBeInTheDocument();
    expect(document.body.textContent).not.toContain('ui/personalization/profile/save');
    expect(screen.queryByText('个人资料已保存')).not.toBeInTheDocument();
  });

  it('opens the recall wizard from the import memory action', async () => {
    renderPromptPage();

    const overview = await screen.findByLabelText('个性化概览');
    fireEvent.click(within(overview).getByRole('button', { name: '导入记忆' }));

    const dialog = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
    expect(within(dialog).getByRole('tab', { name: '参考资料' })).toHaveAttribute('aria-selected', 'true');
  });

  it('labels the personalization overview as read-only when prompt assets fall back', async () => {
    const error = Object.assign(new Error('method not found'), { code: -32601 });
    backend.listPromptAssets.mockRejectedValueOnce(error);

    renderPromptPage();

    const overview = await screen.findByLabelText('个性化概览');
    await waitFor(() => {
      expect(within(overview).getByText('prompt-assets/list 暂不可用；当前仅显示只读的提示词与参考资料。')).toBeInTheDocument();
    });
    expect(within(overview).queryByText(/已接入提示词与参考资料/)).not.toBeInTheDocument();
  });
});

describe('PromptPageView backend wiring', () => {
  it('surfaces a malformed active prompt preference instead of coercing it to empty', async () => {
    backend.getPreference.mockResolvedValue(42);

    renderPromptPage();

    expect(await screen.findByText('同步失败，显示的是上次成功的数据。')).toBeInTheDocument();
    expect(document.body.textContent).not.toContain('invalid UI preference response');
    expect(screen.queryByText('main/reviewer')).not.toBeInTheDocument();
  });

  it('loads prompt assets for the dashboard and saves edits with the backend payload shape', async () => {
    renderPromptPage();

    expect(screen.getByRole('status')).toHaveTextContent('正在加载提示词...');

    await waitFor(() => {
      expect(backend.listPromptAssets).toHaveBeenCalledWith({ cwd: '/repo/app' });
      expect(backend.getPreference).toHaveBeenCalledWith({
        cwd: '/repo/app',
        key: 'settings.activePromptKey',
      });
    });
    expect(await screen.findByText('代码审查专家')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '编辑' }));
    fireEvent.change(screen.getByRole('textbox', { name: '名称' }), { target: { value: '审查提示词' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => {
      expect(backend.writePrompt).toHaveBeenCalledWith({
        cwd: '/repo/app',
        id: 'main/reviewer',
        name: '审查提示词',
        description: '审查代码质量',
        agentType: 'coder',
        priority: 5,
        when_to_use: '用户要求代码审查时使用',
        content: '先检查阻塞问题',
        tags: ['review'],
        enabled: true,
        scope: 'project',
      });
    });
    expect(await screen.findByText('提示词已保存：审查提示词')).toBeInTheDocument();
  });

  it('blocks saving an empty agent type instead of defaulting it to main', async () => {
    window.__SUPER_DOLPHIN_PROMPT_DEBUG__ = true;
    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));
    const editor = await screen.findByRole('dialog', { name: '编辑提示词' });
    fireEvent.change(within(editor).getByRole('textbox', { name: 'Agent Key' }), {
      target: { value: '' },
    });
    fireEvent.click(within(editor).getByRole('button', { name: '保存' }));

    expect(await within(editor).findByText('请填写 Agent Key')).toBeInTheDocument();
    expect(backend.writePrompt).not.toHaveBeenCalled();
    delete window.__SUPER_DOLPHIN_PROMPT_DEBUG__;
  });

  it('does not write back priority zero when the canonical item omits priority', async () => {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [promptItemWithout('priority')],
    });
    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));
    fireEvent.click(screen.getByRole('button', { name: '保存' }));

    await waitFor(() => expect(backend.writePrompt).toHaveBeenCalledTimes(1));
    expect(backend.writePrompt.mock.calls[0][0]).not.toHaveProperty('priority');
  });

  it('exposes editor scope as radios and saves the selected scope', async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));
    const editor = await screen.findByRole('dialog', { name: '编辑提示词' });
    const scopeGroup = within(editor).getByRole('radiogroup', { name: '可用范围' });
    const projectScope = within(scopeGroup).getByRole('radio', { name: '这个项目' });
    const globalScope = within(scopeGroup).getByRole('radio', { name: '全局可用' });

    expect(projectScope).toBeChecked();
    expect(globalScope).not.toBeChecked();

    fireEvent.click(globalScope);
    expect(projectScope).not.toBeChecked();
    expect(globalScope).toBeChecked();
    fireEvent.click(within(editor).getByRole('button', { name: '保存' }));

    await waitFor(() => {
      expect(backend.writePrompt).toHaveBeenCalledWith(expect.objectContaining({
        id: 'main/reviewer',
        scope: 'global',
      }));
    });
  });

  it('shows created, started, and disabled prompt lifecycle states', async () => {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [
        canonicalPromptWireItem({
          id: 'main/created',
          name: '已创建助手',
          description: '尚未强制使用',
          content: '普通能力',
          tags: ['intent:expert'],
          enabled: true,
          scope: 'project',
        }),
        canonicalPromptWireItem({
          id: 'main/started',
          name: '已启动助手',
          description: '当前强制使用',
          content: '启动能力',
          tags: ['intent:expert'],
          enabled: true,
          scope: 'project',
        }),
        canonicalPromptWireItem({
          id: 'main/stopped',
          name: '已停用助手',
          description: '已经停用',
          content: '停用能力',
          tags: ['intent:expert'],
          enabled: false,
          scope: 'project',
        }),
      ],
    });
    backend.getPreference.mockResolvedValue('main/started');

    renderPromptPage();

    const createdCard = (await screen.findByText('已创建助手')).closest('article');
    const startedCard = screen.getByText('已启动助手').closest('article');
    const stoppedCard = screen.getByText('已停用助手').closest('article');
    expect(within(createdCard).getByText('已创建')).toBeInTheDocument();
    expect(within(startedCard).getByText('已启动')).toBeInTheDocument();
    expect(within(startedCard).getByText('强制使用')).toBeInTheDocument();
    expect(within(stoppedCard).getByText('已停用')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '启用中' })).not.toBeInTheDocument();
    expect(screen.getByText('已创建助手')).toBeInTheDocument();
    expect(screen.getByText('已启动助手')).toBeInTheDocument();
    expect(screen.getByText('已停用助手')).toBeInTheDocument();
    expect(screen.queryByRole('tablist', { name: '提示词分类' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /全部范围/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /全部状态/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '添加给 AI 的内容' })).not.toBeInTheDocument();
  });

  it('falls back to readonly dashboard prompts when prompt-assets/list is not registered', async () => {
    const missingMethodError = new Error('method not found');
    missingMethodError.code = -32601;
    backend.listPromptAssets.mockRejectedValueOnce(missingMethodError);
    backend.getDashboardPrompts.mockResolvedValueOnce({
      prompts: [dashboardPromptWireItem()],
    });

    renderPromptPage();

    expect(await screen.findByText('旧提示词')).toBeInTheDocument();
    expect(screen.getByText(/只读模式/)).toBeInTheDocument();
    expect(backend.getDashboardPrompts).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(screen.getByRole('button', { name: '查看' })).toBeInTheDocument();
  });

  it('fails fast on malformed prompt-assets/list responses without readonly fallback', async () => {
    backend.listPromptAssets.mockResolvedValueOnce({ prompts: {} });

    renderPromptPage();

    expect(await screen.findByRole('alert')).toHaveTextContent('加载提示词失败');
    expect(backend.getDashboardPrompts).not.toHaveBeenCalled();
    expect(screen.queryByText(/只读模式/)).not.toBeInTheDocument();
  });

  it('does not publish prompt assets after the public RPC validator rejects a nested field', async () => {
    backend.listPromptAssets.mockRejectedValueOnce(new TypeError(
      'ui/prompt-assets/list response prompt assets response.prompts[0].enabled Expected boolean, received string',
    ));

    renderPromptPage();

    expect(await screen.findByRole('alert')).toHaveTextContent('加载提示词失败，请重试。');
    expect(document.body.textContent).not.toContain('ui/prompt-assets/list');
    expect(screen.queryByText('代码审查专家')).not.toBeInTheDocument();
    expect(backend.getDashboardPrompts).not.toHaveBeenCalled();
  });

  it.each([
    ['empty item', {}],
    ...['content', 'description', 'agentType', 'when_to_use', 'createdAt', 'updatedAt']
      .map((field) => [`missing stable ${field}`, promptItemWithout(field)]),
    ['missing id', {
      name: '缺少 ID',
      content: '不能启动',
      description: '',
      agentType: 'coder',
      when_to_use: '测试时',
      createdAt: '2026-07-11T00:00:00Z',
      updatedAt: '2026-07-11T00:00:00Z',
      enabled: true,
      scope: 'project',
      tags: ['intent:expert'],
      priority: 1,
    }],
    ['string boolean', {
      id: 'main/string-boolean',
      name: '错误布尔值',
      content: '不能启动',
      description: '',
      agentType: 'coder',
      when_to_use: '测试时',
      createdAt: '2026-07-11T00:00:00Z',
      updatedAt: '2026-07-11T00:00:00Z',
      enabled: 'false',
      scope: 'project',
      tags: ['intent:expert'],
      priority: 1,
    }],
    ['unknown scope', {
      id: 'main/unknown-scope',
      name: '错误范围',
      content: '不能启动',
      description: '',
      agentType: 'coder',
      when_to_use: '测试时',
      createdAt: '2026-07-11T00:00:00Z',
      updatedAt: '2026-07-11T00:00:00Z',
      enabled: true,
      scope: 'workspace',
      tags: ['intent:expert'],
      priority: 1,
    }],
    ['unknown prompt kind', {
      id: 'main/unknown-kind',
      name: '错误类别',
      content: '不能启动',
      description: '',
      agentType: 'coder',
      when_to_use: '测试时',
      createdAt: '2026-07-11T00:00:00Z',
      updatedAt: '2026-07-11T00:00:00Z',
      enabled: true,
      scope: 'project',
      tags: ['intent:unknown'],
      priority: 1,
    }],
    ['missing required name', {
      id: 'main/missing-name',
      content: '不能启动',
      description: '',
      agentType: 'coder',
      when_to_use: '测试时',
      createdAt: '2026-07-11T00:00:00Z',
      updatedAt: '2026-07-11T00:00:00Z',
      enabled: true,
      scope: 'project',
      tags: ['intent:expert'],
      priority: 1,
    }],
  ])('rejects a malformed prompt item: %s', async (_label, item) => {
    backend.listPromptAssets.mockResolvedValueOnce({ prompts: [item] });

    renderPromptPage();

    expect(await screen.findByRole('alert')).toHaveTextContent('加载提示词失败');
    expect(screen.queryByText(item.name || '未命名')).not.toBeInTheDocument();
    expect(screen.queryByRole('article')).not.toBeInTheDocument();
    expect(backend.getDashboardPrompts).not.toHaveBeenCalled();
  });

  it('renders a canonical prompt-assets/list item', async () => {
    backend.listPromptAssets.mockResolvedValueOnce({
      prompts: [{
        id: 'main/canonical',
        name: '规范提示词',
        content: '严格解析',
        description: '完整后端 wire shape',
        agentType: 'coder',
        when_to_use: '验证响应契约时',
        createdAt: '2026-07-11T00:00:00Z',
        updatedAt: '2026-07-11T00:00:00Z',
        enabled: true,
        scope: 'project',
        tags: ['intent:expert', 'contract'],
        priority: 3,
      }],
    });

    renderPromptPage();

    expect(await screen.findByText('规范提示词')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows sync failure when readonly fallback dashboard prompts are malformed after fallback is selected', async () => {
    const missingMethodError = new Error('method not found');
    missingMethodError.code = -32601;
    backend.listPromptAssets
      .mockRejectedValueOnce(missingMethodError)
      .mockRejectedValueOnce(missingMethodError);
    backend.getDashboardPrompts
      .mockResolvedValueOnce({
        prompts: [dashboardPromptWireItem()],
      })
      .mockResolvedValueOnce({ prompts: [dashboardPromptWireItem({ id: '17' })] });

    renderPromptPage();

    expect(await screen.findByText('旧提示词')).toBeInTheDocument();
    window.dispatchEvent(new Event('focus'));

    expect(await screen.findByRole('alert')).toHaveTextContent('同步失败，显示的是上次成功的数据');
    expect(screen.getByText('旧提示词')).toBeInTheDocument();
    expect(screen.getByText(/只读模式/)).toBeInTheDocument();
  });

  it('copies saved prompt content after reading the complete prompt body', async () => {
    backend.getPrompt.mockResolvedValueOnce({ prompt: { content: '完整提示词内容' } });

    renderPromptPage();
    const card = (await screen.findByText('代码审查专家')).closest('article');
    fireEvent.click(within(card).getByRole('button', { name: '复制' }));

    await waitFor(() => {
      expect(backend.getPrompt).toHaveBeenCalledWith({ cwd: '/repo/app', id: 'main/reviewer' });
      expect(backend.copyTextToClipboard).toHaveBeenCalledWith('完整提示词内容');
    });
    expect(await screen.findByText('已复制提示词内容')).toBeInTheDocument();
  });

  it('edits match_when JSON in advanced debug and blocks invalid JSON before saving', async () => {
    window.__SUPER_DOLPHIN_PROMPT_DEBUG__ = true;
    backend.listPromptAssets.mockResolvedValueOnce({
      prompts: [{
        id: 'main/reviewer',
        name: '代码审查专家',
        content: '先检查阻塞问题',
        description: '',
        agentType: 'main',
        when_to_use: '',
        createdAt: '2026-07-11T00:00:00Z',
        updatedAt: '2026-07-11T00:00:00Z',
        tags: ['intent:expert', 'review'],
        enabled: true,
        scope: 'project',
        match_when: { language: 'zh' },
      }],
    });

    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));

    const matchWhenInput = await screen.findByLabelText('match_when JSON');
    expect(matchWhenInput).toHaveValue('{\n  "language": "zh"\n}');

    fireEvent.change(matchWhenInput, { target: { value: '{bad json' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    expect(await screen.findByText(/自动匹配条件不是合法 JSON/)).toBeInTheDocument();
    expect(backend.writePrompt).not.toHaveBeenCalled();

    fireEvent.change(matchWhenInput, { target: { value: '{"tags_has":["review"]}' } });
    fireEvent.click(screen.getByRole('button', { name: '保存' }));
    await waitFor(() => {
      expect(backend.writePrompt).toHaveBeenCalledWith(expect.objectContaining({
        match_when: { tags_has: ['review'] },
      }));
    });
    delete window.__SUPER_DOLPHIN_PROMPT_DEBUG__;
  });

  it('does not render the sections panel in the prompt editor', async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));

    const editor = await screen.findByRole('dialog', { name: '编辑提示词' });
    expect(within(editor).queryByText('提示词分段')).not.toBeInTheDocument();
    expect(within(editor).getByLabelText('AI 使用时怎么做')).toBeInTheDocument();
    expect(backend.listPromptSections).not.toHaveBeenCalled();
  });

  it('does not render a top-right close button in the prompt editor', async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));

    const editor = await screen.findByRole('dialog', { name: '编辑提示词' });
    expect(within(editor).queryByLabelText('关闭编辑器')).not.toBeInTheDocument();
    expect(within(editor).getByRole('button', { name: '取消' })).toBeInTheDocument();
    expect(within(editor).getByRole('button', { name: '保存' })).toBeInTheDocument();
  });

  it('runs prompt intent dry-run from the confirmation wizard without exposing routing internals', async () => {
    backend.dryRunPromptIntent.mockResolvedValueOnce({
      would_use: true,
      action: 'expert',
      reasons: ['question provided: 如何审查这段代码？', 'matched'],
    });

    await openPendingDraftWizard({
      draftKey: 'intent/expert/review',
      name: '代码审查专家',
      content: '先检查阻塞问题',
    });

    fireEvent.click(screen.getByText('试问验证'));
    fireEvent.change(screen.getByLabelText('试问问题'), { target: { value: '如何审查这段代码？' } });
    fireEvent.click(screen.getByRole('button', { name: '验证' }));

    await waitFor(() => {
      expect(backend.dryRunPromptIntent).toHaveBeenCalledWith({
        cwd: '/repo/app',
        draftKey: 'intent/expert/review',
        kind: 'expert',
        card: expect.objectContaining({ title: '代码审查专家', output: '先检查阻塞问题' }),
        question: '如何审查这段代码？',
      });
    });
    expect(await screen.findByText(/这条内容会参与专家能力匹配/)).toBeInTheDocument();
    expect(screen.queryByText(/question provided/)).not.toBeInTheDocument();
  });

  it('does not render a duplicate top-right close button in the prompt intent wizard', async () => {
    const wizard = await openPendingDraftWizard();
    expect(within(wizard).getAllByRole('button', { name: '关闭' })).toHaveLength(1);
  });

  it('exposes wizard scope as radios and submits the selected draft scope', async () => {
    const wizard = await openPendingDraftWizard();
    const scopeGroup = within(wizard).getByRole('radiogroup', { name: '草稿范围' });
    const projectScope = within(scopeGroup).getByRole('radio', { name: '这个项目' });
    const globalScope = within(scopeGroup).getByRole('radio', { name: '全局可用' });

    expect(projectScope).toBeChecked();
    expect(globalScope).not.toBeChecked();

    fireEvent.click(globalScope);
    fireEvent.change(within(wizard).getByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '跨项目都要使用这条审查规则。' },
    });
    fireEvent.click(within(wizard).getByRole('button', { name: '帮我生成' }));

    await waitFor(() => {
      expect(backend.draftPromptIntent).toHaveBeenCalledWith(expect.objectContaining({
        rawInput: '跨项目都要使用这条审查规则。',
        scope: 'global',
      }));
    });
  });

  it('shows a waiting reminder and allows closing while prompt intent generation is still running', async () => {
    backend.draftPromptIntent.mockImplementationOnce(() => new Promise(() => {}));

    await openPendingDraftWizard();
    fireEvent.change(screen.getByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '请帮我整理一个需要较长时间生成的专家能力。' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

    const wizard = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
    expect(within(wizard).getByText('正在整理内容，可能需要一点时间。')).toBeInTheDocument();
    const closeButton = within(wizard).getByRole('button', { name: '关闭' });
    expect(closeButton).toBeEnabled();

    fireEvent.click(closeButton);
    expect(screen.queryByRole('dialog', { name: '添加给 AI 的内容' })).not.toBeInTheDocument();
  });

  it('saves a ready prompt intent draft and refreshes the prompt list', async () => {
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    await openPendingDraftWizard({
      draftKey: 'intent/expert/ready',
      name: '代码审查专家',
      content: '先检查阻塞问题',
    });

    fireEvent.click(screen.getByRole('button', { name: '确认保存' }));

    await waitFor(() => {
      expect(backend.commitPromptIntent).toHaveBeenCalledWith({
        cwd: '/repo/app',
        draftKey: 'intent/expert/ready',
        scope: 'project',
      });
    });
    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: '添加给 AI 的内容' })).not.toBeInTheDocument();
    });
    expect(await screen.findByText('已保存，可在新对话中被 AI 发现和使用')).toBeInTheDocument();
  });

  it('keeps a prompt intent draft open when commit response validation rejects', async () => {
    backend.commitPromptIntent.mockRejectedValueOnce(new TypeError(
      'ui/prompt-intents/commit response prompt intent commit response.prompt_key Expected string, received number',
    ));

    await openPendingDraftWizard({ draftKey: 'intent/expert/malformed-commit' });
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }));

    const wizard = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
    expect(await within(wizard).findByText('保存失败，请重试。')).toBeInTheDocument();
    expect(document.body.textContent).not.toContain('ui/prompt-intents/commit');
    expect(within(wizard).getByRole('button', { name: '确认保存' })).toBeEnabled();
    expect(screen.queryByText('已保存，可在新对话中被 AI 发现和使用')).not.toBeInTheDocument();
  });

  it('requires explicit review confirmation before saving risky prompt intent drafts', async () => {
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    await openPendingDraftWizard({
      draftKey: 'intent/expert/risky',
      name: '风险审查专家',
      content: '先检查风险',
      issues: [{ code: 'default_rule_conflict', severity: 'review', message: '可能和已有规则冲突' }],
    });

    const saveButton = screen.getByRole('button', { name: '确认保存' });
    expect(saveButton).toBeDisabled();
    fireEvent.click(screen.getByLabelText('我已确认这些风险，仍要保存'));
    expect(saveButton).toBeEnabled();
    fireEvent.click(saveButton);

    await waitFor(() => {
      expect(backend.commitPromptIntent).toHaveBeenCalledWith({
        cwd: '/repo/app',
        draftKey: 'intent/expert/risky',
        scope: 'project',
        confirmRisk: true,
      });
    });
  });

  it('confirms global scope when saving a global prompt intent draft', async () => {
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    await openPendingDraftWizard({
      draftKey: 'intent/expert/global',
      name: '全局审查专家',
      content: '跨项目检查问题',
      scope: 'global',
      card: { kind: 'expert', scope: 'global', title: '全局审查专家', output: '跨项目检查问题' },
    });

    fireEvent.click(screen.getByRole('button', { name: '确认保存' }));

    await waitFor(() => {
      expect(backend.commitPromptIntent).toHaveBeenCalledWith({
        cwd: '/repo/app',
        draftKey: 'intent/expert/global',
        scope: 'global',
        confirmGlobal: true,
      });
    });
  });
});

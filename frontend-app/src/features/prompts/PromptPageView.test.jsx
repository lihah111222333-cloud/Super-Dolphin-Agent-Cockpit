import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PromptPageView } from './PromptPageView.jsx';

const backend = vi.hoisted(() => ({
  commitPromptIntent: vi.fn(),
  copyTextToClipboard: vi.fn(),
  deletePrompt: vi.fn(),
  discardPromptIntent: vi.fn(),
  draftPromptIntent: vi.fn(),
  dryRunPromptIntent: vi.fn(),
  getDashboardPrompts: vi.fn(),
  getPreference: vi.fn(),
  getPrompt: vi.fn(),
  listPromptSections: vi.fn(),
  listPromptAssets: vi.fn(),
  setPreference: vi.fn(),
  writePromptSection: vi.fn(),
  writePrompt: vi.fn(),
  deletePromptSection: vi.fn(),
}));

vi.mock('../../shared/api/backendApi.js', () => backend);

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
        agent_key: 'coder',
        tags: ['review'],
        enabled: true,
        scope: 'project',
        priority: 5,
      },
    ],
  });
  backend.getPreference.mockResolvedValue('');
  backend.writePrompt.mockResolvedValue({});
  backend.getPrompt.mockResolvedValue({ prompt: { content: '先检查阻塞问题' } });
  backend.copyTextToClipboard.mockResolvedValue(true);
  backend.dryRunPromptIntent.mockResolvedValue({ would_use: true, action: 'expert', reasons: ['matched'] });
  backend.getDashboardPrompts.mockResolvedValue({ prompts: [] });
  backend.listPromptSections.mockResolvedValue({ sections: [] });
  backend.writePromptSection.mockResolvedValue({ ok: true });
  backend.deletePromptSection.mockResolvedValue({ ok: true });
}

beforeEach(() => {
  vi.clearAllMocks();
  mockPromptList();
});

afterEach(() => {
  cleanup();
});

describe('PromptPageView module', () => {
  it('exports the prompt page view component', () => {
    expect(PromptPageView).toBeTypeOf('function');
  });

  it('shows personalization overview with pending profile and import actions disabled', async () => {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [
        { id: 'main/role', name: '代码审查专家', tags: ['intent:expert'], content: 'review', scope: 'project' },
        { id: 'recall/vue', name: 'Vue 规范', tags: ['intent:recall'], content: 'vue', scope: 'project' },
        { id: 'rule/default', name: '默认规则', tags: ['intent:default_rule'], content: 'rule', scope: 'global' },
        { id: 'draft/profile', name: '待确认角色', draft_key: 'draft-profile', draft_status: 'ready_to_save', tags: ['intent:expert'], content: 'draft', scope: 'project' },
      ],
    });

    renderPromptPage();

    expect(await screen.findByRole('heading', { name: '个性化' })).toBeInTheDocument();
    expect(screen.getByText('管理您的身份信息以及 Super-Dolphin 的记忆内容')).toBeInTheDocument();
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
    expect(within(overview).getByLabelText('昵称')).toBeDisabled();
    expect(within(overview).getByLabelText('职业')).toBeDisabled();
    expect(within(overview).getByLabelText('更多关于您的信息')).toBeDisabled();
    expect(within(overview).getByLabelText('自定义指令')).toBeDisabled();
    expect(within(overview).getByRole('button', { name: '保存个人资料' })).toBeDisabled();
    expect(within(overview).getByRole('button', { name: '导入记忆' })).toBeDisabled();
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

  it('shows created, started, and disabled prompt lifecycle states', async () => {
    backend.listPromptAssets.mockResolvedValue({
      prompts: [
        {
          id: 'main/created',
          name: '已创建助手',
          description: '尚未强制使用',
          content: '普通能力',
          tags: ['intent:expert'],
          enabled: true,
          scope: 'project',
        },
        {
          id: 'main/started',
          name: '已启动助手',
          description: '当前强制使用',
          content: '启动能力',
          tags: ['intent:expert'],
          enabled: true,
          scope: 'project',
        },
        {
          id: 'main/stopped',
          name: '已停用助手',
          description: '已经停用',
          content: '停用能力',
          tags: ['intent:expert'],
          enabled: false,
          scope: 'project',
        },
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

    fireEvent.click(screen.getByRole('button', { name: '已创建' }));
    expect(screen.getByText('已创建助手')).toBeInTheDocument();
    expect(screen.queryByText('已启动助手')).not.toBeInTheDocument();
    expect(screen.queryByText('已停用助手')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '已启动' }));
    expect(screen.queryByText('已创建助手')).not.toBeInTheDocument();
    expect(screen.getByText('已启动助手')).toBeInTheDocument();
    expect(screen.queryByText('已停用助手')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '已停用' }));
    expect(screen.queryByText('已创建助手')).not.toBeInTheDocument();
    expect(screen.queryByText('已启动助手')).not.toBeInTheDocument();
    expect(screen.getByText('已停用助手')).toBeInTheDocument();
  });

  it('falls back to readonly dashboard prompts when prompt-assets/list is not registered', async () => {
    const missingMethodError = new Error('method not found');
    missingMethodError.code = -32601;
    backend.listPromptAssets.mockRejectedValueOnce(missingMethodError);
    backend.getDashboardPrompts.mockResolvedValueOnce({
      prompts: [{
        id: 'legacy/prompt',
        name: '旧提示词',
        content: 'legacy readonly data',
        tags: ['intent:expert'],
      }],
    });

    renderPromptPage();

    expect(await screen.findByText('旧提示词')).toBeInTheDocument();
    expect(screen.getByText(/只读模式/)).toBeInTheDocument();
    expect(backend.getDashboardPrompts).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(screen.getByRole('button', { name: '查看' })).toBeInTheDocument();
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
        tags: ['review'],
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
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/review',
      kind: 'expert',
      scope: 'project',
      status: 'review',
      card: { title: '代码审查专家', output: '先检查阻塞问题' },
      issues: [],
    });
    backend.dryRunPromptIntent.mockResolvedValueOnce({
      would_use: true,
      action: 'expert',
      reasons: ['question provided: 如何审查这段代码？', 'matched'],
    });

    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '添加给 AI 的内容' }));
    fireEvent.change(screen.getByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '当用户要求代码审查时使用。' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
    expect(await screen.findByText('代码审查专家')).toBeInTheDocument();

    fireEvent.click(screen.getByText('试问验证'));
    fireEvent.change(screen.getByLabelText('试问问题'), { target: { value: '如何审查这段代码？' } });
    fireEvent.click(screen.getByRole('button', { name: '验证' }));

    await waitFor(() => {
      expect(backend.dryRunPromptIntent).toHaveBeenCalledWith({
        cwd: '/repo/app',
        draftKey: 'intent/expert/review',
        kind: 'expert',
        card: { title: '代码审查专家', output: '先检查阻塞问题' },
        question: '如何审查这段代码？',
      });
    });
    expect(await screen.findByText(/这条内容会参与专家能力匹配/)).toBeInTheDocument();
    expect(screen.queryByText(/question provided/)).not.toBeInTheDocument();
  });

  it('does not render a duplicate top-right close button in the prompt intent wizard', async () => {
    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '添加给 AI 的内容' }));

    const wizard = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
    expect(within(wizard).getAllByRole('button', { name: '关闭' })).toHaveLength(1);
  });

  it('shows a waiting reminder and allows closing while prompt intent generation is still running', async () => {
    backend.draftPromptIntent.mockImplementationOnce(() => new Promise(() => {}));

    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '添加给 AI 的内容' }));
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
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/ready',
      kind: 'expert',
      scope: 'project',
      status: 'ready_to_save',
      card: { title: '代码审查专家', output: '先检查阻塞问题' },
      issues: [],
    });
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '添加给 AI 的内容' }));
    fireEvent.change(screen.getByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '当用户要求代码审查时使用。' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
    await screen.findByText('代码审查专家');

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

  it('requires explicit review confirmation before saving risky prompt intent drafts', async () => {
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/risky',
      kind: 'expert',
      scope: 'project',
      status: 'ready_to_save',
      card: { title: '风险审查专家', output: '先检查风险' },
      issues: [{ code: 'default_rule_conflict', severity: 'review', message: '可能和已有规则冲突' }],
    });
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '添加给 AI 的内容' }));
    fireEvent.change(screen.getByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: 'review risk: 帮我保存一个需要确认风险的专家能力。' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
    await screen.findByText('风险审查专家');

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
    backend.draftPromptIntent.mockResolvedValueOnce({
      draft_key: 'intent/expert/global',
      kind: 'expert',
      scope: 'global',
      status: 'ready_to_save',
      card: { title: '全局审查专家', output: '跨项目检查问题' },
      issues: [],
    });
    backend.commitPromptIntent.mockResolvedValueOnce({ ok: true });

    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '添加给 AI 的内容' }));
    const wizard = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
    fireEvent.click(within(wizard).getByRole('button', { name: '全局可用' }));
    fireEvent.change(screen.getByLabelText('写下希望 AI 记住或使用的内容'), {
      target: { value: '创建一个所有项目可用的专家能力。' },
    });
    fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
    await screen.findByText('全局审查专家');

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

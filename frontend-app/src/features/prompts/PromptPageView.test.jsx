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
    expect(screen.getByRole('button', { name: '删除' })).toBeDisabled();
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

  it('loads, writes, and deletes prompt sections from the prompt editor', async () => {
    backend.listPromptSections.mockResolvedValueOnce({
      sections: [{ section_key: 'identity', body: '保持审查口吻', trigger_type: 'always', region: 'dynamic', ordinal: 1 }],
    }).mockResolvedValue({ sections: [] });

    renderPromptPage();
    fireEvent.click(await screen.findByRole('button', { name: '编辑' }));

    expect(await screen.findByText('提示词分段')).toBeInTheDocument();
    expect(await screen.findByText('identity')).toBeInTheDocument();
    expect(backend.listPromptSections).toHaveBeenCalledWith({ cwd: '/repo/app', prompt_id: 'main/reviewer' });

    fireEvent.click(screen.getByRole('button', { name: '新增分段' }));
    fireEvent.change(screen.getByLabelText('段名（section_key）'), { target: { value: 'workflow' } });
    fireEvent.change(screen.getByLabelText('内容（body）'), { target: { value: '先列阻塞问题' } });
    fireEvent.click(screen.getByRole('button', { name: '保存分段' }));
    await waitFor(() => {
      expect(backend.writePromptSection).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        prompt_id: 'main/reviewer',
        section_key: 'workflow',
        body: '先列阻塞问题',
      }));
    });

    fireEvent.click(screen.getByRole('button', { name: '删除分段 identity' }));
    await waitFor(() => {
      expect(backend.deletePromptSection).toHaveBeenCalledWith({
        cwd: '/repo/app',
        prompt_id: 'main/reviewer',
        section_key: 'identity',
        scope: 'project',
      });
    });
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
});

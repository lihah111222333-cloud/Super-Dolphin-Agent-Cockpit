import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PromptPageView } from './PromptPageView.jsx';

const backend = vi.hoisted(() => ({
  commitPromptIntent: vi.fn(),
  deletePrompt: vi.fn(),
  discardPromptIntent: vi.fn(),
  draftPromptIntent: vi.fn(),
  getPreference: vi.fn(),
  listPromptAssets: vi.fn(),
  setPreference: vi.fn(),
  writePrompt: vi.fn(),
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
});

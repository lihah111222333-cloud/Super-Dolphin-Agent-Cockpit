import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
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
});

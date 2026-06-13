import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SkillsPage } from './SkillsPage.jsx';

const backend = vi.hoisted(() => ({
  applySkillResolution: vi.fn(),
  createSkill: vi.fn(),
  deleteSkill: vi.fn(),
  getDashboardPage: vi.fn(),
  importSkillDirectories: vi.fn(),
  listSkillFiles: vi.fn(),
  listSkillResolutions: vi.fn(),
  previewSkillResolution: vi.fn(),
  readSkill: vi.fn(),
  selectProjectDirs: vi.fn(),
  suggestSkillSummary: vi.fn(),
  writeSkill: vi.fn(),
}));

vi.mock('../../shared/api/backendApi.js', () => backend);

function renderSkillsPage(projectPath = '/repo/app') {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <SkillsPage projectPath={projectPath} />
    </QueryClientProvider>,
  );
}

function getOverviewMetric(overview, label) {
  const metric = within(overview).getByText(label).closest('div');
  if (!metric) throw new Error(`Missing overview metric: ${label}`);
  return metric;
}

beforeEach(() => {
  vi.clearAllMocks();
  backend.getDashboardPage.mockResolvedValue({
    skills: [{
      name: 'backend',
      display_name: '后端',
      dir: '/repo/app/.agent/skills/backend',
      description: '当你需要 Go 后端开发时使用。',
      trigger_words: ['go', 'service'],
      scope: 'project',
    }],
  });
  backend.listSkillResolutions.mockResolvedValue({ items: [] });
  backend.readSkill.mockResolvedValue({
    skill: {
      content: [
        '---',
        'name: "backend"',
        'display_name: "后端"',
        'description: "当你需要 Go 后端开发时使用。"',
        'trigger_words: ["go", "service"]',
        '---',
        '',
        '## 后端规则',
      ].join('\n'),
    },
  });
  backend.listSkillFiles.mockResolvedValue({
    files: [
      { name: 'SKILL.md', path: '/repo/app/.agent/skills/backend/SKILL.md', is_main: true },
      { name: 'guide.md', path: '/repo/app/.agent/skills/backend/references/guide.md', is_main: false },
    ],
  });
  backend.createSkill.mockResolvedValue({ path: '/repo/app/.agent/skills/deploy/SKILL.md' });
  backend.writeSkill.mockResolvedValue({ path: '/repo/app/.agent/skills/backend/SKILL.md' });
});

describe('SkillsPage module', () => {
  it('exports the skills page component', () => {
    expect(SkillsPage).toBeTypeOf('function');
  });
});

describe('SkillsPage backend migration', () => {
  it('frames the plugin entry around the current local skills surface', async () => {
    renderSkillsPage();

    expect(await screen.findByRole('heading', { name: '插件与技能' })).toBeInTheDocument();
    expect(screen.getByText('本地运行时')).toBeInTheDocument();
    expect(screen.getByText('本地技能库')).toBeInTheDocument();

    const overview = screen.getByRole('region', { name: '插件与技能状态' });
    expect(within(overview).getByRole('heading', { name: '本地技能、个人技能和运行时冲突处理' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: '后端' })).toBeInTheDocument();
    expect(within(overview).getByText('本地技能')).toBeInTheDocument();
    expect(within(overview).getByText('项目共享')).toBeInTheDocument();
    expect(within(overview).getByText('私人使用')).toBeInTheDocument();
    expect(within(overview).getByText('待处理冲突')).toBeInTheDocument();
    expect(within(getOverviewMetric(overview, '本地技能')).getByText('1')).toBeInTheDocument();
    expect(within(getOverviewMetric(overview, '项目共享')).getByText('1')).toBeInTheDocument();
    expect(within(getOverviewMetric(overview, '私人使用')).getByText('0')).toBeInTheDocument();
    expect(within(getOverviewMetric(overview, '待处理冲突')).getByText('0')).toBeInTheDocument();
  });

  it('does not report zero conflicts when conflict sync has not succeeded', async () => {
    backend.listSkillResolutions.mockRejectedValueOnce(new Error('resolver offline'));
    renderSkillsPage();

    expect(await screen.findByRole('heading', { name: '后端' })).toBeInTheDocument();
    const overview = screen.getByRole('region', { name: '插件与技能状态' });
    const conflictMetric = getOverviewMetric(overview, '待处理冲突');
    expect(within(conflictMetric).getByText('待确认')).toBeInTheDocument();
    expect(within(conflictMetric).queryByText('0')).not.toBeInTheDocument();
    expect(await screen.findByRole('alert')).toHaveTextContent('读取技能冲突失败：resolver offline');
  });

  it('keeps conflict status unresolved while project context is pending', async () => {
    renderSkillsPage('');

    expect(await screen.findByRole('heading', { name: '插件与技能' })).toBeInTheDocument();
    const overview = screen.getByRole('region', { name: '插件与技能状态' });
    const conflictMetric = getOverviewMetric(overview, '待处理冲突');
    expect(within(conflictMetric).getByText('待确认')).toBeInTheDocument();
    expect(within(conflictMetric).queryByText('0')).not.toBeInTheDocument();
    expect(screen.getByText('正在连接本地项目...')).toBeInTheDocument();
    expect(backend.getDashboardPage).not.toHaveBeenCalled();
    expect(backend.listSkillResolutions).not.toHaveBeenCalled();
  });

  it('loads skills from dashboard and saves an edited skill through skills/local RPCs', async () => {
    renderSkillsPage();

    expect(await screen.findByRole('heading', { name: '后端' })).toBeInTheDocument();
    expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });
    expect(backend.listSkillResolutions).toHaveBeenCalledWith({ cwd: '/repo/app' });

    const card = screen.getByRole('heading', { name: '后端' }).closest('article');
    fireEvent.click(within(card).getByRole('button', { name: '编辑详情' }));

    const dialog = await screen.findByRole('dialog', { name: '编辑技能' });
    expect(within(dialog).queryByRole('button', { name: '关闭' })).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: '取消' })).toBeInTheDocument();
    expect(backend.readSkill).toHaveBeenCalledWith({
      cwd: '/repo/app',
      path: '/repo/app/.agent/skills/backend/SKILL.md',
    });
    expect(backend.listSkillFiles).toHaveBeenCalledWith({
      cwd: '/repo/app',
      dir: '/repo/app/.agent/skills/backend',
    });
    expect(within(dialog).getByText('guide.md')).toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText('技能简介'), { target: { value: '当你需要维护 Go 服务时使用。' } });
    fireEvent.click(within(dialog).getByRole('button', { name: '保存技能' }));

    await waitFor(() => {
      expect(backend.writeSkill).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        path: '/repo/app/.agent/skills/backend/SKILL.md',
        scope: 'project',
        personal_type: '',
      }));
    });
    expect(backend.writeSkill.mock.calls.at(-1)[0].content).toContain('description: "当你需要维护 Go 服务时使用。"');
  });

  it('opens skill citation links from the editor preview through skills/local RPCs', async () => {
    backend.getDashboardPage.mockResolvedValue({
      skills: [
        {
          name: 'backend',
          display_name: '后端',
          dir: '/repo/app/.agent/skills/backend',
          skill_file: '/repo/app/.agent/skills/backend/SKILL.md',
          description: '当你需要 Go 后端开发时使用。',
          trigger_words: ['go'],
          scope: 'project',
        },
        {
          name: 'docs',
          display_name: 'Docs Skill',
          dir: '/repo/app/.agent/skills/docs',
          skill_file: '/repo/app/.agent/skills/docs/SKILL.md',
          description: '当你需要整理文档时使用。',
          trigger_words: ['docs'],
          scope: 'project',
        },
      ],
    });
    backend.readSkill.mockImplementation(({ path }) => Promise.resolve({
      skill: {
        content: path.includes('/docs/')
          ? [
            '---',
            'name: "docs"',
            'display_name: "Docs Skill"',
            'description: "当你需要整理文档时使用。"',
            '---',
            '',
            '## Docs Body',
          ].join('\n')
          : [
            '---',
            'name: "backend"',
            'display_name: "后端"',
            'description: "当你需要 Go 后端开发时使用。"',
            '---',
            '',
            '参考 [Docs Skill](/repo/app/.agent/skills/docs/SKILL.md) 或 [thread-active](agent://thread-active)。',
          ].join('\n'),
      },
    }));
    backend.listSkillFiles.mockImplementation(({ dir }) => Promise.resolve({
      files: [{ name: 'SKILL.md', path: `${dir}/SKILL.md`, is_main: true }],
    }));

    renderSkillsPage();

    const card = (await screen.findByRole('heading', { name: '后端' })).closest('article');
    fireEvent.click(within(card).getByRole('button', { name: '编辑详情' }));

    const dialog = await screen.findByRole('dialog', { name: '编辑技能' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'thread-active' }));
    expect(await screen.findByText('暂不支持会话跳转：thread-active')).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Docs Skill' }));

    await waitFor(() => {
      expect(backend.readSkill).toHaveBeenCalledWith({
        cwd: '/repo/app',
        path: '/repo/app/.agent/skills/docs/SKILL.md',
      });
      expect(backend.listSkillFiles).toHaveBeenCalledWith({
        cwd: '/repo/app',
        dir: '/repo/app/.agent/skills/docs',
      });
    });
    expect(screen.getByLabelText('技能名称')).toHaveValue('Docs Skill');
    expect(screen.getByText('Docs Body')).toBeInTheDocument();
  });

  it('creates project skills through the internal skills/create RPC', async () => {
    renderSkillsPage();

    await screen.findByRole('heading', { name: '后端' });
    fireEvent.click(screen.getByRole('button', { name: '新建技能' }));

    const dialog = await screen.findByRole('dialog', { name: '新建技能' });
    fireEvent.change(within(dialog).getByLabelText('技能名称'), { target: { value: 'Deploy Skill' } });
    fireEvent.change(within(dialog).getByLabelText('技能内容'), { target: { value: '## Deploy\nShip safely.' } });
    fireEvent.click(within(dialog).getByRole('button', { name: '保存技能' }));

    await waitFor(() => {
      expect(backend.createSkill).toHaveBeenCalledWith({
        cwd: '/repo/app',
        name: 'Deploy-Skill',
        content: expect.stringContaining('## Deploy\nShip safely.'),
      });
    });
    expect(backend.writeSkill).not.toHaveBeenCalledWith(expect.objectContaining({
      path: 'Deploy-Skill',
      scope: 'project',
    }));
  });

  it('keeps new personal skills on the skills/local write RPC with user personal type', async () => {
    renderSkillsPage();

    await screen.findByRole('heading', { name: '后端' });
    fireEvent.click(screen.getByRole('button', { name: '新建技能' }));

    const dialog = await screen.findByRole('dialog', { name: '新建技能' });
    fireEvent.change(within(dialog).getByLabelText('技能名称'), { target: { value: 'Personal Docs' } });
    fireEvent.click(within(dialog).getByRole('button', { name: '私人使用' }));
    fireEvent.change(within(dialog).getByLabelText('技能内容'), { target: { value: '## Personal\nUse privately.' } });
    fireEvent.click(within(dialog).getByRole('button', { name: '保存技能' }));

    await waitFor(() => {
      expect(backend.writeSkill).toHaveBeenCalledWith({
        cwd: '/repo/app',
        path: 'Personal-Docs',
        content: expect.stringContaining('## Personal\nUse privately.'),
        scope: 'personal',
        personal_type: 'user',
      });
    });
    expect(backend.createSkill).not.toHaveBeenCalled();
  });
});

import { describe, expect, it } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import { backend, getOverviewMetric, openSkillTools, renderSkillsPage } from './SkillsPageTestSupport.jsx';

describe('SkillsPage backend migration: dashboard', () => {
  it('frames the plugin entry around the current local skills surface', async () => {
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: '技能库' }));

    expect(await screen.findByRole('heading', { name: '插件与技能' })).toBeInTheDocument();
    expect(screen.getByText('本地运行时')).toBeInTheDocument();
    expect(screen.getByText('本地技能库')).toBeInTheDocument();

    const overview = screen.getByRole('region', { name: '插件与技能状态' });
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

  [
    {
      field: 'name',
      skill: {
        display_name: '后端',
        dir: '/repo/app/.agents/skills/backend',
        skill_file: '/repo/app/.agents/skills/backend/SKILL.md',
        description: '当你需要 Go 后端开发时使用。',
        trigger_words: ['go'],
        scope: 'project',
      },
    },
    {
      field: 'dir',
      skill: {
        name: 'backend',
        display_name: '后端',
        skill_file: '/repo/app/.agents/skills/backend/SKILL.md',
        description: '当你需要 Go 后端开发时使用。',
        trigger_words: ['go'],
        scope: 'project',
      },
    },
  ].forEach(({ field, skill }) => {
    it(`fails fast when the skills dashboard omits required ${field}`, async () => {
      backend.getDashboardPage.mockResolvedValueOnce({ skills: [skill] });

      renderSkillsPage();
      openSkillTools();

      expect(await screen.findByRole('alert')).toHaveTextContent('读取技能失败，请重试。');
      expect(screen.getByRole('alert')).not.toHaveTextContent(`missing ${field}`);
      expect(screen.queryByRole('heading', { name: '后端' })).not.toBeInTheDocument();
    });
  });

  it('fails fast when the skills dashboard skills field is not an array', async () => {
    backend.getDashboardPage.mockResolvedValueOnce({ skills: {} });

    renderSkillsPage();
    openSkillTools();

    expect(await screen.findByRole('alert')).toHaveTextContent('读取技能失败，请重试。');
    expect(screen.queryByRole('heading', { name: '后端' })).not.toBeInTheDocument();
  });

  it('loads skills whose dashboard wire omits skill_file, matching the real backend contract', async () => {
    // 真实后端（contract.SkillInfo）不提供 skill_file 字段；前端必须按真实契约解析，
    // 定位 SKILL.md 由 skillFileForItem 用 dir + '/SKILL.md' 回退。
    backend.getDashboardPage.mockResolvedValueOnce({
      skills: [{
        name: 'backend',
        display_name: '后端',
        dir: '/repo/app/.agents/skills/backend',
        description: '当你需要 Go 后端开发时使用。',
        trigger_words: ['go', 'service'],
        scope: 'project',
      }],
    });

    renderSkillsPage();
    openSkillTools();

    expect(await screen.findByRole('heading', { name: '后端' })).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('fails fast when the skill resolutions response has no items or conflicts array', async () => {
    backend.listSkillResolutions.mockResolvedValueOnce({ total: 1 });

    renderSkillsPage();
    openSkillTools();

    expect(await screen.findByRole('heading', { name: '后端' })).toBeInTheDocument();
    expect(await screen.findByRole('alert')).toHaveTextContent('读取技能冲突失败，请重试。');
  });

  it('does not report zero conflicts when conflict sync has not succeeded', async () => {
    backend.listSkillResolutions.mockRejectedValueOnce(new Error('resolver offline'));
    renderSkillsPage();
    openSkillTools();

    expect(await screen.findByRole('heading', { name: '后端' })).toBeInTheDocument();
    const overview = screen.getByRole('region', { name: '插件与技能状态' });
    const conflictMetric = getOverviewMetric(overview, '待处理冲突');
    expect(within(conflictMetric).getByText('待确认')).toBeInTheDocument();
    expect(within(conflictMetric).queryByText('0')).not.toBeInTheDocument();
    expect(await screen.findByRole('alert')).toHaveTextContent('读取技能冲突失败，请重试。');
    expect(screen.getByRole('alert')).not.toHaveTextContent('resolver offline');
  });

  it('keeps conflict status unresolved while project context is pending', async () => {
    renderSkillsPage('');
    openSkillTools();

    expect(await screen.findByRole('heading', { name: '插件与技能' })).toBeInTheDocument();
    const overview = screen.getByRole('region', { name: '插件与技能状态' });
    const conflictMetric = getOverviewMetric(overview, '待处理冲突');
    expect(within(conflictMetric).getByText('待确认')).toBeInTheDocument();
    expect(within(conflictMetric).queryByText('0')).not.toBeInTheDocument();
    expect(screen.getByText('未选择项目，请先在聊天页选择项目。')).toBeInTheDocument();
    expect(backend.getDashboardPage).not.toHaveBeenCalled();
    expect(backend.listSkillResolutions).not.toHaveBeenCalled();
  });
});

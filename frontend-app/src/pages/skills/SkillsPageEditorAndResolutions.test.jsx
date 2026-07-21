import { describe, expect, it } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { backend, openSkillTools, renderSkillsPage } from './SkillsPageTestSupport.jsx';

describe('SkillsPage backend migration: editor and resolutions', () => {
  it('loads skills from dashboard and saves an edited skill through skills/local RPCs', async () => {
    renderSkillsPage();
    openSkillTools();

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
      path: '/repo/app/.agents/skills/backend/SKILL.md',
    });
    expect(backend.listSkillFiles).toHaveBeenCalledWith({
      cwd: '/repo/app',
      dir: '/repo/app/.agents/skills/backend',
    });
    expect(within(dialog).getByText('guide.md')).toBeInTheDocument();

    fireEvent.change(within(dialog).getByLabelText('技能简介'), { target: { value: '当你需要维护 Go 服务时使用。' } });
    fireEvent.click(within(dialog).getByRole('button', { name: '保存技能' }));

    await waitFor(() => {
      expect(backend.writeSkill).toHaveBeenCalledWith(expect.objectContaining({
        cwd: '/repo/app',
        path: '/repo/app/.agents/skills/backend/SKILL.md',
        scope: 'project',
        personal_type: '',
      }));
    });
    expect(backend.writeSkill.mock.calls.at(-1)[0].content).toContain('description: "当你需要维护 Go 服务时使用。"');
  });

  it('parses conflicts alias and shows partial failure feedback when applying a mirror resolution', async () => {
    backend.listSkillResolutions.mockResolvedValue({
      conflicts: [{
        conflict_id: 'conflict-1',
        kind: 'mirror_drift',
        name: 'backend',
        scope: 'project',
        available_actions: ['canonical_overwrite_mirror'],
        provider_entries: [{ provider: 'codex', source_path_id: 'codex:backend', display_label: 'Codex' }],
      }],
    });
    backend.previewSkillResolution.mockResolvedValue({
      items: [{
        provider: 'codex',
        source_provider: 'codex',
        source_path_id: 'codex:backend',
        preview_id: 'preview-1',
        preview_hash: 'hash-1',
        source_path: '/repo/app/.agents/skills/backend/SKILL.md',
        target_path: '/home/user/.codex/skills/backend/SKILL.md',
      }],
    });
    backend.applySkillResolution.mockResolvedValue({
      action: 'canonical_overwrite_mirror',
      name: 'backend',
      partialFailure: true,
      followUpAction: 'canonical_overwrite_mirror',
    });

    renderSkillsPage();
    openSkillTools();

    expect(await screen.findByText('发现 1 个技能冲突，需要处理后再使用。')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '用本项目内容覆盖外部版本' }));

    await waitFor(() => expect(backend.previewSkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      conflict_id: 'conflict-1',
      action: 'canonical_overwrite_mirror',
    })));
    expect(await screen.findByText('请先确认将要写入的位置，确认应用后才会修改文件。')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '确认应用' }));

    await waitFor(() => expect(backend.applySkillResolution).toHaveBeenCalledWith(expect.objectContaining({
      action: 'canonical_overwrite_mirror',
      preview_id: 'preview-1',
      preview_hash: 'hash-1',
    })));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('技能冲突已部分处理');
    expect(alert).toHaveTextContent('用本项目内容覆盖外部版本');
    expect(screen.queryByText('已处理技能冲突')).not.toBeInTheDocument();
  });

  it('reports a malformed resolution apply response without showing success', async () => {
    backend.listSkillResolutions.mockResolvedValue({
      items: [{
        conflict_id: 'conflict-1',
        kind: 'mirror_drift',
        name: 'backend',
        scope: 'project',
        available_actions: ['canonical_overwrite_mirror'],
        provider_entries: [{ provider: 'codex', source_path_id: 'codex:backend', display_label: 'Codex' }],
      }],
    });
    backend.previewSkillResolution.mockResolvedValue({
      items: [{
        provider: 'codex',
        source_provider: 'codex',
        source_path_id: 'codex:backend',
        preview_id: 'preview-1',
        preview_hash: 'hash-1',
        source_path: '/repo/app/.agents/skills/backend/SKILL.md',
        target_path: '/home/user/.codex/skills/backend/SKILL.md',
      }],
    });
    backend.applySkillResolution.mockRejectedValue(
      new TypeError('SKILLS_LOCAL_RESOLUTION_APPLY response.partialFailure must be a boolean'),
    );

    renderSkillsPage();
    openSkillTools();
    expect(await screen.findByText('发现 1 个技能冲突，需要处理后再使用。')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '用本项目内容覆盖外部版本' }));
    expect(await screen.findByText('请先确认将要写入的位置，确认应用后才会修改文件。')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认应用' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('应用技能冲突处理失败，请重试。');
    expect(screen.queryByText('已处理技能冲突')).not.toBeInTheDocument();
  });

  it('reports a malformed summary suggestion without partially applying it', async () => {
    backend.suggestSkillSummary.mockRejectedValueOnce(
      new TypeError('SKILLS_LOCAL_SUMMARY response.description must be a string'),
    );
    renderSkillsPage();
    openSkillTools();
    expect(await screen.findByRole('heading', { name: '后端' })).toBeInTheDocument();
    const card = screen.getByRole('heading', { name: '后端' }).closest('article');
    fireEvent.click(within(card).getByRole('button', { name: '编辑详情' }));

    const dialog = await screen.findByRole('dialog', { name: '编辑技能' });
    fireEvent.click(within(dialog).getByRole('button', { name: '帮我生成' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('生成简介失败，请重试。');
    expect(within(dialog).queryByRole('button', { name: '使用此简介' })).not.toBeInTheDocument();
  });

  it('opens skill citation links from the editor preview through skills/local RPCs', async () => {
    backend.getDashboardPage.mockResolvedValue({
      skills: [
        {
          name: 'backend',
          display_name: '后端',
          dir: '/repo/app/.agents/skills/backend',
          skill_file: '/repo/app/.agents/skills/backend/SKILL.md',
          description: '当你需要 Go 后端开发时使用。',
          trigger_words: ['go'],
          scope: 'project',
        },
        {
          name: 'docs',
          display_name: 'Docs Skill',
          dir: '/repo/app/.agents/skills/docs',
          skill_file: '/repo/app/.agents/skills/docs/SKILL.md',
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
            '参考 [Docs Skill](/repo/app/.agents/skills/docs/SKILL.md) 或 [agent://thread-active](agent://thread-active)。',
            '入口 [SKILL.md](SKILL.md) 与 [app://backend](app://backend)。',
            '拒绝 ![unsafe-image](javascript:alert(1))、![unsafe-html](data:text/html,%3Cscript%3E) 和 [unsafe-link](javascript:alert(1))。',
          ].join('\n'),
      },
    }));
    backend.listSkillFiles.mockImplementation(({ dir }) => Promise.resolve({
      files: [{ name: 'SKILL.md', path: `${dir}/SKILL.md`, is_main: true }],
    }));

    renderSkillsPage();
    openSkillTools();

    const card = (await screen.findByRole('heading', { name: '后端' })).closest('article');
    fireEvent.click(within(card).getByRole('button', { name: '编辑详情' }));

    const dialog = await screen.findByRole('dialog', { name: '编辑技能' });
    expect(within(dialog).getByRole('button', { name: 'SKILL.md' })).toBeEnabled();
    expect(within(dialog).getByRole('button', { name: 'app://backend' })).toBeEnabled();
    expect(within(dialog).getByRole('button', { name: 'agent://thread-active' })).toBeEnabled();
    expect(within(dialog).queryByRole('button', { name: /unsafe/ })).not.toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'agent://thread-active' }));
    expect(await screen.findByText('暂不支持会话跳转：thread-active')).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Docs Skill' }));

    await waitFor(() => {
      expect(backend.readSkill).toHaveBeenCalledWith({
        cwd: '/repo/app',
        path: '/repo/app/.agents/skills/docs/SKILL.md',
      });
      expect(backend.listSkillFiles).toHaveBeenCalledWith({
        cwd: '/repo/app',
        dir: '/repo/app/.agents/skills/docs',
      });
    });
    expect(screen.getByLabelText('技能名称')).toHaveValue('Docs Skill');
    expect(screen.getByText('Docs Body')).toBeInTheDocument();
  });

  it('ignores RPC response body for skill mutations', async () => {
    backend.writeSkill.mockResolvedValueOnce({ unexpectedWriteBody: 'ignored' });
    backend.createSkill.mockResolvedValueOnce({ unexpectedCreateBody: ['ignored'] });
    backend.deleteSkill.mockResolvedValueOnce({ unexpectedDeleteBody: { ignored: true } });
    renderSkillsPage();
    openSkillTools();
    const heading = await screen.findByRole('heading', { name: '后端' });
    const card = heading.closest('article');

    fireEvent.click(within(card).getByRole('button', { name: '编辑详情' }));
    let dialog = await screen.findByRole('dialog', { name: '编辑技能' });
    fireEvent.click(within(dialog).getByRole('button', { name: '保存技能' }));
    await waitFor(() => expect(backend.writeSkill).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('dialog', { name: '编辑技能' })).not.toBeInTheDocument();
    expect(screen.getByText('已保存')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '新建技能' }));
    dialog = await screen.findByRole('dialog', { name: '新建技能' });
    fireEvent.change(within(dialog).getByLabelText('技能名称'), { target: { value: 'Ignored Result' } });
    fireEvent.change(within(dialog).getByLabelText('技能内容'), { target: { value: '## Body' } });
    fireEvent.click(within(dialog).getByRole('button', { name: '保存技能' }));
    await waitFor(() => expect(backend.createSkill).toHaveBeenCalledTimes(1));
    expect(screen.getByText('已保存')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: '新建技能' })).not.toBeInTheDocument();

    fireEvent.click(within(card).getByRole('button', { name: '删除' }));
    dialog = await screen.findByRole('dialog', { name: '删除技能' });
    fireEvent.click(within(dialog).getByRole('button', { name: '确认删除' }));
    await waitFor(() => expect(backend.deleteSkill).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('已删除 后端')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: '删除技能' })).not.toBeInTheDocument();
  });

  it('creates project skills through the internal skills/create RPC', async () => {
    renderSkillsPage();
    openSkillTools();

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
    openSkillTools();

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

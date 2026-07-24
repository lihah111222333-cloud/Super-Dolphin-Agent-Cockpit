import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const serviceMocks = vi.hoisted(() => ({
  getDashboardPage: vi.fn(),
}));

vi.mock('./services/skillsPageService.js', () => ({
  skillsPageService: {
    getDashboardPage: serviceMocks.getDashboardPage,
  },
}));

import { useSkillToolRegistration } from './skillToolRegistrationModel.js';

const projectSkill = {
  name: 'backend',
  display_name: '项目后端',
  dir: '/repo/app/.agents/skills/backend',
  scope: 'project',
  description: 'Project backend guidance',
  summary: 'Project backend summary',
  trust: 'project',
};

const personalSkill = {
  ...projectSkill,
  display_name: '个人后端',
  dir: '/home/user/.super-dolphin/skills/personal/user/backend',
  scope: 'personal',
  personal_type: 'user',
  trust: 'user',
};

function createOptions() {
  return {
    createTool: vi.fn().mockResolvedValue({}),
    listTools: vi.fn().mockResolvedValue({ tools: [] }),
    projectPath: '/repo/app',
    queryClient: { invalidateQueries: vi.fn().mockResolvedValue(undefined) },
    setPanelNotice: vi.fn(),
  };
}

describe('useSkillToolRegistration same-name guard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('excludes every ambiguous same-name skill when the editor opens', async () => {
    serviceMocks.getDashboardPage.mockResolvedValue({
      skills: [projectSkill, personalSkill],
    });
    const options = createOptions();
    const { result } = renderHook(() => useSkillToolRegistration(options));

    await act(async () => {
      await result.current.openEditor();
    });

    expect(result.current.availableSkills).toEqual([]);
    expect(result.current.selection).toBe('');
    expect(options.createTool).not.toHaveBeenCalled();
  });

  it('rechecks ambiguity before saving and does not create a tool', async () => {
    serviceMocks.getDashboardPage
      .mockResolvedValueOnce({ skills: [projectSkill] })
      .mockResolvedValue({ skills: [projectSkill, personalSkill] });
    const options = createOptions();
    const { result } = renderHook(() => useSkillToolRegistration(options));

    await act(async () => {
      await result.current.openEditor();
    });
    expect(result.current.selection).toBe('backend');

    await act(async () => {
      await result.current.saveTool();
    });

    expect(result.current.saveError).toBe('技能「backend」存在同名冲突，无法注册为工具');
    expect(result.current.selection).toBe('');
    expect(options.createTool).not.toHaveBeenCalled();
  });
});

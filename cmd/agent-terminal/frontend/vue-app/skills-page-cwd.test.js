// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { reactive } from '../lib/vue.esm-browser.prod.js';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  selectProjectDirs: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  selectProjectDirs: apiMock.selectProjectDirs,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: (text) => `<p>${text}</p>`,
  injectSentenceBreaks: vi.fn((text) => text),
}));

import { SkillsPage } from './pages/SkillsPage.js';

function createSkillsPage(overrides = {}, emit = vi.fn()) {
  const props = reactive({
    skills: overrides.skills ?? [{
      name: 'DeploySkill',
      dir: '/skills/deploy',
      summary: 'Helps shipping releases',
      scope: 'project',
    }],
    cwd: overrides.cwd ?? '',
    projectStore: overrides.projectStore ?? { state: { active: '/repo' } },
  });
  const vm = SkillsPage.setup(props, { emit });
  return { props, emit, vm };
}

beforeEach(() => {
  apiMock.callAPI.mockReset().mockResolvedValue({});
  apiMock.selectProjectDirs.mockReset().mockResolvedValue([]);
});

describe('SkillsPage cwd propagation', () => {
  it('uses page cwd for skill editor reads when project store is not ready', async () => {
    const { vm } = createSkillsPage({
      cwd: '/thread-repo',
      projectStore: { state: { active: '.' } },
    });
    apiMock.callAPI.mockImplementation(async (method, payload) => {
      if (method === 'skills/local/read' && payload?.path === '/skills/deploy/SKILL.md') {
        return {
          skill: {
            content: '---\nname: DeploySkill\nsummary: Helps shipping releases\n---\n# deploy body',
            summary: 'Helps shipping releases',
            summary_source: 'generated',
          },
        };
      }
      if (method === 'skills/local/listFiles' && payload?.dir === '/skills/deploy') {
        return { files: [{ name: 'SKILL.md', path: '/skills/deploy/SKILL.md', is_main: true }] };
      }
      return {};
    });

    await vm.onEditSkill(vm.skillCards.value[0]);

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/read', {
      path: '/skills/deploy/SKILL.md',
      cwd: '/thread-repo',
    });
    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/listFiles', {
      dir: '/skills/deploy',
      cwd: '/thread-repo',
    });
  });
});

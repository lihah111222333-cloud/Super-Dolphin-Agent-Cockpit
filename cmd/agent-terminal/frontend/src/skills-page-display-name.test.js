// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({ callAPI: vi.fn() }));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  selectProjectDirs: vi.fn(),
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

import { reactive } from '../lib/vue.esm-browser.prod.js';
import { SkillsPage } from './pages/SkillsPage.js';

function setupDisplayNamePage() {
  const props = reactive({
    skills: [{
      name: 'docker-container-deploy',
      display_name: 'Docker 容器化部署',
      dir: '/skills/docker',
      scope: 'project',
      summary: 'ship containers',
    }],
    projectStore: { state: { active: '/repo' } },
  });
  return SkillsPage.setup(props, { emit: vi.fn() });
}

describe('SkillsPage display names', () => {
  it('uses display names for cards and search without changing identity names', () => {
    const vm = setupDisplayNamePage();
    const card = vm.skillCards.value[0];

    expect(card.name).toBe('docker-container-deploy');
    expect(card.displayName).toBe('Docker 容器化部署');
    expect(card.displayLabel).toBe('Docker 容器化部署');
    vm.searchQuery.value = '容器化';
    expect(vm.filteredSkillCards.value).toHaveLength(1);
    expect(vm.skillCardKey(card)).toContain('docker-container-deploy');
  });
});

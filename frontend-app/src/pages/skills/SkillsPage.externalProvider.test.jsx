import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { SkillsPage } from './SkillsPage.jsx';

const backend = vi.hoisted(() => ({
  applySkillResolution: vi.fn(),
  createDatasourceDocument: vi.fn(),
  createSkill: vi.fn(),
  createSkillTool: vi.fn(),
  deleteDatasourceDocument: vi.fn(),
  deleteSkill: vi.fn(),
  getDashboardPage: vi.fn(),
  getDatasourceDocument: vi.fn(),
  importDatasourceLocalFile: vi.fn(),
  importSkillDirectories: vi.fn(),
  listDatasourceChunks: vi.fn(),
  listDatasourceDocuments: vi.fn(),
  listMCPServers: vi.fn(),
  listSkillFiles: vi.fn(),
  listSkillResolutions: vi.fn(),
  listSkillTools: vi.fn(),
  previewSkillResolution: vi.fn(),
  readSkill: vi.fn(),
  selectDatasourceImportFile: vi.fn(),
  selectFiles: vi.fn(),
  selectProjectDirs: vi.fn(),
  startPlaywrightMCPServer: vi.fn(),
  startSQLiteMCPServer: vi.fn(),
  stopPlaywrightMCPServer: vi.fn(),
  stopSQLiteMCPServer: vi.fn(),
  suggestSkillSummary: vi.fn(),
  updateDatasourceDocument: vi.fn(),
  writeSkill: vi.fn(),
}));

vi.mock('../../shared/api/backendApi.js', () => backend);

beforeEach(() => {
  vi.clearAllMocks();
  backend.getDashboardPage.mockResolvedValue({
    skills: [{
      name: 'backend',
      display_name: '后端',
      dir: '/repo/app/.agents/skills/backend',
      description: 'Go 后端技能',
      scope: 'project',
    }],
  });
  backend.listSkillResolutions.mockResolvedValue({
    items: [{
      conflict_id: 'external-project-conflict',
      kind: 'external_personal_project_same_name',
      name: 'backend',
      scope: 'personal',
      available_actions: ['view_diff', 'use_project_shared_skill'],
      provider_entries: [{
        provider: 'codex',
        source_path_id: 'codex:backend',
        display_label: 'Codex 外部目录',
      }],
    }],
  });
  backend.listSkillTools.mockResolvedValue({ tools: [] });
  backend.listMCPServers.mockResolvedValue({
    mcpServers: { sqlite: { enabled: false }, playwright: { enabled: false } },
  });
  backend.listDatasourceDocuments.mockResolvedValue({ documents: [] });
});

it('does not present a provider-only conflict as a canonical private skill', async () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <SkillsPage projectPath="/repo/app" />
    </QueryClientProvider>,
  );
  fireEvent.click(screen.getByRole('button', { name: '技能库' }));

  const banner = await screen.findByText('发现 1 个技能冲突，需要处理后再使用。');
  const conflict = banner.closest('section')?.querySelector('article.skills-resolution-item');
  expect(conflict).not.toBeNull();
  expect(conflict).toHaveTextContent('backend · 外部 Provider 与项目同名');
  expect(within(conflict).getByText('外部 Provider 版本')).toBeInTheDocument();
  expect(within(conflict).getByText(/Claude\/Codex 外部目录/)).toBeInTheDocument();
  expect(within(conflict).queryByText('私人使用')).not.toBeInTheDocument();

  const overview = document.querySelector('.skills-overview-compact');
  expect(overview).toHaveTextContent('私人使用0');
  fireEvent.click(screen.getByText('私人使用 0', { selector: 'button' }));
  expect(document.querySelector('.skill-card h3')).toBeNull();
  expect(conflict).toHaveTextContent('backend · 外部 Provider 与项目同名');
}, 10_000);

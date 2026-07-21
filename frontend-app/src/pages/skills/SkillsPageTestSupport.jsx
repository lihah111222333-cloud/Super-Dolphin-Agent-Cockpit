import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, vi } from 'vitest';
import { SkillsPage } from './SkillsPage.jsx';
import { resetFrontendHealthForTest } from '../../shared/diagnostics/frontendHealthStore.js';

const backendMock = vi.hoisted(() => {
  const mock = {
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
  selectFiles: vi.fn(),
  selectDatasourceImportFile: vi.fn(),
  selectProjectDirs: vi.fn(),
  startPlaywrightMCPServer: vi.fn(),
  startSQLiteMCPServer: vi.fn(),
  stopPlaywrightMCPServer: vi.fn(),
  stopSQLiteMCPServer: vi.fn(),
  suggestSkillSummary: vi.fn(),
  updateDatasourceDocument: vi.fn(),
    writeSkill: vi.fn(),
  };
  globalThis.__skillsPageTestBackend = mock;
  return mock;
});

vi.mock('../../shared/api/backendApi.js', () => backendMock);

export const backend = globalThis.__skillsPageTestBackend;

export function renderSkillsPage(projectPath = '/repo/app') {
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

export function renderSkillsPageWithClient(queryClient, projectPath = '/repo/app') {
  return render(
    <QueryClientProvider client={queryClient}>
      <SkillsPage projectPath={projectPath} />
    </QueryClientProvider>,
  );
}

export function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

export function openSkillTools() {
  fireEvent.click(screen.getByRole('button', { name: '技能库' }));
}

export function getOverviewMetric(overview, label) {
  const metric = within(overview).getByText(label).closest('div');
  if (!metric) throw new Error(`Missing overview metric: ${label}`);
  return metric;
}

beforeEach(() => {
  resetFrontendHealthForTest();
  vi.clearAllMocks();
  backend.getDashboardPage.mockResolvedValue({
    skills: [{
      name: 'backend',
      display_name: '后端',
      dir: '/repo/app/.agents/skills/backend',
      skill_file: '/repo/app/.agents/skills/backend/SKILL.md',
      description: '当你需要 Go 后端开发时使用。',
      trigger_words: ['go', 'service'],
      scope: 'project',
    }],
  });
  backend.listSkillResolutions.mockResolvedValue({ items: [] });
  backend.listSkillTools.mockResolvedValue({ tools: [] });
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
      { name: 'SKILL.md', path: '/repo/app/.agents/skills/backend/SKILL.md', is_main: true },
      { name: 'guide.md', path: '/repo/app/.agents/skills/backend/references/guide.md', is_main: false },
    ],
  });
  backend.createSkill.mockResolvedValue({ path: '/repo/app/.agents/skills/deploy/SKILL.md' });
  backend.writeSkill.mockResolvedValue({ path: '/repo/app/.agents/skills/backend/SKILL.md' });
  backend.listMCPServers.mockResolvedValue({ mcpServers: { sqlite: { enabled: false }, playwright: { enabled: false } } });
  backend.startSQLiteMCPServer.mockResolvedValue({ serverName: 'sqlite', enabled: true });
  backend.stopSQLiteMCPServer.mockResolvedValue({ serverName: 'sqlite', enabled: false });
  backend.startPlaywrightMCPServer.mockResolvedValue({ serverName: 'playwright', enabled: true });
  backend.stopPlaywrightMCPServer.mockResolvedValue({ serverName: 'playwright', enabled: false });
  backend.listDatasourceDocuments.mockResolvedValue({
    documents: [{
      documentId: 101,
      sourcePath: 'C:\\data\\source.txt',
      fileName: 'source.txt',
      extension: '.txt',
      sizeBytes: 7,
      chunkCount: 1,
      totalChars: 7,
      status: 'ready',
      contentHash: 'sha256:abc',
    }],
  });
  backend.importDatasourceLocalFile.mockResolvedValue({
    documentId: 102,
    sourcePath: 'C:\\data\\new.txt',
    fileName: 'new.txt',
    extension: '.txt',
    sizeBytes: 3,
    contentHash: 'sha256:new',
    chunkCount: 1,
    totalChars: 3,
    status: 'ready',
  });
  backend.getDatasourceDocument.mockResolvedValue({
    document: {
      documentId: 101,
      sourcePath: 'C:\\data\\source.txt',
      fileName: 'source.txt',
      extension: '.txt',
      sizeBytes: 7,
      chunkCount: 1,
      totalChars: 7,
      status: 'ready',
    },
    chunks: [{
      id: 501,
      documentId: 101,
      chunkIndex: 0,
      content: 'content',
      charCount: 7,
      byteCount: 7,
    }],
    hasMore: true,
    nextCursor: 0,
  });
  backend.listDatasourceChunks.mockResolvedValue({
    chunks: [{
      id: 502,
      documentId: 101,
      chunkIndex: 1,
      content: 'more content',
      charCount: 12,
      byteCount: 12,
    }],
    hasMore: false,
    nextCursor: 1,
  });
  backend.updateDatasourceDocument.mockResolvedValue({
    documentId: 101,
    sourcePath: 'C:\\data\\source-renamed.txt',
    fileName: 'source-renamed.txt',
    extension: '.txt',
    sizeBytes: 8,
    chunkCount: 1,
    totalChars: 7,
    status: 'ready',
  });
  backend.deleteDatasourceDocument.mockResolvedValue({ documentId: 101, deleted: true });
  backend.selectFiles.mockResolvedValue(['C:\\data\\new.pdf']);
  backend.selectDatasourceImportFile.mockResolvedValue({
    sourcePath: 'C:\\data\\new.pdf',
    pickerToken: 'picker-token',
  });
});

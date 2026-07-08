import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SkillsPage } from './SkillsPage.jsx';

const backend = vi.hoisted(() => ({
  applySkillResolution: vi.fn(),
  createDatasourceDocument: vi.fn(),
  createSkill: vi.fn(),
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

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

function openSkillTools() {
  fireEvent.click(screen.getByRole('button', { name: 'skills' }));
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
      skill_file: '/repo/app/.agent/skills/backend/SKILL.md',
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
      { name: 'SKILL.md', path: '/repo/app/.agent/skills/backend/SKILL.md', is_main: true },
      { name: 'guide.md', path: '/repo/app/.agent/skills/backend/references/guide.md', is_main: false },
    ],
  });
  backend.createSkill.mockResolvedValue({ path: '/repo/app/.agent/skills/deploy/SKILL.md' });
  backend.writeSkill.mockResolvedValue({ path: '/repo/app/.agent/skills/backend/SKILL.md' });
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

describe('SkillsPage module', () => {
  it('exports the skills page component', () => {
    expect(SkillsPage).toBeTypeOf('function');
  });
});

describe('SkillsPage backend migration', () => {
  it('renders default MCP controls and sends the start and stop RPC actions', async () => {
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: 'MCP工具' }));

    expect(await screen.findByRole('heading', { name: 'MCP工具' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Skill工具' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'skills' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '本地技能库' })).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'SQLite MCP' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Playwright MCP' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '开启 SQLite MCP' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '开启 Playwright MCP' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '关闭 SQLite MCP' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '关闭 Playwright MCP' })).not.toBeInTheDocument();
    expect(screen.queryByText('Slack')).not.toBeInTheDocument();
    const sqliteControl = screen.getByRole('region', { name: 'SQLite MCP 控制' });
    const playwrightControl = screen.getByRole('region', { name: 'Playwright MCP 控制' });

    expect(await within(sqliteControl).findByTestId('sqlite-mcp-status')).toHaveTextContent('已关闭');
    expect(await within(playwrightControl).findByTestId('playwright-mcp-status')).toHaveTextContent('已关闭');
    expect(backend.listMCPServers).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: '开启 SQLite MCP' }));
    await waitFor(() => expect(backend.startSQLiteMCPServer).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(sqliteControl).getByTestId('sqlite-mcp-status')).toHaveTextContent('已开启'));
    expect(within(sqliteControl).getByRole('status')).toHaveTextContent('SQLite MCP 已开启');
    expect(screen.getByRole('button', { name: '关闭 SQLite MCP' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '关闭 SQLite MCP' }));
    await waitFor(() => expect(backend.stopSQLiteMCPServer).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(sqliteControl).getByTestId('sqlite-mcp-status')).toHaveTextContent('已关闭'));
    expect(within(sqliteControl).getByRole('status')).toHaveTextContent('SQLite MCP 已关闭');
    expect(screen.getByRole('button', { name: '开启 SQLite MCP' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '开启 Playwright MCP' }));
    await waitFor(() => expect(backend.startPlaywrightMCPServer).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(playwrightControl).getByTestId('playwright-mcp-status')).toHaveTextContent('已开启'));
    expect(within(playwrightControl).getByRole('status')).toHaveTextContent('Playwright MCP 已开启');
    expect(screen.getByRole('button', { name: '关闭 Playwright MCP' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '关闭 Playwright MCP' }));
    await waitFor(() => expect(backend.stopPlaywrightMCPServer).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(within(playwrightControl).getByTestId('playwright-mcp-status')).toHaveTextContent('已关闭'));
    expect(within(playwrightControl).getByRole('status')).toHaveTextContent('Playwright MCP 已关闭');
    expect(screen.getByRole('button', { name: '开启 Playwright MCP' })).toBeInTheDocument();
  });

  it('renders a single stop action when an MCP server is already enabled', async () => {
    backend.listMCPServers.mockResolvedValueOnce({ mcpServers: { sqlite: { enabled: true }, playwright: { enabled: false } } });
    renderSkillsPage();

    const sqliteControl = screen.getByRole('region', { name: /SQLite MCP/ });
    const sqliteStatus = await within(sqliteControl).findByTestId('sqlite-mcp-status');
    await waitFor(() => expect(sqliteStatus).toHaveClass('is-enabled'));
    const sqliteButton = within(sqliteControl).getByRole('button', { name: /SQLite MCP/ });

    expect(sqliteStatus).toHaveClass('is-enabled');
    expect(sqliteButton).toHaveClass('is-stop');

    fireEvent.click(sqliteButton);
    await waitFor(() => expect(backend.stopSQLiteMCPServer).toHaveBeenCalledTimes(1));
    expect(backend.startSQLiteMCPServer).not.toHaveBeenCalled();
  });

  it('disables MCP controls until a project is selected', async () => {
    renderSkillsPage('');

    const sqliteControl = screen.getByRole('region', { name: /SQLite MCP/ });
    const playwrightControl = screen.getByRole('region', { name: /Playwright MCP/ });

    expect(await within(sqliteControl).findByTestId('sqlite-mcp-status')).toHaveClass('is-missing');
    expect(await within(playwrightControl).findByTestId('playwright-mcp-status')).toHaveClass('is-missing');
    expect(within(sqliteControl).getByRole('button', { name: /SQLite MCP/ })).toBeDisabled();
    expect(within(playwrightControl).getByRole('button', { name: /Playwright MCP/ })).toBeDisabled();
    expect(sqliteControl.querySelector('.mcp-tool-notice')).toHaveTextContent(/MCP/);
    expect(backend.listMCPServers).not.toHaveBeenCalled();
  });

  it('renders datasource_v2 rows and sends create, read, update, and delete actions', async () => {
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));

    expect(await screen.findByText('source.txt')).toBeInTheDocument();
    expect(backend.listDatasourceDocuments).toHaveBeenCalledWith({ limit: 200 });

    fireEvent.click(screen.getByTestId('datasource-import-button'));
    await waitFor(() => {
      expect(backend.selectDatasourceImportFile).toHaveBeenCalledWith({
        filters: [{ displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' }],
      });
      expect(backend.selectFiles).not.toHaveBeenCalled();
      expect(backend.importDatasourceLocalFile).toHaveBeenCalledWith({
        sourcePath: 'C:\\data\\new.pdf',
        pickerToken: 'picker-token',
      });
    });

    fireEvent.click(screen.getByTestId('datasource-view-101'));
    await waitFor(() => {
      expect(backend.getDatasourceDocument).toHaveBeenCalledWith({ documentId: 101 });
      expect(backend.listDatasourceChunks).toHaveBeenCalledWith({ documentId: 101, limit: 50, cursor: 0 });
    });
    const detailDialog = await screen.findByRole('dialog', { name: '数据源详情' });
    const chunks = await within(detailDialog).findAllByTestId('datasource-detail-chunk');
    expect(chunks.map((chunk) => chunk.textContent)).toEqual(['content', 'more content']);
    fireEvent.click(within(detailDialog).getByRole('button', { name: '关闭' }));

    fireEvent.click(screen.getByTestId('datasource-edit-101'));
    const editDialog = await screen.findByRole('dialog', { name: '编辑数据源' });
    fireEvent.change(within(editDialog).getByTestId('datasource-edit-source-path'), {
      target: { value: 'C:\\data\\source-renamed.txt' },
    });
    fireEvent.change(within(editDialog).getByTestId('datasource-edit-file-name'), {
      target: { value: 'source-renamed.txt' },
    });
    fireEvent.click(within(editDialog).getByTestId('datasource-edit-save'));
    await waitFor(() => {
      expect(backend.updateDatasourceDocument).toHaveBeenCalledWith(expect.objectContaining({
        documentId: 101,
        sourcePath: 'C:\\data\\source-renamed.txt',
        fileName: 'source-renamed.txt',
      }));
    });

    fireEvent.click(screen.getByTestId('datasource-delete-101'));
    const deleteDialog = await screen.findByRole('dialog', { name: '删除数据源' });
    fireEvent.click(within(deleteDialog).getByTestId('datasource-delete-confirm'));
    await waitFor(() => {
      expect(backend.deleteDatasourceDocument).toHaveBeenCalledWith({ documentId: 101 });
    });
  });

  it('renders the first datasource chunk before later chunk pages finish and appends the next page', async () => {
    const nextPage = deferred();
    backend.listDatasourceChunks.mockImplementationOnce(() => nextPage.promise);
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));
    expect(await screen.findByText('source.txt')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('datasource-view-101'));

    const detailDialog = await screen.findByRole('dialog', { name: '数据源详情' });
    const firstChunk = await within(detailDialog).findByTestId('datasource-detail-chunk');
    expect(firstChunk).toHaveTextContent('content');
    expect(within(detailDialog).queryByText('more content')).not.toBeInTheDocument();
    await waitFor(() => {
      expect(backend.listDatasourceChunks).toHaveBeenCalledWith({ documentId: 101, limit: 50, cursor: 0 });
    });

    nextPage.resolve({
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

    await waitFor(() => {
      const chunks = within(detailDialog).getAllByTestId('datasource-detail-chunk');
      expect(chunks.map((chunk) => chunk.textContent)).toEqual(['content', 'more content']);
    });
  });

  it('fails fast when a datasource chunk page reports hasMore without chunks', async () => {
    backend.getDatasourceDocument.mockResolvedValueOnce({
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
      chunks: [],
      hasMore: true,
      nextCursor: 0,
    });
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));
    expect(await screen.findByText('source.txt')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('datasource-view-101'));

    const detailDialog = await screen.findByRole('dialog', { name: '数据源详情' });
    expect(await within(detailDialog).findByRole('alert')).toHaveTextContent(
      '操作失败：datasourceV2/get returned hasMore without chunks',
    );
    expect(backend.listDatasourceChunks).not.toHaveBeenCalled();
  });

  it('frames the plugin entry around the current local skills surface', async () => {
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: 'skills' }));

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

  it('fails fast when the skills dashboard omits required skill_file', async () => {
    backend.getDashboardPage.mockResolvedValueOnce({
      skills: [{
        name: 'backend',
        display_name: '后端',
        dir: '/repo/app/.agent/skills/backend',
        description: '当你需要 Go 后端开发时使用。',
        trigger_words: ['go'],
        scope: 'project',
      }],
    });

    renderSkillsPage();
    openSkillTools();

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'skills dashboard response item 0 is missing skill_file',
    );
    expect(screen.queryByRole('heading', { name: '后端' })).not.toBeInTheDocument();
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
    expect(await screen.findByRole('alert')).toHaveTextContent('读取技能冲突失败：resolver offline');
  });

  it('keeps conflict status unresolved while project context is pending', async () => {
    renderSkillsPage('');
    openSkillTools();

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

  it('shows partial failure feedback when applying a skill resolution needs follow-up', async () => {
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
            '参考 [Docs Skill](/repo/app/.agent/skills/docs/SKILL.md) 或 [agent://thread-active](agent://thread-active)。',
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

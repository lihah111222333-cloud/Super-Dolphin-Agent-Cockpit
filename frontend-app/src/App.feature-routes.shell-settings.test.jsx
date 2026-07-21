import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  fireEvent,
  render,
  screen,
  waitFor,
  expect,
  it,
  resetClientStoreForTests,
  App,
  backend,
  openPluginsAndSkillsPage,
} = testEnv;

it('does not expose database Skill tools from the Skills navigation', async () => {
  backend.listSkillTools.mockResolvedValueOnce({
    tools: [{
      id: 7,
      name: 'Format Go',
      description: 'Run formatter',
      command: 'gofmt',
      args: ['-w', './internal/module/skill'],
      enabled: true,
    }],
  });
  render(<App />);
  await screen.findByLabelText('插件与技能');
  openPluginsAndSkillsPage();

  expect(await screen.findByRole('heading', { name: 'MCP工具' })).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Skill工具' })).not.toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'Skill工具' })).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '新增工具' })).not.toBeInTheDocument();
  expect(screen.queryByText('Format Go')).not.toBeInTheDocument();
  expect(screen.queryByText('本地技能库')).not.toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: '后端' })).not.toBeInTheDocument();
  expect(backend.listSkillTools).not.toHaveBeenCalled();
  expect(backend.getDashboardPage).not.toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });
  expect(backend.listSkillResolutions).not.toHaveBeenCalled();
  });

it('keeps composer dock pinned inside the viewport', async () => {
  resetClientStoreForTests({
    bootstrapStatus: 'ready',
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: 'thread-1',
    timelinesByThread: {
      'thread-1': Array.from({ length: 70 }, (_, index) => ({
        id: `m-${index}`,
        role: index % 2 ? 'user' : 'assistant',
        text: `message ${index}`,
        time: '2026-05-30T00:00:00Z',
      })),
    },
  });

  render(<App skipBootstrap />);

  expect(await screen.findByTestId('composer-dock')).toHaveClass('composer', 'composer--docked');
  expect(screen.getByTestId('chat-timeline')).toHaveClass('timeline');
  });

it('connects settings page build info and provider preferences to backend', async () => {
  resetClientStoreForTests({
    bootstrapStatus: 'ready',
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activePage: 'settings',
  });
  const preferenceValues = {
    stallThresholdSec: 60,
    'contextUsageAlerts.thresholds': [65, 80, 95],
    'settings.provider.active': 'codex',
    'settings.provider.codex.codexHome': '/home/test/.codex',
    'settings.provider.codex.codexInstanceKey': 'main',
    'settings.provider.codex.codexModelProvider': 'openai',
    'settings.provider.codex.model': 'gpt-5.5',
    'settings.provider.codex.effort': 'xhigh',
    'settings.provider.codex.sandbox': { type: 'workspaceWrite', writableRoots: ['/repo/app'], networkAccess: false },
  };
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve(preferenceValues[key] ?? null));

  render(<App skipBootstrap />);

  expect(await screen.findByText('Agent Orchestrator v1.2.3')).toBeInTheDocument();
  expect(screen.getByText('linux/amd64')).toBeInTheDocument();
  expect(screen.getByText('2026-05-30T07:00:00Z')).toBeInTheDocument();
  expect(screen.getByText('abc123def456')).toBeInTheDocument();
  expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'stallThresholdSec' });
  expect(backend.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.active' });

  fireEvent.change(screen.getByLabelText('统一超时阈值'), { target: { value: '120' } });
  fireEvent.change(screen.getByLabelText('Warn 阈值'), { target: { value: '70' } });
  fireEvent.change(screen.getByLabelText('Danger 阈值'), { target: { value: '85' } });
  fireEvent.change(screen.getByLabelText('Critical 阈值'), { target: { value: '96' } });
  fireEvent.click(screen.getByRole('button', { name: '保存运行阈值' }));

  await waitFor(() => {
    expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'stallThresholdSec', value: 120 });
    expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'contextUsageAlerts.thresholds', value: [70, 85, 96] });
  });

  expect(screen.queryByLabelText('Model Provider')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('Codex Home'), { target: { value: '/tmp/codex-home' } });
  fireEvent.change(screen.getByLabelText('Instance Key'), { target: { value: 'desktop-main' } });
  fireEvent.change(screen.getByLabelText('Sandbox Policy'), { target: { value: 'readOnly' } });
  fireEvent.click(screen.getByRole('button', { name: '保存 Provider 设置' }));

  await waitFor(() => {
    expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexHome', value: '/tmp/codex-home' });
    expect(backend.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.provider.codex.codexInstanceKey', value: 'desktop-main' });
    expect(backend.setPreference).toHaveBeenCalledWith({
      cwd: '/repo/app',
      key: 'settings.provider.codex.sandbox',
      value: { type: 'readOnly' },
    });
  });
  expect(backend.setPreference).not.toHaveBeenCalledWith(expect.objectContaining({
    key: 'settings.provider.codex.codexModelProvider',
  }));

  backend.getBuildInfo.mockResolvedValueOnce({
    version: 'v1.2.4',
    runtime: 'linux/amd64',
    buildTime: '2026-05-30T08:00:00Z',
    commit: 'feedface9876',
  });
  fireEvent.click(screen.getByRole('button', { name: '刷新构建信息' }));
  expect(await screen.findByText('Agent Orchestrator v1.2.4')).toBeInTheDocument();
  expect(screen.getByText('feedface9876')).toBeInTheDocument();
  });

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { QueryClient } from '@tanstack/react-query';
import { backend, renderSkillsPage, renderSkillsPageWithClient } from './SkillsPageTestSupport.jsx';

describe('SkillsPage backend migration', () => {
  it('renders default MCP controls and sends the start and stop RPC actions', async () => {
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: 'MCP工具' }));

    expect(await screen.findByRole('heading', { name: 'MCP工具' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Skill工具' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '技能库' })).toBeInTheDocument();
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

  it('keeps MCP controls actionable with explicit guidance when no project is selected', async () => {
    renderSkillsPage('');

    const sqliteControl = screen.getByRole('region', { name: /SQLite MCP/ });
    const playwrightControl = screen.getByRole('region', { name: /Playwright MCP/ });

    expect(await within(sqliteControl).findByTestId('sqlite-mcp-status')).toHaveClass('is-missing');
    expect(await within(playwrightControl).findByTestId('playwright-mcp-status')).toHaveClass('is-missing');
    const sqliteToggle = within(sqliteControl).getByRole('button', { name: /SQLite MCP/ });
    const playwrightToggle = within(playwrightControl).getByRole('button', { name: /Playwright MCP/ });
    // 未选择项目时开关不再原生禁用（保留焦点与点击能力），而是以 aria-disabled 标记。
    expect(sqliteToggle).not.toBeDisabled();
    expect(playwrightToggle).not.toBeDisabled();
    expect(sqliteToggle).toHaveAttribute('aria-disabled', 'true');
    expect(playwrightToggle).toHaveAttribute('aria-disabled', 'true');
    expect(sqliteControl.querySelector('.mcp-tool-notice')).toHaveTextContent(/MCP/);

    // 点击必须出现明确的项目引导提示，且不能触发任何 MCP RPC。
    fireEvent.click(sqliteToggle);
    expect(await screen.findByRole('status')).toHaveTextContent('请先在聊天页选择项目，再管理 SQLite MCP。');
    fireEvent.click(playwrightToggle);
    expect(await screen.findByRole('status')).toHaveTextContent('请先在聊天页选择项目，再管理 Playwright MCP。');
    expect(backend.listMCPServers).not.toHaveBeenCalled();
    expect(backend.startSQLiteMCPServer).not.toHaveBeenCalled();
    expect(backend.startPlaywrightMCPServer).not.toHaveBeenCalled();
  });

  it('does not render the removed Skill Fusion promo banner', async () => {
    renderSkillsPage();

    await within(screen.getByRole('region', { name: /SQLite MCP/ })).findByTestId('sqlite-mcp-status');
    expect(screen.queryByText('Skill Fusion')).not.toBeInTheDocument();
    expect(screen.queryByText('BETA')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Try Fusion' })).not.toBeInTheDocument();
    expect(document.querySelector('.skill-fusion-banner')).toBeNull();
  });

  it('mounts MCP tool cards on the unified themed card surface', async () => {
    renderSkillsPage();

    const sqliteControl = screen.getByRole('region', { name: /SQLite MCP/ });
    const playwrightControl = screen.getByRole('region', { name: /Playwright MCP/ });
    await within(sqliteControl).findByTestId('sqlite-mcp-status');

    // 两张 MCP 卡片使用同一套主题化卡片类，且不再携带 fusion-surface 白字渐变残留。
    expect(sqliteControl).toHaveClass('mcp-tool-card');
    expect(playwrightControl).toHaveClass('mcp-tool-card');
    expect(sqliteControl.className).toBe(playwrightControl.className);
    for (const card of document.querySelectorAll('.mcp-tool-card')) {
      expect(card).not.toHaveClass('fusion-surface');
    }
    expect(document.querySelector('.mcp-tool-icon.fusion-surface-glass')).toBeNull();
  });

  it('opens the register skill tool dialog with only real project skills selectable', async () => {
    backend.listSkillTools.mockResolvedValue({ tools: [] });
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: '注册技能工具' }));

    const dialog = await screen.findByRole('dialog', { name: '注册技能工具' });
    // 不提供任意 methodName 输入；只能从真实存在的 Skill 中选择。
    expect(within(dialog).queryByLabelText('方法名（methodName）')).not.toBeInTheDocument();
    expect(within(dialog).queryByRole('dialog', { name: '新建技能' })).not.toBeInTheDocument();
    const select = within(dialog).getByLabelText('选择技能');
    expect(select).toHaveValue('backend');
    expect(within(select).getByRole('option', { name: /后端（backend）/ })).toBeInTheDocument();
    // 描述预填自技能摘要（可编辑），enabled 保留。
    expect(within(dialog).getByLabelText('描述（description）')).toHaveValue('当你需要 Go 后端开发时使用。');
    expect(within(dialog).getByLabelText('启用状态')).toBeChecked();
    expect(backend.getDashboardPage).toHaveBeenCalledWith({ cwd: '/repo/app', page: 'skills' });
    expect(backend.listSkillTools).toHaveBeenCalledWith({ cwd: '/repo/app', keyword: '', limit: 200 });
  });

  it('registers the selected skill with a payload derived from the skill identity', async () => {
    backend.listSkillTools.mockResolvedValue({ tools: [] });
    backend.createSkillTool.mockResolvedValue({
      id: 9,
      cwd: '/repo/app',
      methodName: 'backend',
      description: '自定义描述',
      enabled: true,
      createdAt: '2026-07-17T10:00:00Z',
      updatedAt: '2026-07-17T10:00:00Z',
    });
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries');
    renderSkillsPageWithClient(queryClient);

    fireEvent.click(screen.getByRole('button', { name: '注册技能工具' }));
    const dialog = await screen.findByRole('dialog', { name: '注册技能工具' });
    fireEvent.change(within(dialog).getByLabelText('描述（description）'), { target: { value: '自定义描述' } });
    fireEvent.click(within(dialog).getByLabelText('启用状态'));
    fireEvent.click(within(dialog).getByRole('button', { name: '注册工具' }));

    await waitFor(() => expect(backend.createSkillTool).toHaveBeenCalledWith({
      cwd: '/repo/app',
      methodName: 'backend',
      description: '自定义描述',
      enabled: false,
    }));
    // methodName 必须等于所选 Skill 的稳定名称（对应 .agents/skills/<name>/SKILL.md），
    // 这是“不会注册出不可调用工具”的核心断言。
    expect(backend.createSkillTool.mock.calls[0][0].methodName).toBe('backend');
    // 保存前复查：dashboard 与工具列表各被拉取两次（打开时 + 保存前）。
    await waitFor(() => expect(backend.getDashboardPage).toHaveBeenCalledTimes(2));
    expect(backend.listSkillTools).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['skillTools', '/repo/app'] }));
    expect(await screen.findByRole('status')).toHaveTextContent('已注册工具：backend');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '注册技能工具' })).not.toBeInTheDocument());
  });

  it('blocks saving and shows the real error when the skill list fails to load', async () => {
    backend.getDashboardPage.mockRejectedValueOnce(new Error('dashboard offline'));
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: '注册技能工具' }));
    const dialog = await screen.findByRole('dialog', { name: '注册技能工具' });

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('加载技能列表失败：dashboard offline');
    expect(within(dialog).getByRole('button', { name: '注册工具' })).toBeDisabled();
    expect(backend.createSkillTool).not.toHaveBeenCalled();
  });

  it('keeps the create RPC untouched when no registrable skills exist', async () => {
    backend.getDashboardPage.mockResolvedValue({ skills: [] });
    backend.listSkillTools.mockResolvedValue({ tools: [] });
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: '注册技能工具' }));
    const dialog = await screen.findByRole('dialog', { name: '注册技能工具' });

    expect(await within(dialog).findByText('当前项目没有可注册的技能。')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: '注册工具' })).toBeDisabled();
    expect(backend.createSkillTool).not.toHaveBeenCalled();
  });

  it('excludes already registered skills from the selectable options', async () => {
    backend.listSkillTools.mockResolvedValue({
      tools: [{
        id: 1,
        cwd: '/repo/app',
        methodName: 'backend',
        description: '已注册',
        enabled: true,
        createdAt: '2026-07-17T10:00:00Z',
        updatedAt: '2026-07-17T10:00:00Z',
      }],
    });
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: '注册技能工具' }));
    const dialog = await screen.findByRole('dialog', { name: '注册技能工具' });

    expect(await within(dialog).findByText('当前项目的所有技能均已注册为工具。')).toBeInTheDocument();
    expect(within(dialog).queryByLabelText('选择技能')).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: '注册工具' })).toBeDisabled();
    expect(backend.createSkillTool).not.toHaveBeenCalled();
  });

  it('rejects duplicate registration when the skill was registered after the dialog opened', async () => {
    backend.listSkillTools
      .mockResolvedValueOnce({ tools: [] })
      .mockResolvedValue({
        tools: [{
          id: 1,
          cwd: '/repo/app',
          methodName: 'backend',
          description: '并发注册',
          enabled: true,
          createdAt: '2026-07-17T10:00:00Z',
          updatedAt: '2026-07-17T10:00:00Z',
        }],
      });
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: '注册技能工具' }));
    const dialog = await screen.findByRole('dialog', { name: '注册技能工具' });
    fireEvent.click(within(dialog).getByRole('button', { name: '注册工具' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('技能「backend」已注册为工具，无需重复注册');
    expect(backend.createSkillTool).not.toHaveBeenCalled();
  });

  it('keeps the dialog open with the real error when the backend rejects the registration', async () => {
    backend.listSkillTools.mockResolvedValue({ tools: [] });
    backend.createSkillTool.mockRejectedValue(new Error('skills/tools/create: UNIQUE constraint failed'));
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: '注册技能工具' }));
    const dialog = await screen.findByRole('dialog', { name: '注册技能工具' });
    fireEvent.click(within(dialog).getByRole('button', { name: '注册工具' }));

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('注册工具失败：skills/tools/create: UNIQUE constraint failed');
    expect(screen.getByRole('dialog', { name: '注册技能工具' })).toBeInTheDocument();
  });

  it('shows explicit project guidance from the register card when no project is selected', async () => {
    renderSkillsPage('');

    fireEvent.click(screen.getByRole('button', { name: '注册技能工具' }));

    expect(await screen.findByRole('status')).toHaveTextContent('请先在聊天页选择项目，再注册技能工具。');
    expect(screen.queryByRole('dialog', { name: '注册技能工具' })).not.toBeInTheDocument();
    expect(backend.createSkillTool).not.toHaveBeenCalled();
    expect(backend.listSkillTools).not.toHaveBeenCalled();
  });

});

import { describe, expect, it, vi } from 'vitest';
import { createSlashCommandCatalogService } from './slashCommandCatalogService.js';

const validAutomation = {
  id: 'dag-1',
  title: 'Release check',
  description: 'Check release',
  config: { prompt: 'Check the release candidate' },
};

const validTool = {
  serverName: 'lsp',
  toolName: 'lsp_edit',
  displayName: 'LSP Edit',
  description: 'Edit source',
  enabled: true,
  disabledReason: '',
};

describe('slashCommandCatalogService', () => {
  it('normalizes automation and MCP tool catalogs', async () => {
    const api = {
      getDashboardPage: vi.fn()
        .mockResolvedValueOnce({ dags: [validAutomation] }),
      listToolbridgeTools: vi.fn().mockResolvedValue({ tools: [validTool] }),
    };
    const service = createSlashCommandCatalogService(api);

    await expect(service.loadAutomations('/repo')).resolves.toEqual([expect.objectContaining({
      id: 'automation:dag-1',
      kind: 'automation',
      name: 'Release check',
      payload: { title: 'Release check', content: 'Check the release candidate' },
      disabled: false,
    })]);
    expect(api.getDashboardPage).toHaveBeenNthCalledWith(1, { cwd: '/repo', page: 'dags' });

    await expect(service.loadMCPTools('/repo')).resolves.toEqual([{
      id: 'mcp_tool:lsp:lsp_edit',
      kind: 'mcp_tool',
      name: 'lsp_edit',
      label: 'LSP Edit',
      description: 'Edit source',
      keywords: ['lsp'],
      payload: {
        capability: {
          kind: 'mcp_tool',
          key: 'mcp_tool:lsp:lsp_edit',
          name: 'lsp_edit',
          label: 'LSP Edit',
          serverName: 'lsp',
        },
      },
      disabled: false,
      disabledReason: '',
    }]);
    expect(api.listToolbridgeTools).toHaveBeenCalledWith({ cwd: '/repo' });
  });

  it.each([
    {
      label: 'automation',
      method: 'loadAutomations',
      api: {
        getDashboardPage: vi.fn().mockResolvedValue({
          dags: [validAutomation, { ...validAutomation, id: '' }],
        }),
      },
    },
    {
      label: 'MCP tool',
      method: 'loadMCPTools',
      api: {
        listToolbridgeTools: vi.fn().mockResolvedValue({
          tools: [validTool, { ...validTool, toolName: '' }],
        }),
      },
    },
  ])('rejects the whole $label catalog when one item is malformed', async ({ api, method }) => {
    const service = createSlashCommandCatalogService(api);
    await expect(service[method]('/repo')).rejects.toThrow();
  });

  it('keeps an automation without executable content visible but disabled', async () => {
    const api = {
      getDashboardPage: vi.fn().mockResolvedValue({
        dags: [{
          id: 'dag-empty',
          title: 'Metadata only',
          description: 'No task body',
          config: {},
        }],
      }),
    };
    const service = createSlashCommandCatalogService(api);

    await expect(service.loadAutomations('/repo')).resolves.toEqual([
      expect.objectContaining({
        id: 'automation:dag-empty',
        kind: 'automation',
        disabled: true,
        disabledReason: expect.stringMatching(/\S/u),
        payload: { title: 'Metadata only', content: '' },
      }),
    ]);
  });

  it('uses the documented automation content priority without description fallback', async () => {
    const api = {
      getDashboardPage: vi.fn().mockResolvedValue({
        dags: [{
          id: 'dag-priority',
          title: 'Priority',
          description: 'Description is not executable',
          prompt: 'raw prompt',
          command_template: 'snake template',
          commandTemplate: 'camel template',
          config: { prompt: 'config prompt', command: 'config command' },
        }],
      }),
    };
    const service = createSlashCommandCatalogService(api);

    await expect(service.loadAutomations('/repo')).resolves.toEqual([
      expect.objectContaining({ payload: { title: 'Priority', content: 'raw prompt' } }),
    ]);
  });

});

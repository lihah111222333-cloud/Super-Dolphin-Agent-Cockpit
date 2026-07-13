import { describe, expect, it, vi } from 'vitest';
import { createSlashCommandCatalogService } from './slashCommandCatalogService.js';

const validSkill = {
  name: 'review',
  display_name: 'Code Review',
  dir: '/repo/.agents/skills/review',
  scope: 'project',
  personal_type: '',
  description: 'Review code',
  summary: '',
  trigger_words: ['audit'],
  force_words: ['review'],
};

const validPrompt = {
  id: 'prompt-1',
  name: 'Review prompt',
  description: 'Review carefully',
  enabled: true,
};

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
  it('normalizes all project catalogs and keeps prompt content lazy', async () => {
    const api = {
      getDashboardPage: vi.fn()
        .mockResolvedValueOnce({ skills: [validSkill] })
        .mockResolvedValueOnce({ dags: [validAutomation] }),
      getDashboardPrompts: vi.fn().mockResolvedValue({ prompts: [validPrompt] }),
      getPrompt: vi.fn().mockResolvedValue({
        prompt: { content: 'Review this change carefully.' },
      }),
      listToolbridgeTools: vi.fn().mockResolvedValue({ tools: [validTool] }),
    };
    const service = createSlashCommandCatalogService(api);

    await expect(service.loadSkills('/repo')).resolves.toEqual([{
      id: 'skill:project::review:/repo/.agents/skills/review',
      kind: 'skill',
      name: 'review',
      label: 'Code Review',
      description: 'Review code',
      keywords: ['audit', 'review'],
      payload: {
        capability: {
          kind: 'skill',
          key: 'skill:project::review:/repo/.agents/skills/review',
          name: 'review',
          label: 'Code Review',
          ref: {
            name: 'review',
            scope: 'project',
            personalType: '',
            path: '/repo/.agents/skills/review',
          },
        },
      },
      disabled: false,
      disabledReason: '',
    }]);
    expect(api.getDashboardPage).toHaveBeenNthCalledWith(1, { cwd: '/repo', page: 'skills' });

    await expect(service.loadPrompts('/repo')).resolves.toEqual([expect.objectContaining({
      id: 'prompt:prompt-1',
      kind: 'prompt',
      name: 'Review prompt',
      payload: { promptId: 'prompt-1' },
      disabled: false,
    })]);
    expect(api.getDashboardPrompts).toHaveBeenCalledWith({ cwd: '/repo' });
    expect(api.getPrompt).not.toHaveBeenCalled();

    await expect(service.loadPromptContent('/repo', 'prompt-1'))
      .resolves.toBe('Review this change carefully.');
    expect(api.getPrompt).toHaveBeenCalledWith({ cwd: '/repo', id: 'prompt-1' });

    await expect(service.loadAutomations('/repo')).resolves.toEqual([expect.objectContaining({
      id: 'automation:dag-1',
      kind: 'automation',
      name: 'Release check',
      payload: { title: 'Release check', content: 'Check the release candidate' },
      disabled: false,
    })]);
    expect(api.getDashboardPage).toHaveBeenNthCalledWith(2, { cwd: '/repo', page: 'dags' });

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
      label: 'skill',
      method: 'loadSkills',
      api: {
        getDashboardPage: vi.fn().mockResolvedValue({
          skills: [validSkill, { ...validSkill, name: '' }],
        }),
      },
    },
    {
      label: 'prompt',
      method: 'loadPrompts',
      api: {
        getDashboardPrompts: vi.fn().mockResolvedValue({
          prompts: [validPrompt, { ...validPrompt, id: '' }],
        }),
      },
    },
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

  it('rejects malformed category wrappers instead of treating them as empty', async () => {
    const service = createSlashCommandCatalogService({
      getDashboardPage: vi.fn().mockResolvedValue({ skills: null }),
    });

    await expect(service.loadSkills('/repo')).rejects.toThrow(
      'slash command skills response skills must be an array',
    );
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

  it.each([
    ['content', { prompt: { content: ' first body ' } }, 'first body'],
    ['prompt_text', { prompt: { content: ' ', prompt_text: ' second body ' } }, 'second body'],
    ['promptText', { prompt: { promptText: ' third body ' } }, 'third body'],
  ])('loads prompt body from %s', async (_field, response, expected) => {
    const api = { getPrompt: vi.fn().mockResolvedValue(response) };
    const service = createSlashCommandCatalogService(api);

    await expect(service.loadPromptContent('/repo', 'prompt-1')).resolves.toBe(expected);
  });

  it('rejects a prompt response without an executable body', async () => {
    const api = {
      getPrompt: vi.fn().mockResolvedValue({ prompt: { content: ' ', description: 'not a body' } }),
    };
    const service = createSlashCommandCatalogService(api);

    await expect(service.loadPromptContent('/repo', 'prompt-1')).rejects.toThrow(
      'slash command prompt response content is required',
    );
  });
});

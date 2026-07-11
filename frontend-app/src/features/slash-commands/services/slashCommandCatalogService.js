import { adaptAutomationCommands } from '../adapters/automationSlashCommandAdapter.js';
import { adaptMCPToolCommands } from '../adapters/mcpToolSlashCommandAdapter.js';
import {
  adaptPromptCommands,
  promptContentFromResponse,
} from '../adapters/promptSlashCommandAdapter.js';
import { adaptSkillCommands } from '../adapters/skillSlashCommandAdapter.js';
import {
  getDashboardPage,
  getDashboardPrompts,
  getPrompt,
  listToolbridgeTools,
} from '../../../shared/api/backendApi.js';

const defaultApi = Object.freeze({
  getDashboardPage,
  getDashboardPrompts,
  getPrompt,
  listToolbridgeTools,
});

export function createSlashCommandCatalogService(api = defaultApi) {
  return Object.freeze({
    async loadSkills(cwd) {
      return adaptSkillCommands(await api.getDashboardPage({ cwd, page: 'skills' }));
    },
    async loadPrompts(cwd) {
      return adaptPromptCommands(await api.getDashboardPrompts({ cwd }));
    },
    async loadAutomations(cwd) {
      return adaptAutomationCommands(await api.getDashboardPage({ cwd, page: 'dags' }));
    },
    async loadMCPTools(cwd) {
      return adaptMCPToolCommands(await api.listToolbridgeTools({ cwd }));
    },
    async loadPromptContent(cwd, promptId) {
      return promptContentFromResponse(await api.getPrompt({ cwd, id: promptId }));
    },
  });
}

export const slashCommandCatalogService = createSlashCommandCatalogService();

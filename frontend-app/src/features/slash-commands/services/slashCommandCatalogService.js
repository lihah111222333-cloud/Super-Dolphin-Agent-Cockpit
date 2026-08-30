import { adaptAutomationCommands } from '../adapters/automationSlashCommandAdapter.js';
import { adaptMCPToolCommands } from '../adapters/mcpToolSlashCommandAdapter.js';
import {
  getDashboardPage,
  listToolbridgeTools,
} from '../../../shared/api/backendApi.js';

const defaultApi = Object.freeze({
  getDashboardPage,
  listToolbridgeTools,
});

export function createSlashCommandCatalogService(api = defaultApi) {
  return Object.freeze({
    async loadAutomations(cwd) {
      return adaptAutomationCommands(await api.getDashboardPage({ cwd, page: 'dags' }));
    },
    async loadMCPTools(cwd) {
      return adaptMCPToolCommands(await api.listToolbridgeTools({ cwd }));
    },
  });
}

export const slashCommandCatalogService = createSlashCommandCatalogService();

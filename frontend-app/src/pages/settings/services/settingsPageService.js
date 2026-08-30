import {
  applyModelProvider as applyModelProviderBackend,
  checkAppUpdate as checkAppUpdateBackend,
  getBuildInfo as getBuildInfoBackend,
  getVideoApiKey as getVideoApiKeyBackend,
  installLatestAppUpdate as installLatestAppUpdateBackend,
  listDashboardLogs as listDashboardLogsBackend,
  listModelProviders as listModelProvidersBackend,
  readBuiltinTools as readBuiltinToolsBackend,
  readConfig as readConfigBackend,
  saveModelProviders as saveModelProvidersBackend,
  setPreference as setPreferenceBackend,
  setVideoApiKey as setVideoApiKeyBackend,
  writeBuiltinTool as writeBuiltinToolBackend,
} from '../../../shared/api/backendApi.js';
import { getValidatedPreference } from '../../../shared/api/preferenceResponseGuards.js';

const settingsPageService = Object.freeze({
  applyModelProvider: (payload) => applyModelProviderBackend(payload),
  checkAppUpdate: (payload) => checkAppUpdateBackend(payload),
  getBuildInfo: (payload) => getBuildInfoBackend(payload),
  getPreference: (payload, options) => getValidatedPreference(payload, options),
  getVideoApiKey: (payload) => getVideoApiKeyBackend(payload),
  installLatestAppUpdate: (payload) => installLatestAppUpdateBackend(payload),
  listDashboardLogs: (payload) => listDashboardLogsBackend(payload),
  listModelProviders: (payload) => listModelProvidersBackend(payload),
  readBuiltinTools: (payload) => readBuiltinToolsBackend(payload),
  readConfig: (payload) => readConfigBackend(payload),
  saveModelProviders: (payload) => saveModelProvidersBackend(payload),
  setPreference: (payload) => setPreferenceBackend(payload),
  setVideoApiKey: (payload) => setVideoApiKeyBackend(payload),
  writeBuiltinTool: (payload) => writeBuiltinToolBackend(payload),
});

export { settingsPageService };

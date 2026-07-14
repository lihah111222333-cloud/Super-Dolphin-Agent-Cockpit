import {
  applyModelProvider as applyModelProviderBackend,
  checkAppUpdate as checkAppUpdateBackend,
  copyTextToClipboard as copyTextToClipboardBackend,
  getBuildInfo as getBuildInfoBackend,
  getVideoApiKey as getVideoApiKeyBackend,
  installLatestAppUpdate as installLatestAppUpdateBackend,
  listDashboardLogs as listDashboardLogsBackend,
  listModelProviders as listModelProvidersBackend,
  readBuiltinTools as readBuiltinToolsBackend,
  readConfig as readConfigBackend,
  readLspPromptHint as readLspPromptHintBackend,
  saveModelProviders as saveModelProvidersBackend,
  setPreference as setPreferenceBackend,
  setVideoApiKey as setVideoApiKeyBackend,
  writeBuiltinTool as writeBuiltinToolBackend,
  writeLspPromptHint as writeLspPromptHintRpc,
} from '../../../shared/api/backendApi.js';
import { getValidatedPreference } from '../../../shared/api/preferenceResponseGuards.js';

const settingsPageService = Object.freeze({
  applyModelProvider: (payload) => applyModelProviderBackend(payload),
  checkAppUpdate: (payload) => checkAppUpdateBackend(payload),
  copyTextToClipboard: (text) => copyTextToClipboardBackend(text),
  getBuildInfo: (payload) => getBuildInfoBackend(payload),
  getPreference: (payload, options) => getValidatedPreference(payload, options),
  getVideoApiKey: (payload) => getVideoApiKeyBackend(payload),
  installLatestAppUpdate: (payload) => installLatestAppUpdateBackend(payload),
  listDashboardLogs: (payload) => listDashboardLogsBackend(payload),
  listModelProviders: (payload) => listModelProvidersBackend(payload),
  readBuiltinTools: (payload) => readBuiltinToolsBackend(payload),
  readConfig: (payload) => readConfigBackend(payload),
  readLspPromptHint: (payload) => readLspPromptHintBackend(payload),
  saveModelProviders: (payload) => saveModelProvidersBackend(payload),
  setPreference: (payload) => setPreferenceBackend(payload),
  setVideoApiKey: (payload) => setVideoApiKeyBackend(payload),
  writeBuiltinTool: (payload) => writeBuiltinToolBackend(payload),
  writeLspPromptHint: (payload) => writeLspPromptHintRpc(payload),
});

export { settingsPageService };

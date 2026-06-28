import {
  applyModelProvider as applyModelProviderBackend,
  checkAppUpdate as checkAppUpdateBackend,
  copyTextToClipboard as copyTextToClipboardBackend,
  getBuildInfo as getBuildInfoBackend,
  getPreference as getPreferenceBackend,
  getVideoApiKey as getVideoApiKeyBackend,
  installLatestAppUpdate as installLatestAppUpdateBackend,
  listDashboardLogs as listDashboardLogsBackend,
  listMCPToolLifecycleStates as listMCPToolLifecycleStatesBackend,
  listMCPServers as listMCPServersBackend,
  listModelProviders as listModelProvidersBackend,
  readBuiltinTools as readBuiltinToolsBackend,
  readConfig as readConfigBackend,
  readLspPromptHint as readLspPromptHintBackend,
  saveModelProviders as saveModelProvidersBackend,
  setPreference as setPreferenceBackend,
  setVideoApiKey as setVideoApiKeyBackend,
  upsertMCPToolLifecycleState as upsertMCPToolLifecycleStateBackend,
  writeBuiltinTool as writeBuiltinToolBackend,
  writeLspPromptHint as writeLspPromptHintRpc,
} from '../../../shared/api/backendApi.js';

export function applyModelProvider(payload) {
  return applyModelProviderBackend(payload);
}

export function checkAppUpdate(payload) {
  return checkAppUpdateBackend(payload);
}

export function copyTextToClipboard(text) {
  return copyTextToClipboardBackend(text);
}

export function getBuildInfo(payload) {
  return getBuildInfoBackend(payload);
}

export function getPreference(payload) {
  return getPreferenceBackend(payload);
}

export function getVideoApiKey(payload) {
  return getVideoApiKeyBackend(payload);
}

export function installLatestAppUpdate(payload) {
  return installLatestAppUpdateBackend(payload);
}

export function listDashboardLogs(payload) {
  return listDashboardLogsBackend(payload);
}

export function listMCPToolLifecycleStates(payload) {
  return listMCPToolLifecycleStatesBackend(payload);
}

export function listMCPServers(payload) {
  return listMCPServersBackend(payload);
}

export function listModelProviders(payload) {
  return listModelProvidersBackend(payload);
}

export function readBuiltinTools(payload) {
  return readBuiltinToolsBackend(payload);
}

export function readConfig(payload) {
  return readConfigBackend(payload);
}

export function readLspPromptHint(payload) {
  return readLspPromptHintBackend(payload);
}

export function saveModelProviders(payload) {
  return saveModelProvidersBackend(payload);
}

export function setPreference(payload) {
  return setPreferenceBackend(payload);
}

export function setVideoApiKey(payload) {
  return setVideoApiKeyBackend(payload);
}

export function upsertMCPToolLifecycleState(payload) {
  return upsertMCPToolLifecycleStateBackend(payload);
}

export function writeBuiltinTool(payload) {
  return writeBuiltinToolBackend(payload);
}

export function writeLspPromptHint(payload) {
  return writeLspPromptHintRpc(payload);
}

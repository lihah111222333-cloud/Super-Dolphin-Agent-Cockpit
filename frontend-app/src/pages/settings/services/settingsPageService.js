import {
  checkAppUpdate as checkAppUpdateBackend,
  copyTextToClipboard as copyTextToClipboardBackend,
  getBuildInfo as getBuildInfoBackend,
  getPreference as getPreferenceBackend,
  getVideoApiKey as getVideoApiKeyBackend,
  installLatestAppUpdate as installLatestAppUpdateBackend,
  listDashboardLogs as listDashboardLogsBackend,
  readBuiltinTools as readBuiltinToolsBackend,
  readConfig as readConfigBackend,
  readLspPromptHint as readLspPromptHintBackend,
  setPreference as setPreferenceBackend,
  setVideoApiKey as setVideoApiKeyBackend,
  writeBuiltinTool as writeBuiltinToolBackend,
  writeLspPromptHint as writeLspPromptHintRpc,
} from '../../../shared/api/backendApi.js';

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

export function readBuiltinTools(payload) {
  return readBuiltinToolsBackend(payload);
}

export function readConfig(payload) {
  return readConfigBackend(payload);
}

export function readLspPromptHint(payload) {
  return readLspPromptHintBackend(payload);
}

export function setPreference(payload) {
  return setPreferenceBackend(payload);
}

export function setVideoApiKey(payload) {
  return setVideoApiKeyBackend(payload);
}

export function writeBuiltinTool(payload) {
  return writeBuiltinToolBackend(payload);
}

export function writeLspPromptHint(payload) {
  return writeLspPromptHintRpc(payload);
}

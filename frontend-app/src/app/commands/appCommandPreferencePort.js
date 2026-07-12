import { getPreference, setPreference } from '../../shared/api/backendApi.js';
import { SHORTCUT_PREFERENCE_KEY } from '../../features/shortcut-settings/model/shortcutSettingsModel.js';

function assertAppCommandPreferenceKey(params) {
  if (params?.key !== SHORTCUT_PREFERENCE_KEY) {
    throw new Error(`unsupported app command preference key: ${String(params?.key)}`);
  }
}

function getAppCommandPreference(params) {
  assertAppCommandPreferenceKey(params);
  return getPreference(params);
}

function setAppCommandPreference(params) {
  assertAppCommandPreferenceKey(params);
  return setPreference(params);
}

export const appCommandPreferencePort = Object.freeze({
  getPreference: getAppCommandPreference,
  setPreference: setAppCommandPreference,
});

import {
  commitPromptIntent as commitPromptIntentBackend,
  copyTextToClipboard as copyTextToClipboardBackend,
  deletePrompt as deletePromptBackend,
  discardPromptIntent as discardPromptIntentBackend,
  draftPromptIntent as draftPromptIntentBackend,
  dryRunPromptIntent as dryRunPromptIntentBackend,
  getDashboardPrompts as getDashboardPromptsBackend,
  getPersonalizationProfile as getPersonalizationProfileBackend,
  getPrompt as getPromptBackend,
  listPromptAssets as listPromptAssetsBackend,
  savePersonalizationProfile as savePersonalizationProfileBackend,
  setPreference as setPreferenceBackend,
  writePrompt as writePromptBackend,
} from '../../../shared/api/backendApi.js';
import { getValidatedPreference } from '../../../shared/api/preferenceResponseGuards.js';

const defaultPromptPageApi = Object.freeze({
  commitPromptIntent: commitPromptIntentBackend,
  copyTextToClipboard: copyTextToClipboardBackend,
  deletePrompt: deletePromptBackend,
  discardPromptIntent: discardPromptIntentBackend,
  draftPromptIntent: draftPromptIntentBackend,
  dryRunPromptIntent: dryRunPromptIntentBackend,
  getDashboardPrompts: getDashboardPromptsBackend,
  getPersonalizationProfile: getPersonalizationProfileBackend,
  getPreference: getValidatedPreference,
  getPrompt: getPromptBackend,
  listPromptAssets: listPromptAssetsBackend,
  savePersonalizationProfile: savePersonalizationProfileBackend,
  setPreference: setPreferenceBackend,
  writePrompt: writePromptBackend,
});

function textValue(value) {
  return typeof value === 'string' ? value.trim() : '';
}

function requirePayload(payload, methodName) {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new Error(`${methodName} payload is required`);
  }
  return payload;
}

function requireText(value, fieldName) {
  const text = textValue(value);
  if (!text) throw new Error(`${fieldName} is required`);
  return text;
}

function requireCwd(payload, methodName) {
  requireText(requirePayload(payload, methodName).cwd, 'cwd');
}

function requirePromptId(payload, methodName) {
  requireText(requirePayload(payload, methodName).id, 'id');
}

function requireWritablePromptKey(payload, methodName) {
  const request = requirePayload(payload, methodName);
  if (!textValue(request.id) && !textValue(request.key)) throw new Error('id or key is required');
}

function requireDraftKey(payload, methodName) {
  requireText(requirePayload(payload, methodName).draftKey, 'draftKey');
}

function requireProfile(payload) {
  const request = requirePayload(payload, 'savePersonalizationProfile');
  if (!request.profile || typeof request.profile !== 'object' || Array.isArray(request.profile)) {
    throw new Error('profile is required');
  }
}

function createPromptPageService(api = defaultPromptPageApi) {
  return {
    commitPromptIntent(payload) {
      requireCwd(payload, 'commitPromptIntent');
      requireDraftKey(payload, 'commitPromptIntent');
      return api.commitPromptIntent(payload);
    },
    copyTextToClipboard(text) {
      requireText(text, 'text');
      return api.copyTextToClipboard(text);
    },
    deletePrompt(payload) {
      requireCwd(payload, 'deletePrompt');
      requirePromptId(payload, 'deletePrompt');
      return api.deletePrompt(payload);
    },
    discardPromptIntent(payload) {
      requireCwd(payload, 'discardPromptIntent');
      requireDraftKey(payload, 'discardPromptIntent');
      return api.discardPromptIntent(payload);
    },
    draftPromptIntent(payload) {
      requireCwd(payload, 'draftPromptIntent');
      requireText(requirePayload(payload, 'draftPromptIntent').kind, 'kind');
      requireText(payload.rawInput, 'rawInput');
      return api.draftPromptIntent(payload);
    },
    dryRunPromptIntent(payload) {
      requireCwd(payload, 'dryRunPromptIntent');
      requireDraftKey(payload, 'dryRunPromptIntent');
      requireText(payload.question, 'question');
      return api.dryRunPromptIntent(payload);
    },
    getDashboardPrompts(payload) {
      requireCwd(payload, 'getDashboardPrompts');
      return api.getDashboardPrompts(payload);
    },
    getPersonalizationProfile(payload) {
      requireCwd(payload, 'getPersonalizationProfile');
      return api.getPersonalizationProfile(payload);
    },
    getPreference(payload) {
      requireCwd(payload, 'getPreference');
      requireText(requirePayload(payload, 'getPreference').key, 'key');
      return api.getPreference(payload);
    },
    getPrompt(payload) {
      requireCwd(payload, 'getPrompt');
      requirePromptId(payload, 'getPrompt');
      return api.getPrompt(payload);
    },
    listPromptAssets(payload) {
      requireCwd(payload, 'listPromptAssets');
      return api.listPromptAssets(payload);
    },
    savePersonalizationProfile(payload) {
      requireCwd(payload, 'savePersonalizationProfile');
      requireProfile(payload);
      return api.savePersonalizationProfile(payload);
    },
    setPreference(payload) {
      requireCwd(payload, 'setPreference');
      requireText(requirePayload(payload, 'setPreference').key, 'key');
      return api.setPreference(payload);
    },
    writePrompt(payload) {
      requireCwd(payload, 'writePrompt');
      requireWritablePromptKey(payload, 'writePrompt');
      return api.writePrompt(payload);
    },
  };
}

const promptPageService = createPromptPageService();
const {
  commitPromptIntent,
  copyTextToClipboard,
  deletePrompt,
  discardPromptIntent,
  draftPromptIntent,
  dryRunPromptIntent,
  getDashboardPrompts,
  getPersonalizationProfile,
  getPreference,
  getPrompt,
  listPromptAssets,
  savePersonalizationProfile,
  setPreference,
  writePrompt,
} = promptPageService;

export {
  commitPromptIntent,
  copyTextToClipboard,
  createPromptPageService,
  deletePrompt,
  discardPromptIntent,
  draftPromptIntent,
  dryRunPromptIntent,
  getDashboardPrompts,
  getPersonalizationProfile,
  getPreference,
  getPrompt,
  listPromptAssets,
  promptPageService,
  savePersonalizationProfile,
  setPreference,
  writePrompt,
};

import { setPreference, setThreadConfig } from '../../../../../../shared/api/backendApi.js';
import {
  normalizeActiveProviderName,
  normalizeProviderConfigValue,
  normalizeProviderRuntimeConfig,
} from '../../providerRuntimeConfig.js';
import { providerPreferenceKey } from '../../providerPreferences.js';
import {
  DEFAULT_PROVIDER,
  normalizeProviderName,
  normalizeThreadConfig,
} from '../clientStoreUtils.js';
import { threadConfigTargetIdForState } from '../clientStoreRuntimeThreadModel.js';
import { actionNotice } from './clientStoreActionNotice.js';

function hasComposerConfigKey(config, key) {
  return Object.prototype.hasOwnProperty.call(config, key);
}

function composerConfigRequestedThreadId(config, state) {
  const hasThreadTarget = hasComposerConfigKey(config, 'threadId') || hasComposerConfigKey(config, 'thread_id');
  return hasThreadTarget ? (config.threadId || config.thread_id) : state.activeThreadId;
}

async function composerModelConfigTarget(config, state, loadThreadConfig) {
  const threadId = threadConfigTargetIdForState(state, composerConfigRequestedThreadId(config, state));
  const existingConfig = threadId ? state.threadConfigByThread[threadId] : null;
  return {
    threadId,
    threadConfig: existingConfig || (threadId ? await loadThreadConfig(threadId) : null),
    hasModel: hasComposerConfigKey(config, 'model'),
    hasEffort: hasComposerConfigKey(config, 'effort'),
    nextModel: normalizeProviderConfigValue(config.model),
    nextEffort: normalizeProviderConfigValue(config.effort),
  };
}

async function saveThreadComposerModelConfig(target, set, addWarning) {
  const provider = normalizeProviderName(target.threadConfig.provider) || DEFAULT_PROVIDER;
  set({ threadConfigSaving: true });
  try {
    const saved = await setThreadConfig({
      threadId: target.threadId,
      model: target.hasModel
        ? target.nextModel
        : normalizeProviderConfigValue(target.threadConfig.override.model),
      effort: target.hasEffort
        ? target.nextEffort
        : normalizeProviderConfigValue(target.threadConfig.override.effort),
    });
    const normalized = normalizeThreadConfig(saved, target.threadId, provider);
    set((current) => ({
      threadConfigByThread: {
        ...current.threadConfigByThread,
        [target.threadId]: normalized,
      },
      threadConfigSaving: false,
      actionNotice: actionNotice('线程配置已保存，下次发送生效。', 'success'),
    }));
    return true;
  }
  catch (error) {
    set({
      threadConfigSaving: false,
      actionNotice: actionNotice('线程配置保存失败，请重试。', 'error'),
    });
    addWarning('error', 'thread.config.set.failed', {
      threadId: target.threadId,
      error: 'action failure; see Health diagnostic ID',
    });
    throw error;
  }
}

async function saveGlobalComposerModelConfig(cwd, state, target, set, notifyRPCFailure) {
  const provider = normalizeActiveProviderName(state.provider, 'provider.config') || DEFAULT_PROVIDER;
  const current = state.providerConfig || normalizeProviderRuntimeConfig({}, provider);
  const value = normalizeProviderRuntimeConfig({
    model: target.hasModel ? target.nextModel || current.model : current.model,
    effort: target.hasEffort ? target.nextEffort || current.effort : current.effort,
    codexModelProvider: current.codexModelProvider,
  }, provider);
  try {
    await setPreference({
      cwd,
      key: providerPreferenceKey(provider, 'model'),
      value: value.model,
    });
    await setPreference({
      cwd,
      key: providerPreferenceKey(provider, 'effort'),
      value: value.effort,
    });
    set({
      providerConfig: value,
      actionNotice: actionNotice('全局模型配置已保存', 'success'),
    });
    return true;
  }
  catch (error) {
    notifyRPCFailure('全局模型配置保存', 'provider.config.save.failed', error, { provider });
    throw error;
  }
}

const composerModelActionDeps = {
  actionNotice,
  composerModelConfigTarget,
  normalizeThreadConfig,
  saveGlobalComposerModelConfig,
  saveThreadComposerModelConfig,
  setThreadConfig: (payload) => setThreadConfig(payload),
  threadConfigTargetIdForState,
};

const composerModelProviderActionDeps = {
  actionNotice,
  defaultProvider: DEFAULT_PROVIDER,
  normalizeProviderRuntimeConfig,
  providerPreferenceKey,
  setPreference: (payload) => setPreference(payload),
};

export {
  composerConfigRequestedThreadId,
  composerModelActionDeps,
  composerModelProviderActionDeps,
  composerModelConfigTarget,
  saveGlobalComposerModelConfig,
  saveThreadComposerModelConfig,
};

import {
  appendUniqueAttachments,
  attachmentKey,
  createImageFileAttachment,
  droppedFilePath,
  fileListOf,
  fileLooksImage,
  normalizeAttachment,
  normalizeFileAttachment,
} from '../../composer/composerAttachments.js';
import {
  addComposerCapability,
  reconcileComposerCapabilities,
  removeComposerCapability,
} from '../../capabilities/composerCapabilities.js';
import { saveClipboardImage, selectFiles } from '../../../../../shared/api/backendApi.js';
import { recoveryActionMessageFromRPCError } from '../../../../../shared/recovery/recoveryFailure.js';
import { requireProviderPreferenceValue } from '../providerRuntimeConfig.js';
import { normalizeString, resolveLaunchPreferences } from './clientStoreUtils.js';
import { actionNotice, actionNoticeRuntimeFields } from './send/clientStoreActionNotice.js';
import {
  composerConfigRequestedThreadId,
  composerModelActionDeps,
  composerModelProviderActionDeps,
  composerModelConfigTarget,
  saveGlobalComposerModelConfig,
  saveThreadComposerModelConfig,
} from './send/clientStoreComposerModelActions.js';
import { addForkThreadState, forkActionDeps, loadForkSharedFiles } from './send/clientStoreForkActions.js';
import {
  createDashboardCommandRequest,
  createLaunchIntentId,
  createSendDraftRequest,
  forkSourceThread,
  freshThreadRetryRequest,
  sendDraftThreadName,
} from './send/clientStoreSendInput.js';
import {
  deleteProvisionalThreadAfterSendFailure,
  isCodexIdentityAutoResumeError,
  isStoppedThreadTurnStartError,
  startNewDraftThread,
  startTurnWithStoppedThreadRecovery,
} from './send/clientStoreSendLaunch.js';
import {
  createdThreadIdForSendRollback,
  optimisticSendDraftState,
  optimisticSendThreads,
  promotedDraftThreadState,
  rollbackSendDraftState,
  saveFailedSendDraftSnapshot,
  sendRollbackRestoresVisibleComposer,
} from './send/clientStoreSendState.js';

// 保留 store 装配的稳定入口；各职责由上面的唯一 owner 模块实现。
const imageFileAttachment = createImageFileAttachment({ saveClipboardImage });

const composerAttachmentActionDeps = {
  actionNotice,
  appendUniqueAttachments,
  attachmentKey,
  droppedFilePath,
  fileListOf,
  fileLooksImage,
  imageFileAttachment,
  normalizeAttachment,
  normalizeFileAttachment,
  normalizeString,
  selectFiles: () => selectFiles(),
};

const composerActionDeps = {
  attachment: composerAttachmentActionDeps,
  capability: {
    addComposerCapability,
    reconcileComposerCapabilities,
    removeComposerCapability,
  },
  model: composerModelActionDeps,
  modelProvider: {
    ...composerModelProviderActionDeps,
    requireProviderPreferenceValue,
  },
  send: {
    createSendDraftRequest,
    createdThreadIdForSendRollback,
    deleteProvisionalThreadAfterSendFailure,
    freshThreadRetryRequest,
    isCodexIdentityAutoResumeError,
    optimisticSendDraftState,
    promotedDraftThreadState,
    resolveLaunchPreferences,
    rollbackSendDraftState,
    recoveryActionMessageFromRPCError,
    saveFailedSendDraftSnapshot,
    sendRollbackRestoresVisibleComposer,
    startNewDraftThread,
    startTurnWithStoppedThreadRecovery,
  },
};

function compareSequence(left, right) {
  try {
    const a = BigInt(normalizeString(left) || '0');
    const b = BigInt(normalizeString(right) || '0');
    if (a === b) return 0;
    return a < b ? -1 : 1;
  }
  catch {
    return 0;
  }
}

export {
  actionNotice,
  actionNoticeRuntimeFields,
  addForkThreadState,
  composerActionDeps,
  composerAttachmentActionDeps,
  compareSequence,
  composerConfigRequestedThreadId,
  composerModelConfigTarget,
  createDashboardCommandRequest,
  createLaunchIntentId,
  createSendDraftRequest,
  createdThreadIdForSendRollback,
  deleteProvisionalThreadAfterSendFailure,
  forkActionDeps,
  forkSourceThread,
  freshThreadRetryRequest,
  imageFileAttachment,
  isCodexIdentityAutoResumeError,
  isStoppedThreadTurnStartError,
  loadForkSharedFiles,
  optimisticSendDraftState,
  optimisticSendThreads,
  promotedDraftThreadState,
  rollbackSendDraftState,
  recoveryActionMessageFromRPCError,
  saveFailedSendDraftSnapshot,
  saveGlobalComposerModelConfig,
  saveThreadComposerModelConfig,
  sendDraftThreadName,
  sendRollbackRestoresVisibleComposer,
  startNewDraftThread,
  startTurnWithStoppedThreadRecovery,
};

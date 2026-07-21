import {
  deleteThread as deleteThreadRPC,
  recoverThread,
} from '../../../../../../shared/api/backendApi.js';
import { sessionApi } from '../../../../../../shared/api/sessionApi.js';
import { firstOptionalPresent, optionalTextField } from '../../../contractStoreModel.js';
import { normalizeThreadIdentity } from '../../threadIdentity.js';
import { normalizeString } from '../clientStoreUtils.js';
import { sendDraftThreadName } from './clientStoreSendInput.js';

async function startNewDraftThread(request, resolveLaunchPreferences) {
  const launchPreferences = await resolveLaunchPreferences(request.cwd);
  const thread = await sessionApi.start({
    cwd: request.cwd,
    name: sendDraftThreadName(request.text),
    ...launchPreferences,
    deferSpawn: true,
    launchIntentId: request.launchIntentId,
  });
  const identity = normalizeThreadIdentity(thread);
  if (!identity.threadId) throw new Error('thread/start response missing threadId');
  return {
    identity,
    launchPreferences,
    threadId: identity.threadId,
  };
}

async function deleteProvisionalThreadAfterSendFailure(threadId, addWarning) {
  if (!threadId) return;
  try {
    await deleteThreadRPC({ threadId });
  }
  catch (cleanupError) {
    addWarning('warn', 'thread.provisional.delete.failed', {
      threadId,
      error: cleanupError.message || String(cleanupError),
    });
  }
}

function isStoppedThreadTurnStartError(error) {
  const message = normalizeString(
    firstOptionalPresent(error?.message, error?.cause?.message, optionalTextField(error)),
  ).toLowerCase();
  return message.includes('resolve session: thread') && message.includes(' is stopped');
}

function isCodexIdentityAutoResumeError(error) {
  const message = normalizeString(
    firstOptionalPresent(error?.message, error?.cause?.message, optionalTextField(error)),
  ).toLowerCase();
  return message.includes('resolve session: auto-resume failed') && message.includes('codex identity required for resume');
}

async function startTurnWithStoppedThreadRecovery(params) {
  try {
    return await sessionApi.startTurn(params);
  }
  catch (error) {
    if (!isStoppedThreadTurnStartError(error)) throw error;
    await recoverThread({ cwd: params.cwd, threadId: params.threadId });
    return sessionApi.startTurn(params);
  }
}

export {
  deleteProvisionalThreadAfterSendFailure,
  isCodexIdentityAutoResumeError,
  isStoppedThreadTurnStartError,
  startNewDraftThread,
  startTurnWithStoppedThreadRecovery,
};

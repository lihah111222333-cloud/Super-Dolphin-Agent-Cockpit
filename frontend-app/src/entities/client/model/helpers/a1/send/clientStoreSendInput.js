import { buildTurnInput, normalizeAttachment } from '../../../composer/composerAttachments.js';
import {
  cloneComposerCapabilities,
  composerCapabilityRequestFields,
} from '../../../capabilities/composerCapabilities.js';
import { normalizeThreadId } from '../../threadIdentity.js';
import { clockNowISO, clockNowMillis, normalizeString } from '../clientStoreUtils.js';
import {
  cwdForExistingThreadSend,
  reusableThreadIdForSend,
  threadMatchesIdentifier,
} from '../clientStoreRuntimeThreadModel.js';
import { firstOptionalPresent } from '../../../contractStoreModel.js';

function createLaunchIntentId() {
  const id = globalThis.crypto?.randomUUID?.() || `${clockNowMillis()}-${Math.random().toString(16).slice(2)}`;
  return `launch_${id}`;
}

function sendDraftThreadName(text) {
  return normalizeString(text).slice(0, 40) || '新对话';
}

function createSendDraftRequest(state, cwd) {
  const text = normalizeString(state.draft);
  const attachments = state.attachments.map(normalizeAttachment).filter(Boolean);
  const input = buildTurnInput(text, attachments);
  if (input.length === 0) return null;
  const capabilityPayload = composerCapabilityRequestFields(state.composerCapabilities);
  const previousActiveThreadId = state.activeThreadId;
  const previousThreadId = reusableThreadIdForSend(state, previousActiveThreadId);
  const launchIntentId = createLaunchIntentId();
  const provisionalThreadId = previousThreadId || launchIntentId;
  const requestCwd = previousThreadId
    ? cwdForExistingThreadSend(state, previousThreadId, cwd)
    : cwd;
  return {
    cwd: requestCwd,
    text,
    attachments,
    input,
    capabilityPayload,
    previousDraft: state.draft,
    previousAttachments: state.attachments,
    previousComposerCapabilities: cloneComposerCapabilities(state.composerCapabilities),
    previousActiveThreadId,
    previousThreadId,
    launchIntentId,
    provisionalThreadId,
    optimisticItem: {
      id: `user-${launchIntentId}`,
      role: 'user',
      text,
      attachments,
      time: clockNowISO(),
      done: true,
      optimistic: true,
    },
  };
}

function freshThreadRetryRequest(request) {
  const launchIntentId = createLaunchIntentId();
  return {
    ...request,
    previousThreadId: '',
    launchIntentId,
    provisionalThreadId: launchIntentId,
    optimisticItem: {
      ...request.optimisticItem,
      id: `user-${launchIntentId}`,
    },
  };
}

function dashboardCommandTemplate(card) {
  return normalizeString(firstOptionalPresent(card?.command_template, card?.commandTemplate));
}

function dashboardCommandPrompt(card) {
  const command = dashboardCommandTemplate(card);
  if (!command) throw new Error('dashboard command card command_template is required');
  return `请执行以下命令并反馈结果：\n${command}`;
}

function createDashboardCommandRequest(state, cwd, card) {
  return createSendDraftRequest({
    ...state,
    draft: dashboardCommandPrompt(card),
    attachments: [],
  }, cwd);
}

function forkSourceThread(state, threadId) {
  const id = normalizeThreadId(threadId);
  if (!id) return null;
  return state.threads.find((thread) => threadMatchesIdentifier(thread, id)) || null;
}

export {
  createDashboardCommandRequest,
  createLaunchIntentId,
  createSendDraftRequest,
  forkSourceThread,
  freshThreadRetryRequest,
  sendDraftThreadName,
};

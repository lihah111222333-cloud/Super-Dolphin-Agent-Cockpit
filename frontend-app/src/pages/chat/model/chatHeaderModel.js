import { textValue } from '../../shared/pageShared.js';
import { activeThreadForStore } from '../adapters/threadIdentityAdapter.js';

function bootstrapFailureMessage(error) {
  return textValue(error) || '应用初始化失败，请重试。';
}

function chatHeaderFeedbackForStore(store) {
  const bootstrapStatus = textValue(store?.bootstrapStatus);
  const bootstrapError = textValue(store?.error);
  const bootstrapRecovery = bootstrapStatus === 'failed'
    || (bootstrapStatus === 'loading' && Boolean(bootstrapError));
  const failureMessage = bootstrapRecovery
    ? bootstrapFailureMessage(bootstrapError)
    : '';
  if (failureMessage) return {
    message: failureMessage,
    tone: 'error',
    bootstrapRecovery: true,
    retrying: bootstrapStatus === 'loading',
  };
  const activeThreadId = textValue(store?.activeThreadId);
  const recoveryThreadId = textValue(activeThreadForStore(store)?.id) || activeThreadId;
  if (recoveryThreadId && store?.threadRecoveryPendingByThread?.[recoveryThreadId]) return {
    message: '正在恢复',
    tone: 'info',
    recoveryRequesting: true,
  };
  if (store?.actionNotice?.message) return store.actionNotice;
  return null;
}

export { chatHeaderFeedbackForStore };

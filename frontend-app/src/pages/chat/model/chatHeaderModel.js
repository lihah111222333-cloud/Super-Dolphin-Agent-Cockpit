import { textValue } from '../../shared/pageShared.js';
import { activeThreadForStore } from '../adapters/threadIdentityAdapter.js';

const BACKEND_CONNECTION_FAILED_PREFIX = '\u8fde\u63a5\u540e\u7aef\u5931\u8d25\uff1a';
const BACKEND_CONNECTION_FAILED_LABEL = '\u8fde\u63a5\u540e\u7aef\u5931\u8d25';

function bootstrapFailureMessage(error) {
  const message = textValue(error) || '未知错误';
  if (message.startsWith(BACKEND_CONNECTION_FAILED_LABEL)) return message;
  return `${BACKEND_CONNECTION_FAILED_PREFIX}${message}`;
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

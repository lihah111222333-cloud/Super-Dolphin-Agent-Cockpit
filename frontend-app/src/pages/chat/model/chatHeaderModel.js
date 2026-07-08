import { textValue } from '../../shared/pageShared.js';

const BACKEND_CONNECTION_FAILED_PREFIX = '\u8fde\u63a5\u540e\u7aef\u5931\u8d25\uff1a';

function chatHeaderFeedbackForStore(store) {
  const bootstrapFailureMessage = store?.bootstrapStatus === 'failed' && textValue(store?.error)
    ? `${BACKEND_CONNECTION_FAILED_PREFIX}${textValue(store?.error)}`
    : '';
  if (store?.actionNotice?.message) return store.actionNotice;
  return bootstrapFailureMessage ? { message: bootstrapFailureMessage, tone: 'error' } : null;
}

export { chatHeaderFeedbackForStore };

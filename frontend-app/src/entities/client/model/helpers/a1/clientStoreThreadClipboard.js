import { copyTextToClipboard } from '../../../../../shared/api/backendApi.js';

function errorMessage(error) {
  return error.message || String(error);
}

async function commitPreparedThreadInfoWrite(runtime, preparedClipboardWrite, text, threadId) {
  if (!preparedClipboardWrite?.commit) return null;
  try {
    await preparedClipboardWrite.commit(text);
    return null;
  }
  catch (error) {
    const message = errorMessage(error);
    runtime.addWarning('warn', 'thread.copy.prepared_clipboard.failed', { threadId, error: message });
    return `prepared clipboard write failed: ${message}`;
  }
}

async function writeThreadInfoClipboard(runtime, preparedClipboardWrite, text, threadId) {
  const preparedFailure = await commitPreparedThreadInfoWrite(runtime, preparedClipboardWrite, text, threadId);
  const copyFailures = preparedFailure ? [preparedFailure] : [];
  if (!preparedFailure && preparedClipboardWrite?.commit) return true;
  try {
    await copyTextToClipboard(text);
    return true;
  }
  catch (error) {
    if (copyFailures.length > 0) {
      throw new Error(`${copyFailures.join('; ')}; fallback copy failed: ${errorMessage(error)}`, { cause: error });
    }
    throw error;
  }
}

export {
  commitPreparedThreadInfoWrite,
  errorMessage,
  writeThreadInfoClipboard,
};

const EMPTY_API_ERROR_MESSAGE = '';

function apiErrorMessage(error, fallbackMessage = '请求失败') {
  const message = error?.message ? error.message : String(error ?? EMPTY_API_ERROR_MESSAGE);
  return message.trim() || fallbackMessage;
}

export { apiErrorMessage };

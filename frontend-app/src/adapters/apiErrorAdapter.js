function apiErrorMessage(error, fallbackMessage = '请求失败') {
  const message = error?.message || String(error || '');
  return message.trim() || fallbackMessage;
}

export { apiErrorMessage };

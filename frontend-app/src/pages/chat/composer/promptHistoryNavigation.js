export function shouldNavigatePromptHistory(event, textarea, direction) {
  const expectedKey = direction === 'previous' ? 'ArrowUp' : direction === 'next' ? 'ArrowDown' : '';
  if (!expectedKey || event.key !== expectedKey || event.defaultPrevented) return false;
  if (event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return false;
  const keyCode = Number(event.keyCode || event.which || 0);
  if (event.isComposing || event.nativeEvent?.isComposing || keyCode === 229) return false;
  if (!textarea || typeof textarea.value !== 'string') return false;
  const { selectionStart, selectionEnd, value } = textarea;
  if (!Number.isInteger(selectionStart) || selectionStart !== selectionEnd) return false;
  if (direction === 'previous') return value.lastIndexOf('\n', selectionStart - 1) === -1;
  return value.indexOf('\n', selectionStart) === -1;
}

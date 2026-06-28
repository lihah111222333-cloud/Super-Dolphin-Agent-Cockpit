export const COMPOSER_INPUT_MIN_HEIGHT = 38;
export const COMPOSER_INPUT_MAX_HEIGHT = 720;
export const COMPOSER_INPUT_BOUNDARY_GAP = 16;
export const COMPOSER_CHAT_READABLE_MIN_HEIGHT = 160;
export const COMPOSER_APP_HEIGHT_MAX_RATIO = 0.5;

export function applyComposerTextareaAutoHeight(textarea, maxHeight = COMPOSER_INPUT_MAX_HEIGHT) {
  if (!textarea?.style) return COMPOSER_INPUT_MIN_HEIGHT;
  const resolvedMaxHeight = Math.max(COMPOSER_INPUT_MIN_HEIGHT, Number(maxHeight) || COMPOSER_INPUT_MAX_HEIGHT);
  textarea.style.height = 'auto';
  const scrollHeight = Math.max(Number(textarea.scrollHeight) || 0, COMPOSER_INPUT_MIN_HEIGHT);
  const nextHeight = Math.min(scrollHeight, resolvedMaxHeight);
  textarea.style.height = `${nextHeight}px`;
  textarea.style.overflowY = scrollHeight > resolvedMaxHeight ? 'auto' : 'hidden';
  return nextHeight;
}

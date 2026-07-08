const STREAMING_REVEAL_SHORT_TEXT_CHARS = 16;
const STREAMING_REVEAL_CATCHUP_FRAMES = 80;
const STREAMING_REVEAL_MAX_CHARS_PER_FRAME = 8;
const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)';

function streamingRevealStepSize(remaining) {
  if (remaining <= STREAMING_REVEAL_SHORT_TEXT_CHARS) return 1;
  return Math.max(
    2,
    Math.min(STREAMING_REVEAL_MAX_CHARS_PER_FRAME, Math.ceil(remaining / STREAMING_REVEAL_CATCHUP_FRAMES)),
  );
}

function nextStreamingState({
  current,
  latestTarget,
  streamKey,
}) {
  if (current.streamKey !== streamKey) return current;
  const currentText = current.visibleText;
  if (!latestTarget.startsWith(currentText) || currentText.length > latestTarget.length) {
    return { streamKey, visibleText: latestTarget };
  }
  const remaining = latestTarget.length - currentText.length;
  if (remaining <= 0) return current;
  return {
    streamKey,
    visibleText: latestTarget.slice(0, currentText.length + streamingRevealStepSize(remaining)),
  };
}

export {
  REDUCED_MOTION_QUERY,
  nextStreamingState,
};

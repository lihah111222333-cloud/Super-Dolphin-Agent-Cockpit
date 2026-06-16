export const TIMELINE_SCROLL_LOAD_THRESHOLD = 32;

const TIMELINE_BOTTOM_STICKY_THRESHOLD = 48;

export function scrollTimelineElementToBottom(timeline, smooth = false) {
  if (!timeline) return;
  if (smooth && typeof timeline.scrollTo === 'function') {
    timeline.scrollTo({ top: timeline.scrollHeight, behavior: 'smooth' });
  } else {
    timeline.scrollTop = timeline.scrollHeight;
  }
}

export function isTimelineNearBottom(timeline) {
  if (!timeline) return true;
  const scrollHeight = Number(timeline.scrollHeight) || 0;
  const clientHeight = Number(timeline.clientHeight) || 0;
  const scrollTop = Number(timeline.scrollTop) || 0;
  if (scrollHeight <= clientHeight) return true;
  return scrollHeight - clientHeight - scrollTop <= TIMELINE_BOTTOM_STICKY_THRESHOLD;
}

export function requestTimelineBottomScroll(scrollToBottom) {
  if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') {
    scrollToBottom();
    return;
  }
  window.requestAnimationFrame(scrollToBottom);
}

const STICKY_MODE = 'sticky';
const READING_MODE = 'reading';
const FOLLOW_SOURCES = new Set(['streaming', 'load', 'mutation', 'resize']);

function stateWithMode(state, mode, threadId = state.threadId) {
  if (state.mode === mode && state.threadId === threadId) return state;
  return Object.freeze({ mode, threadId });
}

export function createScrollIntentState(threadId = '') {
  return Object.freeze({ mode: STICKY_MODE, threadId });
}

export function reduceScrollIntent(state, event) {
  if (!state || typeof event?.type !== 'string') throw new Error('scroll intent transition requires state and event');
  switch (event.type) {
    case 'thread-changed':
      return createScrollIntentState(event.threadId);
    case 'message-sent':
    case 'explicit-bottom':
      return stateWithMode(state, STICKY_MODE);
    case 'scroll-position':
      return stateWithMode(state, event.nearBottom ? STICKY_MODE : READING_MODE);
    case 'wheel':
      if (event.ctrlKey || Math.abs(event.deltaX) >= Math.abs(event.deltaY) || event.deltaY >= 0) return state;
      return stateWithMode(state, READING_MODE);
    case 'touch':
      return event.direction === 'up' ? stateWithMode(state, READING_MODE) : state;
    case 'key':
      if (event.targetEditable) return state;
      if (event.key === 'End') return stateWithMode(state, STICKY_MODE);
      if (event.key === 'PageUp' || event.key === 'Home' || event.key === 'ArrowUp') {
        return stateWithMode(state, READING_MODE);
      }
      return state;
    default:
      throw new Error(`unsupported scroll intent event: ${event.type}`);
  }
}

export function shouldFollowTimeline(state, source) {
  if (!FOLLOW_SOURCES.has(source)) throw new Error(`unsupported scroll follow source: ${source}`);
  return state?.mode === STICKY_MODE;
}

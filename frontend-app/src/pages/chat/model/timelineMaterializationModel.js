const TIMELINE_INITIAL_MATERIALIZED_MESSAGES = 80;
const TIMELINE_MATERIALIZATION_INCREMENT = 80;

function selectMaterializedTimeline(messages, count) {
  if (!Array.isArray(messages)) throw new TypeError('messages must be an array');
  if (!Number.isSafeInteger(count) || count < 0) {
    throw new TypeError('count must be a non-negative integer');
  }
  const materializedCount = Math.max(TIMELINE_INITIAL_MATERIALIZED_MESSAGES, count);
  return messages.slice(Math.max(0, messages.length - materializedCount));
}

export {
  TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
  TIMELINE_MATERIALIZATION_INCREMENT,
  selectMaterializedTimeline,
};

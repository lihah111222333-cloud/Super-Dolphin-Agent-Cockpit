import { useCallback, useMemo, useState } from 'react';

const TIMELINE_INITIAL_MATERIALIZED_MESSAGES = 80;
const TIMELINE_MATERIALIZATION_INCREMENT = 80;

function timelineMaterializationKey({ activeThreadId, introMode, timelineContentBlocked }) {
  return `${activeThreadId || ''}:${introMode ? 'intro' : 'thread'}:${timelineContentBlocked ? 'blocked' : 'ready'}`;
}

function useTimelineMaterialization({ activeThreadId, introMode, messages, timelineContentBlocked }) {
  const materializationKey = timelineMaterializationKey({ activeThreadId, introMode, timelineContentBlocked });
  const [materialization, setMaterialization] = useState(() => ({
    count: TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
    key: materializationKey,
  }));
  const messageCount = messages.length;
  const activeMaterialization = materialization.key === materializationKey
    ? materialization
    : { count: TIMELINE_INITIAL_MATERIALIZED_MESSAGES, key: materializationKey };
  if (activeMaterialization !== materialization) {
    setMaterialization(activeMaterialization);
  }
  const materializedCount = Math.min(
    Math.max(TIMELINE_INITIAL_MATERIALIZED_MESSAGES, activeMaterialization.count),
    Math.max(TIMELINE_INITIAL_MATERIALIZED_MESSAGES, messageCount),
  );

  const visibleStart = Math.max(0, messageCount - materializedCount);
  const visibleMessages = useMemo(() => messages.slice(visibleStart), [messages, visibleStart]);
  const hiddenOlderCount = visibleStart;
  const revealOlder = useCallback(() => {
    setMaterialization((current) => {
      const currentCount = current.key === materializationKey
        ? current.count
        : TIMELINE_INITIAL_MATERIALIZED_MESSAGES;
      return {
        count: Math.min(
          messageCount,
          Math.max(TIMELINE_INITIAL_MATERIALIZED_MESSAGES, currentCount) + TIMELINE_MATERIALIZATION_INCREMENT,
        ),
        key: materializationKey,
      };
    });
  }, [materializationKey, messageCount]);

  return {
    hiddenOlderCount,
    revealOlder,
    visibleMessages,
  };
}

export { useTimelineMaterialization };

import { useCallback, useMemo, useState } from 'react';
import {
  TIMELINE_INITIAL_MATERIALIZED_MESSAGES,
  TIMELINE_MATERIALIZATION_INCREMENT,
  selectMaterializedTimeline,
} from '../model/timelineMaterializationModel.js';

function timelineMaterializationKey({ activeThreadId, introMode, timelineContentBlocked }) {
  const threadId = activeThreadId === null || activeThreadId === undefined ? '' : activeThreadId;
  return `${threadId}:${introMode ? 'intro' : 'thread'}:${timelineContentBlocked ? 'blocked' : 'ready'}`;
}

function useTimelineMaterialization({ activeThreadId, introMode, messages, timelineContentBlocked }) {
  /*
   * 这里控制一次渲染多少条消息，不改 store。
   * 切换线程或进入加载态时，要重新计算展开数量。
   */
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
  const visibleMessages = useMemo(
    () => selectMaterializedTimeline(messages, activeMaterialization.count),
    [activeMaterialization.count, messages],
  );
  const hiddenOlderCount = Math.max(0, messageCount - visibleMessages.length);
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

// @ts-nocheck
import { defaultLayoutForMode, normalizeChatLayout, normalizeCmdLayout } from './thread-view.model.js';
import {
  normalizeSplitRatio,
  normalizeThreadRailWidth,
  normalizeCmdCardCols,
} from './thread-ui-normalize.js';

export const PREF_ACTIVE_THREAD_ID = 'activeThreadId';
export const PREF_ACTIVE_CMD_THREAD_ID = 'activeCmdThreadId';

export const PREF_VIEW_CHAT = 'viewPrefs.chat';
export const PREF_VIEW_CMD = 'viewPrefs.cmd';
export const PREF_PINNED_THREADS_CHAT = 'threadPins.chat';
export const PREF_ARCHIVED_THREADS_CHAT = 'threadArchives.chat';

export function normalizeChatPrefs(value) {
  const input = value && typeof value === 'object' ? value : {};
  return {
    layout: normalizeChatLayout(input.layout || defaultLayoutForMode('chat')),
    splitRatio: normalizeSplitRatio(input.splitRatio),
    threadRailWidth: normalizeThreadRailWidth(input.threadRailWidth),
  };
}

export function normalizeCmdPrefs(value) {
  const input = value && typeof value === 'object' ? value : {};
  return {
    layout: normalizeCmdLayout(input.layout || defaultLayoutForMode('cmd')),
    splitRatio: normalizeSplitRatio(input.splitRatio),
    cardCols: normalizeCmdCardCols(input.cardCols),
  };
}

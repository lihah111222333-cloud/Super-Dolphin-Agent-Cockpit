import { optionalTextField } from '../contractStoreModel.js';
import {
  compactTimelineText,
  dedupeAssistantTimelineItems,
  preferredAssistantTimelineItem,
  sameRuntimeAssistantContentLoose,
  sameTimelineContent,
  sameTimelineContentCompact,
  sameTimelineContentPrefix,
  sortTimelineChronologically } from '../timelineRuntime.js';

function findLastUserIndex(items = []) {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    if (items[index]?.role === 'user') return index;
  }
  return -1;
}

function isTurnAssistantItem(item) {
  return item?.role === 'assistant' && (item?.kind === 'assistant' || !item?.kind);
}

function replaceSingleAssistantItem(existingItems, singleItem, finalItem, streamId) {
  const isStreamItem = singleItem.id === streamId;
  const preferred = isStreamItem ? finalItem : preferredAssistantTimelineItem(singleItem, finalItem);
  return existingItems.map((item) => (item.id === singleItem.id ? { ...preferred, done: true } : item));
}

function markTurnAssistantItemsDone(existingItems, lastUserIndex) {
  return existingItems.map((item, index) => {
    if (index > lastUserIndex && isTurnAssistantItem(item) && item.done === false) return { ...item, done: true };
    return item;
  });
}

function mergeAccumulatedAssistantCompletion(existingItems, completion, turnAssistantItems, lastUserIndex) {
  const finalItem = completion.item;
  const accumulatedText = turnAssistantItems.map((item) => optionalTextField(item.text)).join('');
  const compactAccumulated = compactTimelineText(accumulatedText);
  const compactFinal = compactTimelineText(finalItem.text);
  if (!compactAccumulated || compactAccumulated !== compactFinal) return null;
  if (turnAssistantItems.length === 1) {
    return replaceSingleAssistantItem(existingItems, turnAssistantItems[0], finalItem, completion.streamId);
  }
  return markTurnAssistantItemsDone(existingItems, lastUserIndex);
}

function isDuplicateAssistantCompletion(item, finalItem, index, lastUserIndex) {
  if (item.role !== 'assistant' || item.done === false) return false;
  if (sameTimelineContent(item, finalItem)) return true;
  if (index <= lastUserIndex) return false;
  return (
    sameTimelineContentCompact(item, finalItem) ||
    sameRuntimeAssistantContentLoose(item, finalItem) ||
    (item.runtime && finalItem.runtime && sameTimelineContentPrefix(item, finalItem))
  );
}

function canReplaceDuplicateCompletion(completion, duplicateItem, duplicateIndex, lastUserIndex) {
  return !completion.explicitId || duplicateItem.runtime || duplicateIndex > lastUserIndex;
}

function replaceDuplicateAssistantCompletion(items, duplicateIndex, finalItem) {
  return dedupeAssistantTimelineItems(sortTimelineChronologically(items.map((item, index) => (
    index === duplicateIndex ? preferredAssistantTimelineItem(item, finalItem) : item
  ))));
}

export function mergeRuntimeAssistantCompletionImpl(existingItems = [], completion) {
  if (!completion?.item) return existingItems;
  const finalItem = completion.item;
  let lastUserIndex = findLastUserIndex(existingItems);
  const turnAssistantItems = existingItems.slice(lastUserIndex + 1).filter((item) => isTurnAssistantItem(item));
  const accumulatedMerge = mergeAccumulatedAssistantCompletion(existingItems, completion, turnAssistantItems, lastUserIndex);
  if (accumulatedMerge) return accumulatedMerge;

  const dropIds = new Set([finalItem.id, completion.streamId].filter(Boolean));
  const withoutReplaced = existingItems.filter((item) => !dropIds.has(item.id));
  lastUserIndex = findLastUserIndex(withoutReplaced);
  const duplicateIndex = withoutReplaced.findIndex((item, index) => (
    isDuplicateAssistantCompletion(item, finalItem, index, lastUserIndex)
  ));
  if (duplicateIndex < 0) return dedupeAssistantTimelineItems([...withoutReplaced, finalItem]);
  if (!canReplaceDuplicateCompletion(completion, withoutReplaced[duplicateIndex], duplicateIndex, lastUserIndex)) {
    return dedupeAssistantTimelineItems([...withoutReplaced, finalItem]);
  }
  return replaceDuplicateAssistantCompletion(withoutReplaced, duplicateIndex, finalItem);
}

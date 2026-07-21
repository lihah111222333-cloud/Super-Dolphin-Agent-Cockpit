import {
  RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS,
  RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES,
  RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_RATIO,
  RUNTIME_ASSISTANT_LOOSE_DUPLICATE_SHINGLE_SIZE,
  RUNTIME_ASSISTANT_PREFIX_DUPLICATE_MIN_CHARS,
  compactTimelineText,
  firstOptionalPresent,
  normalizeString,
  normalizeTimelineKind,
} from "./timelineRuntimeFields.js";

export function sameTimelineContent(left, right) {
  if (
    normalizeTimelineKind(left) === "approval" ||
    normalizeTimelineKind(right) === "approval"
  )
    return false;
  return (
    left?.role === right?.role &&
    normalizeTimelineKind(left) === normalizeTimelineKind(right) &&
    normalizeString(left?.text) === normalizeString(right?.text)
  );
}

export function sameTimelineContentCompact(left, right) {
  return (
    left?.role === right?.role &&
    normalizeTimelineKind(left) === normalizeTimelineKind(right) &&
    compactTimelineText(left?.text) &&
    compactTimelineText(left?.text) === compactTimelineText(right?.text)
  );
}

function looseTimelineText(value) {
  return compactTimelineText(value)
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, "");
}

function looseTimelineShingleMatch(shorterText, longerText) {
  if (shorterText.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS)
    return false;
  const shingleSize = Math.min(
    RUNTIME_ASSISTANT_LOOSE_DUPLICATE_SHINGLE_SIZE,
    Math.floor(
      shorterText.length / RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES,
    ),
  );
  if (shingleSize <= 0) return false;
  const shingles = new Set();
  for (
    let index = 0;
    index <= shorterText.length - shingleSize;
    index += shingleSize
  )
    shingles.add(shorterText.slice(index, index + shingleSize));
  shingles.add(shorterText.slice(-shingleSize));
  const candidates = [...shingles].filter(Boolean);
  if (candidates.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES)
    return false;
  const matches = candidates.filter((candidate) =>
    longerText.includes(candidate),
  ).length;
  return (
    matches >= RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_MATCHES &&
    matches / candidates.length >= RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_RATIO
  );
}

export function sameRuntimeAssistantContentLoose(left, right) {
  if (!left?.runtime && !right?.runtime) return false;
  if (
    left?.role !== right?.role ||
    normalizeTimelineKind(left) !== normalizeTimelineKind(right)
  )
    return false;
  const leftText = looseTimelineText(left?.text);
  const rightText = looseTimelineText(right?.text);
  if (!leftText || !rightText) return false;
  const shorterText =
    leftText.length <= rightText.length ? leftText : rightText;
  const longerText = leftText.length > rightText.length ? leftText : rightText;
  if (shorterText.length < RUNTIME_ASSISTANT_LOOSE_DUPLICATE_MIN_CHARS)
    return false;
  return (
    longerText.includes(shorterText) ||
    looseTimelineShingleMatch(shorterText, longerText)
  );
}

export function sameTimelineContentPrefix(left, right) {
  if (
    left?.role !== right?.role ||
    normalizeTimelineKind(left) !== normalizeTimelineKind(right)
  )
    return false;
  const leftText = compactTimelineText(left?.text);
  const rightText = compactTimelineText(right?.text);
  const shorterLength = Math.min(leftText.length, rightText.length);
  if (shorterLength < RUNTIME_ASSISTANT_PREFIX_DUPLICATE_MIN_CHARS)
    return false;
  return leftText.startsWith(rightText) || rightText.startsWith(leftText);
}

function sameTimelineSubstring(left, right) {
  if (
    left?.role !== right?.role ||
    normalizeTimelineKind(left) !== normalizeTimelineKind(right)
  )
    return false;
  const leftText = compactTimelineText(left?.text);
  const rightText = compactTimelineText(right?.text);
  if (
    !leftText ||
    !rightText ||
    Math.min(leftText.length, rightText.length) < 15
  )
    return false;
  return leftText.includes(rightText) || rightText.includes(leftText);
}

export function sameTimelineDuplicateContent(left, right) {
  if (
    normalizeTimelineKind(left) === "approval" ||
    normalizeTimelineKind(right) === "approval"
  )
    return false;
  return (
    sameTimelineContent(left, right) ||
    sameTimelineContentCompact(left, right) ||
    sameRuntimeAssistantContentLoose(left, right) ||
    sameTimelineSubstring(left, right)
  );
}

export function preferredAssistantTimelineItem(existingItem, incomingItem) {
  const isRuntime = Boolean(
    firstOptionalPresent(existingItem?.runtime, incomingItem?.runtime),
  );
  if (existingItem?.runtime !== incomingItem?.runtime) {
    const base = incomingItem?.runtime ? existingItem : incomingItem;
    return isRuntime ? { ...base, runtime: true } : base;
  }
  return normalizeString(incomingItem?.text).length >
    normalizeString(existingItem?.text).length
    ? incomingItem
    : existingItem;
}

export function dedupeAssistantTimelineItems(items = []) {
  const output = [];
  let lastUserIndex = -1;
  const seenIds = new Set();
  for (const item of items) {
    if (item?.role === "user") {
      output.push(item);
      lastUserIndex = output.length - 1;
      continue;
    }
    if (item?.role !== "assistant" || !compactTimelineText(item.text)) {
      output.push(item);
      continue;
    }
    const itemIdentity =
      item.id && item.turnId ? `${item.turnId}\u0000${item.id}` : "";
    if (itemIdentity && seenIds.has(itemIdentity)) continue;
    const duplicateIndices = output
      .map((candidate, index) => ({ candidate, index }))
      .filter(
        ({ candidate, index }) =>
          index > lastUserIndex &&
          candidate?.role === "assistant" &&
          candidate?.turnId === item?.turnId &&
          sameTimelineDuplicateContent(candidate, item),
      )
      .map(({ index }) => index)
      .reverse();
    if (duplicateIndices.length > 0) {
      const mergedItem = duplicateIndices.reduce(
        (current, index) =>
          preferredAssistantTimelineItem(output[index], current),
        item,
      );
      const anyDone =
        Boolean(item.done) ||
        duplicateIndices.some((index) => output[index].done);
      output[duplicateIndices[duplicateIndices.length - 1]] = anyDone
        ? { ...mergedItem, done: true }
        : mergedItem;
      const indicesToRemove = new Set(duplicateIndices.slice(0, -1));
      if (indicesToRemove.size > 0) {
        const retainedItems = output
          .slice(lastUserIndex + 1)
          .filter(
            (_, offset) => !indicesToRemove.has(lastUserIndex + 1 + offset),
          );
        output.splice(
          lastUserIndex + 1,
          output.length - lastUserIndex - 1,
          ...retainedItems,
        );
      }
      continue;
    }
    output.push(item);
    if (itemIdentity) seenIds.add(itemIdentity);
  }
  return output;
}

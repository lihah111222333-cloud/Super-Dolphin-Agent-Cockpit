function parseHistoryMetadata(raw) {
  if (!raw) return null;
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw);
    } catch {
      return null;
    }
  }
  return typeof raw === 'object' ? raw : null;
}

const IMAGE_PLACEHOLDER_RE = /<image\b[^>]*>[\s\S]*?<\/image>/gi;

function extractFirstString(source, keys) {
  if (!source || typeof source !== 'object') return '';
  for (const key of keys) {
    const value = source[key];
    if (typeof value === 'string' && value.trim()) return value.trim();
  }
  return '';
}

function extractHistoryInputEntries(metadata) {
  return Array.isArray(metadata?.input) ? metadata.input.filter(Boolean) : [];
}

function isHistoryImageInput(entry) {
  const inputType = extractFirstString(entry, ['type', 'kind', 'inputType', 'input_type']).toLowerCase();
  return inputType === 'image' || inputType === 'input_image' || inputType === 'local_image' || inputType === 'localimage';
}


function decodeFileURLToPath(rawValue) {
  const raw = (rawValue || '').toString().trim();
  if (!raw.toLowerCase().startsWith('file://')) return raw;
  try {
    const url = new URL(raw);
    const pathname = decodeURIComponent(url.pathname || '');
    if (url.host) return `//${url.host}${pathname}`;
    if (/^\/[A-Za-z]:\//.test(pathname)) return pathname.slice(1);
    return pathname || raw.replace(/^file:\/\//i, '');
  } catch {
    return decodeURIComponent(raw.replace(/^file:\/\//i, ''));
  }
}

function basenameFromPath(rawValue) {
  const raw = (rawValue || '').toString().trim();
  if (!raw) return '';
  if (raw.toLowerCase().startsWith('data:image/')) return '';
  const resolved = raw.toLowerCase().startsWith('file://') ? decodeFileURLToPath(raw) : raw;
  const withoutQuery = resolved.split('?')[0].split('#')[0];
  const parts = withoutQuery.split(/[\\/]/).filter(Boolean);
  return parts[parts.length - 1] || '';
}

// CLIPBOARD_ASSET_NAME_RE matches the basenames produced by the Go backend's
// SaveClipboardImage (`clipboard-*.png`). Such files live in os.TempDir() and
// are served by Wails' clipboard asset route at `/clipboard/<basename>`.
const CLIPBOARD_ASSET_NAME_RE = /^clipboard-.+\.png$/i;

function clipboardAssetURLForPath(path) {
  const raw = (path || '').toString().trim();
  if (!raw) return '';
  const base = basenameFromPath(raw);
  if (!base || !CLIPBOARD_ASSET_NAME_RE.test(base)) return '';
  return `/clipboard/${encodeURIComponent(base)}`;
}

function historyPreviewUrl(source, path) {
  if (source) {
    const lower = source.toLowerCase();
    if (lower.startsWith('http://') || lower.startsWith('https://') || lower.startsWith('data:image/')) {
      return source;
    }
  }
  return clipboardAssetURLForPath(path);
}

function buildHistoryImageAttachments(metadata) {
  const inputs = extractHistoryInputEntries(metadata);
  const attachments = [];
  let imageCount = 0;
  for (const entry of inputs) {
    if (!isHistoryImageInput(entry)) continue;
    const source = extractFirstString(entry, ['url', 'imageUrl', 'image_url', 'path', 'filePath', 'file_path']);
    const explicitName = extractFirstString(entry, ['name', 'fileName', 'filename', 'label', 'title']);
    const path = !source || source.toLowerCase().startsWith('data:image/')
      ? ''
      : (source.toLowerCase().startsWith('file://') ? decodeFileURLToPath(source) : source);
    const previewUrl = historyPreviewUrl(source, path);
    const name = explicitName || basenameFromPath(path || source) || `image-${imageCount + 1}`;
    const attachment = { kind: 'image', name, path, previewUrl };
    const sha256 = extractFirstString(entry, ['sha256', 'hash']);
    if (sha256) attachment.sha256 = sha256;
    attachments.push(attachment);
    imageCount += 1;
  }
  return attachments;
}

// DEDUP_PLACEHOLDER_RE matches the placeholder text the claudecli provider
// emits in place of repeated image content blocks. The 12-hex-char prefix is
// the leading slice of sha256(rawBytes) chosen to fit a short visible hint
// while remaining unique within a single session.
const DEDUP_PLACEHOLDER_RE = /\[image previously attached in this session, sha256:([0-9a-f]+)…\]/g;

// backfillDedupedImageAttachments walks a timeline forward, builds a
// sha256-prefix -> attachment lookup from prior user images, and for any user
// turn whose text carries `[image previously attached … sha256:abc…]`
// placeholders, it clones the matching prior image attachment in and strips
// the placeholder from the displayed text. This keeps the bubble preview
// consistent with what the user sent even though the wire payload was
// deduped down to a small text marker.
function backfillDedupedImageAttachments(timeline) {
  if (!Array.isArray(timeline) || timeline.length === 0) return timeline;
  const byPrefix = new Map();
  const recordPrefixesFor = (attachments) => {
    if (!Array.isArray(attachments)) return;
    for (const att of attachments) {
      if (att?.kind !== 'image') continue;
      const hash = (att.sha256 || '').toString().toLowerCase();
      if (!hash) continue;
      byPrefix.set(hash.slice(0, 12), att);
    }
  };
  for (let i = 0; i < timeline.length; i += 1) {
    const item = timeline[i];
    if (item?.kind !== 'user') continue;
    recordPrefixesFor(item.attachments);
    const text = (item.text || '').toString();
    if (!DEDUP_PLACEHOLDER_RE.test(text)) continue;
    DEDUP_PLACEHOLDER_RE.lastIndex = 0; // reset because /g state is sticky
    const seenAtt = new Set(Array.isArray(item.attachments) ? item.attachments : []);
    const cloned = [];
    let match;
    while ((match = DEDUP_PLACEHOLDER_RE.exec(text)) !== null) {
      const prior = byPrefix.get((match[1] || '').toLowerCase());
      if (prior && !seenAtt.has(prior)) {
        cloned.push(prior);
        seenAtt.add(prior);
      }
    }
    DEDUP_PLACEHOLDER_RE.lastIndex = 0;
    const cleanedText = text
      .replace(DEDUP_PLACEHOLDER_RE, '')
      .replace(/[ \t]+\n/g, '\n')
      .replace(/\n{3,}/g, '\n\n')
      .trim();
    DEDUP_PLACEHOLDER_RE.lastIndex = 0;
    const nextAttachments = [...(Array.isArray(item.attachments) ? item.attachments : []), ...cloned];
    timeline[i] = { ...item, text: cleanedText, attachments: nextAttachments };
  }
  return timeline;
}

function stripImagePlaceholders(text, enabled) {
  const source = (text || '').toString();
  if (!enabled || !source) return source;
  return source
    .replace(IMAGE_PLACEHOLDER_RE, ' ')
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
}

function toCreatedAtMillis(value) {
  if (value instanceof Date) return value.getTime();
  const parsed = Date.parse((value || '').toString());
  return Number.isFinite(parsed) ? parsed : Number.NaN;
}

function toNumericMessageID(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : Number.NaN;
}

function sortHistoryMessagesChronologically(messages) {
  return messages
    .map((message, index) => ({
      message,
      index,
      createdAt: toCreatedAtMillis(message?.createdAt),
      numericId: toNumericMessageID(message?.id),
    }))
    .sort((left, right) => {
      if (Number.isFinite(left.createdAt) && Number.isFinite(right.createdAt) && left.createdAt !== right.createdAt) {
        return left.createdAt - right.createdAt;
      }
      if (Number.isFinite(left.numericId) && Number.isFinite(right.numericId) && left.numericId !== right.numericId) {
        return left.numericId - right.numericId;
      }
      return left.index - right.index;
    })
    .map((entry) => entry.message);
}

function countDialogTimelineItems(items) {
  if (!Array.isArray(items)) return 0;
  return items.reduce((count, item) => {
    const kind = (item?.kind || '').toString().trim();
    return count + ((kind === 'assistant' || kind === 'user') ? 1 : 0);
  }, 0);
}

function getLatestDialogTextLength(items) {
  if (!Array.isArray(items)) return 0;
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    const kind = (item?.kind || '').toString().trim();
    if (kind !== 'assistant' && kind !== 'user') continue;
    return (item?.text || '').length;
  }
  return 0;
}

function getLatestDialogTimestamp(items) {
  if (!Array.isArray(items)) return Number.NaN;
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    const kind = (item?.kind || '').toString().trim();
    if (kind !== 'assistant' && kind !== 'user') continue;
    const tsMillis = toCreatedAtMillis(item?.ts);
    if (Number.isFinite(tsMillis)) return tsMillis;
  }
  return Number.NaN;
}

function getLatestKindTimestamp(items, targetKind) {
  if (!Array.isArray(items)) return Number.NaN;
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    if ((item?.kind || '').toString().trim() !== targetKind) continue;
    const tsMillis = toCreatedAtMillis(item?.ts);
    if (Number.isFinite(tsMillis)) return tsMillis;
  }
  return Number.NaN;
}

function shouldFilterStaleThinkingItem(item, latestAssistantTs, hasRuntimeHistoryContext) {
  if (!hasRuntimeHistoryContext) return false;
  if ((item?.kind || '').toString().trim() !== 'thinking') return false;
  if (item?.done === true || !Number.isFinite(latestAssistantTs)) return false;
  const thinkingTs = toCreatedAtMillis(item?.ts);
  if (!Number.isFinite(thinkingTs)) return false;
  return latestAssistantTs >= thinkingTs;
}

function historyMessageToTimelineItem(threadId, message, index) {
  const role = (message?.role || '').toString().trim().toLowerCase();
  let kind = '';
  if (role === 'assistant') kind = 'assistant';
  else if (role === 'user') kind = 'user';
  if (!kind) return null;
  const rawCreatedAt = message?.createdAt;
  const ts = rawCreatedAt instanceof Date ? rawCreatedAt.toISOString() : (rawCreatedAt || '').toString();
  const rawID = Number(message?.id);
  const itemID = Number.isFinite(rawID) && rawID > 0 ? `${threadId}-history-${Math.floor(rawID)}` : `${threadId}-history-${index + 1}`;
  const metadata = kind === 'user' ? parseHistoryMetadata(message?.metadata) : null;
  const attachments = kind === 'user' ? buildHistoryImageAttachments(metadata) : [];
  const item = {
    id: itemID,
    kind,
    text: kind === 'user'
      ? stripImagePlaceholders(message?.content, attachments.length > 0)
      : (message?.content || '').toString(),
    ts,
  };
  if (attachments.length > 0) item.attachments = attachments;

  if (kind !== 'user') return item;
  if (!metadata) return item;
  if (metadata.internal === true) item.internal = true;
  const sourceKind = extractFirstString(metadata, ['sourceKind', 'source_kind']);
  if (sourceKind) item.sourceKind = sourceKind;
  const fromThreadId = extractFirstString(metadata, ['fromThreadId', 'from_thread_id', 'from']);
  if (fromThreadId) item.fromThreadId = fromThreadId;
  const toThreadId = extractFirstString(metadata, ['toThreadId', 'to_thread_id', 'to']);
  if (toThreadId) item.toThreadId = toThreadId;
  const fromDisplay = extractFirstString(metadata, ['fromDisplay', 'from_display']);
  if (fromDisplay) item.fromDisplay = fromDisplay;
  const toDisplay = extractFirstString(metadata, ['toDisplay', 'to_display']);
  if (toDisplay) item.toDisplay = toDisplay;
  return item;
}

export function applyImmediateTimelineFromMessages({ threadId, response, state, normalizeThreadID, freezeTimelineItemsAtomic, logDebug, logInfo, logWarn }) {
  const logDiagnostic = typeof logDebug === 'function' ? logDebug : logInfo;
  const id = typeof normalizeThreadID === 'function' ? normalizeThreadID(threadId) : (threadId || '').toString().trim();
  const messages = Array.isArray(response?.messages) ? response.messages : [];
  if (!id || messages.length === 0) return false;
  const orderedMessages = sortHistoryMessagesChronologically(messages);
  const timeline = orderedMessages.map((message, index) => historyMessageToTimelineItem(id, message, index)).filter(Boolean);
  if (timeline.length === 0) return false;
  // Restore image previews on dedup-placeholder turns by cloning the prior
  // matching image (matched by sha256 prefix) and stripping the placeholder
  // text from display. Mutates `timeline` in place.
  backfillDedupedImageAttachments(timeline);
  const existing = Array.isArray(state?.timelinesByThread?.[id]) ? state.timelinesByThread[id] : [];
  const existingDialogCount = countDialogTimelineItems(existing);
  const incomingDialogCount = countDialogTimelineItems(timeline);
  const existingLatestDialogTs = getLatestDialogTimestamp(existing);
  const incomingLatestDialogTs = getLatestDialogTimestamp(timeline);
  const existingTsValid = Number.isFinite(existingLatestDialogTs);
  const incomingTsValid = Number.isFinite(incomingLatestDialogTs);
  // 跳过条件核心判断：本地是否比 incoming "更新或相等"
  // - 两边都有有效 ts → 直接比时间戳
  // - incoming 有有效 ts 而本地没有 → 本地数据不完整（live-patch 无 ts），强制接受 incoming
  // - existing 有有效 ts 而 incoming 没有 → 保守保留已有本地数据
  // - 两边都无有效 ts → fallback 比消息数量
  let sameOrNewerExistingDialog = false;
  if (existingDialogCount > incomingDialogCount) {
    sameOrNewerExistingDialog = true;
  } else if (existingTsValid && incomingTsValid) {
    if (existingLatestDialogTs > incomingLatestDialogTs) {
      sameOrNewerExistingDialog = true;
    } else if (existingLatestDialogTs === incomingLatestDialogTs && existingDialogCount === incomingDialogCount) {
      const existingLen = getLatestDialogTextLength(existing);
      const incomingLen = getLatestDialogTextLength(timeline);
      sameOrNewerExistingDialog = existingLen >= incomingLen;
    } else {
      sameOrNewerExistingDialog = false;
    }
  } else if (!existingTsValid && incomingTsValid) {
    sameOrNewerExistingDialog = false;
  } else if (existingTsValid && !incomingTsValid) {
    sameOrNewerExistingDialog = true;
  } else {
    sameOrNewerExistingDialog = existing.length >= timeline.length;
  }
  if (existingDialogCount > 0 && existingDialogCount >= incomingDialogCount && sameOrNewerExistingDialog) {
    if (typeof logInfo === 'function') logInfo('thread', 'messages.load.local_timeline.skipped_stale', {
      thread_id: id,
      existing_count: existing.length,
      incoming_count: timeline.length,
      existing_dialog_count: existingDialogCount,
      incoming_dialog_count: incomingDialogCount,
      existing_latest_dialog_ts: existingTsValid ? existingLatestDialogTs : null,
      incoming_latest_dialog_ts: incomingTsValid ? incomingLatestDialogTs : null,
    });
    return false;
  }
  // Check if local has optimistic user messages that would be lost
  const hasOptimistic = existing.some((it) => (it?.id || '').toString().includes('-optimistic-'));
  const optimisticItems = hasOptimistic ? existing.filter((it) => (it?.id || '').toString().includes('-optimistic-')) : [];
  if (typeof logDiagnostic === 'function') logDiagnostic('thread', 'history.immediate_apply', {
    thread_id: id,
    existing_total: existing.length,
    incoming_total: timeline.length,
    existing_dialog_count: existingDialogCount,
    incoming_dialog_count: incomingDialogCount,
    has_optimistic: hasOptimistic,
    optimistic_count: optimisticItems.length,
    optimistic_ids: optimisticItems.map((it) => (it?.id || '').toString()).slice(0, 4),
    incoming_user_count: timeline.filter((it) => it?.kind === 'user').length,
  });
  if (typeof logInfo === 'function') logInfo('thread', 'messages.load.local_timeline.overwrite_decision', {
    thread_id: id,
    existing_total: existing.length,
    incoming_total: timeline.length,
    existing_dialog_count: existingDialogCount,
    incoming_dialog_count: incomingDialogCount,
    has_optimistic: hasOptimistic,
  });
  const incomingIds = new Set(timeline.map((it) => it?.id).filter(Boolean));
  const incomingUserTexts = new Set(timeline.filter((it) => it?.kind === 'user').map((it) => (it?.text || '').trim()));
  const latestIncomingAssistantTs = getLatestKindTimestamp(timeline, 'assistant');
  const hasRuntimeHistoryContext = Boolean(state?.statuses?.[id] || state?.agentRuntimeById?.[id]);

  const missingFromIncoming = optimisticItems.filter((it) => {
    if (incomingIds.has(it?.id)) return false;
    return !incomingUserTexts.has((it?.text || '').trim());
  });

  const thread = Array.isArray(state?.threads) ? state.threads.find((t) => normalizeThreadID(t?.id) === id) : null;
  const isStreaming = !thread?.isTurnCompletedLocally && (thread?.state === 'responding' || thread?.state === 'thinking' || thread?.state === 'starting');

  let hasPreservedStreamingItem = false;
  let localStreamingItemToPreserve = null;

  if (isStreaming) {
    const localStreamingItem = existing.find(it => it?.kind === 'assistant' && it?.done === false);
    if (localStreamingItem) {
      let lastIncomingAssistantIndex = -1;
      for (let i = timeline.length - 1; i >= 0; i--) {
        if (timeline[i].kind === 'assistant') { lastIncomingAssistantIndex = i; break; }
      }
      if (lastIncomingAssistantIndex >= 0) {
        const incomingItem = timeline[lastIncomingAssistantIndex];
        const streamingText = (localStreamingItem.text || '').trim();
        const incomingText = (incomingItem.text || '').trim();
        const shortPrefixMatchLen = Math.min(10, Math.min(streamingText.length, incomingText.length));
        const overlapMatch = streamingText && incomingText
          && (streamingText.includes(incomingText) || incomingText.includes(streamingText));
        const prefixMatch = shortPrefixMatchLen > 0
          && (streamingText.startsWith(incomingText.slice(0, shortPrefixMatchLen)) || incomingText.startsWith(streamingText.slice(0, shortPrefixMatchLen)));
        if (overlapMatch || prefixMatch) {
          if (incomingText.length > streamingText.length) {
            incomingItem.done = false;
          } else {
            timeline.splice(lastIncomingAssistantIndex, 1);
            localStreamingItemToPreserve = localStreamingItem;
          }
        } else {
          localStreamingItemToPreserve = localStreamingItem;
        }
      } else {
        localStreamingItemToPreserve = localStreamingItem;
      }
    }
  }

  const existingNonDialogItems = existing.filter((it) => {
    const kind = (it?.kind || '').toString().trim();
    if (kind === 'assistant' || kind === 'user') {
      if (localStreamingItemToPreserve && it === localStreamingItemToPreserve) {
        hasPreservedStreamingItem = true;
        return true;
      }
      return false;
    }
    if ((it?.id || '').toString().includes('-optimistic-')) return false;
    if (shouldFilterStaleThinkingItem(it, latestIncomingAssistantTs, hasRuntimeHistoryContext)) return false;
    return true;
  });

  if (isStreaming && !hasPreservedStreamingItem && thread?.lastMessage) {
    let lastIncomingAssistantIndex = -1;
    for (let i = timeline.length - 1; i >= 0; i--) {
      if (timeline[i].kind === 'assistant') { lastIncomingAssistantIndex = i; break; }
    }

    let isSameMessage = false;
    if (lastIncomingAssistantIndex >= 0) {
      const incomingText = (timeline[lastIncomingAssistantIndex].text || '').trim();
      const lastMsgText = (thread.lastMessage || '').trim();
      const matchLen = Math.min(10, Math.min(incomingText.length, lastMsgText.length));
      if (matchLen > 0 && (incomingText.startsWith(lastMsgText.slice(0, matchLen)) || lastMsgText.startsWith(incomingText.slice(0, matchLen)))) {
        isSameMessage = true;
      }
    }

    if (isSameMessage) {
      timeline[lastIncomingAssistantIndex].done = false;
    } else {
      existingNonDialogItems.push({
        id: `${id}-streaming-fallback`,
        kind: 'assistant',
        text: thread.lastMessage,
        done: false,
        ts: new Date().toISOString()
      });
    }
  }

  if (existingNonDialogItems.length > 0 && typeof logDiagnostic === 'function') {
    logDiagnostic('thread', 'history.preserved_runtime_items', {
      thread_id: id,
      preserved_count: existingNonDialogItems.length,
      sample_kinds: existingNonDialogItems.map((it) => it?.kind || 'unknown').slice(0, 10),
    });
  }

  const mergedItems = [...missingFromIncoming, ...timeline, ...existingNonDialogItems];
  mergedItems.sort((a, b) => {
    const tsA = (a && a.ts) ? String(a.ts) : '';
    const tsB = (b && b.ts) ? String(b.ts) : '';
    
    if (tsA && tsB) {
      if (tsA < tsB) return -1;
      if (tsA > tsB) return 1;
    } else if (tsA) {
      return -1;
    } else if (tsB) {
      return 1;
    }

    const idA = (a && a.id) ? String(a.id) : '';
    const idB = (b && b.id) ? String(b.id) : '';
    const partsA = idA.split('-');
    const partsB = idB.split('-');
    
    if (partsA.length > 1 && partsB.length > 1) {
      const prefixA = partsA.slice(0, -1).join('-');
      const prefixB = partsB.slice(0, -1).join('-');
      if (prefixA === prefixB && prefixA.length > 0) {
        const numA = Number(partsA[partsA.length - 1]);
        const numB = Number(partsB[partsB.length - 1]);
        if (!Number.isNaN(numA) && !Number.isNaN(numB) && numA !== numB) return numA - numB;
      }
    }
    
    if (idA < idB) return -1;
    if (idA > idB) return 1;
    return 0;
  });

  const frozenTimeline = freezeTimelineItemsAtomic(mergedItems, existing);
  if (!frozenTimeline.changed) return false;

  if (hasOptimistic) {
    const survivingOptimisticCount = mergedItems.filter((it) => (it?.id || '').toString().includes('-optimistic-')).length;
    if (survivingOptimisticCount < optimisticItems.length) {
      if (typeof logDiagnostic === 'function') logDiagnostic('thread', 'history.optimistic_deduped', {
        thread_id: id,
        original_optimistic_count: optimisticItems.length,
        deduped_count: optimisticItems.length - survivingOptimisticCount,
        surviving_count: survivingOptimisticCount,
      });
    }
  }

  state.timelinesByThread = { ...state.timelinesByThread, [id]: frozenTimeline.items };
  if (typeof logInfo === 'function') logInfo('thread', 'messages.load.local_timeline.applied', { thread_id: id, count: timeline.length, merged_count: mergedItems.length });
  return true;
}

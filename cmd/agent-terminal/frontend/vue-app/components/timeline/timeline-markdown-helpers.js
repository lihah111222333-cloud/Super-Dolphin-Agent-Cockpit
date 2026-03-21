export function decodeHtmlAttr(value) {
  return (value || '')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>');
}

export function whitespaceMeta(value) {
  const raw = (value || '').toString();
  const compact = raw.replace(/\s+/g, ' ').trim();
  return {
    value: raw,
    compact,
    has_multi_space: /\s{2,}/.test(raw),
    char_len: raw.length,
  };
}

export function logRenderedFileRefPaths(logInfo, rawText, html) {
  const source = (rawText || '').toString();
  const rendered = (html || '').toString();
  if (!rendered.includes('data-file-path="')) return;
  const matches = Array.from(rendered.matchAll(/data-file-path="([^"]+)"/g));
  if (matches.length === 0) return;
  const paths = matches.slice(0, 8).map((item) => whitespaceMeta(decodeHtmlAttr(item[1])));
  logInfo('ui', 'chat.fileRef.render.paths', {
    refs: paths.length,
    refs_truncated: Math.max(matches.length - paths.length, 0),
    paths,
    source_len: source.length,
    source_preview: source.slice(0, 280),
  });
}

export function describeClickNode(node) {
  const el = node && node.nodeType === 3 ? node.parentElement : node;
  if (!el || typeof el !== 'object') return {};
  return {
    tag: (el.tagName || '').toString().toLowerCase(),
    class_name: (el.className || '').toString(),
    text_preview: ((el.textContent || '').toString().trim()).slice(0, 120),
  };
}

export function resolveFileRefNode(target, event) {
  let refNode = null;
  if (target && typeof target.closest === 'function') {
    refNode = target.closest('.chat-md-inline-code.is-file-ref, .chat-md-file-ref');
  }
  if (!refNode && typeof event?.composedPath === 'function') {
    const path = event.composedPath();
    refNode = path.find((node) => {
      if (!node || !node.classList || typeof node.classList.contains !== 'function') return false;
      return node.classList.contains('is-file-ref') || node.classList.contains('chat-md-file-ref');
    }) || null;
  }
  return refNode;
}

export function resolveCitationNode(target, event) {
  let citationNode = null;
  if (target && typeof target.closest === 'function') {
    citationNode = target.closest('.chat-md-citation[data-citation-kind]');
  }
  if (!citationNode && typeof event?.composedPath === 'function') {
    const path = event.composedPath();
    citationNode = path.find((node) => {
      if (!node || !node.classList || typeof node.classList.contains !== 'function') return false;
      return node.classList.contains('chat-md-citation') && typeof node.getAttribute === 'function' && Boolean(node.getAttribute('data-citation-kind'));
    }) || null;
  }
  return citationNode;
}

export function resolveTerminalCitationTargetId(items, chunkId) {
  const target = (chunkId || '').toString().trim();
  const source = Array.isArray(items) ? items : [];
  let firstCommandId = '';
  for (let index = source.length - 1; index >= 0; index -= 1) {
    const item = source[index];
    if (!item || typeof item !== 'object') continue;
    if ((item.kind || '').toString().trim() !== 'command') continue;
    const itemId = (item.id || '').toString().trim();
    if (!firstCommandId && itemId) firstCommandId = itemId;
    if (!target) continue;
    const candidates = [item.id, item.chunkId, item.chunk_id, item.terminalChunkId, item.terminal_chunk_id]
      .map((value) => (value || '').toString().trim())
      .filter(Boolean);
    if (candidates.includes(target)) return itemId;
  }
  return firstCommandId;
}

export function scrollToTimelineItem(itemId) {
  const targetId = (itemId || '').toString().trim();
  if (!targetId || typeof document === 'undefined') return;
  const nodes = Array.from(document.querySelectorAll('[data-chat-item-id]'));
  const targetNode = nodes.find((node) => (node.getAttribute('data-chat-item-id') || '').toString() === targetId);
  if (!targetNode || typeof targetNode.scrollIntoView !== 'function') return;
  targetNode.scrollIntoView({ block: 'center', behavior: 'smooth' });
}

import { onBeforeUnmount, ref } from '../../../lib/vue.esm-browser.prod.js';
import { renderAssistantMarkdown } from '../../utils/assistant-markdown.js';
import { createStreamingMarkdownStateResolver } from '../../utils/assistant-markdown-streaming.js';
import { logInfo, logWarn } from '../../services/log.js';
import { initPretextLayout } from '../../services/pretext-layout.js';
import {
  describeClickNode,
  logRenderedFileRefPaths,
  resolveCitationNode,
  resolveFileRefNode,
  resolveTerminalCitationTargetId,
  scrollToTimelineItem,
  whitespaceMeta,
} from './timeline-markdown-helpers.js';

function createRenderAssistantBody(assistantMarkdownCache) {
  return function renderAssistantBody(text) {
    const key = (text || '').toString();
    if (!key) return '';
    if (assistantMarkdownCache.has(key)) {
      return assistantMarkdownCache.get(key) || '';
    }
    let html = '';
    try {
      html = renderAssistantMarkdown(key);
      logRenderedFileRefPaths(logInfo, key, html);
    } catch (err) {
      logWarn('ui', 'chat.markdown.render_failed', { error: String(err || ''), text_length: key.length });
      const safeText = key.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      html = `<div style="color:#ef4444;padding:8px;border:1px solid #fee2e2;border-radius:4px;margin-bottom:8px;font-size:13px;">Markdown 渲染失败 (强制回退展示)</div><pre style="white-space:pre-wrap;word-break:break-all;font-size:13px;">${safeText}</pre>`;
    }
    assistantMarkdownCache.set(key, html);
    if (assistantMarkdownCache.size > 280) {
      const first = assistantMarkdownCache.keys().next().value;
      assistantMarkdownCache.delete(first);
    }
    return html;
  };
}

function clearCitationTargetTimer(state) {
  if (state.citationTargetClearTimer) {
    clearTimeout(state.citationTargetClearTimer);
    state.citationTargetClearTimer = 0;
  }
}

function createFocusCitationItem(activeCitationItemId, state) {
  return function focusCitationItem(itemId) {
    const targetId = (itemId || '').toString().trim();
    clearCitationTargetTimer(state);
    activeCitationItemId.value = targetId;
    if (!targetId) return;
    const scroll = () => scrollToTimelineItem(targetId);
    if (typeof requestAnimationFrame === 'function') requestAnimationFrame(scroll);
    else setTimeout(scroll, 0);
    state.citationTargetClearTimer = setTimeout(() => {
      activeCitationItemId.value = '';
      state.citationTargetClearTimer = 0;
    }, 2200);
  };
}

function emitCitationClick(emit, event, node, payload) {
  if (typeof event?.preventDefault === 'function') event.preventDefault();
  if (typeof event?.stopPropagation === 'function') event.stopPropagation();
  const nextPayload = {
    kind: (payload?.kind || '').toString().trim(),
    raw: (node?.textContent || '').toString().trim(),
    ...(payload || {}),
  };
  logInfo('ui', 'chat.citation.click.emit', nextPayload);
  emit('citation-click', nextPayload);
}

function handleCopyButton(target, event, copyTextToClipboard) {
  const copyBtn = target?.closest?.('.chat-md-code-copy-btn');
  if (!copyBtn || typeof copyBtn.hasAttribute !== 'function' || !copyBtn.hasAttribute('data-copy-code')) {
    return false;
  }
  const code = (copyBtn.getAttribute('data-copy-code') || '').toString();
  copyTextToClipboard(code).then((ok) => {
    if (!ok) return;
    copyBtn.classList.add('is-copied');
    setTimeout(() => copyBtn.classList.remove('is-copied'), 1800);
  });
  event.preventDefault();
  event.stopPropagation();
  return true;
}

function handleExpandButton(target, event) {
  const expandBtn = target?.closest?.('.chat-md-code-expand-btn');
  if (!expandBtn || typeof expandBtn.hasAttribute !== 'function' || !expandBtn.hasAttribute('data-expand-code')) {
    return false;
  }
  const block = expandBtn.closest('.chat-md-code-block[data-collapsible]');
  if (block) block.classList.toggle('is-expanded');
  event.preventDefault();
  event.stopPropagation();
  return true;
}

function handleFileRefClick(target, event, emit) {
  logInfo('ui', 'chat.fileRef.click.entry', { target: describeClickNode(target) });
  const refNode = /** @type {any} */ (resolveFileRefNode(target, event));
  if (!refNode) return false;

  const attrPathRaw = (refNode.getAttribute('data-file-path') || '').toString();
  const path = attrPathRaw.trim();
  const lineRaw = Number(refNode.getAttribute('data-file-line') || 0);
  const line = Number.isFinite(lineRaw) && lineRaw > 0 ? Math.floor(lineRaw) : 1;
  const column = Number(refNode.getAttribute('data-file-column') || 0);
  logInfo('ui', 'chat.fileRef.click.path_attr', {
    path_attr: whitespaceMeta(attrPathRaw),
    path_trimmed: whitespaceMeta(path),
    line_raw: lineRaw,
    column_raw: column,
    ref_text: (refNode.textContent || '').toString().trim(),
  });
  if (!path) {
    logWarn('ui', 'chat.fileRef.click.no_path', {
      ref_text: (refNode.textContent || '').toString().trim(),
      line_raw: lineRaw,
      column_raw: column,
    });
    return true;
  }
  if (typeof event.preventDefault === 'function') event.preventDefault();
  if (typeof event.stopPropagation === 'function') event.stopPropagation();
  const payload = {
    path,
    line,
    column: Number.isFinite(column) && column > 0 ? Math.floor(column) : 0,
    raw: (refNode.textContent || '').toString().trim(),
  };
  logInfo('ui', 'chat.fileRef.click.emit', payload);
  emit('file-ref-click', payload);
  return true;
}

function handleCitationClick(target, event, emit, items, focusCitationItem) {
  const citationNode = /** @type {any} */ (resolveCitationNode(target, event));
  if (!citationNode) return false;
  const kind = (citationNode.getAttribute('data-citation-kind') || '').toString().trim();
  if (kind === 'terminal') {
    const chunkId = (citationNode.getAttribute('data-terminal-chunk-id') || '').toString().trim();
    focusCitationItem(resolveTerminalCitationTargetId(items, chunkId));
    emitCitationClick(emit, event, citationNode, {
      kind,
      chunkId,
      lineStart: Number(citationNode.getAttribute('data-line-start') || 0) || 0,
      lineEnd: Number(citationNode.getAttribute('data-line-end') || 0) || 0,
    });
    return true;
  }
  if (kind === 'image') {
    emitCitationClick(emit, event, citationNode, {
      kind,
      assetPointer: (citationNode.getAttribute('data-asset-pointer') || '').toString().trim(),
      imageSrc: (citationNode.getAttribute('data-image-src') || '').toString().trim(),
      path: (citationNode.getAttribute('data-file-path') || '').toString().trim(),
    });
    return true;
  }
  if (kind === 'task') {
    emitCitationClick(emit, event, citationNode, {
      kind,
      title: (citationNode.getAttribute('data-task-title') || '').toString().trim(),
      prompt: (citationNode.getAttribute('data-task-prompt') || '').toString().trim(),
    });
    return true;
  }
  if (kind === 'skill' || kind === 'conversation') {
    emitCitationClick(emit, event, citationNode, {
      kind,
      skillId: (citationNode.getAttribute('data-skill-id') || '').toString().trim(),
      skillName: (citationNode.getAttribute('data-skill-name') || '').toString().trim(),
      path: (citationNode.getAttribute('data-skill-path') || '').toString().trim(),
      conversationId: (citationNode.getAttribute('data-conversation-id') || '').toString().trim(),
    });
    return true;
  }
  if (kind === 'automation-update' || kind === 'code-comment') {
    emitCitationClick(emit, event, citationNode, {
      kind,
      title: (citationNode.getAttribute('data-comment-title') || citationNode.getAttribute('data-automation-name') || '').toString().trim(),
      message: (citationNode.getAttribute('data-message') || '').toString().trim(),
      prompt: (citationNode.getAttribute('data-automation-prompt') || '').toString().trim(),
      path: (citationNode.getAttribute('data-file-path') || '').toString().trim(),
      lineStart: Number(citationNode.getAttribute('data-line-start') || 0) || 0,
      lineEnd: Number(citationNode.getAttribute('data-line-end') || 0) || 0,
    });
    return true;
  }
  return false;
}

export function useAssistantBodyActions(props, emit, { copyTextToClipboard }) {
  const assistantMarkdownCache = new Map();
  const streamingFrameVersion = ref(0);
  const activeCitationItemId = ref('');
  const state = { citationTargetClearTimer: 0 };
  const renderAssistantBody = createRenderAssistantBody(assistantMarkdownCache);
  const rawStreamingAssistantState = createStreamingMarkdownStateResolver(renderAssistantBody, () => {
    streamingFrameVersion.value += 1;
  }, (stallInfo) => {
    logWarn('ui', 'chat.streaming.stall_detected', stallInfo);
  });
  const focusCitationItem = createFocusCitationItem(activeCitationItemId, state);
  if (typeof window !== 'undefined') initPretextLayout();

  function streamingAssistantState(item) {
    streamingFrameVersion.value;
    return rawStreamingAssistantState(item);
  }

  function isCitationTarget(item) {
    const itemId = (item?.id || '').toString().trim();
    return Boolean(itemId) && itemId === (activeCitationItemId.value || '').toString().trim();
  }

  function onAssistantBodyClick(event) {
    const rawTarget = event?.target || null;
    const target = rawTarget && rawTarget.nodeType === 3 ? rawTarget.parentElement : rawTarget;
    if (handleCopyButton(target, event, copyTextToClipboard)) return;
    if (handleExpandButton(target, event)) return;
    if (handleFileRefClick(target, event, emit)) return;
    if (handleCitationClick(target, event, emit, props.items, focusCitationItem)) return;
    logWarn('ui', 'chat.fileRef.click.no_ref_node', { target: describeClickNode(target) });
  }

  onBeforeUnmount(() => {
    clearCitationTargetTimer(state);
    rawStreamingAssistantState.dispose?.();
  });

  return {
    renderAssistantBody,
    streamingAssistantState,
    streamingFrameVersion,
    isCitationTarget,
    onAssistantBodyClick,
  };
}

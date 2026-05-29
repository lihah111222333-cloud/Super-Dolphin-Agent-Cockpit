// @ts-nocheck

import { resolveCitationNode, resolveFileRefNode } from '../components/timeline/timeline-markdown-helpers.js';

function toPositiveInt(value, fallback = 0) {
  const parsed = Number(value || 0);
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : fallback;
}

function buildFileRefPayload(node) {
  if (!node || typeof node.getAttribute !== 'function') return null;
  const path = (node.getAttribute('data-file-path') || '').toString().trim();
  if (!path) return null;
  return {
    path,
    line: toPositiveInt(node.getAttribute('data-file-line'), 1),
    column: toPositiveInt(node.getAttribute('data-file-column'), 0),
    raw: (node.textContent || '').toString().trim(),
  };
}

function buildCitationPayload(node) {
  if (!node || typeof node.getAttribute !== 'function') return null;
  const kind = (node.getAttribute('data-citation-kind') || '').toString().trim();
  if (!kind) return null;
  if (kind === 'terminal') {
    return {
      kind,
      chunkId: (node.getAttribute('data-terminal-chunk-id') || '').toString().trim(),
      lineStart: toPositiveInt(node.getAttribute('data-line-start'), 0),
      lineEnd: toPositiveInt(node.getAttribute('data-line-end'), 0),
      raw: (node.textContent || '').toString().trim(),
    };
  }
  if (kind === 'image') {
    return {
      kind,
      assetPointer: (node.getAttribute('data-asset-pointer') || '').toString().trim(),
      imageSrc: (node.getAttribute('data-image-src') || '').toString().trim(),
      path: (node.getAttribute('data-file-path') || '').toString().trim(),
      raw: (node.textContent || '').toString().trim(),
    };
  }
  if (kind === 'task') {
    return {
      kind,
      title: (node.getAttribute('data-task-title') || '').toString().trim(),
      prompt: (node.getAttribute('data-task-prompt') || '').toString().trim(),
      raw: (node.textContent || '').toString().trim(),
    };
  }
  if (kind === 'skill' || kind === 'conversation') {
    return {
      kind,
      skillId: (node.getAttribute('data-skill-id') || '').toString().trim(),
      skillName: (node.getAttribute('data-skill-name') || '').toString().trim(),
      path: (node.getAttribute('data-skill-path') || '').toString().trim(),
      conversationId: (node.getAttribute('data-conversation-id') || '').toString().trim(),
      raw: (node.textContent || '').toString().trim(),
    };
  }
  if (kind === 'automation-update' || kind === 'code-comment') {
    return {
      kind,
      title: (node.getAttribute('data-comment-title') || node.getAttribute('data-automation-name') || '').toString().trim(),
      message: (node.getAttribute('data-message') || '').toString().trim(),
      prompt: (node.getAttribute('data-automation-prompt') || '').toString().trim(),
      path: (node.getAttribute('data-file-path') || '').toString().trim(),
      lineStart: toPositiveInt(node.getAttribute('data-line-start'), 0),
      lineEnd: toPositiveInt(node.getAttribute('data-line-end'), 0),
      raw: (node.textContent || '').toString().trim(),
    };
  }
  return {
    kind,
    raw: (node.textContent || '').toString().trim(),
  };
}

export function resolveRenderedMarkdownAction(event) {
  const rawTarget = event?.target || null;
  const target = rawTarget && rawTarget.nodeType === 3 ? rawTarget.parentElement : rawTarget;
  const fileRefNode = resolveFileRefNode(target, event);
  if (fileRefNode) {
    const payload = buildFileRefPayload(fileRefNode);
    return payload ? { type: 'file-ref', node: fileRefNode, payload } : null;
  }
  const citationNode = resolveCitationNode(target, event);
  if (citationNode) {
    const payload = buildCitationPayload(citationNode);
    return payload ? { type: 'citation', node: citationNode, payload } : null;
  }
  return null;
}

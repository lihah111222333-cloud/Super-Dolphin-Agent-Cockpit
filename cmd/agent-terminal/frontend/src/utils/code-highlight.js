// @ts-nocheck
import hljs from 'highlight.js/lib/core';

import go from 'highlight.js/lib/languages/go';
import javascript from 'highlight.js/lib/languages/javascript';
import python from 'highlight.js/lib/languages/python';
import c from 'highlight.js/lib/languages/c';
import cpp from 'highlight.js/lib/languages/cpp';
import rust from 'highlight.js/lib/languages/rust';
import typescript from 'highlight.js/lib/languages/typescript';
import bash from 'highlight.js/lib/languages/bash';
import json from 'highlight.js/lib/languages/json';
import yaml from 'highlight.js/lib/languages/yaml';
import css from 'highlight.js/lib/languages/css';
import xml from 'highlight.js/lib/languages/xml';
import sql from 'highlight.js/lib/languages/sql';
import diff from 'highlight.js/lib/languages/diff';
import markdown from 'highlight.js/lib/languages/markdown';

const LANGUAGE_ALIAS_MAP = {
  js: 'javascript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  yml: 'yaml',
  sh: 'bash',
  shell: 'bash',
  zsh: 'bash',
  console: 'bash',
  text: 'plaintext',
  txt: 'plaintext',
  md: 'markdown',
};

const FILE_LANGUAGE_MAP = {
  go: 'go',
  js: 'javascript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  py: 'python',
  c: 'c',
  h: 'c',
  cc: 'cpp',
  cxx: 'cpp',
  cpp: 'cpp',
  hpp: 'cpp',
  rs: 'rust',
  sh: 'bash',
  bash: 'bash',
  zsh: 'bash',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  css: 'css',
  html: 'xml',
  htm: 'xml',
  xml: 'xml',
  svg: 'xml',
  sql: 'sql',
  diff: 'diff',
  patch: 'diff',
  md: 'markdown',
};

const BASENAME_LANGUAGE_MAP = {
  dockerfile: 'bash',
  makefile: 'bash',
};

const LANGUAGE_LOADERS = [
  ['go', go],
  ['javascript', javascript],
  ['python', python],
  ['c', c],
  ['cpp', cpp],
  ['rust', rust],
  ['typescript', typescript],
  ['bash', bash],
  ['json', json],
  ['yaml', yaml],
  ['css', css],
  ['xml', xml],
  ['sql', sql],
  ['diff', diff],
  ['markdown', markdown],
];

let registered = false;

function ensureRegistered() {
  if (registered) return;
  for (const [name, loader] of LANGUAGE_LOADERS) {
    if (!hljs.getLanguage(name)) {
      hljs.registerLanguage(name, loader);
    }
  }
  registered = true;
}

export function escapeHtml(value) {
  return (value || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function normalizeCodeLanguage(rawLanguage) {
  const raw = (rawLanguage || '').toString().trim().toLowerCase();
  if (!raw) return '';
  return LANGUAGE_ALIAS_MAP[raw] || raw;
}

export function detectLanguageFromFilePath(filePath = '') {
  const normalized = (filePath || '').toString().trim();
  if (!normalized) return '';
  const basename = normalized.split(/[\\/]/).pop()?.toLowerCase() || '';
  if (basename && BASENAME_LANGUAGE_MAP[basename]) {
    return BASENAME_LANGUAGE_MAP[basename];
  }
  const match = basename.match(/\.([a-z0-9]+)$/i);
  if (!match) return '';
  return FILE_LANGUAGE_MAP[match[1].toLowerCase()] || '';
}

export function highlightSnippet(code, options = {}) {
  ensureRegistered();

  const source = (code || '').toString();
  const preferredLanguage = normalizeCodeLanguage(options.language || '');
  const language = preferredLanguage || detectLanguageFromFilePath(options.filePath || '');
  const classLanguage = language || 'plaintext';

  if (!language || !hljs.getLanguage(language)) {
    return {
      language: classLanguage,
      html: escapeHtml(source),
    };
  }

  try {
    return {
      language,
      html: hljs.highlight(source, { language, ignoreIllegals: true }).value,
    };
  } catch {
    return {
      language: classLanguage,
      html: escapeHtml(source),
    };
  }
}

export function highlightCode(code, filePath = '') {
  const source = (code || '').toString();
  const language = detectLanguageFromFilePath(filePath);
  const lines = source.split('\n');

  return lines.map((line) => {
    const highlighted = highlightSnippet(line, { language, filePath });
    return `<span class="hljs language-${escapeHtml(highlighted.language || 'plaintext')}">${highlighted.html}</span>`;
  });
}
